# 伸缩、Worker 与队列

> **这份文档不是当前要做的事，是「将来长成什么样」的评估记录。**
>
> 起因是一轮讨论：fries 现在只是个管理后台，如果要接一堆后台 worker、
> 甚至用它重写一个已有的重负载 Python 系统，架构该怎么组织。
> 写下来是为了**不用再纠结第二次** —— 和 MULTI-TENANCY.md §1.3 同一个用意。
>
> 结论先行：**要伸缩就加副本，不要拆微服务；要 worker 就加 `cmd/`，不要加服务。
> 队列用 PostgreSQL，不用 Redis。**

## 1. fries 能不能水平扩展 —— 能，今天就能

几个关键决定恰好都做对了：

| 已经具备 | 在哪 |
|---|---|
| 会话存在库里，不在内存 | `sessions` 表 |
| 配置和权限策略靠 `LISTEN/NOTIFY` 广播刷新，多实例自动同步 | `backend/internal/repo/listen.go` |
| 定时任务用 PG advisory lock，多实例只跑一份 | `backend/internal/task/task.go` |
| service 层框架无关（不 import echo/huma，AGENTS.md 红线 #6） | 业务逻辑本身可提取 |

**两处会退化但不会崩**，多副本之前要先修 —— **PG 实现都已就绪，用 `server.shared_state_store: postgres` 一键切换**（默认 `memory`，单副本零延迟）：

| 组件 | 多副本时的表现 | 现状 |
|---|---|---|
| 限流器（`backend/internal/middleware/ratelimit.go`） | 每副本一份桶 → 实际阈值放大 N 倍 | ✅ PG 版已实现（`rate_limits` 表，固定窗口计数）。⚠️ 语义从令牌桶变**固定窗口**（每秒一桶），窗口边界略松（跨窗口瞬时约 2×）；每秒上限用 **perSecond 稳态值**（不是 burst），和内存版对齐 —— 早先误传 burst 会让 PG 版稳态松 3 倍。**每请求打一次库**有代价，高并发下 Redis 才是对的存储，PG 版是「多副本又暂不想引 Redis」的过渡。DB 出错时 **fail-open** 放行（护栏不该把 DB 抖动放大成全站 429）—— 与幂等键的 fail-closed 刻意相反：限流放过一次无伤，幂等放过一次=重复执行，正确性比可用性更重要 |
| 幂等键（`backend/internal/middleware/idempotency.go`） | 每副本一份记忆 → 重放可能溜过去 | ✅ PG 版已实现（`idempotency_keys` 表，`INSERT ON CONFLICT` 原子认领）。DB 出错 **fail-closed**（拒绝，见上）。失败/panic/超时时释放键走 **defer + 独立 background ctx** —— 否则 panic 跳过释放、超时后请求 ctx 已取消 DELETE 失效，都会留孤儿键把该操作重试锁死到 TTL（默认 24h，且 PG 版跨副本/重启不自愈，是持久化相对内存版的新回归，已修）。⚠️ 仍只记键不记响应，真要做「重试重放原响应」时接口本身要扩（注释里写明了） |

**发信已异步**（`backend/internal/notify/async.go`）：`AsyncMailer` 把 SendEmail 挪出请求路径（入队即返回，
后台 worker 投递）。这**首先是安全修复** —— 忘记密码申请接口对存在账号才发信，同步发信时「存在则慢一个
SES 往返」是用户枚举的时序侧信道（见 MULTI-TENANCY 的枚举防护）；异步后两条路径耗时拉平，信号消失。
顺带也是「worker + 队列」的雏形。⚠️ 现在是**内存队列**，进程崩了未发的信会丢（低频事务邮件可接受，用户会重试）；
要不丢就上 **PG outbox**（表 + 认领式 worker，和上面幂等/限流的 memory→postgres 一个套路，不引 Redis）——
这正是本文档「队列用 PostgreSQL」结论的落点，留作高频通知到来时再做。

## 2. 为什么不拆微服务 —— 理由是多租户，不是保守

拆服务会**直接打掉 MULTI-TENANCY.md 第 ② 步换来的隔离保证**，三条：

1. **隔离的地基是包可见性。** `internal/repo/internal/sqlcgen` 让业务代码在**编译期**就拿不到不带租户的句柄，而那是单进程、单 module 的机制。拆开之后每个服务都要自带一份 `ForTenant` 包装 + `lint-sql` + 运行期 tracer，「一个分析器两个入口」变成「N 个分析器 2N 个入口」。
   而 MULTI-TENANCY.md §15.8 那轮审计刚刚证明了：**检查器一分成两份，中间那道缝就是下一个漏洞。**

2. **租户来源会变质。** 现在是「会话行上的 `tenant_id`」，拆开之后变成「上游服务传的请求头」—— 那正是 MULTI-TENANCY.md §4.2 明令禁止的事。要补回来就得签名令牌 + 每个服务各验一次，又多一处只能靠自觉的地方。

3. **审计哈希链是数据库触发器按租户各一条。** 多个服务写同一个库还行；一旦各自分库，链就断了 —— 而「能证明审计没被改过」是这套系统拿得出手的性质之一。

## 3. 一堆 worker 怎么办 —— 一个 module，多个 `cmd/`

| 方案 | 评价 |
|---|---|
| 塞进同一个进程（`--role=worker`） | 简单，但 API 和 worker 的资源画像完全不同，绑一起没法分别扩缩 |
| **同 module、多 `cmd/`、共享 `internal/repo`** | ✅ **推荐**。独立进程、独立扩缩容，但**隔离机制只有一份** |
| 真微服务 | 见 §2 |

推荐的核心理由：**worker 拿的是同一个 `store.ForTenant(id)`**，于是 `make lint-sql`、运行期 tracer、跨租户测试**自动覆盖 worker 的查询**，一行额外代码都不用写。

将来的形状大概是：

```
cmd/server      HTTP —— 认证 / 授权 / 审计 / 租户 / 设置
cmd/worker      作业消费者
cmd/scheduler   周期调度（已有 internal/task 那套 advisory lock）
cmd/gen         生成器与检查器（已有）
cmd/migrate     goose（现在由 Makefile 直接调）
```

部署上一个镜像多入口、或多个镜像同一 module 都行，**关键是 module 一份**。

## 4. 队列选 PostgreSQL，不选 Redis

理由不是洁癖（DECISIONS.md §6 那条「不引 Redis」本来就在）：

> **PG 作业表带 `tenant_id` 列，它自动落进现有的隔离体系** —— `lint-sql` 检查它的查询，
> 运行期 tracer 核它的 SQL，跨租户测试照样跑。
> **Redis 做不到：键没有列的概念。**

候选是 [river](https://github.com/riverqueue/river)：5.5k star、活跃、driver 就是本项目已经在用的 pgx v5、支持**事务内入队**（作业和业务数据同一个事务提交，要么都成要么都不成）、多队列、周期任务。过 AGENTS.md 红线 #11。

⚠️ 引入前仍要按红线 #11 复核一遍 star 与维护状态 —— 这份文档写于 2026-08-11。

### 4.1 队列按工作类型分，不按租户分

N 个租户 N 个队列会爆炸。队列是资源池，按**工作类型**分（拉取 / LLM / 合并）。
租户之间的公平性靠**取任务时轮转**解决（`DISTINCT ON (tenant_id)` 或每租户配额窗口）。

⚠️ **公平调度必须一开始就做。** 事后加要改取任务的核心查询，代价高得多。

## 5. 案例：一个真实重负载系统长什么样

评估时读了另一个项目（Python + FastAPI + Celery 的员工行为画像系统，约 94k 行、
169 个接口、20 个 Celery 任务、双 PostgreSQL 库 + Neo4j）。它的现状对本项目有三条直接价值。

### 5.1 三块复杂度是「Python 税」，换成 Go 直接消失

| 那边的机制 | 为什么存在 | Go 里 |
|---|---|---|
| 三个 worker 容器，pool 模型各不同（prefork / gevent / prefork） | GIL：LLM 调用是 16–35 秒纯 I/O 等待，prefork 会白占进程；拉取要真并行 | **不存在这个区分**，goroutine 一视同仁。连带消失：`max-tasks-per-child` 防泄漏重启、autoscale 调参、每容器内存分配 |
| LLM 并发上限「对齐协程池数量」 | 那个上限一半是业务约束（上游 RPM/TPM），一半是 Python 约束 | 只剩前者。一个 `semaphore.Weighted`，数值纯由上游限额决定 |
| 跨库两阶段提交 + 一张记录失败的兜底表 | 两个库在**同一个实例**上，但写两边要跨库 | 合成一个库两个 schema，整套机制连同它的失败模式一起没了 |

### 5.2 编排状态放内存，就要为它写一堆兜底

那边的流水线是 Celery chain，配一个自定义的 barrier 基类。它的文件注释本身就是自白：

> 一条链硬失败会永久阻塞收尾任务 → 所以在失败回调里把自己加进「已完成」集合解阻塞
> → 极端情况下本轮收尾不跑 → 所以还要一个每 10 分钟扫一次的清道夫兜底。

**换成「一轮跑 = 表里的一行状态机」，这三样一起消失：**

```
pipeline_runs (tenant_id, id, kind, status, ...)
pipeline_jobs (tenant_id, run_id, subject_id, stage, status, attempt, error, ...)
```

- 每个阶段完成时，**在同一个事务里**更新自己那行 + 入队下一阶段（这正是「事务内入队」的用途）
- 「这一轮完成没有」= 一句带 `FILTER` 的 count，不需要 barrier、不需要清道夫
- 取消 = 更新 run 的状态，每个阶段开头查一次
- 一条链硬失败 = 那一行标 failed，**不阻塞任何人**

而且这张表天然带 `tenant_id`。

### 5.3 Redis 键没有租户维度，是那边最大的多租户欠账

那边的 Redis 键只有功能前缀（`glm:` `drill:` `lock:` 之类），没有租户维度 ——
于是**所有租户共享限流、锁和缓存状态**，一个租户能吃掉全部 LLM 并发。

对本项目的启示是两层限流：

```
平台总闸    保护上游 API 不被打挂 / 不被 429
每租户配额  保护租户之间不互相饿死
```

只有第一层的话，「一个租户跑批量任务让所有客户变慢」就是必然。
这和 MULTI-TENANCY.md §12.3 说的是同一件事。

## 6. 性能扩展的顺序

按成本从低到高，**不要跳步**：

1. **限流器和幂等键换 PG 实现** —— 多副本的前提，接口现成
2. **每租户配额 / 平台总闸分开** —— 瓶颈在上游限额时，加机器没用
3. **作业表索引以 `(tenant_id, status, ...)` 打头** —— 和 MULTI-TENANCY.md §8.4 同一条规矩
4. **只增不减的大表按时间分区** —— 手法和 `audit_logs` 一样
5. Casbin 全量 reload —— MULTI-TENANCY.md §8.5 记了阈值（100 租户），真慢了再改「按租户一个 enforcer + LRU」
6. 每租户配额上限 —— MULTI-TENANCY.md §12.3 明说这一轮不做

**现在明确不做**：拆服务、引 Redis、换 ORM。

## 7. 边界与保质期

- 这份评估基于 2026-08-11 的代码与调研。star 数、库的维护状态会变，引入前按红线 #11 重新核
- 那个 Python 系统只读到了架构文档、任务签名与超时配置、协调器、并发闸门配置、Redis 键清单
  —— **没有逐个读它的 169 个接口和消费者实现**。真要动手前那部分得补
- 「用 Go 重写它」是好几个月的事，不是 1:1 搬运（§5.1 那三块是净删除）。
  倾向的路径是：**共用一个数据库和一份租户表，按模块搬，先搬流水线、最后搬接口** ——
  流水线收益密度最高，而且和接口层耦合最松
