# 配置管理 API + UI 设计

> 状态：待实现。定稿于 2026-08-11。
> 相关：DECISIONS.md §5（配置分层）、MULTI-TENANCY.md §7.2（配置分两类）、
> MULTI-TENANCY.md §10.5（租户级设置的平台上下界）。

## 1. 范围

**只做已经有代码读的 7 项。**

| 类别 | key | 谁能改 |
|---|---|---|
| 租户级 | `security.password_min_length` | 租户管理员 |
| 租户级 | `security.password_require_mix` | 租户管理员 |
| 租户级 | `security.password_max_age_days` | 租户管理员 |
| 租户级 | `security.login_max_failures` | 租户管理员 |
| 租户级 | `security.login_lock_minutes` | 租户管理员 |
| 平台级 | `audit.retention_days` | 平台管理员 |
| 平台级 | `ui.system_name` | 平台管理员 |
| 平台级 | `limits.<租户级 key>.min` / `.max`（6 行，见迁移 00011） | 平台管理员 |

**明确不做**：DECISIONS.md §5 那张表里还没有消费方的项（上传大小、分页默认值、
导出行数上限、AI 模型与 prompt、token 配额）。理由是项目自己写过的那句话 ——
「给每个租户一个改了也不生效的配置项，比不给改更糟」。

**明确不做**：改严密码策略后强制存量用户改密。新策略只在「改密」和「新建用户」
时校验，存量密码不动（它们是哈希，系统根本不知道多长）。页面上要把这点说清楚。

## 2. 为什么是注册表，不是写死的强类型接口

硬约束：**上下界是平台管理员随时能调的运行时值**，所以前端表单必须从服务端拿区间。

写死的方案（`GET/PUT /settings/security` 直接用一个 5 字段结构体 + huma 校验）在这里
有个死结：huma 的 `minimum:"10"` 是**编译期常量**，而 MULTI-TENANCY.md §10.5 的区间是运行时可调的。
写死就等于绕开刚补上的那套护栏。

既然「服务端能描述有哪些项、什么类型、区间多少」这件事必然要有，就把它做成
和 `perm` 一样的一等公民：**一处声明，产出接口校验、前端表单、文档**。

## 3. 🔴 注册表必须同时是白名单

这是 review 时发现的洞，不补上就别做这个功能。

`Settings.Set` 和 `SetPlatform` 现在**接受任意 key 字符串**。配置页一接上来，
它们就成了「往配置表里写任意键」的入口。最要紧的后果在平台端：

MULTI-TENANCY.md §10.5 的护栏靠「`platform_settings` 里有没有 `limits.<key>.min` 这一行」，
而 `internal/config/bounds.go` 里明写着**没有对应行的 key 不受限**。于是键名敲错一个字母 ——

```
limits.security.password_min_length.mn   ← 少了个 i
```

—— 那条下界就**静默消失**了，没有任何报错，页面上看着还像保存成功。
租户第二天就能把密码策略调到 1 位。

**修法三条：**

1. 注册表是白名单：`Set` / `SetPlatform` 拒绝未声明的 key
2. `limits.*` 的键名由注册表**拼出来**，不是人敲
3. 启动时核对 `platform_settings` 里的 `limits.*` 都指向真实存在的租户级 key，
   孤儿行打 WARN（不 fail 启动 —— 一行脏数据不该让服务起不来）

**对称性**：注册表也要有 `Realm` 字段，租户端接口拒绝平台级 key。
这和 MULTI-TENANCY.md §3.2 ② 那个「平台权限点混进租户角色页」是**完全同一个 bug 形状**，
同一个病同一个药。

## 4. 结构

| 放哪 | 为什么 |
|---|---|
| `internal/config/registry.go` | 注册表住这里，因为 `Set` 要拿它当白名单。住 service 里的话 config 得反向 import service，成环 |
| `internal/service/settings/service.go` + `errors.go` | 组装视图模型、翻错误。红线 #6：handler 不写业务逻辑 |
| `internal/handler/settings.go` | 路由注册 + 出入参 |
| `frontend/src/routes/` 下新增 SettingsPage.tsx | 租户端 |
| `frontend/src/routes/platform/` 下新增 PlatformSettingsPage.tsx | 平台端 |

⚠️ **前端两个页面不进 `features/`**：那个目录只允许 `ListPage` / `NewPage` / `DetailPage`
三种页面（`lint-structure` 有白名单），而配置是**单表单页**，形态上属于
`ChangePasswordPage` 那一类，归 `routes/`。

### 注册表的形状

```go
type Item struct {
    Key      string        // security.password_min_length
    Realm    perm.Realm    // tenant / platform
    Kind     Kind          // int / bool / string
    Group    string        // 分组标识，前端按它折行
    Name     string        // 中文名
    Desc     string        // 说明，显示在输入框下面
    Unit     string        // 位 / 天 / 次 / 分钟，空表示没有单位
    Default  any           // 兜底值，和 defaultSecurity() 对齐
    Bounded  bool          // 是否受 limits.* 约束（bool/string 项为 false）
}
```

## 5. 权限点

**租户端：新增 `settings.security` 模块**（`Realm: RealmTenant`），动作 `list` / `update`。

⚠️ **为什么现在就带分组后缀**：MODULES.md 写的是「`settings.*`（按分组拆分资源）」。
先注册成 `settings` 再拆成 `settings.security`，会**改掉 Casbin 的资源名** ——
已有的授权行全部失效，而且和 MULTI-TENANCY.md §8.3 那个「索引改名让唯一冲突翻译静默退化」是同一类
静默失败。反过来先带后缀，将来加 `settings.notify` 是纯增量。

**平台端：新增 `PlatformSetting` 模块**（`Realm: RealmPlatform`），动作 `list` / `update`。
平台管理员这一轮即全权（MULTI-TENANCY.md §6），点声明出来是为了路由有东西可挂、审计里看得出是什么动作。

## 6. 接口

```
GET  /api/v1/settings              → 分组 → 项（含当前值和允许区间）
PUT  /api/v1/settings              → {items: [{key, value}]}
GET  /api/v1/platform/settings     → 同上（平台级项 + limits.*）
PUT  /api/v1/platform/settings     → 同上
```

`GET` 返回的每一项都带 `min` / `max`（可空），前端直接拿来做输入框的约束提示 ——
**前端不硬编码任何 key，也不硬编码任何区间**。

## 7. 数据流

写：`handler` → `service` 逐项校验（key 在不在注册表、Realm 对不对、类型对不对）
→ `config.Settings.Set`（MULTI-TENANCY.md §10.5 的上下界护栏在这里）→ `UpsertSetting`
→ 触发器 `NOTIFY settings_changed`（负载是 tenant_id）→ 各实例 `ReloadTenant`。

读：全部走内存缓存，不查库。

⚠️ 一次 `PUT` 多项时**逐项 `Set`**，不包事务：`Set` 内部会 `ReloadTenant`，
包在事务里会让缓存刷到未提交的值。多项部分失败时返回第一个错误 ——
配置项之间没有相互约束，部分成功可以接受。

## 8. 错误处理

| 情况 | 返回 |
|---|---|
| key 不在注册表 / Realm 不对 | `settings.unknown_key`（**不区分这两种** —— 别把平台有哪些 key 透给租户，和 MULTI-TENANCY.md §11.2 同理） |
| 类型不对 | `common.validation_failed`，带 `body.items[i].value` |
| 超出平台区间 | `common.validation_failed`（`bounds.go` 现成的文案） |

## 9. 测试

| 层 | 测什么 |
|---|---|
| 单元 | 注册表白名单：未声明的 key 被拒；租户端拒平台级 key；`limits.*` 键名由注册表拼出 |
| 单元 | 上下界：`int` 和 `float64` 两种入参都受约束（MULTI-TENANCY.md §15.7 那个教训） |
| 集成 | 改配置 → `NOTIFY` → 缓存刷新 → 下一次登录真的用新策略 |
| 集成 | **跨租户**：A 改自己的密码策略，B 的不受影响；A 改不动 B 的 |
| 集成 | 租户管理员打平台设置接口 → 401（按路径选会话套，MULTI-TENANCY.md §15.5） |

⚠️ 跨租户那条必须**两个租户都有数据**（MULTI-TENANCY.md §3.2 ⑧ 的陷阱）。

## 10. 落地顺序

1. 注册表 + 白名单 + `Set`/`SetPlatform` 收口 + 单元测试（**先补洞**）
2. 启动自检：注册表 key 与 config 包里那批 Key 常量一致；孤儿 `limits.*` WARN
3. service + handler + 权限点 + 集成测试
4. 两个前端页面 + 菜单
5. `MODULES.md` 登记、`ERROR_CODES.md` 由 `make errdoc` 重生成
