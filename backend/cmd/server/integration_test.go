package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/middleware"
	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// 这一组是集成测试：起真 PostgreSQL、跑真迁移、走完整 HTTP 链路。
// `go test -short`（make dev-check）会跳过，`make check` 会真跑。

// testSecret 是测试用的会话密钥，够 32 字节。
const testSecret = "test-session-secret-must-be-32-bytes-long"

type liveApp struct {
	*app
	pool *pgxpool.Pool
	// tenant 是这组用例默认操作的租户。多租户之后**任何数据操作都得说明是哪个租户**，
	// 没有「全局」这回事了。
	tenant testdb.TenantFixture
	store  *repo.Store
	q      *repo.TenantQueries
}

func newLiveApp(t *testing.T) *liveApp {
	t.Helper()

	pool := testdb.Start(t)
	// 租户要在 newApp 之前建好：启动时会加载各租户的配置和权限策略。
	tenant := testdb.NewTenant(t, pool, 0)

	cfg, err := config.Load("../../../config/config.example.yaml")
	if err != nil {
		t.Fatalf("加载配置失败：%v", err)
	}
	cfg.Session.Secret = testSecret

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := newApp(t.Context(), cfg, logger, pool, "test")
	if err != nil {
		t.Fatalf("装配应用失败：%v", err)
	}
	store := repo.New(pool)
	return &liveApp{app: a, pool: pool, tenant: tenant, store: store, q: store.ForTenant(tenant.ID)}
}

// call 发一个请求。cookies 和 csrf 传空就是匿名请求。
func (a *liveApp) call(t *testing.T, method, path string, body any, session *sessionState) *httptest.ResponseRecorder {
	t.Helper()
	return a.callWithHeaders(t, method, path, body, session, nil)
}

// callWithHeaders 同上，另外带一批请求头（幂等键这类）。
func (a *liveApp) callWithHeaders(
	t *testing.T, method, path string, body any,
	session *sessionState, headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}

	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != nil {
		for _, c := range session.cookies {
			req.AddCookie(c)
		}
		if session.csrf != "" {
			req.Header.Set(auth.HeaderCSRFToken, session.csrf)
		}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	a.echo.ServeHTTP(rec, req)
	return rec
}

// sessionState 是一次登录之后要带着走的东西。
type sessionState struct {
	cookies []*http.Cookie
	csrf    string
}

// login 登录并返回会话状态。
func (a *liveApp) login(t *testing.T, username, password string) (*sessionState, *httptest.ResponseRecorder) {
	t.Helper()

	rec := a.call(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"tenant_code": a.tenant.Code,
		"account":     username,
		"password":    password,
	}, nil)
	if rec.Code != http.StatusOK {
		return nil, rec
	}

	var body struct {
		Data struct {
			CSRFToken string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("登录响应不是合法 JSON：%v", err)
	}
	return &sessionState{cookies: rec.Result().Cookies(), csrf: body.Data.CSRFToken}, rec
}

// bootstrapAdmin 建首个管理员并返回账号密码。
func (a *liveApp) bootstrapAdmin(t *testing.T) (string, string) {
	t.Helper()

	result, err := auth.Bootstrap(t.Context(), a.store)
	if err != nil {
		t.Fatalf("引导管理员失败：%v", err)
	}
	if !result.Created {
		t.Fatal("库里本来就有用户，测试数据没清干净")
	}
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatalf("刷新授权策略失败：%v", err)
	}
	return result.Username, result.Password
}

// createUser 建一个普通用户，roleKey 为空表示不给任何角色。
func (a *liveApp) createUser(t *testing.T, username, password, roleKey string) uuid.UUID {
	t.Helper()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.q.CreateUser(t.Context(), repo.CreateUserArgs{
		ID:           id,
		Username:     username,
		DisplayName:  username,
		PasswordHash: auth.HashPassword(password),
	})
	if err != nil {
		t.Fatalf("建用户失败：%v", err)
	}

	if roleKey != "" {
		role, err := a.q.GetRoleByKey(t.Context(), roleKey)
		if err != nil {
			t.Fatalf("找角色 %s 失败：%v", roleKey, err)
		}
		if err := a.q.AssignUserRole(t.Context(),
			repo.AssignUserRoleArgs{UserID: user.ID, RoleID: role.ID}); err != nil {
			t.Fatalf("赋角色失败：%v", err)
		}
	}
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatalf("刷新授权策略失败：%v", err)
	}
	return user.ID
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("响应不是合法 JSON：%v，body=%s", err, rec.Body)
	}
	return p
}

// ---------------------------------------------------------------- 用例

func TestBootstrapThenLoginThenChangePassword(t *testing.T) {
	a := newLiveApp(t)
	username, password := a.bootstrapAdmin(t)

	session, rec := a.login(t, username, password)
	if session == nil {
		t.Fatalf("首个管理员应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}

	var loginBody struct {
		Data struct {
			MustChangePassword bool `json:"must_change_password"`
			User               struct {
				Username string   `json:"username"`
				Roles    []string `json:"roles"`
				Scope    string   `json:"scope"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	if !loginBody.Data.MustChangePassword {
		t.Error("首个管理员必须被标记为「首次登录要改密」")
	}
	if len(loginBody.Data.User.Roles) != 1 || loginBody.Data.User.Roles[0] != "admin" {
		t.Errorf("首个管理员应该有 admin 角色，得到 %v", loginBody.Data.User.Roles)
	}
	if loginBody.Data.User.Scope != "all" {
		t.Errorf("admin 角色的数据范围应是 all，得到 %q", loginBody.Data.User.Scope)
	}
	if !hasCookie(session.cookies, "fries_session") {
		t.Error("登录没有种下会话 cookie")
	}
	for _, c := range session.cookies {
		if c.Name == "fries_session" && !c.HttpOnly {
			t.Error("会话 cookie 必须是 HttpOnly，否则 XSS 能直接偷走")
		}
	}

	// 没改密码之前，除了改密接口哪都去不了
	blocked := a.call(t, http.MethodGet, "/api/v1/me", nil, session)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("必须改密时访问 /me 应该 403，得到 %d：%s", blocked.Code, blocked.Body)
	}
	if code := decodeProblem(t, blocked).Code; code != errs.MustChangePassword.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.MustChangePassword.Code, code)
	}

	// 密码强度按 settings 表里的策略校验
	weak := a.call(t, http.MethodPost, "/api/v1/me/password", map[string]string{
		"old_password": password,
		"new_password": "short",
	}, session)
	if weak.Code != http.StatusBadRequest {
		t.Fatalf("弱密码应该被拒，得到 %d：%s", weak.Code, weak.Body)
	}

	changed := a.call(t, http.MethodPost, "/api/v1/me/password", map[string]string{
		"old_password": password,
		"new_password": "NewPassw0rd2026",
	}, session)
	if changed.Code != http.StatusOK {
		t.Fatalf("改密码应该成功，得到 %d：%s", changed.Code, changed.Body)
	}

	// 改完密码，原会话还在，其它接口通了
	me := a.call(t, http.MethodGet, "/api/v1/me", nil, session)
	if me.Code != http.StatusOK {
		t.Fatalf("改完密码后 /me 应该通，得到 %d：%s", me.Code, me.Body)
	}
	var meBody struct {
		Data struct {
			Permissions []string `json:"permissions"`
			Menus       []struct {
				Key string `json:"key"`
			} `json:"menus"`
		} `json:"data"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meBody); err != nil {
		t.Fatal(err)
	}
	if len(meBody.Data.Permissions) == 0 {
		t.Error("admin 应该拿到全部权限点")
	}
	if len(meBody.Data.Menus) == 0 {
		t.Error("admin 应该看得到菜单")
	}

	// 旧密码不能再用
	if _, rec := a.login(t, username, password); rec.Code != http.StatusUnauthorized {
		t.Errorf("改完密码后旧密码应该失效，得到 %d", rec.Code)
	}
}

func TestLoginAcceptsUsernameEmailOrPhone(t *testing.T) {
	a := newLiveApp(t)
	id := a.createUser(t, "multi", "Passw0rd2026x", "admin")

	if _, err := a.pool.Exec(t.Context(),
		`UPDATE users SET email = $2, phone = $3 WHERE tenant_id = $4 AND id = $1`,
		id, "Zhang@Example.com", "13800138000", a.tenant.ID); err != nil {
		t.Fatalf("补邮箱手机号失败：%v", err)
	}

	for name, account := range map[string]string{
		"用户名":     "multi",
		"邮箱":      "Zhang@Example.com",
		"邮箱大小写不同": "zhang@example.com", // 邮箱大小写不敏感
		"手机号":     "13800138000",
	} {
		t.Run(name, func(t *testing.T) {
			session, rec := a.login(t, account, "Passw0rd2026x")
			if session == nil {
				t.Fatalf("用%s登录应该成功，得到 %d：%s", name, rec.Code, rec.Body)
			}
		})
	}

	// 唯一约束现在就生效：同一个邮箱不能再占第二次
	other := a.createUser(t, "other", "Passw0rd2026x", "")
	if _, err := a.pool.Exec(t.Context(),
		`UPDATE users SET email = $2 WHERE tenant_id = $3 AND id = $1`,
		other, "zhang@example.com", a.tenant.ID); err == nil {
		t.Error("邮箱重复应该被唯一索引挡住（大小写不敏感）")
	}
}

func TestCSRFProtectsCookieRequests(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "csrf-user", "Passw0rd2026x", "admin")

	session, rec := a.login(t, "csrf-user", "Passw0rd2026x")
	if session == nil {
		t.Fatalf("登录失败：%d %s", rec.Code, rec.Body)
	}

	// 带着 cookie 但不带 CSRF 头 —— 这正是 CSRF 攻击的样子
	noToken := *session
	noToken.csrf = ""
	blocked := a.call(t, http.MethodPost, "/api/v1/auth/logout", nil, &noToken)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("写请求缺 CSRF 头应该 403，得到 %d：%s", blocked.Code, blocked.Body)
	}
	if code := decodeProblem(t, blocked).Code; code != errs.CSRFInvalid.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.CSRFInvalid.Code, code)
	}

	// 带对了就放行
	ok := a.call(t, http.MethodPost, "/api/v1/auth/logout", nil, session)
	if ok.Code != http.StatusOK {
		t.Fatalf("带上 CSRF 头应该成功，得到 %d：%s", ok.Code, ok.Body)
	}

	// 登出之后会话立即失效，不等 cookie 过期
	after := a.call(t, http.MethodGet, "/api/v1/me", nil, session)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("登出后会话应该立即失效，得到 %d：%s", after.Code, after.Body)
	}
}

func TestPermissionsAreEnforced(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "nobody", "Passw0rd2026x", "") // 不给任何角色

	session, rec := a.login(t, "nobody", "Passw0rd2026x")
	if session == nil {
		t.Fatalf("登录失败：%d %s", rec.Code, rec.Body)
	}

	denied := a.call(t, http.MethodGet, "/api/v1/audit-logs", nil, session)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("没有权限点应该 403，得到 %d：%s", denied.Code, denied.Body)
	}
	if code := decodeProblem(t, denied).Code; code != errs.PermDenied.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.PermDenied.Code, code)
	}

	// 菜单也要跟着空掉：前后端看到的是同一份权限（DECISIONS.md §3.6）
	me := a.call(t, http.MethodGet, "/api/v1/me", nil, session)
	var meBody struct {
		Data struct {
			Permissions []string `json:"permissions"`
			Menus       []any    `json:"menus"`
		} `json:"data"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meBody); err != nil {
		t.Fatal(err)
	}
	if len(meBody.Data.Permissions) != 0 || len(meBody.Data.Menus) != 0 {
		t.Errorf("没有角色的人不该有权限或菜单：%+v", meBody.Data)
	}

	// 给上 admin 角色之后立刻生效（策略重载）
	a.createUser(t, "somebody", "Passw0rd2026x", "admin")
	adminSession, _ := a.login(t, "somebody", "Passw0rd2026x")
	allowed := a.call(t, http.MethodGet, "/api/v1/audit-logs", nil, adminSession)
	if allowed.Code != http.StatusOK {
		t.Fatalf("admin 应该查得了审计，得到 %d：%s", allowed.Code, allowed.Body)
	}
}

func TestAuditRecordsEveryRequest(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "auditor", "Passw0rd2026x", "admin")

	// 一次失败登录 + 一次成功登录，两条都要留痕
	a.login(t, "auditor", "wrong-password")
	session, _ := a.login(t, "auditor", "Passw0rd2026x")

	rec := a.call(t, http.MethodGet, "/api/v1/audit-logs?page_size=50", nil, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("查审计失败：%d %s", rec.Code, rec.Body)
	}

	var body struct {
		Data []struct {
			Resource   string         `json:"resource"`
			Action     string         `json:"action"`
			HTTPStatus int            `json:"http_status"`
			ActorName  string         `json:"actor_name"`
			Detail     map[string]any `json:"detail"`
		} `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	var failedLogin, okLogin bool
	for _, e := range body.Data {
		if e.Resource != "auth" || e.Action != "login" {
			continue
		}
		if e.HTTPStatus == http.StatusUnauthorized {
			failedLogin = true
		}
		if e.HTTPStatus == http.StatusOK {
			okLogin = true
			if e.ActorName != "auditor" {
				t.Errorf("成功登录的审计应该记下是谁，得到 %q", e.ActorName)
			}
		}
		if _, leaked := e.Detail["password"]; leaked {
			t.Error("审计里出现了密码字段")
		}
		if raw, _ := json.Marshal(e.Detail); strings.Contains(string(raw), "Passw0rd2026x") {
			t.Errorf("审计摘要里泄露了明文密码：%s", raw)
		}
	}
	if !failedLogin {
		t.Error("登录失败没有留痕 —— 这正是最需要审计的场景")
	}
	if !okLogin {
		t.Error("登录成功没有留痕")
	}
	if body.Pagination.Total < 2 {
		t.Errorf("两次登录都该留痕，总数只有 %d 条", body.Pagination.Total)
	}

	// 查询本身也是审计对象：读写全记（DECISIONS.md §6）。
	// 上一次查询的那条审计是在响应发出之后才写的，所以要再查一次才看得到。
	again := a.call(t, http.MethodGet, "/api/v1/audit-logs?resource=audit&action=list", nil, session)
	var readBody struct {
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(again.Body.Bytes(), &readBody); err != nil {
		t.Fatal(err)
	}
	if readBody.Pagination.Total == 0 {
		t.Error("查询操作没有留痕 —— 谁翻过哪些数据同样要记")
	}
}

func TestAPITimesAreUTC(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "tz-user", "Passw0rd2026x", "admin")

	session, rec := a.login(t, "tz-user", "Passw0rd2026x")
	if session == nil {
		t.Fatalf("登录失败：%d %s", rec.Code, rec.Body)
	}

	// DECISIONS.md §2.5：API 输出的时间一律 RFC3339 带 Z。
	// 靠人记得写 .UTC() 是守不住的，这条断言就是那道守卫。
	var login struct {
		Data struct {
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(login.Data.ExpiresAt, "Z") {
		t.Errorf("expires_at 不是 UTC：%s", login.Data.ExpiresAt)
	}

	logs := a.call(t, http.MethodGet, "/api/v1/audit-logs?page_size=5", nil, session)
	var body struct {
		Data []struct {
			OccurredAt string `json:"occurred_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(logs.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) == 0 {
		t.Fatal("没有审计记录可验")
	}
	for _, e := range body.Data {
		if !strings.HasSuffix(e.OccurredAt, "Z") {
			t.Errorf("occurred_at 不是 UTC：%s", e.OccurredAt)
		}
	}
}

func TestAuditChainDetectsTampering(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "chain-user", "Passw0rd2026x", "admin")
	session, _ := a.login(t, "chain-user", "Passw0rd2026x")
	a.call(t, http.MethodGet, "/api/v1/me", nil, session)
	a.call(t, http.MethodGet, "/api/v1/audit-logs", nil, session)

	broken, checked, err := audit.VerifyChain(t.Context(), a.q)
	if err != nil {
		t.Fatalf("验哈希链失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("一条审计都没有，链验了个寂寞")
	}
	if broken != nil {
		t.Fatalf("干净的链不该有断点，报了 %s", broken)
	}

	// 模拟有人绕过应用直接改库（测试里用的是 owner 身份，改得动）。
	//
	// ⚠️ 豁免标记写在 **SQL 里面**而不是 Go 注释里：运行期那层兜底
	// （repo/trace.go）拿到的是字符串的值，Go 注释它看不见。
	// 写在 SQL 里，构建期和运行期认的是同一行。
	if _, err := a.pool.Exec(t.Context(),
		`-- tenant-exempt: 这一句的**全部意义**就是绕过应用层 —— 带上租户条件就不是在模拟攻击了
		 UPDATE audit_logs SET action = 'tampered'
		 WHERE id = (SELECT id FROM audit_logs ORDER BY occurred_at LIMIT 1)`); err != nil {
		t.Fatalf("改审计表失败：%v", err)
	}

	broken, _, err = audit.VerifyChain(t.Context(), a.q)
	if err != nil {
		t.Fatalf("验哈希链失败：%v", err)
	}
	if broken == nil {
		t.Fatal("审计被改了却没验出来 —— 哈希链形同虚设")
	}
}

// API Key 认证失败记在哪个租户名下（MULTI-TENANCY.md §9.7）。
//
// 口径和 §7.1 的登录失败一样：**prefix 命中就算那个组织的**。
// 猜后半段的爆破恰恰是最该被记的一种失败，而被爆破的是**客户的**对接凭据 ——
// 记成平台级事件的话，客户在自己的审计里什么都看不到。
//
// 反过来，prefix 根本不存在的记 NULL：猜不出是谁的，也不该硬安给谁。
func TestAPIKeyFailureAuditGoesToItsTenant(t *testing.T) {
	a := newLiveApp(t)

	role, err := a.q.GetRoleByKey(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	_, prefix, hash := auth.NewAPIKey()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(t.Context(),
		`INSERT INTO service_accounts (tenant_id, id, name, key_prefix, key_hash, role_id)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		a.tenant.ID, id, "对接系统", prefix, []byte(hash), role.ID); err != nil {
		t.Fatalf("建 Service Account 失败：%v", err)
	}

	cases := []struct {
		name       string
		key        string
		wantTenant bool
	}{
		{"prefix 有效但 secret 猜错", "fsa_" + prefix + "_wrongsecret", true},
		{"prefix 根本不存在", "fsa_deadbeefdeadbeef_whatever", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodGet, "/api/v1/audit-logs", nil)
			req.Header.Set("X-API-Key", c.key)
			rec := httptest.NewRecorder()
			a.echo.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("这个 key 应该 401，得到 %d：%s", rec.Code, rec.Body)
			}

			// 直接查库：审计的 tenant_id 不在接口返回里，而它正是这一条要验的东西。
			//
			// 豁免标记写在 **SQL 里面** —— 运行期兜底（repo/trace.go）拿到的是
			// 字符串的值，Go 注释它看不见。写在 SQL 里，构建期和运行期认同一行。
			var tenantID *uuid.UUID
			if err := a.pool.QueryRow(t.Context(),
				`-- tenant-exempt: 这里查的就是「归属对不对」，按租户过滤等于把断言掐掉
				 SELECT tenant_id FROM audit_logs ORDER BY occurred_at DESC LIMIT 1`,
			).Scan(&tenantID); err != nil {
				t.Fatal(err)
			}

			switch {
			case c.wantTenant && (tenantID == nil || *tenantID != a.tenant.ID):
				t.Errorf("prefix 命中就该归属那个组织，得到 %v", tenantID)
			case !c.wantTenant && tenantID != nil:
				t.Errorf("猜不出是谁的就该记 NULL，得到 %v", tenantID)
			}
		})
	}
}

func TestServiceAccountUsesAPIKeyAndSkipsCSRF(t *testing.T) {
	a := newLiveApp(t)

	role, err := a.q.GetRoleByKey(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	fullKey, prefix, hash := auth.NewAPIKey()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(t.Context(),
		`INSERT INTO service_accounts (tenant_id, id, name, key_prefix, key_hash, role_id)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		a.tenant.ID, id, "对接系统", prefix, []byte(hash), role.ID); err != nil {
		t.Fatalf("建 Service Account 失败：%v", err)
	}
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/audit-logs", nil)
	req.Header.Set("X-API-Key", fullKey)
	rec := httptest.NewRecorder()
	a.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Service Account 应该走同一套权限查得了审计，得到 %d：%s", rec.Code, rec.Body)
	}

	// 写请求也不该要求 CSRF —— 没有 cookie 就没有 CSRF 风险（DECISIONS.md §6）
	write := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/logout", nil)
	write.Header.Set("X-API-Key", fullKey)
	writeRec := httptest.NewRecorder()
	a.echo.ServeHTTP(writeRec, write)
	if code := decodeProblem(t, writeRec).Code; code == errs.CSRFInvalid.Code {
		t.Error("API Key 认证不该被 CSRF 拦住，外部系统对接会莫名其妙 403")
	}

	// 错误的 key 一律 401
	bad := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/audit-logs", nil)
	bad.Header.Set("X-API-Key", "fsa_"+prefix+"_wrongsecret")
	badRec := httptest.NewRecorder()
	a.echo.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnauthorized {
		t.Errorf("错误的 API Key 应该 401，得到 %d", badRec.Code)
	}
}

func TestAccountLocksAfterRepeatedFailures(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "locky", "Passw0rd2026x", "admin")

	policy := a.settings.Security(a.tenant.ID)
	for range policy.LoginMaxFailures {
		a.login(t, "locky", "wrong")
	}

	// 密码对了也进不去：账号已经锁上了
	_, rec := a.login(t, "locky", "Passw0rd2026x")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("连续失败到阈值后应该锁定，得到 %d：%s", rec.Code, rec.Body)
	}
	if code := decodeProblem(t, rec).Code; code != errs.AccountLocked.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.AccountLocked.Code, code)
	}
}

func hasCookie(cookies []*http.Cookie, name string) bool {
	for _, c := range cookies {
		if c.Name == name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- 跨租户（HTTP 层）

// TestCrossTenantOverHTTP 从用户视角走一遍：A 公司的管理员登录之后，
// 无论怎么去够 B 公司的数据都够不着。
//
// repo 层已经有一组更细的隔离测试（internal/repo/tenant_isolation_test.go），
// 这一条测的是**整条链路**：登录 → 会话带租户 → 中间件放进 Principal →
// service 取 MustTenant → ForTenant 注入条件。中间任何一环断了这里都会红。
//
// ⚠️ 两个租户都必须有数据（MULTI-TENANCY.md §3.2 ⑧）—— 只给 A 造数据的话，
// 「A 看不到 B」是因为库里根本没有 B 的东西，等于什么都没测。
func TestCrossTenantOverHTTP(t *testing.T) {
	a := newLiveApp(t)

	// B 公司：另一个租户，有自己的管理员和自己的人
	other := testdb.NewTenant(t, a.pool, 1)
	qb := a.store.ForTenant(other.ID)
	bUserID := uuid.New()
	if _, err := qb.CreateUser(t.Context(), repo.CreateUserArgs{
		ID: bUserID, Username: "b-staff", DisplayName: "B 公司的人",
		PasswordHash: auth.HashPassword("Passw0rd2026x"),
	}); err != nil {
		t.Fatalf("建 B 公司的用户失败：%v", err)
	}

	// A 公司：管理员一枚
	a.createUser(t, "a-admin", "Passw0rd2026x", "admin")
	session, rec := a.login(t, "a-admin", "Passw0rd2026x")
	if session == nil {
		t.Fatalf("A 公司管理员应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}

	t.Run("列表看不到别家的人", func(t *testing.T) {
		rec := a.call(t, http.MethodGet, "/api/v1/users?page_size=100", nil, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("列用户应该 200，得到 %d：%s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), bUserID.String()) ||
			strings.Contains(rec.Body.String(), "b-staff") {
			t.Fatalf("A 的用户列表里出现了 B 公司的人：%s", rec.Body)
		}
	})

	t.Run("按 id 查别家的人是 404 不是 403", func(t *testing.T) {
		// 跨租户访问要表现成「不存在」（§11.2）：回 403 等于确认了这个 id 真的存在，
		// 那就成了一个可以拿来枚举的存在性探针。
		rec := a.call(t, http.MethodGet, "/api/v1/users/"+bUserID.String(), nil, session)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("查别家的人应该 404，得到 %d：%s", rec.Code, rec.Body)
		}
		if code := decodeProblem(t, rec).Code; code != errs.NotFound.Code {
			t.Errorf("错误码应是 %s，得到 %s", errs.NotFound.Code, code)
		}
	})

	t.Run("删别家的人删不掉", func(t *testing.T) {
		rec := a.call(t, http.MethodDelete, "/api/v1/users/"+bUserID.String(),
			map[string]int{"version": 0}, session)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("删别家的人应该 404，得到 %d：%s", rec.Code, rec.Body)
		}
		// 光看状态码不够 —— 回头确认那个人**真的还在**
		if _, err := qb.GetUserByID(t.Context(), bUserID); err != nil {
			t.Fatalf("B 公司的人被删掉了：%v", err)
		}
	})

	t.Run("登录时公司代码认错人也进不去", func(t *testing.T) {
		// 用 A 的公司代码 + B 的账号：账号在 A 里不存在，必须失败，
		// 而且回应要和「密码错误」一模一样，不能泄露「这个账号在别家存在」。
		rec := a.call(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"tenant_code": a.tenant.Code,
			"account":     "b-staff",
			"password":    "Passw0rd2026x",
		}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("拿别家的账号登本家应该 401，得到 %d：%s", rec.Code, rec.Body)
		}
		if code := decodeProblem(t, rec).Code; code != errs.InvalidCredentials.Code {
			t.Errorf("错误码应是 %s，得到 %s", errs.InvalidCredentials.Code, code)
		}
	})

	t.Run("公司代码不存在的回应和密码错误一样", func(t *testing.T) {
		// 不一样的话就成了「这家公司是不是你们客户」的探测接口（§7.6）
		rec := a.call(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"tenant_code": "no-such-company",
			"account":     "a-admin",
			"password":    "Passw0rd2026x",
		}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("公司代码不存在应该 401，得到 %d：%s", rec.Code, rec.Body)
		}
		if code := decodeProblem(t, rec).Code; code != errs.InvalidCredentials.Code {
			t.Errorf("错误码应是 %s，得到 %s", errs.InvalidCredentials.Code, code)
		}
	})
}

// TestSuspendedTenantKillsLiveSessions 是 §8.2 的守门测试。
//
// ⚠️ review 时实测出来的问题：停用一个租户**只挡住了新登录**，
// 已经发出去的 cookie 照样能查数据。也就是说平台端显示「已停用」，
// 而客户的人能一直用到 cookie 过期 —— 比明着没做更危险，因为它看起来生效了。
//
// 现在每次认证都会核一遍租户状态，所以下一个请求就失效，
// 连绕过应用直接改库的停用也照样生效。
func TestSuspendedTenantKillsLiveSessions(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "staff", "Passw0rd2026x", "admin")

	session, rec := a.login(t, "staff", "Passw0rd2026x")
	if session == nil {
		t.Fatalf("应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}
	if rec := a.call(t, http.MethodGet, "/api/v1/users", nil, session); rec.Code != http.StatusOK {
		t.Fatalf("停用之前应该查得到，得到 %d", rec.Code)
	}

	// 停用这个组织（平台端界面是第 ⑤ 步，这里直接改库 —— 顺带也验了「改库也挡得住」）
	if _, err := a.pool.Exec(t.Context(),
		`UPDATE tenants SET status = 'suspended' WHERE id = $1`, a.tenant.ID); err != nil {
		t.Fatal(err)
	}

	rec = a.call(t, http.MethodGet, "/api/v1/users", nil, session)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("组织停用之后旧会话就该失效，得到 %d：%s", rec.Code, rec.Body)
	}
	if code := decodeProblem(t, rec).Code; code != errs.TenantSuspended.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.TenantSuspended.Code, code)
	}

	// 重新启用之后要能完全恢复。
	//
	// ⚠️ 这一段是二轮 review 用探针试出来的：权限策略原来只加载「启用中的租户」，
	// 于是停用 → 策略里没了 → 重新启用**没有任何东西会触发重载**
	// （authz_changed 只挂在角色相关的表上），那家公司的人能登录但每个请求都 403，
	// 只能重启服务才恢复。现在策略和配置都遍历**全部**租户。
	if _, err := a.pool.Exec(t.Context(),
		`UPDATE tenants SET status = 'active' WHERE id = $1`, a.tenant.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec := a.call(t, http.MethodGet, "/api/v1/users", nil, session); rec.Code != http.StatusOK {
		t.Fatalf("重新启用之后旧会话应该完全恢复，得到 %d：%s", rec.Code, rec.Body)
	}

	// 再停一次，验后面那段「重新登录」的前提
	if _, err := a.pool.Exec(t.Context(),
		`UPDATE tenants SET status = 'suspended' WHERE id = $1`, a.tenant.ID); err != nil {
		t.Fatal(err)
	}

	// 重新登录也进不去，而且**不能**告诉他是「组织停用」——
	// 登录失败的三种原因必须给一样的回应，否则就成了客户名单探测器（§4.1、§7.6）
	_, rec = a.login(t, "staff", "Passw0rd2026x")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("组织停用后重新登录应该 401，得到 %d：%s", rec.Code, rec.Body)
	}
	if code := decodeProblem(t, rec).Code; code != errs.InvalidCredentials.Code {
		t.Errorf("登录失败必须是通用的 %s，不能泄露组织状态，得到 %s",
			errs.InvalidCredentials.Code, code)
	}
}

// TestIdempotencyKeyIsScopedPerTenant 是 §10.6 的守门测试。
//
// 幂等键是**客户端自己取的字符串**。不带租户的话，A 公司用过的键
// B 公司再用就会被当成重放直接 409 —— 拿不到对方的结果，但能搅黄对方的请求。
func TestIdempotencyKeyIsScopedPerTenant(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "a-admin", "Passw0rd2026x", "admin")
	sessionA, _ := a.login(t, "a-admin", "Passw0rd2026x")

	headers := map[string]string{middleware.HeaderIdempotencyKey: "same-key"}
	body := map[string]any{"username": "u1", "display_name": "U1"}
	if rec := a.callWithHeaders(t, http.MethodPost, "/api/v1/users", body, sessionA, headers); rec.Code != http.StatusCreated {
		t.Fatalf("A 公司建人应该成功，得到 %d：%s", rec.Code, rec.Body)
	}
	// 同一个人再用同一个键 —— 这才是幂等键该拦的
	if rec := a.callWithHeaders(t, http.MethodPost, "/api/v1/users", body, sessionA, headers); rec.Code != http.StatusConflict {
		t.Fatalf("同一个人重放同一个键应该 409，得到 %d", rec.Code)
	}

	// B 公司用同一个键，不该受影响
	other := testdb.NewTenant(t, a.pool, 1)
	qb := a.store.ForTenant(other.ID)
	if _, err := qb.CreateUser(t.Context(), repo.CreateUserArgs{
		ID: uuid.New(), Username: "b-admin", DisplayName: "B 管理员",
		PasswordHash: auth.HashPassword("Passw0rd2026x"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := qb.AssignUserRole(t.Context(), repo.AssignUserRoleArgs{
		UserID: mustUserID(t, qb, "b-admin"), RoleID: other.AdminRoleID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}

	a.tenant = other // login 用它拼公司代码
	sessionB, rec := a.login(t, "b-admin", "Passw0rd2026x")
	if sessionB == nil {
		t.Fatalf("B 公司应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}
	rec = a.callWithHeaders(t, http.MethodPost, "/api/v1/users", body, sessionB, headers)
	if rec.Code != http.StatusCreated {
		t.Fatalf("B 公司用同一个幂等键应该照样能建人，得到 %d：%s —— 幂等键没带租户", rec.Code, rec.Body)
	}
}

func mustUserID(t *testing.T, q *repo.TenantQueries, username string) uuid.UUID {
	t.Helper()
	users, err := q.ListUsers(t.Context(), repo.ListUsersArgs{Limit: 100, DepartmentIds: []uuid.UUID{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.User.Username == username {
			return u.User.ID
		}
	}
	t.Fatalf("没找到用户 %s", username)
	return uuid.Nil
}

// TestPlatformAuditChainIsSeparate 验平台级那条链（§7.1、§10.3）。
//
// 未认证请求（公司代码填错的登录）没有租户，它们的审计 tenant_id 是 NULL，
// 走的是**另一条**哈希链。两件事要成立：
//  1. 那条链自己能验得过
//  2. 租户查自己的审计时看不到它们
func TestPlatformAuditChainIsSeparate(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "somebody", "Passw0rd2026x", "admin")

	// 公司代码填错 —— 这条审计的 tenant_id 是 NULL
	a.call(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"tenant_code": "no-such-company",
		"account":     "somebody",
		"password":    "Passw0rd2026x",
	}, nil)
	// 公司代码对、密码错 —— 这条记在本租户名下（§7.1）
	a.login(t, "somebody", "wrong-password")
	session, _ := a.login(t, "somebody", "Passw0rd2026x")

	platformBroken, platformChecked, err := audit.VerifyPlatformChain(t.Context(), a.store.Unscoped())
	if err != nil {
		t.Fatalf("验平台审计链失败：%v", err)
	}
	if platformChecked == 0 {
		t.Fatal("公司代码填错的那次登录应该留下一条平台级审计")
	}
	if platformBroken != nil {
		t.Fatalf("平台链不该有断点，报了 %s", platformBroken)
	}

	tenantBroken, tenantChecked, err := audit.VerifyChain(t.Context(), a.q)
	if err != nil {
		t.Fatalf("验租户审计链失败：%v", err)
	}
	if tenantBroken != nil {
		t.Fatalf("租户链不该有断点，报了 %s", tenantBroken)
	}
	if tenantChecked == 0 {
		t.Fatal("租户应该有自己的审计")
	}

	// 租户从接口查审计，看不到平台级那批
	rec := a.call(t, http.MethodGet, "/api/v1/audit-logs?page_size=100", nil, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("查审计应该 200，得到 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "no-such-company") {
		t.Fatal("租户看到了平台级的审计记录（公司代码填错的那次）")
	}
}

// TestUniqueConflictsStayFriendly 是 §8.3 的守门测试。
//
// `repo.IsUniqueViolation(err, "uk_users_username")` 是**按索引名精确匹配的字符串常量**。
// 多租户这一轮把这些索引全重建成了带 tenant_id 的版本 —— 名字要是顺手改了，
// 所有翻译一起失效，用户看到的从「这个用户名已经被占用」退化成一个通用 500，
// 而编译期什么都查不出来。
//
// `make lint-sql` 会静态核对索引名存不存在；这一条从接口层确认**翻译真的还在生效**。
func TestUniqueConflictsStayFriendly(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "boss", "Passw0rd2026x", "admin")
	session, _ := a.login(t, "boss", "Passw0rd2026x")

	cases := []struct {
		name string
		path string
		body map[string]any
		code string
	}{
		{
			name: "用户名重复",
			path: "/api/v1/users",
			body: map[string]any{"username": "boss", "display_name": "撞名字的"},
			code: "user.username_taken",
		},
		{
			name: "部门编号重复",
			path: "/api/v1/departments",
			body: map[string]any{"name": "研发二部", "code": "RD"},
			code: "department.code_taken",
		},
	}

	// 先占好位
	if rec := a.call(t, http.MethodPost, "/api/v1/departments",
		map[string]any{"name": "研发部", "code": "RD"}, session); rec.Code != http.StatusCreated {
		t.Fatalf("建部门应该成功，得到 %d：%s", rec.Code, rec.Body)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := a.call(t, http.MethodPost, c.path, c.body, session)
			if rec.Code >= http.StatusInternalServerError {
				t.Fatalf("唯一冲突退化成了 %d —— 索引名和 IsUniqueViolation 对不上了：%s",
					rec.Code, rec.Body)
			}
			if got := decodeProblem(t, rec).Code; got != c.code {
				t.Errorf("错误码应是 %s，得到 %s：%s", c.code, got, rec.Body)
			}
		})
	}
}

// ---------------------------------------------------------------- 平台管理端（第 ⑤ 步）

// platformLogin 用平台管理员登录，返回它自己那套会话状态。
func (a *liveApp) platformLogin(t *testing.T, username, password string) (*sessionState, *httptest.ResponseRecorder) {
	t.Helper()
	rec := a.call(t, http.MethodPost, "/api/v1/platform/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, nil)
	if rec.Code != http.StatusOK {
		return nil, rec
	}
	var body struct {
		Data struct {
			CSRFToken string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("平台登录响应不是合法 JSON：%v", err)
	}
	return &sessionState{cookies: rec.Result().Cookies(), csrf: body.Data.CSRFToken}, rec
}

// bootstrapPlatformAdmin 引导首个平台管理员并把强制改密解掉，方便后面的用例。
func (a *liveApp) bootstrapPlatformAdmin(t *testing.T) (string, string) {
	t.Helper()
	result, err := auth.BootstrapPlatform(t.Context(), a.store)
	if err != nil {
		t.Fatalf("引导平台管理员失败：%v", err)
	}
	if !result.Created {
		t.Fatal("库里本来就有平台管理员，测试数据没清干净")
	}
	// 首次登录强制改密会挡住别的接口，这里直接解掉 —— 那条链路另有用例覆盖
	// tenant-exempt: platform_admins 是平台级表，本来就没有 tenant_id
	if _, err := a.pool.Exec(t.Context(),
		`UPDATE platform_admins SET must_change_password = false WHERE username = $1`,
		result.Username); err != nil {
		t.Fatal(err)
	}
	return result.Username, result.Password
}

// TestPlatformOpensTenantEndToEnd 是第 ⑤ 步的验收标准：
//
//	**开一家新公司，并把凭据交付给客户。**
//
// 走完整条路：平台管理员登录 → 开组织 → 拿到一次性凭据 →
// 客户拿着「公司代码 + admin + 密码」登进自己的组织 → 被要求改密。
func TestPlatformOpensTenantEndToEnd(t *testing.T) {
	a := newLiveApp(t)
	username, password := a.bootstrapPlatformAdmin(t)

	session, rec := a.platformLogin(t, username, password)
	if session == nil {
		t.Fatalf("平台管理员应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}

	rec = a.call(t, http.MethodPost, "/api/v1/platform/tenants",
		map[string]string{"code": "newco", "name": "新公司"}, session)
	if rec.Code != http.StatusCreated {
		t.Fatalf("开组织应该 201，得到 %d：%s", rec.Code, rec.Body)
	}

	var created struct {
		Data struct {
			Tenant        struct{ Code, Name, Status string }
			AdminUsername string `json:"admin_username"`
			AdminPassword string `json:"admin_password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.AdminPassword == "" {
		t.Fatal("没拿到一次性初始密码 —— 客户就登不进去了")
	}

	// 权限策略要重载：新组织的内置角色是刚建出来的
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}

	// 客户拿着凭据登进自己的组织
	a.tenant.Code = "newco"
	tenantSession, rec := a.login(t, created.Data.AdminUsername, created.Data.AdminPassword)
	if tenantSession == nil {
		t.Fatalf("客户拿着交付的凭据应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}

	// ⚠️ 初始密码经过了两个人的手（平台管理员 → 客户），必须强制改密
	rec = a.call(t, http.MethodGet, "/api/v1/users", nil, tenantSession)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("首次登录应该被卡在改密码，得到 %d：%s", rec.Code, rec.Body)
	}
	if code := decodeProblem(t, rec).Code; code != errs.MustChangePassword.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.MustChangePassword.Code, code)
	}
}

// 租户管理员不能把自己的安全策略调到平台的下限以下（MULTI-TENANCY.md §10.5）。
//
// 不拦的话，租户把密码策略调到 1 位、锁定时间调到 0 —— 他给自己公司挖坑，
// 但出了事是**平台的品牌**在承担。这是 SaaS 的常规做法。
//
// 护栏在 Settings.Set 上，那是租户级配置唯一的写入口：
// 将来配置管理页面接上来时自动受保护，那个页面不用记得自己校验一遍。
func TestTenantCannotEscapePlatformBounds(t *testing.T) {
	a := newLiveApp(t)
	ctx := t.Context()

	rejected := []struct {
		name  string
		key   string
		value any
	}{
		{"密码短到形同虚设", config.KeyPasswordMinLength, 1},
		{"密码长到没人记得住", config.KeyPasswordMinLength, 999},
		{"连错多少次都不锁", config.KeyLoginMaxFailures, 100},
		{"锁一秒等于没锁", config.KeyLoginLockMinutes, 0},
		// ⚠️ 配置页面接上来之后，值是从 JSON body 里解出来的 —— 那是 float64，不是 int。
		// 校验只认 int 的话，**恰恰是租户改配置的那条路会静默跳过校验**
		{"从 JSON 解出来的值", config.KeyPasswordMinLength, float64(1)},
	}
	for _, c := range rejected {
		t.Run(c.name, func(t *testing.T) {
			if err := a.settings.Set(ctx, a.tenant.ID, c.key, c.value, nil); err == nil {
				t.Fatalf("%s = %v 应该被拒", c.key, c.value)
			}
		})
	}

	// 区间内的照样能改 —— 这不是把租户级配置变成摆设
	if err := a.settings.Set(ctx, a.tenant.ID, config.KeyPasswordMinLength, 16, nil); err != nil {
		t.Fatalf("区间内的值应该能设，得到 %v", err)
	}
	if got := a.settings.Security(a.tenant.ID).PasswordMinLength; got != 16 {
		t.Errorf("设完应该立即生效，得到 %d", got)
	}

	// 区间本身是平台设置，**平台收紧一档，租户立刻受新的约束** —— 不用发版
	if err := a.settings.SetPlatform(ctx, "limits."+config.KeyPasswordMinLength+".min", 20, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.settings.Set(ctx, a.tenant.ID, config.KeyPasswordMinLength, 16, nil); err == nil {
		t.Error("平台把下限提到 20 之后，16 就不该再设得上了")
	}
}

// TestPlatformCannotTouchTenantData 是 §6 / §10.11 那句话的守门测试：
//
//	**平台管理员开组织、停组织，但结构上碰不到客户的业务数据。**
//
// 这句话是将来跟客户解释隔离时最有力的一句，前提是它真的成立 ——
// 而它成立靠的是三件事：平台服务只拿得到 Store.Platform()（编译期）、
// 认证按路径选会话套（一进门）、授权中间件那条 Realm 对齐（兜底）。
// 这里测的是**运行期从外面打过来会怎样**，也就是后两条合起来的效果。
//
// 期望的是 401 而不是 403：认证中间件按请求路径决定认哪套会话，
// 于是一边的会话到了另一边就是「没带凭据」，而不是「带了但不够格」。
// 401 也确实是更该给的那个 —— 403 等于告诉对方「你的身份我认出来了」，
// 而且前端拿到 401 会按路径把人送去**对应的**登录页。
//
// Realm 对齐那条没在这里显形（它守的是「已经认出主体但域不对」），
// 单独由 internal/middleware/realm_test.go 直接测中间件，理由见那边的注释。
func TestPlatformCannotTouchTenantData(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "staff", "Passw0rd2026x", "admin")

	username, password := a.bootstrapPlatformAdmin(t)
	platformSession, rec := a.platformLogin(t, username, password)
	if platformSession == nil {
		t.Fatalf("平台管理员应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}

	// 拿平台会话去打租户的业务接口 —— 一个都不该通
	for _, path := range []string{"/api/v1/users", "/api/v1/roles", "/api/v1/departments", "/api/v1/audit-logs"} {
		rec := a.call(t, http.MethodGet, path, nil, platformSession)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("平台管理员打 %s 应该 401，得到 %d：%s", path, rec.Code, rec.Body)
		}
	}

	// 反过来：租户用户（哪怕是拿通配权限的 admin）也打不了平台接口
	tenantSession, _ := a.login(t, "staff", "Passw0rd2026x")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/platform/tenants", nil},
		{http.MethodPost, "/api/v1/platform/tenants", map[string]string{"code": "evil", "name": "坏人"}},
	} {
		rec := a.call(t, c.method, c.path, c.body, tenantSession)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("租户 admin 打 %s 应该 401，得到 %d：%s —— "+
				"这个 admin 手里是通配 *:*，对任何资源名都成立；"+
				"平台接口拦不住他的话，他就能给自己开组织", c.path, rec.Code, rec.Body)
		}
	}

	// ⚠️ 光看状态码不够：**回头确认那个组织真的没被建出来**。
	// 状态码可能来自下游的某一层，而我们要证的是「handler 根本没跑」。
	// tenant-exempt: tenants 是平台级表，本来就没有 tenant_id
	var evil int
	if err := a.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM tenants WHERE code = 'evil'`).Scan(&evil); err != nil {
		t.Fatal(err)
	}
	if evil != 0 {
		t.Fatal("租户 admin 居然真的开出了一个组织")
	}
}

// TestPlatformSuspendTakesEffectImmediately 验「停用立刻生效」（§8.2）。
func TestPlatformSuspendTakesEffectImmediately(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "staff", "Passw0rd2026x", "admin")
	tenantSession, _ := a.login(t, "staff", "Passw0rd2026x")

	username, password := a.bootstrapPlatformAdmin(t)
	platformSession, _ := a.platformLogin(t, username, password)

	rec := a.call(t, http.MethodPost,
		"/api/v1/platform/tenants/"+a.tenant.ID.String()+"/status",
		map[string]any{"status": "suspended", "version": 0}, platformSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("停用组织应该 200，得到 %d：%s", rec.Code, rec.Body)
	}

	// 那家公司**已经登录**的人下一个请求就该失效
	rec = a.call(t, http.MethodGet, "/api/v1/users", nil, tenantSession)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("组织停用后旧会话应该立刻失效，得到 %d：%s", rec.Code, rec.Body)
	}
	if code := decodeProblem(t, rec).Code; code != errs.TenantSuspended.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.TenantSuspended.Code, code)
	}

	// 平台管理员自己不受影响 —— 他不属于任何组织
	if rec := a.call(t, http.MethodGet, "/api/v1/platform/tenants", nil, platformSession); rec.Code != http.StatusOK {
		t.Fatalf("平台管理员不该被组织停用影响，得到 %d：%s", rec.Code, rec.Body)
	}
}

// TestPlatformFirstLoginCanUnlockItself 是「首次登录强制改密」那条链路的守门测试。
//
// ⚠️ 浏览器实测踩到过一个**死锁**：平台管理员首次登录被要求改密，
// 而「必须改密」的放行清单里只有租户端那两个 operation id ——
// 于是改密接口本身也被挡住，他连改都改不了，只能上数据库救。
//
// 这条测试盯的就是那份清单：平台端的改密和退出登录必须在里面。
func TestPlatformFirstLoginCanUnlockItself(t *testing.T) {
	a := newLiveApp(t)

	result, err := auth.BootstrapPlatform(t.Context(), a.store)
	if err != nil {
		t.Fatal(err)
	}
	session, rec := a.platformLogin(t, result.Username, result.Password)
	if session == nil {
		t.Fatalf("首次登录应该能登进来（只是要改密），得到 %d：%s", rec.Code, rec.Body)
	}

	// 别的接口都该被挡在改密这一步
	if rec := a.call(t, http.MethodGet, "/api/v1/platform/tenants", nil, session); rec.Code != http.StatusForbidden {
		t.Fatalf("没改密之前不该能开组织，得到 %d", rec.Code)
	}

	// 但改密本身必须走得通 —— 平台的密码要求比租户严（至少 12 位 + 混合）
	rec = a.call(t, http.MethodPost, "/api/v1/platform/me/password", map[string]string{
		"old_password": result.Password,
		"new_password": "Platform2026Admin",
	}, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("改密接口必须放行，否则首次登录就是死锁，得到 %d：%s", rec.Code, rec.Body)
	}

	// 改完就能干活了。⚠️ 改密会把其它会话踢掉，所以要重新登录
	session, rec = a.platformLogin(t, result.Username, "Platform2026Admin")
	if session == nil {
		t.Fatalf("改完密码应该能用新密码登录，得到 %d：%s", rec.Code, rec.Body)
	}
	if rec := a.call(t, http.MethodGet, "/api/v1/platform/tenants", nil, session); rec.Code != http.StatusOK {
		t.Fatalf("改完密码应该能开组织了，得到 %d：%s", rec.Code, rec.Body)
	}
}

// TestPlatformPasswordPolicyIsStricter 验平台密码**不吃租户级策略**（§9.2）。
func TestPlatformPasswordPolicyIsStricter(t *testing.T) {
	a := newLiveApp(t)

	// 把这个租户的密码策略放到最松 —— 平台不该受影响
	// tenant-exempt: 造数据，租户 id 显式传进去了
	if _, err := a.pool.Exec(t.Context(), `
		INSERT INTO settings (tenant_id, key, value) VALUES ($1, $2, '1'::jsonb), ($1, $3, 'false'::jsonb)`,
		a.tenant.ID, "security.password_min_length", "security.password_require_mix"); err != nil {
		t.Fatal(err)
	}
	if err := a.settings.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}

	result, err := auth.BootstrapPlatform(t.Context(), a.store)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := a.platformLogin(t, result.Username, result.Password)

	rec := a.call(t, http.MethodPost, "/api/v1/platform/me/password", map[string]string{
		"old_password": result.Password,
		"new_password": "short",
	}, session)
	if rec.Code == http.StatusOK {
		t.Fatal("平台管理员用了一个 5 位纯小写的密码 —— 它吃了租户放松过的策略")
	}
}

// TestBothSessionsCoexistInOneBrowser 验「同一个浏览器里能同时登着两边」。
//
// 两套 cookie 名不同就是为了这个（§10.1）。⚠️ 浏览器实测踩到过：
// CSRF 中间件原来**按当前主体**挑该用哪套配置 —— 于是一个人登着平台端再去登租户时，
// 登录请求（公开接口）带着平台 cookie，被拿平台那套校验，请求头里当然没有
// 平台的 CSRF token，直接 403「请求校验失败，请刷新页面」，刷多少次都没用。
//
// 现在按**请求打向哪一套接口**挑配置。
func TestBothSessionsCoexistInOneBrowser(t *testing.T) {
	a := newLiveApp(t)
	a.createUser(t, "staff", "Passw0rd2026x", "admin")

	username, password := a.bootstrapPlatformAdmin(t)
	platformSession, rec := a.platformLogin(t, username, password)
	if platformSession == nil {
		t.Fatalf("平台管理员应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}

	// 揣着平台会话去登租户 —— 这就是同一个浏览器的情形。
	//
	// ⚠️ **不带任何 CSRF 头**，这一点是关键：真实浏览器此刻手上只有平台那套
	// CSRF cookie，租户那套还不存在（还没登过）。前端按目标接口挑 cookie，
	// 挑到的租户 CSRF cookie 是空的，所以请求头也是空的。
	// 后端要是按主体挑配置，就会拿平台那套去校验一个空头 —— 直接 403。
	// 再塞一张**过期的租户会话 cookie**：真实浏览器里常有 —— 上次登过、库重建了、
	// 或者会话被踢了，cookie 还留着。认证中间件会先认出平台身份，
	// 而这个请求打的是租户接口；拿平台的会话 id 去验租户的 CSRF token 必然对不上。
	browserCookies := &sessionState{cookies: append(
		append([]*http.Cookie{}, platformSession.cookies...),
		&http.Cookie{Name: "fries_session", Value: "stale-token-from-last-time"},
	)}
	rec = a.callWithHeaders(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"tenant_code": a.tenant.Code,
		"account":     "staff",
		"password":    "Passw0rd2026x",
	}, browserCookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("揣着平台会话登租户应该照样成功，得到 %d：%s —— "+
			"CSRF 是不是按主体而不是按目标接口挑的配置？", rec.Code, rec.Body)
	}

	// 两套 cookie 名必须不同，否则后登的会把先登的顶掉
	names := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		names[c.Name] = true
	}
	// 租户登录发下来的 cookie 里，**不能有平台那套的名字** ——
	// 同名的话后登的会把先登的顶掉，两边都莫名其妙掉线（§10.1）
	for _, c := range platformSession.cookies {
		if names[c.Name] && c.Value != "" {
			t.Fatalf("两套会话共用了 cookie 名 %q —— 同一浏览器里会互相踢掉", c.Name)
		}
	}
}
