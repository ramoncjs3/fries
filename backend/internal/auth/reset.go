package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/notify"
	"github.com/ramoncjs3/fries/internal/repo"
)

const (
	// resetTokenBytes 是重置 token 的熵。和会话 token 一样 32 字节。
	resetTokenBytes = 32
	// resetTokenTTL 是重置链接的有效期。短一点：这是高价值凭据。
	resetTokenTTL = 30 * time.Minute
)

// ResetRequestInput 是「忘记密码」申请入参。
type ResetRequestInput struct {
	TenantCode string // 公司代码（和登录一样，账号只在租户内唯一）
	Identifier string // 邮箱 / 用户名 / 手机号
}

// RequestPasswordReset 处理忘记密码申请：找到用户就发一封带一次性 token 的邮件。
//
// ⚠️ **防用户枚举**：handler 无论这里返回什么，都对前端回同一句话。这里返回的 error 只给
// 内部日志用，绝不能让「公司代码/用户是否存在」从响应上被观测到。所以：
//   - 租户不存在 / 停用、用户不存在 / 停用 / 没邮箱 —— 一律返回 nil（静默，什么都不做）。
//   - 只有真正的内部故障（查库炸了）才返回非 nil，让 handler 记日志，但对前端仍是同一句话。
//
// 防枚举：不管账号存不存在，响应体一致、且发信已**异步**（app 把 mailer 包在 AsyncMailer 里，
// SendEmail 入队即返回）—— 存在与否两条路径都不含 SES 往返，之前那个「存在则慢一个往返」的
// 时序侧信道已收口。剩下的耗时差只有几条 SELECT/INSERT（亚毫秒），淹在网络抖动里。
// 申请接口还有 IP 维度限流兜着（防脚本压垮，不是防时序）。
func (s *Service) RequestPasswordReset(ctx context.Context, in ResetRequestInput) error {
	tenant, err := s.store.Platform().GetTenantByCode(ctx, in.TenantCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 公司代码不存在：静默
		}
		return errs.Internal.Wrap(err)
	}
	if tenant.Status != tenantActive {
		return nil // 租户停用：静默
	}
	// 从这里起把审计记在这个租户名下 —— 客户能看到「有人在替我们的人申请重置」。
	audit.SetTenantID(ctx, tenant.ID)

	q := s.store.ForTenant(tenant.ID)
	user, err := q.GetUserByIdentifier(ctx, in.Identifier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 用户不存在：静默
		}
		return errs.Internal.Wrap(err)
	}
	if user.Status != statusActive || user.Email == nil || *user.Email == "" {
		return nil // 停用账号、或没登记邮箱，无从发信：静默
	}

	token := randomToken(resetTokenBytes)
	id, err := uuid.NewV7()
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	// 一人一条有效 token：先把此前没用的作废。
	if err := q.InvalidateUserResetTokens(ctx, user.ID); err != nil {
		return errs.Internal.Wrap(err)
	}
	if err := q.CreatePasswordResetToken(ctx, repo.CreatePasswordResetTokenArgs{
		ID:        id,
		UserID:    user.ID,
		TokenHash: hashToken(token),
		ExpiresAt: time.Now().UTC().Add(resetTokenTTL),
	}); err != nil {
		return errs.Internal.Wrap(err)
	}

	return s.mailer.SendEmail(ctx, notify.Message{
		To:      []string{*user.Email},
		Subject: "重置你的密码",
		Body: fmt.Sprintf(
			"你申请了重置密码。点下面的链接设置新密码（%d 分钟内有效，只能用一次）：\n\n%s\n\n"+
				"如果不是你本人操作，忽略这封邮件即可，你的密码不会有任何变化。",
			int(resetTokenTTL.Minutes()), s.resetLink(token)),
	})
}

// resetLink 拼「重置密码」页面的链接。BaseURL 为空（本地开发）时给相对路径 ——
// 这时多半是 LogMailer，token 已经进日志，够本地测。
func (s *Service) resetLink(token string) string {
	return strings.TrimRight(s.resetBaseURL, "/") + "/reset-password?token=" + token
}

// ResetPasswordInput 是「用 token 设新密码」入参。
type ResetPasswordInput struct {
	Token       string
	NewPassword string
}

// ResetPassword 用忘记密码邮件里的 token 设置新密码。
//
// token 一次性、防 TOCTOU：先原子认领（MarkPasswordResetTokenUsed 只标记还没用的、返回行数），
// 认领到才改密。改密成功后**吊销该用户全部会话** —— 走到这一步说明账号可能被别人接管过。
func (s *Service) ResetPassword(ctx context.Context, in ResetPasswordInput) error {
	if in.Token == "" {
		return errs.ValidationFailed.WithField("body.token", "缺少重置 token")
	}

	// 按 token 哈希定位（用户还没登录，租户未知，走 Unscoped）—— 查出来的行才告诉我们租户和用户。
	row, err := s.store.Unscoped().GetLivePasswordResetToken(ctx, hashToken(in.Token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.ValidationFailed.Detailf("重置链接无效或已过期，请重新申请")
		}
		return errs.Internal.Wrap(err)
	}
	audit.SetTenantID(ctx, row.TenantID)
	q := s.store.ForTenant(row.TenantID)

	// 先校验新密码强度（租户策略），不合格就别浪费 token。
	if err := CheckPasswordStrength(in.NewPassword, s.settings.Security(row.TenantID)); err != nil {
		return err
	}

	// 原子认领：并发里只有一个请求能把它从「未用」标成「已用」。
	claimed, err := q.MarkPasswordResetTokenUsed(ctx, row.ID)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	if claimed == 0 {
		return errs.ValidationFailed.Detailf("重置链接无效或已过期，请重新申请")
	}

	if err := q.SetUserPassword(ctx, repo.SetUserPasswordArgs{
		ID:           row.UserID,
		PasswordHash: HashPassword(in.NewPassword),
	}); err != nil {
		return errs.Internal.Wrap(err)
	}
	// 重置密码 = 之前的会话都不可信，全踢掉（和管理员重置密码一致）。
	if err := q.RevokeUserSessions(ctx, row.UserID); err != nil {
		return errs.Internal.Wrap(err)
	}
	return nil
}

// RandomToken 生成一个高熵随机 token（明文）。给注册验证等场景复用。
func RandomToken(n int) string { return randomToken(n) }

// HashToken 算 token 的存储哈希（库里只存这个，明文只发给用户一次）。
func HashToken(token string) []byte { return hashToken(token) }
