package notify

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// AsyncMailer 把发信挪到请求路径之外：SendEmail 只入队就立即返回，真正的投递在后台 worker 里做。
//
// 为什么要它（安全，不只是性能）：忘记密码「申请」接口对**存在的账号**才发信，同步发信时
// 「存在 → 慢一个 SES 往返、不存在 → 快」构成用户枚举的时序侧信道（review 发现）。入队是 O(1)、
// 和存不存在无关，把 SES 往返移出请求后，两条路径耗时拉平，枚举信号消失。顺带也为将来的
// 高频通知铺路（worker 数、队列长可调）。
//
// ⚠️ **绝不阻塞 SendEmail**：队列满了就丢弃 + 记 Error，不能 block —— 一 block 就又把「发没发」
// 和请求耗时耦合起来，时序侧信道原样回来。低频事务邮件（重置/验证）偶尔丢一封可接受（用户会重试），
// 要持久化不丢再上 PG outbox（和幂等/限流的 memory→postgres 一个套路，留作后续）。
//
// 它自己实现 Mailer，所以套在任何 Mailer（LogMailer / SES）外面，调用方一行不用改。
type AsyncMailer struct {
	inner     Mailer
	log       *slog.Logger
	queue     chan Message
	wg        sync.WaitGroup
	timeout   time.Duration // 单封信的投递超时（worker 用，不是请求超时）
	recipient *recipientLimiter
}

// AsyncOptions 是 AsyncMailer 的可调项，零值给一套合理默认。
type AsyncOptions struct {
	Buffer  int           // 队列长度，默认 256
	Workers int           // 后台 worker 数，默认 2
	Timeout time.Duration // 单封投递超时，默认 30s
	// PerRecipientEvery / PerRecipientBurst 是**按收件人**的发信频率上限（防滥用）：
	// 忘记密码/自助注册都对用户给的邮箱发信，攻击者能借平台可信发件域轰炸受害者、或放大发信
	// （review 发现）。默认 burst 3 + 每 20 分钟回 1 个：正常人重试够用，批量轰炸挡下。
	PerRecipientEvery time.Duration
	PerRecipientBurst int
}

func (o AsyncOptions) withDefaults() AsyncOptions {
	if o.Buffer <= 0 {
		o.Buffer = 256
	}
	if o.Workers <= 0 {
		o.Workers = 2
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.PerRecipientEvery <= 0 {
		o.PerRecipientEvery = 20 * time.Minute
	}
	if o.PerRecipientBurst <= 0 {
		o.PerRecipientBurst = 3
	}
	return o
}

// recipientLimiter 按收件人做固定预算的令牌桶（照 middleware 的 memoryRateStore 抄，
// 但**空闲淘汰阈值要够长** —— 太短的话桶被清掉，下次又能突发一整轮，限流形同虚设）。
type recipientLimiter struct {
	every rate.Limit
	burst int
	idle  time.Duration

	mu        sync.Mutex
	buckets   map[string]*recipientBucket
	lastSweep time.Time
}

type recipientBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newRecipientLimiter(every time.Duration, burst int) *recipientLimiter {
	// 记住一个收件人的时间要盖住「攒满一整桶」的跨度，否则淘汰后突发预算白白重置。
	idle := every*time.Duration(burst) + time.Hour
	return &recipientLimiter{
		every:     rate.Every(every),
		burst:     burst,
		idle:      idle,
		buckets:   map[string]*recipientBucket{},
		lastSweep: nowFn(),
	}
}

func (r *recipientLimiter) allow(key string) bool {
	now := nowFn()
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.lastSweep) >= r.idle {
		r.lastSweep = now
		for k, b := range r.buckets {
			if now.Sub(b.lastSeen) > r.idle {
				delete(r.buckets, k)
			}
		}
	}
	b, ok := r.buckets[key]
	if !ok {
		b = &recipientBucket{limiter: rate.NewLimiter(r.every, r.burst)}
		r.buckets[key] = b
	}
	b.lastSeen = now
	return b.limiter.Allow()
}

// nowFn 便于测试替换时间（默认 time.Now）。
var nowFn = time.Now

// NewAsyncMailer 造一个异步发信器并起好 worker。用完要 Shutdown 把队列排空。
func NewAsyncMailer(inner Mailer, log *slog.Logger, opts AsyncOptions) *AsyncMailer {
	opts = opts.withDefaults()
	m := &AsyncMailer{
		inner:     inner,
		log:       log,
		queue:     make(chan Message, opts.Buffer),
		timeout:   opts.Timeout,
		recipient: newRecipientLimiter(opts.PerRecipientEvery, opts.PerRecipientBurst),
	}
	for range opts.Workers {
		m.wg.Add(1)
		go m.worker()
	}
	return m
}

// SendEmail 把邮件入队后立即返回（非阻塞）。队列满就丢弃 + 记日志 —— 返回值永远是 nil，
// 好让调用方（尤其忘记密码）无论如何都走同一条「成功」路径，不泄露发没发。
func (m *AsyncMailer) SendEmail(_ context.Context, msg Message) error {
	// 按收件人限流：挡住忘记密码/自助注册被拿来轰炸受害者或放大发信。超限就丢弃 + 记日志，
	// 一样返回 nil（不因限流与否泄露时序）。key 用排序后的收件人集合，多收件人也稳定。
	if !m.recipient.allow(recipientKey(msg.To)) {
		m.log.Warn("收件人发信频率超限，丢弃这一封（疑似滥用/轰炸）",
			slog.String("subject", msg.Subject),
			slog.String("to", strings.Join(msg.To, ", ")))
		return nil
	}
	select {
	case m.queue <- msg:
	default:
		m.log.Error("邮件队列已满，丢弃这一封（考虑调大 buffer / worker，或上持久化 outbox）",
			slog.String("subject", msg.Subject),
			slog.String("to", strings.Join(msg.To, ", ")))
	}
	return nil
}

// recipientKey 把收件人集合拼成稳定的限流 key（排序 + 规范化，收件人顺序/大小写/别名不影响限流）。
func recipientKey(to []string) string {
	norm := make([]string, len(to))
	for i, addr := range to {
		norm[i] = normalizeEmail(addr)
	}
	sort.Strings(norm)
	return strings.Join(norm, ",")
}

// normalizeEmail 把邮箱归一到「投递到哪个真实收件箱」，用作按收件人限流的 key。
//
// 不归一的话，同一个物理收件箱能用**子地址别名**换出无数个不同 key、各自独享 burst，把
// 「挡忘密轰炸 / 注册发信放大」的限流整条绕过：
//   - `victim+1@x.com`、`victim+2@x.com` …… 全投到 `victim@x.com`（+tag 是标准子地址，主流
//     provider 都投基础邮箱）；
//   - Gmail 还忽略 local 里的点，`v.ictim@gmail.com` == `victim@gmail.com`。
//
// 所以：小写 + 去空白 + 去掉 local 的 `+tag`；域是 gmail 再去掉 local 里的点。点号只对 Gmail
// 生效（别的 provider 点号可能有意义，一刀切会把不同人误判成同一收件箱）。
func normalizeEmail(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 {
		return addr // 不像邮箱，原样（也就各自独立，不误合）
	}
	local, domain := addr[:at], addr[at+1:]
	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}
	if domain == "gmail.com" || domain == "googlemail.com" {
		local = strings.ReplaceAll(local, ".", "")
	}
	return local + "@" + domain
}

// worker 从队列取信，用一个**新鲜的、带超时的 context** 投递 —— 不能用请求的 ctx（早取消了）。
func (m *AsyncMailer) worker() {
	defer m.wg.Done()
	for msg := range m.queue {
		m.deliver(msg)
	}
}

// deliver 投递一封信。**单独包一层并 recover**：inner.SendEmail 正常返回 error，但畸形输入/nil
// 解引用等一旦 panic，后台 goroutine 的未捕获 panic 会终结**整个进程**（一封坏信打挂全站）。
// 兜住后记日志、丢掉这封、继续下一封。
func (m *AsyncMailer) deliver(msg Message) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("异步发信 panic（已兜住，丢弃此信）",
				slog.String("subject", msg.Subject),
				slog.String("to", strings.Join(msg.To, ", ")),
				slog.Any("panic", r))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if err := m.inner.SendEmail(ctx, msg); err != nil {
		m.log.Error("异步发信失败",
			slog.String("subject", msg.Subject),
			slog.String("to", strings.Join(msg.To, ", ")),
			slog.String("error", err.Error()))
	}
}

// Shutdown 停止入队并把队列里剩下的信发完，最多等到 ctx 到期。
// 服务优雅关闭、HTTP 存量请求都收尾之后再调它（那之后不会再有人入队）。
func (m *AsyncMailer) Shutdown(ctx context.Context) error {
	close(m.queue)
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
