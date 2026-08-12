// Package notify 是**平台级事务邮件**（DECISIONS.md §8.4）。
//
// ⚠️ 这里的 Mailer 是**平台自己的发件通道**，专给「没有租户上下文」的认证流用：
// 忘记密码（按 token_hash 查，还没确定租户）、自助注册的邮箱验证（租户还没建）。
// 这类信天然只能由平台发（发件域、凭据都是平台的），所以凭据放 config.yaml（部署机密，
// 不进库、不上页面）是对的，别搬进租户配置。
//
// 底层包在 Mailer 接口后（先接 AWS SES），换 SMTP / 云厂商都不动调用方。
//
// ❗ **别把这个当「租户多渠道通知」的扩展点**。租户自选 SES / Slack / 飞书 / SMTP 发**他们自己**
// 的通知，是另一个还没做的子系统：每租户一份配置、存库（密钥加密）、页面可配、按租户解析 provider。
// 那个需要的是「按租户解析 + provider 注册表」，不是替换这里的单个全局 Mailer 实现。
// DECISIONS.md §57 里「nikoksr/notify 多渠道」说的是那个未来子系统，不是本包现在的职责。
package notify

import (
	"context"
	"log/slog"
	"strings"
)

// Message 是一封要发的邮件。
//
// 先只做纯文本：验证码、重置链接这类不需要富文本。HTML 正文以后有需要再加字段。
type Message struct {
	To      []string // 收件人，至少一个
	Subject string
	Body    string // 纯文本正文
}

// Mailer 发邮件。**业务代码只依赖这个接口**，不碰具体实现（SES / SMTP / 日志）。
type Mailer interface {
	SendEmail(ctx context.Context, msg Message) error
}

// LogMailer 不真发信，只把邮件记进日志。
//
// 本地开发、测试、以及**生产没配发信通道**时都用它 —— 让「忘记密码」这类流程能跑通、
// 在日志里看得到本该发出去的内容，而不是一配就要求接上真 SES。
type LogMailer struct {
	log *slog.Logger
}

// NewLogMailer 造一个只记日志的 Mailer。
func NewLogMailer(log *slog.Logger) *LogMailer {
	return &LogMailer{log: log}
}

// SendEmail 把邮件记进日志，永远成功。
func (m *LogMailer) SendEmail(ctx context.Context, msg Message) error {
	m.log.InfoContext(ctx, "邮件（未真发，LogMailer）",
		slog.String("to", strings.Join(msg.To, ", ")),
		slog.String("subject", msg.Subject),
		slog.Int("body_len", len(msg.Body)))
	return nil
}
