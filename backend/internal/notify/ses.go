package notify

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

// SESConfig 是 SES mailer 需要的连接信息，来自 config.yaml（凭据是敏感项，不进库）。
type SESConfig struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	From            string
}

// sesMailer 用 AWS SES 发信，直连 aws-sdk-go-v2/ses。
//
// ⚠️ **正文按纯文本发（Body.Text）**，不是 HTML。本包的 Message.Body 全程当纯文本（含 `\n`
// 换行），发信里也拼了用户可控字段（如注册流程的 company_name）。若按 HTML 发，这些字段会被
// SES 当标签渲染 —— 攻击者能借平台的可信发件域 + DKIM 签名投递任意 HTML 钓鱼正文，且 `\n`
// 在 HTML 里还不换行、正文挤成一行。所以这里显式走 Text。（早先用的 nikoksr/notify amazonses
// 封装只填 Body.Html、Text 被注释掉，正是上面那个坑，故改直连 SDK。）
//
// client 在构造时建一次、并发安全复用。凭据用 config.yaml 里的静态 key，**不走
// LoadDefaultConfig** —— 那会顺带读环境变量/`~/.aws/credentials`/实例角色，可能静默用上非预期
// 的凭据；我们只认显式配的这一份。
type sesMailer struct {
	client *ses.Client
	from   string
}

// NewSESMailer 造一个用 SES 发信的 Mailer。
func NewSESMailer(cfg SESConfig) *sesMailer {
	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}
	return &sesMailer{client: ses.NewFromConfig(awsCfg), from: cfg.From}
}

// SendEmail 发一封信给 msg.To 里的所有收件人。
func (m *sesMailer) SendEmail(ctx context.Context, msg Message) error {
	if len(msg.To) == 0 {
		return fmt.Errorf("发信没有收件人")
	}
	utf8 := aws.String("UTF-8")
	_, err := m.client.SendEmail(ctx, &ses.SendEmailInput{
		Source:      aws.String(m.from),
		Destination: &types.Destination{ToAddresses: msg.To},
		Message: &types.Message{
			Subject: &types.Content{Data: aws.String(msg.Subject), Charset: utf8},
			// 纯文本正文 —— 见类型注释：绝不用 Body.Html，否则用户可控字段成 HTML 注入面。
			Body: &types.Body{Text: &types.Content{Data: aws.String(msg.Body), Charset: utf8}},
		},
	})
	if err != nil {
		return fmt.Errorf("SES 发信: %w", err)
	}
	return nil
}
