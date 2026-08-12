package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// captureMailer 记下收到的信；可选阻塞，模拟慢发信器（SES 往返）。
type captureMailer struct {
	mu      sync.Mutex
	got     []Message
	block   chan struct{} // 非 nil 时，SendEmail 阻塞到它被关闭
	failErr error
}

func (c *captureMailer) SendEmail(_ context.Context, msg Message) error {
	if c.block != nil {
		<-c.block
	}
	c.mu.Lock()
	c.got = append(c.got, msg)
	c.mu.Unlock()
	return c.failErr
}

func (c *captureMailer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestAsyncMailerDeliversInBackground(t *testing.T) {
	inner := &captureMailer{}
	m := NewAsyncMailer(inner, discardLog(), AsyncOptions{Workers: 2})

	// 不同收件人，避开按收件人的限流（那个单独测）。
	for i := range 5 {
		to := fmt.Sprintf("user%d@b.com", i)
		if err := m.SendEmail(context.Background(), Message{To: []string{to}, Subject: "hi"}); err != nil {
			t.Fatalf("SendEmail 应立即成功：%v", err)
		}
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown：%v", err)
	}
	if inner.count() != 5 {
		t.Errorf("应投递 5 封，实际 %d", inner.count())
	}
}

// TestAsyncMailerRecipientLimit 同一收件人超过 burst 就丢弃（防轰炸/放大），返回值仍是 nil。
func TestAsyncMailerRecipientLimit(t *testing.T) {
	inner := &captureMailer{}
	// burst 3、every 很大 → 头 3 封放行，之后全丢。
	m := NewAsyncMailer(inner, discardLog(), AsyncOptions{
		Workers: 2, PerRecipientBurst: 3, PerRecipientEvery: time.Hour,
	})
	for range 10 {
		if err := m.SendEmail(context.Background(), Message{To: []string{"victim@target.com"}, Subject: "spam"}); err != nil {
			t.Fatalf("SendEmail 应返回 nil（限流不该冒错）：%v", err)
		}
	}
	// 换个收件人不受影响。
	_ = m.SendEmail(context.Background(), Message{To: []string{"other@x.com"}, Subject: "hi"})
	_ = m.Shutdown(context.Background())
	if inner.count() != 4 { // victim 3 + other 1
		t.Errorf("应只投递 4 封（同收件人限 3 + 另一人 1），实际 %d", inner.count())
	}
}

// panicOnceMailer 对第一封信 panic，之后的信正常收下 —— 模拟「一封坏信让内层 panic」。
type panicOnceMailer struct {
	mu       sync.Mutex
	panicked bool
	got      []Message
}

func (p *panicOnceMailer) SendEmail(_ context.Context, msg Message) error {
	p.mu.Lock()
	first := !p.panicked
	p.panicked = true
	p.mu.Unlock()
	if first {
		panic("boom：畸形输入让内层发信炸了")
	}
	p.mu.Lock()
	p.got = append(p.got, msg)
	p.mu.Unlock()
	return nil
}

// TestAsyncMailerWorkerSurvivesPanic 内层发信 panic 不能打挂 worker（更别说整个进程）：
// 单 worker 下第一封 panic，第二封仍要被投递 —— 证明 worker 兜住 panic 后继续干活。
// （若没 recover，goroutine 的未捕获 panic 会终结整个测试进程，这个测试根本跑不完。）
func TestAsyncMailerWorkerSurvivesPanic(t *testing.T) {
	inner := &panicOnceMailer{}
	m := NewAsyncMailer(inner, discardLog(), AsyncOptions{Workers: 1})

	_ = m.SendEmail(context.Background(), Message{To: []string{"first@x.com"}, Subject: "会 panic"})
	_ = m.SendEmail(context.Background(), Message{To: []string{"second@x.com"}, Subject: "应送达"})
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown：%v", err)
	}

	inner.mu.Lock()
	defer inner.mu.Unlock()
	if len(inner.got) != 1 || inner.got[0].To[0] != "second@x.com" {
		t.Fatalf("panic 后 worker 应存活并投递第二封，实际收到：%v", inner.got)
	}
}

// TestNormalizeEmail 别名归一：+tag 一律去掉，gmail 再去点号，其余原样（只小写去空白）。
func TestNormalizeEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Victim@X.com ", "victim@x.com"},
		{"victim+1@x.com", "victim@x.com"},
		{"victim+anything@x.com", "victim@x.com"},
		{"v.ictim@gmail.com", "victim@gmail.com"},
		{"vic.tim+7@googlemail.com", "victim@googlemail.com"},
		{"a.b.c@other.com", "a.b.c@other.com"}, // 非 gmail 不去点
		{"notanemail", "notanemail"},
	}
	for _, c := range cases {
		if got := normalizeEmail(c.in); got != c.want {
			t.Errorf("normalizeEmail(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestAsyncMailerAliasBypassBlocked 同一个真实收件箱用 +别名 / gmail 点号换 key，仍受同一个
// burst 约束 —— 否则按收件人限流形同虚设。
func TestAsyncMailerAliasBypassBlocked(t *testing.T) {
	inner := &captureMailer{}
	m := NewAsyncMailer(inner, discardLog(), AsyncOptions{
		Workers: 2, PerRecipientBurst: 3, PerRecipientEvery: time.Hour,
	})
	// 都投到 victim@gmail.com：+tag 和点号别名各来几封。
	for _, alias := range []string{
		"victim@gmail.com", "victim+1@gmail.com", "vic.tim@gmail.com",
		"victim+2@gmail.com", "v.i.c.t.i.m@gmail.com",
	} {
		_ = m.SendEmail(context.Background(), Message{To: []string{alias}, Subject: "spam"})
	}
	_ = m.Shutdown(context.Background())
	if inner.count() != 3 { // burst 3，别名不该放大成 5
		t.Errorf("别名应共享同一 burst（3），实际投递 %d 封 —— 限流被别名绕过了", inner.count())
	}
}

// TestAsyncMailerDoesNotBlock 关键安全属性：内层发信卡住时，SendEmail 仍立即返回（不耦合请求耗时）。
func TestAsyncMailerDoesNotBlock(t *testing.T) {
	block := make(chan struct{})
	inner := &captureMailer{block: block}
	// buffer=1、worker=1：第 1 封被 worker 取走后卡住，第 2 封占满 buffer，
	// 后续应立即走「丢弃」分支而不是阻塞。
	m := NewAsyncMailer(inner, discardLog(), AsyncOptions{Workers: 1, Buffer: 1})

	done := make(chan struct{})
	go func() {
		// 不同收件人：让它们过限流、真去挤队列，测的是「队列满也不阻塞」。
		for i := range 50 {
			_ = m.SendEmail(context.Background(), Message{To: []string{fmt.Sprintf("u%d@y.com", i)}, Subject: "s"})
		}
		close(done)
	}()
	select {
	case <-done:
		// 好：50 次调用没被卡住。
	case <-time.After(2 * time.Second):
		t.Fatal("SendEmail 阻塞了 —— 时序侧信道会因此回归")
	}
	close(block) // 放行内层，让 worker 收尾
	_ = m.Shutdown(context.Background())
}

// TestAsyncMailerSwallowsSendError 内层发信报错时，SendEmail 仍返回 nil（调用方走统一成功路径，防枚举）。
func TestAsyncMailerSwallowsSendError(t *testing.T) {
	inner := &captureMailer{failErr: errors.New("SES 挂了")}
	m := NewAsyncMailer(inner, discardLog(), AsyncOptions{})
	if err := m.SendEmail(context.Background(), Message{To: []string{"a@b.com"}, Subject: "x"}); err != nil {
		t.Errorf("入队即成功，内层错误不该冒到调用方：%v", err)
	}
	_ = m.Shutdown(context.Background())
}

func TestAsyncMailerShutdownTimeout(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	inner := &captureMailer{block: block}
	m := NewAsyncMailer(inner, discardLog(), AsyncOptions{Workers: 1, Buffer: 4})
	_ = m.SendEmail(context.Background(), Message{To: []string{"a@b.com"}, Subject: "x"})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); err == nil {
		t.Error("worker 卡在发信里，Shutdown 应在 ctx 到期时返回错误")
	}
}
