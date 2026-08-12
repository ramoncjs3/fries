package notify_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/ramoncjs3/fries/internal/notify"
)

// LogMailer 和 sesMailer 都要满足 Mailer 接口 —— 编译期钉住。
var (
	_ notify.Mailer = (*notify.LogMailer)(nil)
	_ notify.Mailer = notify.NewSESMailer(notify.SESConfig{})
)

// TestLogMailerAlwaysSucceeds 验 LogMailer 永远成功、不真发 —— 未配发信通道时的兜底。
func TestLogMailerAlwaysSucceeds(t *testing.T) {
	m := notify.NewLogMailer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := m.SendEmail(context.Background(), notify.Message{
		To:      []string{"user@example.com"},
		Subject: "测试",
		Body:    "正文",
	})
	if err != nil {
		t.Fatalf("LogMailer 应永远成功，得到：%v", err)
	}
}

// TestSESMailerRejectsNoRecipients 验没有收件人时不去打 SES，直接报错。
func TestSESMailerRejectsNoRecipients(t *testing.T) {
	m := notify.NewSESMailer(notify.SESConfig{Region: "us-east-1", From: "no-reply@example.com"})
	err := m.SendEmail(context.Background(), notify.Message{Subject: "x", Body: "y"})
	if err == nil {
		t.Fatal("没有收件人应报错，而不是去 SES 空发一趟")
	}
}
