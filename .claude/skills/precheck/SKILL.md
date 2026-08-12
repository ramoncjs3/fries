---
name: precheck
description: 提交 / 收尾前的自检。当用户说「提交吧」「做完了吧」「可以合并了吗」「帮我 review 一下再提」，或你自己准备收尾一段工作时使用。覆盖 make check 查不了、但漏了会出事的那几件。
---

# 提交前自检

## 铁律

**`make check` 绿只是必要条件，不是充分条件。** 它查得了编译、格式、租户 SQL、生成物是否最新；
查不了「测试有没有真在测该测的东西」「隔离到底隔没隔上」。这个 skill 就是补那道缺口。

**顺序**：先让 `make check` 全绿，再走下面这张单子。`make check` 红着别谈提交。

---

## 第 1 步：make check 必须全绿

```bash
make check
```

它已经机械保证了这些，**你不用再手查**（这几条以前靠自觉，现在是红灯）：
- 每条查询带租户条件（`lint-sql`）
- **新表有 `tenant_id` 列却没登记进 `tenantsql.tenantTables`** → 当场报（`checkTenantTableSchema`）
- **租户表唯一索引没 `tenant_id` 打头 / 用了内联 UNIQUE** → 当场报
- 生成物（sqlc / OpenAPI 类型 / SCHEMA.md / ERROR_CODES.md / errorCodes.ts）都是最新的
- 权限点声明和路由一一对应（selfcheck 双向校验）、菜单图标都注册了

⚠️ 假失败：集成测试起容器时报 `registry-1.docker.io ... TLS handshake timeout`
（或容器中途 `connection reset by peer`）。这**不是代码问题**，是 docker daemon 连不上
registry —— 测试逻辑压根没跑到。重跑一次；**还红就 `TESTCONTAINERS_RYUK_DISABLED=true make check`**
（卡的是 testcontainers 的 reaper，postgres 镜像本地有也照样报）。别去改代码。

## 第 2 步：机器查不了的隔离与测试质量 ⬅ 这才是本 skill 的价值

逐条对照，任何一条「否」都不能提交：

- [ ] **跨租户测试两个租户都有数据**了吗？只给 A 造数据、断言 B 查不到，是**假绿** ——
  库里本来就没有 B 的东西，把 `WHERE tenant_id = ?` 整个删掉照样过（MULTI-TENANCY.md §3.2 ⑧）。
  用 `testdb.TwoTenants`。
- [ ] **写操作测了「影响行数是 0」**吗？跨租户的 UPDATE/DELETE 应该动 0 行、表现成 NotFound。
  这条读操作的 `assert.go` 兜底覆盖不到（写没有返回行可核）。
- [ ] **新加的守门测试做了变异验证**吗？把被测逻辑改回坏版本，确认测试会**红**。
  不会红的测试等于没写 —— 这个项目的检查器都配了变异测试，照着来。
- [ ] 靠「DB 层收权」防的东西（审计防篡改这类），测试用的是**受限角色**而非 owner 吗？
  owner 连库 superuser 绕过一切权限检查，测不出（`testdb.StartWithAppRole`）。
- [ ] 破例绕过隔离的地方（`-- tenant-exempt:` / `Unscoped()` / `Platform()` /
  `schemaExemptTenantTables`）**写了理由**、且理由**站得住**吗？这些是给人读的，不是消警告的。

## 第 3 步：登记两处（机器不管）

- [ ] `docs/MODULES.md` 登记 / 更新了模块（表、权限点、页面、错误码前缀）？
- [ ] 踩到的坑、定下的约定追加进 `docs/MEMORY.md` 了？一条一段，标题写结论。
  尤其是「症状 → 根因」型的，它同时喂养 `troubleshoot` skill。

## 第 4 步：改动面自查

- [ ] 有没有把生成物手改了（`sqlcgen/` / `schema.d.ts` / `SCHEMA.md` / `errorCodes.ts`）？
  这些改 YAML/SQL/迁移重跑，别手动编辑。
- [ ] 有没有误建的二进制、`.bak`、临时文件混进 `git status`？
- [ ] 内部错误有没有可能漏进 5xx 响应？返给前端的只能是通用文案 + request_id（红线 #5）。
- [ ] 新写接口三关齐了吗：认证、授权（权限点必填）、审计（红线 #3）。

---

## 提交

确认以上全过，再提交。提交信息说清「做了什么 + 为什么」，多个逻辑改动分多个提交。
**别在 `make check` 还红或上面任一条还「否」的时候说「做完了」。**
