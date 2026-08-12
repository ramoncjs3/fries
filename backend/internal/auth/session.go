package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// sessionTokenBytes 是会话 token 的长度。32 字节随机足够，且 cookie 不会太长。
const sessionTokenBytes = 32

// CSRFCookieSuffix 拼在会话 cookie 名后面，得到 CSRF cookie 名。
const CSRFCookieSuffix = "_csrf"

// HeaderCSRFToken 是前端回传 CSRF token 的请求头。
const HeaderCSRFToken = "X-CSRF-Token"

// SessionConfig 是会话相关配置，来自 config/config.yaml。
type SessionConfig struct {
	// Secret 用来签 CSRF token，至少 32 字节。
	Secret string
	// TTL 是会话有效期。
	TTL time.Duration
	// CookieName 是会话 cookie 名。
	CookieName string
	// Secure 为 true 时 cookie 只走 HTTPS。生产必须 true。
	Secure bool
}

// CSRFCookieName 返回 CSRF cookie 的名字。
func (c SessionConfig) CSRFCookieName() string { return c.CookieName + CSRFCookieSuffix }

// hashToken 把会话 token 变成存库的形式。
//
// **库里只存哈希**：备份被人拿走、或者 DBA 顺手看了一眼，都不能直接拿去冒充登录。
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// csrfToken 由会话 ID 和密钥算出来，不用额外存储。
//
// double-submit 的原理：这个值放在**非 httpOnly** 的 cookie 里给前端读，
// 前端把它塞进请求头；攻击者的站点能让浏览器带上 cookie，但读不到跨站 cookie 的值，
// 也就凑不出这个请求头（DECISIONS.md §6）。
func csrfToken(secret string, sessionID uuid.UUID) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(sessionID.String()))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCSRF 校验请求头里的 CSRF token 是不是这个会话的。
func VerifyCSRF(secret string, sessionID uuid.UUID, presented string) bool {
	want := csrfToken(secret, sessionID)
	return hmac.Equal([]byte(want), []byte(presented))
}

// SessionCookie 造会话 cookie。
//
// HttpOnly 挡 XSS 偷 token；SameSite=Lax 挡大部分 CSRF，再加 double-submit 兜底。
func (c SessionConfig) SessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     c.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// CSRFCookie 造 CSRF cookie。**故意不是 HttpOnly** —— 前端要读它。
func (c SessionConfig) CSRFCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     c.CSRFCookieName(),
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: false,
		Secure:   c.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearCookies 造一对让浏览器删除 cookie 的 Set-Cookie。
func (c SessionConfig) ClearCookies() []*http.Cookie {
	expired := time.Unix(0, 0)
	return []*http.Cookie{
		{Name: c.CookieName, Value: "", Path: "/", Expires: expired, MaxAge: -1,
			HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode},
		{Name: c.CSRFCookieName(), Value: "", Path: "/", Expires: expired, MaxAge: -1,
			HttpOnly: false, Secure: c.Secure, SameSite: http.SameSiteLaxMode},
	}
}
