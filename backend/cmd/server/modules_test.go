package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 第 ④ 步三个模块（部门 / 角色 / 用户）的集成测试。
//
// 挑的都是**光看代码看不出来、错了又很难发现**的地方：成环、乐观锁、
// 会话吊销、最后一个管理员保护。纯 CRUD 的 happy path 不在这里堆用例 ——
// 那些一跑页面就知道好没好。

// asAdmin 引导管理员、登录、改掉初始密码，返回可用的会话。
//
// bootstrap 出来的管理员是 must_change_password 的，不改密的话除了改密接口
// 什么都调不了（DECISIONS.md §6）。
func (a *liveApp) asAdmin(t *testing.T) *sessionState {
	t.Helper()

	username, password := a.bootstrapAdmin(t)
	session, rec := a.login(t, username, password)
	if rec.Code != http.StatusOK {
		t.Fatalf("管理员登录失败：%d %s", rec.Code, rec.Body)
	}

	const newPassword = "Adm1nPassw0rd2026"
	rec = a.call(t, http.MethodPost, "/api/v1/me/password", map[string]string{
		"old_password": password,
		"new_password": newPassword,
	}, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("改初始密码失败：%d %s", rec.Code, rec.Body)
	}

	// 改密会把其它会话踢掉，当前这条留着；重新登录拿一份干净的
	session, rec = a.login(t, username, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("改密后重新登录失败：%d %s", rec.Code, rec.Body)
	}
	return session
}

// dataID 从 `{"data":{"id":...}}` 里取 id。
func dataID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v，body=%s", err, rec.Body)
	}
	if body.Data.ID == "" {
		t.Fatalf("响应里没有 id：%s", rec.Body)
	}
	return body.Data.ID
}

func mustCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("期望 HTTP %d，得到 %d：%s", wantStatus, rec.Code, rec.Body)
	}
	if got := decodeProblem(t, rec).Code; got != wantCode {
		t.Errorf("期望错误码 %s，得到 %s", wantCode, got)
	}
}

// ---------------------------------------------------------------- 部门

func TestDepartmentRejectsCycleAndKeepsTreeIntact(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	rec := a.call(t, http.MethodPost, "/api/v1/departments", map[string]any{
		"name": "技术部", "code": "TECH", "sort_order": 1, "status": "active",
	}, session)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建部门失败：%d %s", rec.Code, rec.Body)
	}
	parent := dataID(t, rec)

	rec = a.call(t, http.MethodPost, "/api/v1/departments", map[string]any{
		"name": "后端组", "code": "TECH-BE", "parent_id": parent, "sort_order": 1, "status": "active",
	}, session)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建子部门失败：%d %s", rec.Code, rec.Body)
	}
	child := dataID(t, rec)

	// 把父节点挂到自己的子节点下面 —— 允许的话这一支会整个从树上断开
	rec = a.call(t, http.MethodPut, "/api/v1/departments/"+parent, map[string]any{
		"name": "技术部", "code": "TECH", "parent_id": child,
		"sort_order": 1, "status": "active", "version": 0,
	}, session)
	mustCode(t, rec, http.StatusBadRequest, "department.cycle")

	// 挂到自己身上也是环
	rec = a.call(t, http.MethodPut, "/api/v1/departments/"+parent, map[string]any{
		"name": "技术部", "code": "TECH", "parent_id": parent,
		"sort_order": 1, "status": "active", "version": 0,
	}, session)
	mustCode(t, rec, http.StatusBadRequest, "department.cycle")

	// 下面还有子部门时不给删
	rec = a.call(t, http.MethodDelete, "/api/v1/departments/"+parent,
		map[string]any{"version": 0}, session)
	mustCode(t, rec, http.StatusConflict, "department.has_children")
}

func TestDepartmentOptimisticLock(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	rec := a.call(t, http.MethodPost, "/api/v1/departments", map[string]any{
		"name": "财务部", "code": "FIN", "sort_order": 1, "status": "active",
	}, session)
	id := dataID(t, rec)

	body := map[string]any{"name": "财务中心", "code": "FIN", "sort_order": 1, "status": "active", "version": 0}
	if rec = a.call(t, http.MethodPut, "/api/v1/departments/"+id, body, session); rec.Code != http.StatusOK {
		t.Fatalf("第一次更新应该成功：%d %s", rec.Code, rec.Body)
	}
	// 拿着已经过期的 version 再改一次 —— 模拟两个人同时打开编辑框
	rec = a.call(t, http.MethodPut, "/api/v1/departments/"+id, body, session)
	mustCode(t, rec, http.StatusConflict, "common.version_conflict")
}

// TestDepartmentMembers 加人 / 移人。一个人只属于一个部门，加入即从原部门移出。
func TestDepartmentMembers(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	rec := a.call(t, http.MethodPost, "/api/v1/departments", map[string]any{
		"name": "技术部", "code": "TECH",
	}, session)
	tech := dataID(t, rec)
	rec = a.call(t, http.MethodPost, "/api/v1/departments", map[string]any{
		"name": "财务部", "code": "FIN",
	}, session)
	fin := dataID(t, rec)

	rec = a.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "member1", "display_name": "成员一",
	}, session)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建用户失败：%d %s", rec.Code, rec.Body)
	}
	var created struct {
		Data struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	userID := created.Data.User.ID

	// 候选人里应该有他 —— 他还不在技术部
	rec = a.call(t, http.MethodGet, "/api/v1/departments/"+tech+"/candidates", nil, session)
	if !strings.Contains(rec.Body.String(), userID) {
		t.Fatalf("候选人里应该有刚建的用户：%s", rec.Body)
	}

	// 加入技术部
	rec = a.call(t, http.MethodPost, "/api/v1/departments/"+tech+"/members",
		map[string]any{"user_ids": []string{userID}}, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("加入部门失败：%d %s", rec.Code, rec.Body)
	}

	// 加进去之后就不该再出现在候选人里
	rec = a.call(t, http.MethodGet, "/api/v1/departments/"+tech+"/candidates", nil, session)
	if strings.Contains(rec.Body.String(), userID) {
		t.Errorf("已经在部门里的人不该还出现在候选人里：%s", rec.Body)
	}

	// 加到财务部 = 自动从技术部移出（一个人只属于一个部门）
	rec = a.call(t, http.MethodPost, "/api/v1/departments/"+fin+"/members",
		map[string]any{"user_ids": []string{userID}}, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("换部门失败：%d %s", rec.Code, rec.Body)
	}
	rec = a.call(t, http.MethodGet, "/api/v1/users?department_id="+tech, nil, session)
	if strings.Contains(rec.Body.String(), userID) {
		t.Errorf("换了部门之后不该还留在原部门：%s", rec.Body)
	}

	// 移出：不属于任何部门，但账号还在
	rec = a.call(t, http.MethodDelete, "/api/v1/departments/"+fin+"/members",
		map[string]any{"user_ids": []string{userID}}, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("移出部门失败：%d %s", rec.Code, rec.Body)
	}
	rec = a.call(t, http.MethodGet, "/api/v1/users/"+userID, nil, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("移出部门不该影响账号：%d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"department_id":null`) {
		t.Errorf("移出之后 department_id 应该是 null：%s", rec.Body)
	}
}

// ---------------------------------------------------------------- 角色

func TestRolePermissionsAreValidatedAgainstRegistry(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	// 权限点必须是注册表里真有的，否则会在库里留下一条永远匹配不上的死策略
	rec := a.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"key": "ghost", "name": "幽灵", "data_scope": "self", "status": "active",
		"permissions": []string{"nosuchmodule:list"},
	}, session)
	mustCode(t, rec, http.StatusBadRequest, "role.unknown_permission")

	// 通配只留给内置 admin
	rec = a.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"key": "godmode", "name": "全能", "data_scope": "all", "status": "active",
		"permissions": []string{"*:*"},
	}, session)
	mustCode(t, rec, http.StatusForbidden, "role.wildcard_reserved")
}

func TestBuiltinRoleIsImmutable(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	rec := a.call(t, http.MethodGet, "/api/v1/roles?page_size=50", nil, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("查角色失败：%d %s", rec.Code, rec.Body)
	}
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			Key     string `json:"key"`
			Builtin bool   `json:"builtin"`
			Version int    `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}

	var adminID string
	var version int
	for _, r := range list.Data {
		if r.Key == "admin" {
			adminID, version = r.ID, r.Version
			if !r.Builtin {
				t.Error("admin 角色应该标记为内置")
			}
		}
	}
	if adminID == "" {
		t.Fatal("没找到内置 admin 角色")
	}

	// 改内置角色 = 有可能把自己锁在门外，一律拒绝
	rec = a.call(t, http.MethodPut, "/api/v1/roles/"+adminID, map[string]any{
		"name": "被改了", "data_scope": "self", "status": "disabled",
		"permissions": []string{}, "version": version,
	}, session)
	mustCode(t, rec, http.StatusForbidden, "role.builtin_immutable")

	rec = a.call(t, http.MethodDelete, "/api/v1/roles/"+adminID,
		map[string]any{"version": version}, session)
	mustCode(t, rec, http.StatusForbidden, "role.builtin_immutable")
}

// TestRolePermissionChangeTakesEffect 验证「改完权限立刻生效」这条链路是通的：
// role_permissions 变更 → 触发器 NOTIFY → Casbin 重载。
//
// 测试里手动 Reload 代替 NOTIFY（httptest 不走真实的监听 goroutine），
// 重点验的是**策略数据本身对不对**。
func TestRolePermissionChangeTakesEffect(t *testing.T) {
	a := newLiveApp(t)
	admin := a.asAdmin(t)

	rec := a.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"key": "viewer", "name": "只读", "data_scope": "self", "status": "active",
		"permissions": []string{"audit:list"},
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建角色失败：%d %s", rec.Code, rec.Body)
	}
	roleID := dataID(t, rec)

	a.createUser(t, "viewer-user", "Passw0rd2026x", "viewer")
	session, rec := a.login(t, "viewer-user", "Passw0rd2026x")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录失败：%d %s", rec.Code, rec.Body)
	}

	if rec = a.call(t, http.MethodGet, "/api/v1/audit-logs", nil, session); rec.Code != http.StatusOK {
		t.Fatalf("有 audit:list 却查不了审计：%d %s", rec.Code, rec.Body)
	}
	if rec = a.call(t, http.MethodGet, "/api/v1/users", nil, session); rec.Code != http.StatusForbidden {
		t.Fatalf("没有 user:list 却查得了用户：%d %s", rec.Code, rec.Body)
	}

	// 把审计权限换成用户权限
	rec = a.call(t, http.MethodPut, "/api/v1/roles/"+roleID, map[string]any{
		"name": "只读", "data_scope": "self", "status": "active",
		"permissions": []string{"user:list"}, "version": 0,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("改角色权限失败：%d %s", rec.Code, rec.Body)
	}
	if err := a.checker.Reload(t.Context()); err != nil {
		t.Fatalf("刷新策略失败：%v", err)
	}

	if rec = a.call(t, http.MethodGet, "/api/v1/users", nil, session); rec.Code != http.StatusOK {
		t.Errorf("改成 user:list 之后应该查得了用户：%d %s", rec.Code, rec.Body)
	}
	if rec = a.call(t, http.MethodGet, "/api/v1/audit-logs", nil, session); rec.Code != http.StatusForbidden {
		t.Errorf("audit:list 已经去掉了，还查得了审计：%d %s", rec.Code, rec.Body)
	}
}

// ---------------------------------------------------------------- 用户

func TestCreateUserReturnsPasswordOnceAndForcesChange(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	rec := a.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "zhangsan", "display_name": "张三",
		"email": "zhangsan@example.com", "status": "active", "role_ids": []string{},
	}, session)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建用户失败：%d %s", rec.Code, rec.Body)
	}

	var created struct {
		Data struct {
			User            struct{ ID string } `json:"user"`
			InitialPassword string              `json:"initial_password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.InitialPassword == "" {
		t.Fatal("没返回初始密码")
	}

	// 初始密码能登录，但登录后除了改密什么都干不了
	newSession, rec := a.login(t, "zhangsan", created.Data.InitialPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("用初始密码登录失败：%d %s", rec.Code, rec.Body)
	}
	rec = a.call(t, http.MethodGet, "/api/v1/users", nil, newSession)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("首次登录没改密就能调别的接口：%d %s", rec.Code, rec.Body)
	}
	mustCode(t, rec, http.StatusForbidden, "auth.must_change_password")

	// 详情接口不能把密码哈希漏出去
	rec = a.call(t, http.MethodGet, "/api/v1/users/"+created.Data.User.ID, nil, session)
	if body := rec.Body.String(); strings.Contains(body, "password_hash") || strings.Contains(body, "$argon2id$") {
		t.Errorf("用户详情里出现了密码哈希：%s", body)
	}
}

func TestUniqueIdentifiersAreEnforced(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	base := map[string]any{
		"username": "dup", "display_name": "重复", "email": "Dup@Example.com",
		"phone": "13800138000", "status": "active", "role_ids": []string{},
	}
	if rec := a.call(t, http.MethodPost, "/api/v1/users", base, session); rec.Code != http.StatusCreated {
		t.Fatalf("建用户失败：%d %s", rec.Code, rec.Body)
	}

	cases := []struct {
		name  string
		patch map[string]any
		code  string
	}{
		{"用户名重复", map[string]any{"email": "other@example.com", "phone": "13900139000"}, "user.username_taken"},
		// 邮箱唯一索引建在 lower(email) 上，大小写不同也算重复
		{"邮箱大小写不同也重复", map[string]any{"username": "dup2", "phone": "13900139000"}, "user.email_taken"},
		{"手机号重复", map[string]any{"username": "dup3", "email": "other2@example.com"}, "user.phone_taken"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := map[string]any{}
			for k, v := range base {
				body[k] = v
			}
			for k, v := range c.patch {
				body[k] = v
			}
			rec := a.call(t, http.MethodPost, "/api/v1/users", body, session)
			mustCode(t, rec, http.StatusConflict, c.code)
		})
	}
}

// TestDisableUserRevokesSessions 停用之后必须立刻踢下线。
// 只改状态不踢会话的话，这个人能一直用到 cookie 过期。
func TestDisableUserRevokesSessions(t *testing.T) {
	a := newLiveApp(t)
	admin := a.asAdmin(t)

	userID := a.createUser(t, "victim", "Passw0rd2026x", "admin")
	session, rec := a.login(t, "victim", "Passw0rd2026x")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录失败：%d %s", rec.Code, rec.Body)
	}
	if rec = a.call(t, http.MethodGet, "/api/v1/me", nil, session); rec.Code != http.StatusOK {
		t.Fatalf("停用前应该正常：%d %s", rec.Code, rec.Body)
	}

	rec = a.call(t, http.MethodPut, "/api/v1/users/"+userID.String(), map[string]any{
		"display_name": "victim", "email": "", "phone": "",
		"status": "disabled", "role_ids": []string{}, "version": 0,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("停用失败：%d %s", rec.Code, rec.Body)
	}

	if rec = a.call(t, http.MethodGet, "/api/v1/me", nil, session); rec.Code != http.StatusUnauthorized {
		t.Errorf("停用之后旧会话还能用：%d %s", rec.Code, rec.Body)
	}
}

// TestResetPasswordRevokesSessionsAndForcesChange 重置密码同样要踢会话 ——
// 会重置密码往往说明号已经不安全了，留着旧会话等于没重置。
func TestResetPasswordRevokesSessionsAndForcesChange(t *testing.T) {
	a := newLiveApp(t)
	admin := a.asAdmin(t)

	userID := a.createUser(t, "forgot", "Passw0rd2026x", "admin")
	session, _ := a.login(t, "forgot", "Passw0rd2026x")

	rec := a.call(t, http.MethodPost, "/api/v1/users/"+userID.String()+"/reset-password", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("重置密码失败：%d %s", rec.Code, rec.Body)
	}
	var result struct {
		Data struct {
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Password == "" {
		t.Fatal("没返回临时密码")
	}

	if rec = a.call(t, http.MethodGet, "/api/v1/me", nil, session); rec.Code != http.StatusUnauthorized {
		t.Errorf("重置密码之后旧会话还能用：%d %s", rec.Code, rec.Body)
	}

	// 临时密码能登录，但必须马上改
	newSession, rec := a.login(t, "forgot", result.Data.Password)
	if rec.Code != http.StatusOK {
		t.Fatalf("临时密码登录失败：%d %s", rec.Code, rec.Body)
	}
	// 处于「必须改密」状态时，除了改密和登出，**连 /me 都会 403** ——
	// 这是设计如此，前端的 AuthGate 靠这个错误码把人导到改密页。
	rec = a.call(t, http.MethodGet, "/api/v1/me", nil, newSession)
	mustCode(t, rec, http.StatusForbidden, "auth.must_change_password")
}

// TestCannotRemoveLastAdmin 最后一个管理员不能删也不能停用 ——
// 真删了就没人能进后台，只能上数据库救。
func TestCannotRemoveLastAdmin(t *testing.T) {
	a := newLiveApp(t)
	admin := a.asAdmin(t)
	adminID := meUserID(t, a, admin)

	// 自己删自己：先被 self_target 挡住（这是另一条独立的护栏）
	rec := a.call(t, http.MethodDelete, "/api/v1/users/"+adminID,
		map[string]any{"version": 0}, admin)
	mustCode(t, rec, http.StatusForbidden, "user.self_target")

	// 换个有权限的人来删他。
	// **不能用 createUser(..., "admin")** —— 那样新人自己也是管理员，
	// bootstrap 管理员就不再是「最后一个」了，护栏根本不会触发。
	// 所以专门建一个只有用户管理权限、没有通配权限的角色。
	rec = a.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"key": "usermgr", "name": "用户管理员", "data_scope": "all", "status": "active",
		"permissions": []string{"user:list", "user:update", "user:delete"},
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建角色失败：%d %s", rec.Code, rec.Body)
	}

	a.createUser(t, "usermgr-user", "Passw0rd2026x", "usermgr")
	mgr, rec := a.login(t, "usermgr-user", "Passw0rd2026x")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录失败：%d %s", rec.Code, rec.Body)
	}

	rec = a.call(t, http.MethodDelete, "/api/v1/users/"+adminID,
		map[string]any{"version": 0}, mgr)
	mustCode(t, rec, http.StatusConflict, "user.last_admin")

	// 停用同理
	rec = a.call(t, http.MethodPut, "/api/v1/users/"+adminID, map[string]any{
		"display_name": "系统管理员", "email": "", "phone": "",
		"status": "disabled", "role_ids": []string{}, "version": 0,
	}, mgr)
	mustCode(t, rec, http.StatusConflict, "user.last_admin")
}

// TestCannotStripLastAdminRoles 把最后一个管理员的角色勾掉，
// 效果和停用他一样 —— 所有人都进不来了，而且更隐蔽（页面上那个人看着还好好的）。
func TestCannotStripLastAdminRoles(t *testing.T) {
	a := newLiveApp(t)
	admin := a.asAdmin(t)
	adminID := meUserID(t, a, admin)

	// 状态仍然是 active，只是把角色清空
	rec := a.call(t, http.MethodPut, "/api/v1/users/"+adminID, map[string]any{
		"display_name": "系统管理员", "status": "active",
		"role_ids": []string{}, "version": 0,
	}, admin)
	mustCode(t, rec, http.StatusConflict, "user.last_admin")
}

func meUserID(t *testing.T, a *liveApp, session *sessionState) string {
	t.Helper()
	rec := a.call(t, http.MethodGet, "/api/v1/me", nil, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("查 /me 失败：%d %s", rec.Code, rec.Body)
	}
	var me struct {
		Data struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	return me.Data.User.ID
}
