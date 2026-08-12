// Package auth 管认证：你是谁。授权（你能干什么）在 internal/authz。
//
// 会话存 PG + httpOnly cookie + argon2id + CSRF double submit（DECISIONS.md §1、§6）。
// **不用 localStorage JWT**：httpOnly 挡 XSS 偷 token，且能服务端即时踢人。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
)

// argon2id 参数。OWASP 建议的量级，本项目用户量小，取偏保守的一档。
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// dummyHash 用于「用户不存在」时照样跑一次校验，抹平时间差，
// 免得攻击者靠响应快慢枚举出哪些用户名存在。
var dummyHash = HashPassword("dummy-password-for-constant-time")

// HashPassword 把明文密码哈希成可存库的字符串。
//
// 格式带上参数，将来调大 cost 时老密码仍然验得过（下次改密自动升级）。
func HashPassword(plain string) string {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		// 读不到随机数是致命环境问题，继续跑只会产生弱密码
		panic("auth: 读随机数失败: " + err.Error())
	}
	sum := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum))
}

// VerifyPassword 校验明文密码和哈希是否匹配。
func VerifyPassword(plain, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// CheckPasswordStrength 按当前安全策略校验密码强度。策略来自 DB settings，
// 后台改完立即生效（DECISIONS.md §5）。
func CheckPasswordStrength(plain string, policy config.Security) error {
	if len([]rune(plain)) < policy.PasswordMinLength {
		return errs.ValidationFailed.
			WithField("body.new_password", fmt.Sprintf("密码至少 %d 位", policy.PasswordMinLength))
	}
	if !policy.PasswordRequireMix {
		return nil
	}

	var hasUpper, hasLower, hasDigit bool
	for _, r := range plain {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return errs.ValidationFailed.
			WithField("body.new_password", "密码必须同时包含大写字母、小写字母和数字")
	}
	return nil
}

// 初始密码的字母表：**挖掉了所有肉眼分不清的字符** —— 0/O、1/l/I。
//
// 这串密码的宿命是被人念出来、抄到微信里、再打进登录框（§5 选的就是「管理员当面转交」
// 这条零依赖的路），所以可辨认比字母表大小重要。原来用 base64url，`O` 和 `0` 长得一样，
// 交付一个新组织时我自己就抄错过两次。
//
// 每类各留 24/24/8 个，合计 56 —— 每个字符约 5.8 bit，14 位就有 81 bit 熵，够了。
const (
	passwordLower  = "abcdefghijkmnpqrstuvwxyz" // 去掉 l、o
	passwordUpper  = "ABCDEFGHJKLMNPQRSTUVWXYZ" // 去掉 I、O
	passwordDigits = "23456789"                 // 去掉 0、1
)

// RandomPassword 生成一个随机初始密码，n 是**字符数**。
//
// **不让人自己设初始密码** —— 管理员设的十有八九是 123456。
//
// 三类字符各保证至少一个：初始密码不走 CheckPasswordStrength（是系统发的，不是人设的），
// 但客户改密时会对着策略要求「大小写 + 数字」，发一串纯字母的初始密码等于给人添堵。
// 靠概率也行不通 —— 12 位里没有数字的概率约 1/500，那就是每开 500 个组织翻一次车。
//
// 调用方必须保证：这个值只出现一次（返回给管理员当场转交），不写日志、不落库明文。
func RandomPassword(n int) string {
	const all = passwordLower + passwordUpper + passwordDigits
	if n < 3 {
		panic("auth: 初始密码至少 3 位")
	}

	out := make([]byte, 0, n)
	// 先各占一个坑位，保证三类齐全
	out = append(out, passwordLower[randomIndex(len(passwordLower))])
	out = append(out, passwordUpper[randomIndex(len(passwordUpper))])
	out = append(out, passwordDigits[randomIndex(len(passwordDigits))])
	for len(out) < n {
		out = append(out, all[randomIndex(len(all))])
	}

	// 洗牌，否则前三位的类型是固定的，等于白送三个字符的信息
	for i := len(out) - 1; i > 0; i-- {
		j := randomIndex(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// randomIndex 返回 [0, n) 内的均匀随机数。用 rand.Int 而不是 `randomBytes()[0] % n`：
// 后者在 n 不整除 256 时有模偏置，会让某些字符更常出现。
func randomIndex(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// 读不到随机数是致命环境问题，继续跑只会产生弱凭据
		panic("auth: 读随机数失败: " + err.Error())
	}
	return int(v.Int64())
}

// randomToken 生成 n 字节的随机串，base64url 编码。会话 token 和 API Key 的密文段用它。
func randomToken(n int) string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(n))
}

// randomHex 生成 n 字节的随机串，十六进制编码。
//
// API Key 的 prefix 段要用它：base64url 里有 `_`，而 API Key 正是用 `_` 分段的，
// 混进去会把 key 切错（这个坑踩过一次）。
func randomHex(n int) string {
	return hex.EncodeToString(randomBytes(n))
}

func randomBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// 读不到随机数是致命环境问题，继续跑只会产生弱凭据
		panic("auth: 读随机数失败: " + err.Error())
	}
	return buf
}
