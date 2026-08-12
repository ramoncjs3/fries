# 项目记忆

> **动手前先读这里。** 这个项目踩过的坑、临时定下的约定、容易忘的关键信息。
>
> 规则：
> - 踩到坑、定了约定、发现了不显然的事实 → **立刻追加**，别等
> - 一条一段，标题写「结论」不写「现象」，正文说清「是什么」和「怎么办」
> - **只追加在末尾**，顺序就是时间序 —— 所以标题不带日期。
>   环境类的条目（哪个版本、哪台机器）在正文里写清绝对日期，别写「最近」「上次」
> - 通用的技术选型不写这里，写 `DECISIONS.md`
> - 发现某条过时了就删掉，不要留着误导人
>
> ⚠️ 开头那批带 `2026-08-07 ·` 前缀的是最早的写法，后面三十多条都没带 ——
> 与其给新条目硬补日期、让两种格式一直混着，不如把规则改成上面这样。
> **别再往标题里加日期。**

---

## 2026-08-07 · GOPROXY 换企业私服时凭据不能写在 URL 里

Go 会**拒绝**在 HTTP 的 proxy URL 里传凭据（`http://user:pass@host/...` 直接报错）。
换私服时 `go env -w GOPROXY` 只写不带凭据的地址，凭据写进 `~/.netrc`。

默认用公共代理就行：`https://proxy.golang.org,direct`（国内 `https://goproxy.cn,direct`）。

---

## 2026-08-07 · 项目级 skill 命名前先查全局有没有同名

建了一个叫 `review` 的项目级 skill 之后发现，全局已经存在同名 skill
（"Review the changes since a fixed point…"），项目里的被它盖住了 —— 路由表指向项目文件，
实际调用跑的是全局那个。

已改名为 `precheck`。**新建项目级 skill 前先跑一遍 `Skill` 工具的可用列表确认不重名。**

---

## 2026-08-07 · 文档之间的一致性必须机械检查

前三轮 review 找出 13 个问题，其中 6 个是「改了 A 忘了同步 B」（如把错误码从 `httpx` 拆到 `errs`
之后，6 处引用没改，包括 AGENTS.md 红线本身）。

**规矩：改任何一个概念之前，先 `grep` 它在项目里的所有出现位置，列成清单一起改。**
`make lint-docs` 会兜底检查引用路径、标识符、章节号是否有效，但预防比检查便宜。

---

## 2026-08-07 · huma 与 Echo 的版本是绑死的

huma ≥ v2.39 的官方适配器 `adapters/humaecho` 只支持 **Echo v5**（v2.38 及以前是 Echo v4）。
本项目走 huma v2.39.1 + Echo v5。

**升级任何一边之前先确认另一边**：单独升 huma 会编译不过，单独降 Echo 也一样。
Echo v5 的几处坑：`echo.Context` 从接口变成结构体（handler 签名是 `*echo.Context`）、
错误处理器参数顺序变成 `(c, err)`、`c.Response()` 返回 `http.ResponseWriter`
（要拿状态码用 `echo.ResolveResponseStatus`）、`e.Routes()` 变成 `e.Router().Routes()`。

---

## 2026-08-07 · 本地开发不用 docker，数据库用本机的 PG

约定：**本地开发直接跑前后端**（`make dev` 起 air 热重载，`make fe-dev` 起 vite），
数据库用 brew 装的 PostgreSQL 17（占着 5432）。`make db-setup` 建角色和库。
docker 只在部署时用。

由此带来的一个坑：`make up`（compose）里的 PG 不能也映射到 5432，否则客户端会连到
本机那个，报 `role "fries" does not exist`，看着像容器没起来，其实是连错了库。
**compose 的 PG 映射到宿主机 5433**，`make up` 跑迁移时用 `FRIES_DATABASE__PORT=5433`
覆盖。两处端口分别写在 `Makefile` 的 `COMPOSE_PG_PORT` 和 compose 文件里，改要一起改。

---

## 2026-08-07 · huma 默认会往响应体里塞 `$schema`

`huma.DefaultConfig` 通过 **CreateHooks** 挂了一个 SchemaLinkTransformer，会给每个响应
加 `$schema` 字段和 `Link` 头，和 §4.2 定死的封套对不上。

关掉要**同时清两处**：`cfg.Transformers` 和 `cfg.CreateHooks` —— CreateHooks 在 `NewAPI`
里才执行，只清 Transformers 会被它原样加回来。已在 `httpx.NewConfig` 里处理，
`cmd/server` 有回归测试盯着。

---

## 2026-08-07 · 错误码不能实现 `GetStatus()`

`errs.Code` / `errs.Error` 实现的是 `StatusCode() int`（Echo 的 `HTTPStatusCoder`），
**故意不叫 `GetStatus()`** —— 那是 `huma.StatusError` 的签名，一旦实现，huma 会绕开我们
覆盖的 `NewError`，把错误对象原样当响应体写出去，RFC 9457 格式和 `code` 字段就都没了。

---

## 2026-08-07 · 校验失败的 errors[].message 目前是英文

huma 自带的入参校验产出的是英文提示（如 `expected number <= 5`），会原样进 RFC 9457 的
`errors[]`。顶层 `detail` 是中文（来自错误码），不影响用户看到的主要文案。

huma 没有留翻译钩子。第 ③ 步做前端表单映射时再决定：要么在 `httpx` 里按前缀翻译一张表，
要么前端按 `location` 自己出文案。**别忘了这件事。**

---

## 2026-08-07 · 工具链版本锁在 Makefile，装到 backend/bin

`make tools` 按固定版本装 sqlc / goose / golangci-lint / air 到 `backend/bin`（不进版本库）。
第一次装 sqlc、goose 时 Go 会因为它们要求更高的语言版本而**自动下载对应工具链**，
要几分钟且要联网，别以为卡死了。

sqlc.yaml 里给 `numeric` 配了 `shopspring/decimal` 映射，但现在没有 numeric 列，
所以 go.mod 里还没有这个依赖 —— **第一次加金额字段时要先 `go get github.com/shopspring/decimal`**。

---

## 2026-08-07 · pgx 的简单协议会把 jsonb 参数当成 bytea

集成测试里跑迁移要用 `QueryExecModeSimpleProtocol`（一个文件里多条语句，只有简单协议
能一次发完）。但如果**应用也用这个连接池**，写审计时会报
`invalid input syntax for type json` —— 简单协议把 `[]byte` 渲染成 bytea 的
`'\x7b7d'` 字面量，塞进 jsonb 列就炸了。

**规矩：简单协议只用于跑迁移，应用连接池保持默认的扩展协议**（和生产一致）。
已在 `internal/repo/testdb` 里分开处理。

---

## 2026-08-07 · API Key 的段分隔符不能撞上 base64

API Key 形如 `fsa_<prefix>_<secret>`。一开始 prefix 用 `randomToken`（base64url），
而 base64url 的字符集里**有下划线**，于是 key 被切成四段，认证永远失败、还只报 401，
查了半天。

现在 prefix 用十六进制，拆的时候用 `strings.SplitN(key, "_", 3)`（secret 里仍可能有 `_`）。

---

## 2026-08-07 · 中间件顺序里有两个不显然的约束

1. **审计中间件必须在认证中间件外面**。认证在内层把主体写进 `c.SetRequest(...)` 的
   context，外层的审计在 `next()` 返回后才读得到「这是谁干的」。反过来放，所有审计
   都会记成匿名。
2. **授权中间件要先填审计的 resource/action 再判权限**。被 403 拦掉的请求 handler
   根本不会跑，不先填就只能记成一条看不出是哪个接口的 `http:request` ——
   而这类请求恰恰是最需要查的。

---

## 2026-08-07 · 本地开发时审计防篡改是不生效的

`make db-setup` 建的 `fries` 是超级用户，**superuser 绕过一切权限检查**，
迁移里那句 `REVOKE UPDATE, DELETE ON audit_logs` 对它无效。

生产部署必须另建受限角色 `fries_app` 连库（迁移里检测到该角色存在就会自动授权）。
服务启动时会检查当前身份能不能改审计表，能改就打一条 WARN —— 别忽略它。

---

## 2026-08-07 · 会话密钥用占位符 + 生成时替换

`config.example.yaml` 里的 `session.secret` 是一个够长的占位符，所以自检和 CI 跑得起来；
`make dev` / `make up` 第一次生成 `config.yaml` 时会用 `openssl rand -base64 32` 换掉它。
服务启动时发现还在用占位符会大声警告 —— 共用同一个密钥等于没有密钥。

---

## 2026-08-07 · 进程级 `time.Local = time.UTC`，别靠每处写 .UTC()

pgx 把 `timestamptz` 解码成的 `time.Time` **带的是本机时区**（和 PG 会话时区无关，
二进制协议传的是绝对时刻）。于是接口输出就成了 `+08:00`，而 §2.5 要求带 Z 的 RFC3339。

`cmd/server/main.go` 和 `internal/repo/testdb` 里都在入口设了 `time.Local = time.UTC`
—— 靠「每个字段记得 `.UTC()`」是守不住的，少写一处就漂一处。
`TestAPITimesAreUTC` 是这条约定的守卫。

---

## 2026-08-07 · 登录要先验密码再看账号状态

原来的顺序是「查用户 → 看状态/锁定 → 验密码」，于是「账号已停用 / 已锁定」这两个
回应变成了**用户名枚举探针**：不知道密码的人也能问出哪些账号存在。

现在先验密码，密码对了才告诉他账号本身的状态。

---

## 2026-08-07 · 不要 `import * as icons from 'lucide-react'`

菜单图标名是后端给的字符串，一开始图省事整包导入再按名字取 —— 结果整个图标库
（一千多个）都进了 bundle，首屏从 500 KB 涨到 1.4 MB。

改成 `src/components/MenuIcon.tsx` 里的显式注册表。**新模块用了新图标要在那里加一行**，
忘了加只是显示成一个圆点，不会白屏。

---

## 2026-08-07 · huma 默认把数组标成可空

Go 切片在 OpenAPI 里默认是 `["array","null"]`，生成的 TS 类型就成了 `T[] | null`，
每个用它的地方都得写一次 `?? []`。我们的约定是**列表一律返回 `[]`**，
所以在 `httpx.NewConfig` 里设了 `huma.DefaultArrayNullable = false` 让 schema 跟约定一致。

---

## 2026-08-07 · 登录成功后必须先刷 /me 再跳页

登录之前那次 `/me` 是 401，错误留在 React Query 缓存里。登录成功直接跳转的话，
下一个页面的闸门读到的还是那个旧错误，会把人又踢回登录页 —— 现象是登录后卡在
转圈或者反复跳登录页。

**规矩：`onSuccess` 里先 `await invalidateQueries({ queryKey: meQueryKey })` 再 navigate。**

---

_(新条目追加在下面)_

## huma 的 body 字段默认全是必填

没加 `,omitempty` 的字段一律 required，**哪怕写了 `default:`**。
表现是「可选字段不传就 400」，而 OpenAPI 上还写着它有默认值，自相矛盾。

`default:` tag 只写进文档，反序列化时不会填值 —— 所以默认值必须自己兜，
而且要兜在 **service 的 `applyDefaults()`**，不是 handler：
handler 有多个入口（create / update），漏一个就往库里写空串撞 CHECK 约束变 500。

## 同一 tick 里改多次 URL 参数，只有最后一次生效

`setSearchParams` 的函数式写法拿到的是**当前这次渲染**的参数，
连调几次每次都从同一份旧值算起，后一次盖掉前一次。
表现是「点了清空一点反应都没有」，但单个条件的 ✕ 又是好的。
要一起改多个就在一个 `update` 里删完（`params.reset()`）。

## 根字号 14px，Tailwind 的 rem 工具类全都缩了 0.875 倍

`h-11` 是 38.5px 不是 44px，`h-14` 是 49px 不是 56px。
设计稿给 px 的地方（表头、行高）用 `--spacing-*` 定 px token，别用 rem 刻度凑。

## GRANT ON ALL TABLES 是快照，不覆盖以后建的表

生产用受限角色（`fries_app`）时，新迁移建的表会直接 permission denied。
除了给新表补 GRANT，还要 `ALTER DEFAULT PRIVILEGES`，否则每张新表都得记得回来加。

## 最后一个管理员有三条路会被锁死，不是一条

停用、删除、**以及把角色勾掉**。第三条最隐蔽：状态还是「启用」，
页面上那个人看着好好的，其实已经什么都干不了了。
`user.Update` 里必须同时判 status 和「改完还有没有通配权限」。

## 表单弹窗的初始值必须走 defaultValues，不能靠 reset()

「弹窗常驻 + 数据到了 `reset()` 一下」是错的。受控控件（`<Controller>` 包的下拉）
在 reset **之后**才挂载，首次渲染读到 `undefined`，Radix 就把它当非受控组件接管内部状态，
之后 prop 再变也不认。

症状有两个，一起出现：**下拉框明明有值却是空的**、**表单一打开就是「脏」的**。
排查时被后者带偏过——以为是脏检查的问题，其实是同一个根因。

正确做法：外层取数据，数据齐了用 `key` 挂一个全新的内层表单，初始值只从
`useForm({ defaultValues })` 进。详见 DECISIONS.md §7.5。

## 不许用 window.confirm / alert / prompt

内嵌浏览器、WebView、开了「阻止弹窗」的标签页会**静默屏蔽并返回 false**。
表现是**弹窗永远关不掉**——ESC 和右上角 ✕ 都没反应，看着像组件坏了。
已加 ESLint 规则禁掉。用 `useConfirm` + `<ConfirmDialog>`。

## Radix 的 SelectValue 只在挂载时解析一次标签

值是后来改的（reset、异步数据到达），触发器上还是空白。
所以全站用封装的 `<SelectField options={...}>`——它自己渲染标签，不依赖 Radix 的解析。

## 图标按钮的热区不能等于图标本身

14px 的 ✕ 只有 14px 热区，鼠标差几像素就点不中，用户的感受是「这个按钮是坏的」。
一律套一层 `size-8` 的容器。

## 测试库的清理清单漏表 = 单独跑过、一起跑就挂

`testdb` 的 `truncatedTables` 漏了 departments/roles，上一个测试留下的
「TECH」编号撞了下一个测试的唯一约束。**加了带唯一约束的新表就要加进清单**；
清空 roles 之后还要把迁移里的内置 admin 角色重新插回去（迁移只跑一次，TRUNCATE 会带走它）。

## 详情页显示了一个字段，才发现列表和详情两个接口对它给的答案不一样

角色详情页加上「成员」这一行之后，页面显示「还没有人用」，而列表上明明写着 2 个人。
查下来是 `GetRole` 的 SQL 只 `SELECT *`，没带 `ListRoles` 里那两个计数子查询。
**这个 bug 一直都在，只是以前详情不显示这个字段，没人发现。**

规矩：**同一个字段在列表和详情里必须由同一段 SQL 算出来。** 加详情字段时顺手核一遍
列表接口的返回，两处对不上比不显示更糟——人会以为数据错了。

## 套了 <form> 之后，分节之间的间距会消失

外层 `space-y-8` 管的是它直接子元素的间距。把几个 `<DetailSection>` 包进一个
`<form>` 之后，form 成了唯一的子元素，里面的分节全贴在一起。
表现是「区块标题紧挨着上一组的最后一行」。**间距类要跟着挪到 `<form>` 上。**

## 前端测试用 vitest + jsdom，写完要「把 bug 放回去」验一遍

第 ④ 步收尾时补的（`make dev-check` 里已经带上 `pnpm test`）。盯的都是**真踩过的坑**，
不追覆盖率：表单初始值有没有灌进去、离开拦截会不会把「保存」自己拦下来、
时间是不是北京时间、权限不够能不能手敲 URL 进编辑态。

**写完必须验有没有牙齿**：把被测的那行改回出 bug 的样子，跑一遍，看对应用例是不是真的红。
不红就说明这个测试是摆设。这一批我都验过了。

jsdom 缺 `matchMedia` / `scrollIntoView` / `hasPointerCapture` / `ResizeObserver`，
Radix 的下拉和弹窗全要它们，`src/test/setup.ts` 里补齐了 —— 不补的话报错信息跟被测的
东西毫无关系，能查半天。

另外：`getByText` 找中文名字很容易一次命中两个（页面标题和字段值都是那个名字），
用 `getByRole('heading', ...)` 这类带角色的定位更稳。

## 分组标题比它管的内容还小，层级就是反的

详情页的「基本信息」「安全」原来用 11px 淡灰眉标（`.section-label`，和侧栏分组名同款），
而字段标签是 14px —— **标题比自己管的内容又小又淡**，用户反馈「很难分辨」。

侧栏里那个样式没问题：它压着的菜单项是 14px，但菜单项之间有 40px 行高和缩进撑着层级。
详情页里没有这些，只剩字号在说话，于是层级整个倒过来了。

**同一个「小标题」样式不能无脑复用到留白结构完全不同的地方。**
现在：区块标题 16px/600 前景色 + 下压一条线。

⚠️ 修的时候顺手把字段标签按字号表压到了 12px，**这是过度纠正**：中文标签在 14px 的值
旁边立刻显得像附注，而标签列恰恰是眼睛要顺着往下扫的那一列。已改回 14px，
只用 `text-muted-foreground` 弱化。**层级靠区块标题去拉，不靠把标签压小。**
字号表里「字段标签 12px」那条本来是给表格表头写的，别套到详情页上。

## 表单组件挂早了，`defaultValues` 就是空的——而且再也补不上

「弹窗改成详情页内编辑」之后，直接打开 `/users/:id?edit=1` 发现整个表单是空的，
但标题栏的名字是对的。原因：组件在 `useUser` 返回之前就挂载了，`useForm` 只在
挂载那一刻读一次 `defaultValues`，读到的全是 undefined；数据后来到了，key 没变
就不会重挂，于是永远空着。

**规矩：取数据的组件和建表单的组件必须分开，数据没到就别渲染里面那个。**
这和「初始值必须走 defaultValues 不能靠 reset」是同一条规则的两面。

进编辑态要换 key 重挂表单让它吃到最新数据，但 **key 不能用 `version`**：
编辑到一半后台刷新一次（`refetchOnWindowFocus` 开着，切窗口回来就刷），
版本一变就重挂，正在敲的东西直接没了。用「第几次进编辑」的计数器。

## 左右分栏页换一条记录，也算「离开」

部门页编辑到一半，点左边另一个树节点 —— **改动一声不吭就没了**，一句提示都没有。
因为那次导航只改了 `?detail=`，pathname 没变，离开拦截放行了。

所以规则不是「只比 pathname」这么简单，而是：**同一个 pathname 下，换模式不算离开，
换记录算。** 分栏页要把记录参数报给守卫（`useUnsavedGuard(dirty, ['detail'])`）。
整页详情不用传 —— 换记录就是换 URL 路径，第一层已经拦住了。

## 离开拦截只能比 pathname，不能比查询串

`useBlocker` 的判断里带上 `location.search` 之后，**点「保存」会被自己拦下来** ——
因为退出编辑态是把 `?edit=1` 摘掉，查询串变了，被当成「要离开页面」，
弹一个「放弃未保存的修改？」，而东西明明已经存进去了。

同一页换模式（`?edit=`、`?detail=`、筛选参数）都走查询串，**它们都不算离开**。

还有一条：确认框点「取消」时必须 `blocker.reset()`，否则 blocker 一直卡在 blocked，
之后点任何菜单都没反应——现象是「导航坏了」，很难联想到是上一次拦截没复位。

## 列表状态「返回时不丢」，九成是 URL 设计送的，别自己存一份

把详情从抽屉改成独立页面时，第一反应是「得把筛选条件抄一份带到详情页去」。
其实不用：筛选和页码本来就在列表 URL 上，而且是 `replace` 写的 ——
**列表在浏览器历史里始终是一条带着完整状态的记录**，`navigate(-1)` 退回去就是原样，
再加上 React Query 的缓存，回去连闪都不闪。

真正要写的只有「身后有没有那条记录」的判断（`lib/detail-nav.ts`），
因为链接可能是别人发的、或者 ⌘+点击开的新标签，那种情况只能跳过去。
判断用的记号打在 history state 上，**刷新之后还在**，所以详情页按 F5 再返回也是对的。

顺带记一笔：react-router 的 `<ScrollRestoration>` **只认 `window.scrollY`**，
外壳是「整屏不滚、只有内容区内部滚」的项目直接用不了，得自己盯容器。

## 组件的约定注释写反了，比没写更糟

用户详情抽屉分了三个 tab，「角色」那个只有一个徽章飘在半屏空白里，很难看。
根因不在样式：`DetailDrawer` 的约定注释写着「内容超过两组就分 tab」，
而 DECISIONS.md §7.6 写的是「要分 tab → 用独立页面」——**两条规矩直接打架**，
照着组件注释写的人就跑偏了，而且只有用户模块跑偏，另外三个抽屉还是平铺的。

已经把 `tabs` prop 整个删掉（不给错误用法留口子），§7.6 补了「抽屉里不许有 tab」。
**教训：约定写进组件注释时，回头核一遍 DECISIONS.md 里对应那节说的是不是同一件事。**
`make lint-docs` 查得了引用是否有效，查不了两处说法是否矛盾。

## 集成测试要 Docker，冷启动第一轮会超时

Docker 刚启动时第一个 testcontainer 会因冷启动超时失败，并连累同一包里的其它用例。
热一次再跑就正常——别被第一轮的一片红吓到。

## 「参数结构体里有 TenantID 字段」拦不住任何人

多租户改造一开始的想法是「让每个 Params 都带上 `TenantID`，漏填编译不过」。
**不成立** —— Go 有零值：`ListUsersParams{Keyword: x}` 少填 `TenantID` 照样编译通过，
跑出来是 `uuid.Nil`，查到 0 行、不报错。那正是不用 RLS 想避开的静默失败。

真正管用的是让业务代码**根本碰不到那个字段**：sqlc 的产物挪进
`internal/repo/internal/sqlcgen/`，靠 Go 的 internal 包规则把「拿到裸句柄」这条路
在编译期堵死，外面只透出 `ForTenant` 包装（参数结构体里压根没有 `TenantID`）。

验证过一次：把某条查询的 `tenant_id` 条件删掉、重新生成，
**调用它的业务代码直接编译不过**（那个方法从租户句柄上消失了，掉进 Unscoped 里去了）。
「不是不该用，是用不了」不是修辞。

## 写租户隔离测试，两个陷阱都会踩

1. **只给一个租户造数据**。断言「A 看不到 B」时，B 那边是空的 ——
   把 `WHERE tenant_id = ?` 整条删掉，测试照样绿。两边都必须有数据。
2. **只测读不测写**。读的时候都记得加条件，按 id 更新时很容易觉得「id 唯一就够了」。
   必须断言「拿 A 的句柄改 B 的 id，影响行数是 0」，而且**回头再查一次 B 的数据确认没被改**
   —— 只信返回的影响行数是不够的。

写完之后一定要故意破坏一条 SQL 跑一遍，确认测试真的会红。这两条都是我们实际验过的。

## squawk 不认识 goose 的 Down 段

直接把迁移文件喂给 squawk，会把 `-- +goose Down` 里的 `DROP TABLE` / `DROP COLUMN`
当成要上线的删除操作，一口气报几十条。`make lint-migrations` 因此只把 Up 段
（`sed '/-- +goose Down/,$d'`）喂进去 —— Down 段的职责本来就是删东西，
而且只在开发期跑。

另外 `--assume-in-transaction` 一定要开：goose 把每个迁移包在事务里，
不告诉 squawk 的话 `prefer-robust-stmts` 一条就报 44 处。

## 递归 CTE 只给种子那半加租户条件，静态检查得按「段」查才抓得到

`WITH RECURSIVE` 的两半共用同一个别名 `d`。按「整条 SQL 里有没有 tenant_id」查，
或者按「别名有没有被绑定」查，都会被种子那半糊弄过去。
`gen lint-sql` 因此先按 `UNION` 把查询切段，每段各自要求「每一处租户表引用都绑到了租户参数上」。
验证方式同上：故意删掉递归那半的条件，检查器要报出来。

## `make dev-admin` 之后还要看它打出来的公司代码

多租户之后登录框有三格。迁移建的那个默认租户，`code` 是**随机**的
（MULTI-TENANCY.md §10.9：叫 `default` 的话公网上谁都猜得到），
所以 `make dev-admin` 和服务启动时的 bootstrap 都会把它打出来 —— 不看就登不进去。
重建库之后代码会变，别照着上一次的记。

## `pkill -f "cmd/server"` 杀不掉 `go run` 起的后端

`go run ./cmd/server` 会先编译成 `/tmp/go-build.../exe/server` 再执行，
父进程的命令行里有 `cmd/server`，**真正监听端口的那个子进程没有**。
于是 `pkill -f "cmd/server"` 之后端口还占着，新起的实例静默失败，
你对着**旧代码**在浏览器里调试，怎么改都不生效。

浪费了一轮排查（以为 `/me` 少了字段是序列化问题，其实跑的是上一个二进制）。
杀干净用端口：

```bash
lsof -ti:8080 | xargs kill
```

## 权限模块的 Realm 是必填，没有默认值

`perm.Module.Realm` 分 `tenant` / `platform`，**故意不给默认值**（忘了填直接 panic）。
默认成 tenant 的话，哪天有人写平台模块忘了填，租户管理员在自己的角色配置页上
当场就能勾上「开通租户」—— 而角色页的设计就是「把整个注册表倒出来」。
安全边界上宁可多敲一行。

## 通配权限 `*:*` 会跨 Realm —— Casbin 匹配器管不了这件事

给 `perm.Module` 分了 tenant / platform 之后，以为「租户管理员碰不到平台权限」是自然成立的。
**不成立。** 内置 admin 的策略是一条 `p, admin, <租户>, *, *`，
而匹配器里的 `p.obj == "*"` 对**任何** obj 都成立 —— 包括平台模块的资源名。
写探针实测，`Allow(租户admin, 平台权限点)` 返回 **true**。

Realm 的隔离必须在 `Allow` 里**显式判一次**，不能指望「平台接口另有把守」——
那等于把整个隔离压在一个 if 上。

教训更一般：**通配符和分类边界是两回事**。加了新的分类维度（Realm、将来可能还有别的），
要回头问一句「通配符会不会把它一起通配掉」。

## 加字段时，先找齐所有手拼这个结构体的地方

`perm.Point` 有两处构造：`Module.Point()` 和 `menu.go` 里手拼的两行。
给 Module 加 `Realm` 时只改了第一处 → 菜单判定拿到空 Realm → **所有菜单一起消失**。

现象是「登录进去什么都没有」，非常难第一时间联想到是权限点少了个字段
（MULTI-TENANCY.md §3.1 恰好警告过同一个现象，只是原因不同）。
已经把构造收敛到 `m.point(a)` 一处。**结构体加字段前先 grep 一遍 `Xxx{` 字面量。**

## 两个静态检查看的不是同一件事时，中间一定有缝

`lint-sql` 查「SQL 里 tenant_id 有没有绑到参数上」，
`gen tenant-queries` 查「参数结构体里有没有 TenantID 字段」。

把 `sqlc.arg('tenant_id')` 写成 `sqlc.arg('tid')`：前者绿（确实绑了参数），
后者认不出租户，于是这条查询**悄悄落到不带租户的句柄上**，两边都不报错。

修法是让两者**对账**：落到 `UnscopedQueries` 上的查询必须写了 `-- tenant-exempt:`。
补上之后立刻又查出 3 条基础设施查询没写理由 —— 说明这条对账本身就该有。

## 会话过期不走退出登录，缓存不会被清

401 只是跳回登录页，React Query 的缓存原封不动；登录成功时只 invalidate 了 `/me`。
换一家公司的账号登进来，用户/部门列表会**先渲染上一家公司的缓存**再被后台重取替换
（全局策略就是「先渲染旧数据、后台重取」）。共用电脑时这是实打实的跨租户可见。

登录成功要 `queryClient.clear()`，不是只 invalidate `me`。

## 「只加载启用中的租户」会让停用变成单向门

权限策略和配置缓存原来都按 `WHERE status = 'active'` 遍历租户。看着很合理，实际是个陷阱：

停用 → 下次刷新时它的策略从 enforcer 里消失 → **重新启用之后没有任何东西会触发重载**
（`authz_changed` 只挂在 roles / user_roles / role_permissions 上，`tenants` 上没有触发器）
→ 那家公司的人能登录，但每个请求都 403，只能重启服务才恢复。

改成遍历**全部**租户就没这个问题了：停用的租户认证根本过不去（认证时核 `tenants.status`），
把它的策略留在内存里没有任何风险，反而省掉一整类「状态没跟上」的问题。

**一般化的教训：内存缓存按某个可变状态过滤时，先问一句「这个状态反转回来时谁负责重载」。**

## 安全上「不能说」和体验上「必须说」，分场合

登录接口**不能**告诉对方「这家公司被停用了」——三种失败必须给一样的回应，
否则登录接口就是一个「这家公司是不是你们客户」的探测器。

但**已经拿着有效会话**的人被停用挡下来时，必须说清楚原因。静默踢回登录页的话，
他再登一次看到的是「用户名或密码错误」，整家公司被莫名其妙挡在门外，还以为是自己记错了密码。

同一条信息，对匿名请求要藏，对持有本组织有效会话的人要讲 —— 判断依据是
**对方已经证明了自己属于这个组织没有**。

## 检查器里的「提前 return」会把整份白名单变成死代码

`lint-sql` 查两件事：谁调 `Unscoped()`、谁调 `Platform()`。它先读文件，然后
`if !rxUnscopedCall.Match(raw) { return nil }`，再往下才是同时检查两个句柄的循环。

于是**只调 `.Platform()` 不调 `.Unscoped()` 的文件在那一行就被跳过了** ——
`platformCallers` 那份白名单整整是死代码。而 `Platform().ListTenants()`
就是整份客户名单（MULTI-TENANCY.md §8.1），业务模块拿它一点阻力都没有。

发现方式不是读代码，是**往不该有的地方加一行再跑检查**：在 `internal/service/user`
里写 `_ = s.store.Platform()`，`make lint-sql` 全绿。

**一般化的教训：一个检查器同时管 N 件事时，前置过滤必须是那 N 个条件的并集。**
而验证它的唯一办法是给每一件事各写一条「故意违规」的守门测试 ——
检查器自己没有测试的时候，它漏判是**静默**的：所有查询照样绿。

## 「有 tenant_id 条件」不等于「这张表被约束住了」

`lint-sql` 原来的判定：一段 SQL 里出现裸的 `tenant_id = $1`，就把这一段所有
不带别名的租户表全算成绑好了。于是这条**通过了检查**：

```sql
UPDATE users SET status = 'disabled'
WHERE id = ANY(sqlc.arg('user_ids')::uuid[])
  AND department_id IN (SELECT id FROM departments WHERE tenant_id = sqlc.arg('tenant_id'))
```

条件绑的是 `departments`，`users` 其实光着 —— 传一串别家公司的 user_id
就能把人一起停掉。这一类漏法**别的几层都看不见**：

- `ForTenant` 包装：查询确实有 tenant_id 参数，照样注入，看着完全正常
- 运行期兜底（`repo/assert.go`）：核的是返回行，**写操作没有返回行可核**
- 只剩「有人记得写跨租户测试」，那不是机制

修法是把裸绑定判成「只有一处不带别名的租户表引用时才算数」，多于一处要求写限定名。
**一般化的教训：静态检查里凡是「就近推断」的那一步，都要问「推错了会怎样」。**

## 限流按 IP 分桶，在 SaaS 下是不够的

一家公司十个人十个 IP，每人都在自己的额度内，加起来照样能把服务压住，
而**别家客户没有任何办法**。所以要两个维度：IP 那一维跑在认证之前（登录接口靠它），
租户那一维跑在认证之后（那时才知道是哪家公司）。

⚠️ 租户维度的阈值要定得宽：组织内部共用一个桶，太紧就变成同事之间互相挤。
它要挡的是「一个组织的量把别家挤掉」，不是「组织内部谁用得多」。

顺带一条：**给中间件加维度时要确认它挂对了位置。** 按租户分桶的限流挂在
`Authenticate` 之前的话，每次请求读到的主体都是空的，键永远是空串 —— 等于没装，
而且不报任何错。守门测试要断言的是「A 打爆之后 B 照样能用」，
不是「A 会被挡」—— 后者只证明限流器活着，前者才证明它是分桶的。

## 常量定义了不等于用上了

`audit.ActorPlatform` 定义好了、DB 的 CHECK 约束也放行了、注释还专门解释了它的意义，
但中间件里只有 `if p.Type == PrincipalService { service } else { user }` 两档 ——
平台管理员落到 `user`。整个常量只在「平台登录」那一处被手写用过。

于是开组织、停组织这些**最高权限的动作**在审计里全是普通 user，
只能靠「tenant_id 是 NULL 而 actor_id 不是」反推。

**枚举类型的翻译一律写成穷举 switch，default 挑最保守的那档。**
if/else 两档的写法在加第三种类型时不会编译错误，只会静默归到 else 里。

## 逃生口要写在「被检查的那个东西」里面，不是旁边

`lint-sql` 原来的豁免写法是 Go 注释 `// tenant-exempt: 理由`，紧挨着那条 SQL。
加了运行期兜底之后立刻不够用了：**tracer 拿到的是字符串的值，Go 注释它看不见**。

改成把标记写进 SQL 本身：

```go
pool.Exec(ctx, `-- tenant-exempt: 这一句的全部意义就是绕过应用层
    UPDATE audit_logs SET action = 'tampered' WHERE id = $1`)
```

一处声明，构建期和运行期都认，而且**它跟着这条 SQL 走** —— 有人把这段挪到别的函数里，
豁免和理由一起挪过去，不会掉队。

⚠️ sqlc 的查询用不了这招：它把除 `-- name:` 以外的注释**全部剥掉**。那批只能靠
「生成一份查询名清单」，理由留在 `db/queries/*.sql` 里。**生成，不能手写** ——
手写就是同一份白名单存在两处。

## 给检查器写测试，样本本身会被检查器抓

`internal/tenantsql` 和 `repo/trace.go` 的测试里全是**故意写坏的 SQL**，
`make lint-sql` 一次抓了 7 条。这不是误报，是它在正常工作。

解法就是用那个逃生口，并在理由里写清「这是故意写坏的样本」。
⚠️ Go 注释那种写法**只往上找 5 行**，表驱动用例里样本一多就够不着，
每组前面都要有一条。

## 测试要从真正的入口进，否则可能在「开关没起作用」的状态下全绿

给 tracer 写测试助手时，第一版直接调了内部的 `check`。而开关（`tenantAssertions`）
那道门在外层的 `TraceQueryStart` 上 —— 于是所有用例都在「兜底其实没开」的状态下通过，
**包括那些断言「必须炸」的**（它们炸了，但不是因为开关开着）。

是「关掉之后不该检查」那条用例把它抓出来的。**每写一个测试助手，先问它绕过了哪几层。**

## 一个判据被两处调用时，它就该单独成包

`lint-sql`（构建期）和 pgx tracer（运行期）判的是同一件事：这条 SQL 有没有把
每一处都绑到租户上。两边各写一份的话，中间那道缝就是下一个漏洞 ——
这个项目已经栽过一次（`sqlc.arg('tid')` 那个）。

抽成 `internal/tenantsql` 叶子包（只依赖标准库），两边 import 同一份。
⚠️ 加 internal 包要同步改三处：`lintstructure.go` 白名单、`DECISIONS.md` §1.1 的树、
以及包自己的 doc comment 说清「为什么它不能住在 repo 或 cmd/gen 里」。

## 「运行时可调的约束」不能写进 huma 的 tag

huma 的 `minimum:"10"` 是编译期常量，而平台给租户划的上下界存在
`platform_settings` 里、平台管理员随时能调。写死 tag 就等于绕开那套护栏。

这条约束**决定了配置管理必须是注册表驱动的**：既然「有哪些项、什么类型、
区间多少」必须由服务端在运行时说，那就必然要有一份服务端能描述这些的东西。
前端也不能抄一份范围 —— 平台一收紧，前端还按老范围放行，用户在点保存那一刻才被拒。

## 配置的写入口必须是白名单，不然「敲错键名」是静默失败

`Settings.Set` / `SetPlatform` 原来接受任意 key 字符串。而上下界的语义是
**「没有对应行 = 不受限」**，于是平台管理员把 `limits.security.password_min_length.min`
敲成 `.mn`，那条下限就悄悄没了 —— 没有报错，页面上还像保存成功了。

修法：注册表同时是白名单，未声明的 key 一律拒绝；界的键名由 `config.BoundKey()`
拼出来，不由人敲。**顺带一条**：值的类型也要在写入口校验，往 int 项写 `10.5`
会存进去、读的时候解不出 int 退回默认值 —— 又是一次「写了不生效」。

## 测不出东西的测试比没有更糟

写跨租户隔离时顺手加了一条「A 的响应里不该出现 B 的值」，断言是
`strings.Contains(body,"20") && !strings.Contains(body,"12")` —— 这个条件几乎
永远为假，它永远不会红。而它躺在那里会让人以为这个方向已经测过了。

配置接口压根没有「按 id 查别人」的入口（租户从会话取），所以那个方向**无洞可测**。
正确的做法是删掉它并写一行注释说明为什么不测，而不是留一条假的。

## 一个页面要显示的东西，别内联在某个页面里

平台管理端的顶栏原来内联在组织列表页里。加第二个页面时才发现：要么新页面没有
导航，要么复制一份顶栏。抽成 `PlatformShell` 布局路由之后两个页面都干净了。

**判断标准**：这段 JSX 描述的是「这个页面」还是「这一类页面」。是后者就该往上提。

## 有种子数据的表，清理方式是「快照 + 还原」，不是 TRUNCATE

`platform_settings` 装的是迁移种下的平台配置（审计保留期、产品名、租户设置的上下界）。
它当时既不在 `truncatedTables` 里、也没有别的还原手段，于是：

- **不能 TRUNCATE**：清空了种子就再也回不来，后面每个用例都看到「一条界都没有」，
  而那恰好等于「不受限」——测试会**变绿**，因为护栏被关掉了
- **也不能不清**：有用例会改上下界，不还原就泄漏给后面每一个用例

实际后果：新写的配置管理用例**单独跑全绿、和整套一起跑挂**，报
「这一项不能小于 20（平台的下限）」——那个 20 是几百行之外另一个用例设的。
和 MEMORY.md 里「清理清单漏表 = 单独跑过、一起跑就挂」是同一个坑的变体。

解法是**启动时照一张快照，每个用例前恢复成那张**（`testdb.snapshotPlatformSettings`）。
好处是种子数据只有迁移一处定义，测试里不抄第二份，改迁移不用改测试。
还原用「先删后插」不用 upsert —— 用例可能**新增**了一行，upsert 留不掉它。

## 加通知触发器要按列限定，否则热路径会变成重载风暴

`service_accounts` 上原来没有 `authz_changed` 触发器（00003 只给 roles /
role_permissions / user_roles 挂了）。后果是**给机器账号换角色之后降权不生效** ——
enforcer 里还是旧绑定，那个对接凭据会以旧权限一直跑到下次有人改角色为止。

补触发器时有个坑：`TouchServiceAccount` **每个 API 请求**都会写 `last_used_at`。
挂成全表 `AFTER UPDATE` 的话，每一次调用都广播一次策略重载，而重载是
「整体重建 enforcer、遍历所有租户」—— 那不是性能退化，是把系统打死。

用 `UPDATE OF role_id, status, deleted_at` 限定列。它按语句 SET 列表里提到了哪些列判，
和值有没有真的变无关，所以 `SET last_used_at = now()` 不会触发。

## 「改完显式 Reload」的测试，有没有触发器都会绿

测试里没有起 LISTEN 协程（那是 `cmd/server` 的 `watchChanges` 干的），所以既有的
权限变更测试都是「做动作 → 手动 `checker.Reload()` → 断言语义」。

这类测试**测不到触发器存不存在**。上面那个漏了三年的触发器，就是被这种写法一直掩盖着。

要测触发器只能直接测：开一条连接 `LISTEN authz_changed`，做动作，看通知来不来。
而且两半都要测 —— **「不该发通知的时候不发」那半更要紧**，
它是「按列限定」那个设计唯一的守卫。

## 菜单图标名忘了注册，只会显示成一个圆点

图标名是后端 `perm.Menu.Icon` 里的字符串，前端按名字去显式注册表取组件
（不整包 import lucide —— 那会让首屏从 500 KB 涨到 1.4 MB）。

漏注册**不报错、不白屏**，只是那一项菜单变成个圆点。写这条检查的时候，
仓库里就已经躺着一个了（配置管理的 `shield`，我自己上一轮加的）。

现在 `make lint-structure` 会交叉核对：`perm/modules/*.go` 里的 `Icon:` 和
`MenuIcon.tsx` 里的注册表必须对得上。**「同一个标识符在两处各写一份」的地方，
一律该有机械核对** —— 这个项目已经栽过好几次了。

---

## 审计防篡改的 REVOKE 必须打到分区，父表 REVOKE 不下沉

`audit_logs` 是**分区表**，数据在子分区 `audit_logs_YYYYMM` 里。PostgreSQL 直接寻址分区
（`DELETE FROM audit_logs_202608 …`）校验的是**分区自己的 ACL**，父表上的 `REVOKE` 不会下沉到分区。

老迁移只 `REVOKE ALL ON audit_logs`（父表），而更早的 `GRANT … ON ALL TABLES` + 00006 的
`ALTER DEFAULT PRIVILEGES` 把 UPDATE/DELETE 授给了每一个（现有和未来的）分区 —— 应用账号
`fries_app` 能直接删改审计，且能把哈希链一并改自洽，预防和检测双双失效。**这是真洞，已用受限角色复现。**

修法（迁移 00013）：`ensure_audit_partition()` 建完新分区当场 `REVOKE UPDATE, DELETE`，
并给已存在的分区补一次。自检 `warnIfAuditTamperable` 也改成查**分区**（`pg_inherits`）而非父表 ——
只看父表会误报「防篡改已生效」。

**为什么一直没被发现**：集成测试用库 owner 连（superuser 绕过一切权限检查），测不到这类约束。
守它的回归测试必须起独立容器、在迁移**之前**建好受限角色 `fries_app`（见
`testdb.StartWithAppRole` + `audit_tamper_test.go`）。**凡是靠「DB 层收权」防的东西，测试就得用那个受限角色，不能用 owner。**

---

## 加租户表 / 唯一索引，漏写现在会被 lint-sql 当场拦（不再是静默）

`make lint-sql` 的 `checkTenantTableSchema`（`cmd/gen/lintsql.go`）回放迁移，机械校验两件事：

- **有 `tenant_id` 列的表必须登记进 `tenantsql.tenantTables`** —— 漏登记 = 那张表的查询一条都不被租户检查。
- **租户表上的唯一性必须用 `CREATE UNIQUE INDEX` 且 `tenant_id` 打头** —— 内联 `UNIQUE` 约束、
  非 tenant_id 打头都会被拦（否则跨租户唯一性串味，A 租户建不了 B 租户已有的值）。

破例走 map 白名单并写理由：`schemaExemptTenantTables`（纯触发器表，如 `audit_chain_head`）、
`schemaGlobalUniqueIndexes`（认证索引 `uk_sessions_token` / `uk_service_accounts_prefix`，登录时租户未知）。
以前 MODULES.md 承诺「索引会被 lint-sql 拦」其实没实现，现在补上了。

---

## 前端错误码打错现在编译期就报（`make errcodes-ts` 生成联合类型）

`ApiError.code` 收成 `frontend/src/api/errorCodes.ts` 里的 `ErrorCode` 联合类型，
由 `make errcodes-ts` 从后端 `errs.All()` 注册表生成（`make check` 校验它最新）。
`err.code === 'commmon.x'`（敲错）不在联合里 → tsc 当场报，不再静默不匹配。

⚠️ **`errcodes-ts` 这个 gen 命令绝不能带 `-tags genonly`**（不像 lint-sql / tenant-queries）：
genonly 会摘掉 `internal/config`、各 service 包的 init，`errs.All()` 少码，联合类型静默缩水。

---

## docs / openapi 端点生产默认关（`server.expose_docs`）

`/api/v1/docs`、`/openapi`、`/schemas` 不经过授权中间件，开着等于把整份接口清单
（含 `/platform/*` 平台管理端）摊给未登录的人做侦察。现在由 `server.expose_docs` 控制，
**默认 false**，`config.example.yaml`（本地开发）里设 true。关掉不影响 `make gen-api`
（它走 `-openapi` 离线导出，不碰 HTTP 端点）。已有的 `config.yaml` 若没这行，本地 docs 会消失，补一行即可。

---

## 限流器 / 幂等键的跨副本共享状态：server.shared_state_store 开关

内存版每副本一份，多副本部署时限流阈值放大 N 倍、幂等重放能溜过去。现在两者都有 PG 版
（`rate_limits` / `idempotency_keys` 表），`server.shared_state_store` 选 `memory`（默认，
单副本零延迟）或 `postgres`（多副本共享）。两张表都没有 tenant_id（维度/租户已拼进 key），
是跨租户 infra 表，查询走 Unscoped + `-- tenant-exempt`。

⚠️ **限流器 PG 版是有取舍的**，改之前先知道：
- 语义从令牌桶变**固定窗口**（每秒一桶、桶内至多 burst 个），窗口边界处略松。
- **每请求打一次库**，高并发下是真成本 —— Redis 才是对的存储，PG 版是「多副本又暂不想引
  Redis」的过渡。DB 出错时 **fail-open 放行**（护栏不该把 DB 抖动放大成全站 429）。
- 幂等键 PG 版仍只记键不记响应，要做「重试重放原响应」得扩 `IdempotencyStore` 接口。

**踩过的坑**：`db/queries` 里的 `-- tenant-exempt:` 必须写在 `-- name:` **之后**，
`tenantsql.SplitQueries` 按「当前 query」归属，写在 name 前面会错位到上一条 query。
另外用宿主机时钟算过期时间、拿容器 DB 的 `now()` 比时，测试要用**负 ttl**（明确落在过去），
`ttl=0` 会被宿主机/容器的微小时钟偏差搅成 flaky。

---

## 文档里别写 `xxx` 占位路径，`lint-docs` 会当真去找

`make check` 里的 `lint-docs` 会把行内代码里所有形如路径的写法拿去仓库里核对，找不到就红。
所以 README / docs 里举例要**指向真实文件**（如 `modules/supplier.yaml`），别写占位路径：

```
modules/xxx.yaml      ← 会报「提到路径 xxx，但仓库里找不到」
```

两个豁免：**代码围栏内的内容会先被剥掉**（`rxFence`，所以上面这个反例自己不触发），
命令里的占位符也不算路径（`make gen-module name=xxx` 不会被查）。

写这条记忆时先踩了一次 —— 正文里直接写那个占位路径，`lint-docs` 当场把它拦了。

## 对外发布前清一遍公司痕迹，不只是 module path

2026-08-12 从内部仓库转公开仓库（module path 改为 `github.com/ramoncjs3/fries`）时，
除了 115 个文件的 import 路径，还有这些**内部痕迹**要清：
文档里的企业私服地址（含 `user:pass@host` 形态）、`GOPRIVATE` 私有仓库通配、
skill 里指名内部私服的表述，以及一个**误入版本库的 26MB 构建产物**
（`backend/server`，二进制里嵌着旧 module path，已补进 `.gitignore`）。

**约定**：MEMORY / DECISIONS 里记环境问题时，只记**技术结论**
（如「Go 拒绝在 HTTP proxy URL 里传凭据，凭据要进 `~/.netrc`」），
不要贴内部主机名、私服路径、内网域名 —— 它们对读者没用，只会跟着仓库泄出去。
