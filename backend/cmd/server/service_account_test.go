package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
	sasvc "github.com/ramoncjs3/fries/internal/service/service_account"
)

// 机器账号（Service Account）管理。
//
// 这一组的重点不是 CRUD 通不通，是**凭据的生命周期**：
//
//	密钥只出现一次      列表和详情里既没有明文也没有哈希
//	停用/删除/过期      立刻断开，不用等缓存
//	换角色              立刻改变它能干什么 ← 这条曾经是断的，见下
//	轮换                旧密钥当场失效
//	跨租户              读写都够不着别家的

// callAPIKey 拿 API Key 调一个接口。
func (a *liveApp) callAPIKey(t *testing.T, method, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	req.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	a.echo.ServeHTTP(rec, req)
	return rec
}

// createdKey 解出新建/轮换返回的一次性密钥。
func createdKey(t *testing.T, rec *httptest.ResponseRecorder) sasvc.CreatedKey {
	t.Helper()
	var out struct {
		Data sasvc.CreatedKey `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解响应失败：%v\n%s", err, rec.Body)
	}
	if out.Data.Key == "" {
		t.Fatalf("响应里没有密钥：%s", rec.Body)
	}
	return out.Data
}

// newServiceAccount 走接口建一个机器账号，返回它和一次性密钥。
func (a *liveApp) newServiceAccount(t *testing.T, session *sessionState, name, roleKey string) sasvc.CreatedKey {
	t.Helper()
	role, err := a.q.GetRoleByKey(t.Context(), roleKey)
	if err != nil {
		t.Fatalf("找角色 %s 失败：%v", roleKey, err)
	}
	rec := a.call(t, http.MethodPost, "/api/v1/service-accounts", map[string]any{
		"name": name, "description": "集成测试用", "role_id": role.ID.String(),
	}, session)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建机器账号应该 201，得到 %d：%s", rec.Code, rec.Body)
	}

	// ⚠️ 测试里**没有起 LISTEN 协程**（那是 cmd/server 的 watchChanges 干的），
	// 所以 authz_changed 通知没人接，策略不会自己刷新 —— 新建的机器账号
	// 在 enforcer 里还没有角色绑定，拿它调任何接口都是 403。
	// 真实运行时是通知驱动的，这里手动补一次。
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatalf("刷新策略失败：%v", err)
	}
	return createdKey(t, rec)
}

// TestServiceAccountKeyAppearsExactlyOnce 是这个模块最要紧的一条。
//
// 密钥能被取回 = 明文存储。所以：新建时给一次，之后**任何接口都拿不到** ——
// 列表、详情里既没有明文也没有哈希。
func TestServiceAccountKeyAppearsExactlyOnce(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	created := a.newServiceAccount(t, session, "对接系统", "admin")
	secret := created.Key

	if !strings.HasPrefix(secret, "fsa_") {
		t.Fatalf("密钥格式应该是 fsa_<prefix>_<secret>，得到 %q", secret)
	}

	for _, path := range []string{
		"/api/v1/service-accounts?page_size=100",
		"/api/v1/service-accounts/" + created.Account.ID.String(),
	} {
		t.Run(path, func(t *testing.T) {
			rec := a.call(t, http.MethodGet, path, nil, session)
			if rec.Code != http.StatusOK {
				t.Fatalf("应该 200，得到 %d：%s", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			if strings.Contains(body, secret) {
				t.Fatal("响应里出现了完整密钥 —— 它只该在新建那一次出现")
			}
			// 哈希同样不能出去：拿到哈希就能离线爆破
			if strings.Contains(body, "key_hash") {
				t.Fatalf("响应里出现了 key_hash：%s", body)
			}
			// 但 prefix 要有 —— 对接方报障时得能对上号
			if !strings.Contains(body, created.Account.KeyPrefix) {
				t.Errorf("响应里应该有 key_prefix 好让人认出是哪一个：%s", body)
			}
		})
	}
}

// TestServiceAccountRoleChangeTakesEffect 是**这一轮修的那个洞的守门测试**。
//
// `service_accounts` 上原来没有 authz_changed 触发器（00003 只给 roles /
// role_permissions / user_roles 挂了）。于是给机器账号换角色之后，enforcer 里
// 还是旧绑定 —— **降权不生效**，那个对接凭据会以旧权限一直跑到下次有人改角色为止。
//
// ⚠️ **这一条测的是「策略重载之后语义对不对」，不是「触发器有没有发通知」。**
// 测试里没有起 LISTEN 协程（那是 cmd/server 的 watchChanges 干的），
// 所以得显式 Reload —— 也就是说**这条用例有没有触发器都会绿**。
// 触发器本身由 TestServiceAccountAuthzNotify 直接测。两条都要有。
func TestServiceAccountRoleChangeTakesEffect(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	// 先建一个受限角色：只能看审计，不能看用户
	limited := a.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"key": "auditor", "name": "只读审计", "data_scope": "all",
		"permissions": []string{"audit:list"},
	}, session)
	if limited.Code != http.StatusCreated {
		t.Fatalf("建角色应该 201，得到 %d：%s", limited.Code, limited.Body)
	}

	created := a.newServiceAccount(t, session, "对接系统", "admin")

	t.Run("admin 角色下能查用户", func(t *testing.T) {
		if rec := a.callAPIKey(t, http.MethodGet, "/api/v1/users", created.Key); rec.Code != http.StatusOK {
			t.Fatalf("应该 200，得到 %d：%s", rec.Code, rec.Body)
		}
	})

	// 降权：换成只读审计
	auditorRole, err := a.q.GetRoleByKey(t.Context(), "auditor")
	if err != nil {
		t.Fatal(err)
	}
	rec := a.call(t, http.MethodPut, "/api/v1/service-accounts/"+created.Account.ID.String(),
		map[string]any{
			"name": "对接系统", "description": "集成测试用",
			"role_id": auditorRole.ID.String(), "status": "active",
			"version": created.Account.Version,
		}, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("改角色应该 200，得到 %d：%s", rec.Code, rec.Body)
	}
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatalf("刷新策略失败：%v", err)
	}

	t.Run("降权之后立刻生效", func(t *testing.T) {
		rec := a.callAPIKey(t, http.MethodGet, "/api/v1/users", created.Key)
		if rec.Code == http.StatusOK {
			t.Fatal("已经降成只读审计了，还能查用户 —— 策略重载之后语义不对")
		}
	})

	t.Run("新角色该有的权限还在", func(t *testing.T) {
		if rec := a.callAPIKey(t, http.MethodGet, "/api/v1/audit-logs", created.Key); rec.Code != http.StatusOK {
			t.Fatalf("只读审计角色应该查得了审计，得到 %d：%s", rec.Code, rec.Body)
		}
	})
}

// TestServiceAccountLifecycleCutsAccessOff 守「停用 / 删除 / 过期立刻断开」。
//
// 这三条都在认证那一步查（GetServiceAccountByPrefix + AuthenticateAPIKey），
// 不依赖任何缓存 —— 所以连绕过应用直接改库也照样生效。
func TestServiceAccountLifecycleCutsAccessOff(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	t.Run("停用", func(t *testing.T) {
		created := a.newServiceAccount(t, session, "会被停用的", "admin")
		rec := a.call(t, http.MethodPut, "/api/v1/service-accounts/"+created.Account.ID.String(),
			map[string]any{
				"name": "会被停用的", "role_id": created.Account.RoleID.String(),
				"status": "disabled", "version": created.Account.Version,
			}, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("停用应该 200，得到 %d：%s", rec.Code, rec.Body)
		}
		if got := a.callAPIKey(t, http.MethodGet, "/api/v1/audit-logs", created.Key); got.Code != http.StatusUnauthorized {
			t.Fatalf("停用之后应该 401，得到 %d：%s", got.Code, got.Body)
		}
	})

	t.Run("删除", func(t *testing.T) {
		created := a.newServiceAccount(t, session, "会被删除的", "admin")
		rec := a.call(t, http.MethodDelete, "/api/v1/service-accounts/"+created.Account.ID.String(),
			map[string]int{"version": created.Account.Version}, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("删除应该 200，得到 %d：%s", rec.Code, rec.Body)
		}
		if got := a.callAPIKey(t, http.MethodGet, "/api/v1/audit-logs", created.Key); got.Code != http.StatusUnauthorized {
			t.Fatalf("删除之后应该 401，得到 %d：%s", got.Code, got.Body)
		}
	})

	t.Run("过期", func(t *testing.T) {
		created := a.newServiceAccount(t, session, "会过期的", "admin")
		// 过期时间只能往未来填（接口拦了往过去填），所以这里直接改库模拟「时间到了」。
		//
		// ⚠️ 带上租户条件不是形式主义：运行期兜底会核每一条发出去的 SQL
		// （MULTI-TENANCY.md §12.2），漏了当场 panic —— 写这条时就被它抓了一次。
		if _, err := a.pool.Exec(t.Context(),
			`UPDATE service_accounts SET expires_at = now() - interval '1 hour'
			 WHERE tenant_id = $1 AND id = $2`,
			a.tenant.ID, created.Account.ID); err != nil {
			t.Fatal(err)
		}
		if got := a.callAPIKey(t, http.MethodGet, "/api/v1/audit-logs", created.Key); got.Code != http.StatusUnauthorized {
			t.Fatalf("过期之后应该 401，得到 %d：%s", got.Code, got.Body)
		}
	})
}

// TestServiceAccountKeyRotation 守轮换：新的能用、旧的当场失效。
//
// 泄露之后要的就是这个 —— 换一串继续跑，不用重建记录、不用重配权限。
func TestServiceAccountKeyRotation(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	created := a.newServiceAccount(t, session, "对接系统", "admin")
	if got := a.callAPIKey(t, http.MethodGet, "/api/v1/audit-logs", created.Key); got.Code != http.StatusOK {
		t.Fatalf("新建的密钥应该能用，得到 %d", got.Code)
	}

	rec := a.call(t, http.MethodPost,
		"/api/v1/service-accounts/"+created.Account.ID.String()+"/rotate-key", nil, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("轮换应该 200，得到 %d：%s", rec.Code, rec.Body)
	}
	rotated := createdKey(t, rec)

	if rotated.Key == created.Key {
		t.Fatal("轮换出来的密钥和原来一样 —— 那等于没换")
	}
	t.Run("新密钥能用", func(t *testing.T) {
		if got := a.callAPIKey(t, http.MethodGet, "/api/v1/audit-logs", rotated.Key); got.Code != http.StatusOK {
			t.Fatalf("应该 200，得到 %d：%s", got.Code, got.Body)
		}
	})
	t.Run("旧密钥当场失效", func(t *testing.T) {
		if got := a.callAPIKey(t, http.MethodGet, "/api/v1/audit-logs", created.Key); got.Code != http.StatusUnauthorized {
			t.Fatalf("旧密钥应该 401，得到 %d：%s", got.Code, got.Body)
		}
	})
	t.Run("角色和权限没变", func(t *testing.T) {
		// 轮换只换凭据，不该动别的 —— 不然「泄露了换一串」就变成「泄露了重配一遍」
		if got := a.callAPIKey(t, http.MethodGet, "/api/v1/users", rotated.Key); got.Code != http.StatusOK {
			t.Fatalf("轮换不该改变它能干什么，得到 %d：%s", got.Code, got.Body)
		}
	})
}

// TestServiceAccountCrossTenant 守跨租户（MULTI-TENANCY.md §3.2 ⑧、§10.8）。
//
// ⚠️ 两个租户都要有机器账号 —— 只给 A 造的话，「A 看不到 B」是因为库里根本没有 B 的。
func TestServiceAccountCrossTenant(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	// B 公司：一个自己的机器账号
	other := testdb.NewTenant(t, a.pool, 1)
	qb := a.store.ForTenant(other.ID)
	bID := uuid.New()
	if _, err := qb.CreateServiceAccount(t.Context(), repo.CreateServiceAccountArgs{
		ID: bID, Name: "B 的对接", Description: "", KeyPrefix: "bbbbbb",
		KeyHash: []byte("x"), RoleID: other.AdminRoleID, Status: "active",
	}); err != nil {
		t.Fatalf("建 B 公司的机器账号失败：%v", err)
	}

	// A 公司也有一个，否则列表是空的，什么都证明不了
	a.newServiceAccount(t, session, "A 的对接", "admin")

	t.Run("列表看不到别家的", func(t *testing.T) {
		rec := a.call(t, http.MethodGet, "/api/v1/service-accounts?page_size=100", nil, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("应该 200，得到 %d：%s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), bID.String()) ||
			strings.Contains(rec.Body.String(), "B 的对接") {
			t.Fatalf("A 的列表里出现了 B 的机器账号：%s", rec.Body)
		}
	})

	t.Run("按 id 查别家的是 404", func(t *testing.T) {
		rec := a.call(t, http.MethodGet, "/api/v1/service-accounts/"+bID.String(), nil, session)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("应该 404（不是 403，那会变成存在性探针），得到 %d：%s", rec.Code, rec.Body)
		}
	})

	t.Run("轮换别家的密钥不行", func(t *testing.T) {
		// 这条最要紧：轮换是写操作，而且**成功了对方就当场断线**
		rec := a.call(t, http.MethodPost,
			"/api/v1/service-accounts/"+bID.String()+"/rotate-key", nil, session)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("应该 404，得到 %d：%s", rec.Code, rec.Body)
		}
		// 回头确认 B 的密钥前缀原样没动
		after, err := qb.GetServiceAccount(t.Context(), bID)
		if err != nil {
			t.Fatal(err)
		}
		if after.KeyPrefix != "bbbbbb" {
			t.Fatalf("B 的密钥被 A 换掉了：%s", after.KeyPrefix)
		}
	})

	t.Run("删别家的删不掉", func(t *testing.T) {
		rec := a.call(t, http.MethodDelete, "/api/v1/service-accounts/"+bID.String(),
			map[string]int{"version": 0}, session)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("应该 404，得到 %d：%s", rec.Code, rec.Body)
		}
		if _, err := qb.GetServiceAccount(t.Context(), bID); err != nil {
			t.Fatalf("B 的机器账号被 A 删掉了：%v", err)
		}
	})
}

// TestServiceAccountRejectsBadInput 守几条入参校验。
func TestServiceAccountRejectsBadInput(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	role, err := a.q.GetRoleByKey(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("过期时间填在过去", func(t *testing.T) {
		// 不拦的话会造出一个「建出来就已经失效」的账号：对接方拿到密钥就 401，
		// 而页面上看着一切正常
		rec := a.call(t, http.MethodPost, "/api/v1/service-accounts", map[string]any{
			"name": "已经过期的", "role_id": role.ID.String(),
			"expires_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		}, session)
		if rec.Code == http.StatusCreated {
			t.Fatal("过期时间在过去，不该建得出来")
		}
	})

	t.Run("别家的角色 id", func(t *testing.T) {
		other := testdb.NewTenant(t, a.pool, 1)
		rec := a.call(t, http.MethodPost, "/api/v1/service-accounts", map[string]any{
			"name": "借别家角色", "role_id": other.AdminRoleID.String(),
		}, session)
		if rec.Code == http.StatusCreated {
			t.Fatal("用别家公司的角色 id 不该建得出来")
		}
		// 报「角色不存在」而不是「无权限」—— 别确认那个 id 真的存在
		if code := decodeProblem(t, rec).Code; code != sasvc.ErrUnknownRole.Code {
			t.Errorf("错误码应是 %s，得到 %s", sasvc.ErrUnknownRole.Code, code)
		}
	})

	t.Run("重名", func(t *testing.T) {
		a.newServiceAccount(t, session, "重名的", "admin")
		rec := a.call(t, http.MethodPost, "/api/v1/service-accounts", map[string]any{
			"name": "重名的", "role_id": role.ID.String(),
		}, session)
		if code := decodeProblem(t, rec).Code; code != sasvc.ErrNameTaken.Code {
			t.Errorf("重名该给友好提示 %s，得到 %s（%d）", sasvc.ErrNameTaken.Code, code, rec.Code)
		}
	})

	t.Run("版本对不上", func(t *testing.T) {
		created := a.newServiceAccount(t, session, "版本冲突用", "admin")
		rec := a.call(t, http.MethodPut, "/api/v1/service-accounts/"+created.Account.ID.String(),
			map[string]any{
				"name": "改个名", "role_id": created.Account.RoleID.String(),
				"status": "active", "version": created.Account.Version + 99,
			}, session)
		if code := decodeProblem(t, rec).Code; code != errs.VersionConflict.Code {
			t.Errorf("版本对不上该报 %s，得到 %s（%d）", errs.VersionConflict.Code, code, rec.Code)
		}
	})
}

// TestServiceAccountAuthzNotify 直接测**触发器本身**。
//
// 上面那些用例走的是「改完显式 Reload」，所以它们有没有触发器都会绿 ——
// 而这一轮修的恰恰是「触发器压根不存在」。所以必须单独有一条盯着它：
// 开一条连接 LISTEN，做动作，看通知来不来。
//
// 🔴 **两半都要测，第二半更要紧：**
//
//	改角色      → 必须发通知，否则降权不生效
//	写 last_used_at → **必须不发**，否则每次 API 调用都会广播一次全租户策略重载
//
// 第二半是「按列限定」那个设计的守卫。挂成全表 UPDATE 的话第一半照样绿，
// 而系统会在有流量的那一刻被自己打死。
func TestServiceAccountAuthzNotify(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)
	created := a.newServiceAccount(t, session, "通知测试", "admin")

	// 单独一条连接来听 —— 连接池里的连接会被别的查询复用，LISTEN 得钉在一条上
	conn, err := a.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), `LISTEN authz_changed`); err != nil {
		t.Fatal(err)
	}

	// notified 做一个动作，然后看 waitFor 之内有没有收到通知
	notified := func(t *testing.T, do func()) bool {
		t.Helper()
		do()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := conn.Conn().WaitForNotification(ctx)
		return err == nil
	}

	t.Run("改角色要发通知", func(t *testing.T) {
		if !notified(t, func() {
			if _, err := a.pool.Exec(t.Context(),
				`UPDATE service_accounts SET role_id = role_id
				 WHERE tenant_id = $1 AND id = $2`,
				a.tenant.ID, created.Account.ID); err != nil {
				t.Fatal(err)
			}
		}) {
			t.Fatal("改 role_id 没有发出 authz_changed —— 降权不会生效，" +
				"那个凭据会以旧权限一直跑下去")
		}
	})

	t.Run("写 last_used_at 不能发通知", func(t *testing.T) {
		if notified(t, func() {
			// 这正是 TouchServiceAccount 干的事，每个 API 请求都会跑一次
			if _, err := a.pool.Exec(t.Context(),
				`UPDATE service_accounts SET last_used_at = now()
				 WHERE tenant_id = $1 AND id = $2`,
				a.tenant.ID, created.Account.ID); err != nil {
				t.Fatal(err)
			}
		}) {
			t.Fatal("写 last_used_at 也发通知了 —— 每次 API 调用都会广播一次" +
				"全租户策略重载（MULTI-TENANCY.md §8.5），这不是性能退化，是把系统打死")
		}
	})
}
