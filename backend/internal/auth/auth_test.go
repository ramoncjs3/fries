package auth

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/config"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	const plain = "Correct-Horse-Battery-2026"

	hash := HashPassword(plain)
	if strings.Contains(hash, plain) {
		t.Fatal("哈希里出现了明文")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("应该是 argon2id 格式，得到 %q", hash)
	}
	if !VerifyPassword(plain, hash) {
		t.Error("同一个密码应该验得过")
	}
	if VerifyPassword(plain+"x", hash) {
		t.Error("改一个字符就不该验得过")
	}
	if HashPassword(plain) == hash {
		t.Error("每次哈希都该带不同的盐")
	}
}

func TestVerifyPasswordRejectsGarbage(t *testing.T) {
	for _, encoded := range []string{
		"", "not-a-hash", "$argon2id$broken",
		"$argon2id$v=19$m=65536,t=3,p=2$bad-base64$also-bad",
	} {
		if VerifyPassword("whatever", encoded) {
			t.Errorf("坏掉的哈希 %q 不该验过", encoded)
		}
	}
}

func TestPasswordStrengthPolicy(t *testing.T) {
	policy := config.Security{PasswordMinLength: 10, PasswordRequireMix: true}

	cases := map[string]bool{
		"Passw0rd2026":    true,
		"short1A":         false, // 太短
		"alllowercase123": false, // 没有大写
		"ALLUPPERCASE123": false, // 没有小写
		"NoDigitsHereOk":  false, // 没有数字
	}
	for password, wantOK := range cases {
		err := CheckPasswordStrength(password, policy)
		if wantOK && err != nil {
			t.Errorf("%q 应该通过，得到 %v", password, err)
		}
		if !wantOK && err == nil {
			t.Errorf("%q 应该被拒", password)
		}
	}

	// 策略放松之后同一个密码就能过 —— 策略是从 DB settings 读的，改完立即生效
	loose := config.Security{PasswordMinLength: 4, PasswordRequireMix: false}
	if err := CheckPasswordStrength("abcd", loose); err != nil {
		t.Errorf("放松策略后应该通过，得到 %v", err)
	}
}

// 初始密码是**人工转抄**的：平台管理员开完组织，把这串东西发给客户。
// 所以两条硬要求：肉眼分得清（没有 0/O、1/l/I），以及一定过得了「大小写 + 数字」策略。
func TestRandomPasswordIsTranscribable(t *testing.T) {
	policy := config.Security{PasswordMinLength: 12, PasswordRequireMix: true}

	// 跑够多次才有意义 —— 「碰巧没有数字」这种事本来就是低概率翻车
	for range 500 {
		password := RandomPassword(12)

		if len(password) != 12 {
			t.Fatalf("长度应该是字符数 12，得到 %d：%q", len(password), password)
		}
		if i := strings.IndexAny(password, "0O1lI"); i >= 0 {
			t.Fatalf("出现了肉眼分不清的字符 %q：%q", password[i], password)
		}
		if err := CheckPasswordStrength(password, policy); err != nil {
			t.Fatalf("%q 过不了改密策略：%v", password, err)
		}
	}
}

func TestRandomPasswordIsNotPredictable(t *testing.T) {
	// 洗牌漏了的话，前三位的类型是固定的（小写、大写、数字），等于白送信息。
	//
	// 判据是「首位出现过数字」而不是「首位够多样」：不洗牌时首位恒为小写字母，
	// 那也有 24 种，「够多样」照样成立 —— 这个断言写松了就抓不住漏洗牌。
	// 洗牌正常时首位是数字的概率约 8/56，200 次一次都不中的概率是 1e-13 量级。
	var digitFirst, upperFirst int
	for range 200 {
		switch first := RandomPassword(14)[0]; {
		case first >= '0' && first <= '9':
			digitFirst++
		case first >= 'A' && first <= 'Z':
			upperFirst++
		}
	}
	if digitFirst == 0 || upperFirst == 0 {
		t.Errorf("首位从没出现过数字(%d)或大写(%d)，洗牌没生效？", digitFirst, upperFirst)
	}

	seen := make(map[string]bool)
	for range 100 {
		p := RandomPassword(14)
		if seen[p] {
			t.Fatalf("生成了重复的密码：%q", p)
		}
		seen[p] = true
	}
}

func TestAPIKeyRoundTrip(t *testing.T) {
	fullKey, prefix, hash := NewAPIKey()

	gotPrefix, gotSecret, ok := splitAPIKey(fullKey)
	if !ok {
		t.Fatalf("自己生成的 key 应该拆得开：%q", fullKey)
	}
	if gotPrefix != prefix {
		t.Errorf("prefix 对不上：%q vs %q", gotPrefix, prefix)
	}
	if !VerifyPassword(gotSecret, hash) {
		t.Error("密文段应该验得过存下来的哈希")
	}
	if strings.Contains(hash, gotSecret) {
		t.Error("库里存的哈希不该包含密钥明文")
	}
}

func TestSplitAPIKeyRejectsGarbage(t *testing.T) {
	for _, key := range []string{"", "fsa", "fsa_only-two", "other_prefix_secret", "fsa__secret", "fsa_prefix_"} {
		if _, _, ok := splitAPIKey(key); ok {
			t.Errorf("%q 不该被当成合法 API Key", key)
		}
	}
}

func TestCSRFTokenBoundToSession(t *testing.T) {
	const secret = "test-session-secret-must-be-32-bytes-long"
	session := uuid.New()
	other := uuid.New()

	token := csrfToken(secret, session)
	if !VerifyCSRF(secret, session, token) {
		t.Error("自己签的 token 应该验得过")
	}
	if VerifyCSRF(secret, other, token) {
		t.Error("换个会话就不该验得过 —— token 是绑定会话的")
	}
	if VerifyCSRF("another-secret-that-is-long-enough!!", session, token) {
		t.Error("换个密钥就不该验得过")
	}
	if VerifyCSRF(secret, session, "") {
		t.Error("空 token 必须判否")
	}
}

func TestSessionCookiesAreSafe(t *testing.T) {
	cfg := SessionConfig{CookieName: "fries_session", Secure: true}
	session := cfg.SessionCookie("token-value", nowPlusHour())

	if !session.HttpOnly {
		t.Error("会话 cookie 必须 HttpOnly —— 否则 XSS 直接偷走")
	}
	if !session.Secure {
		t.Error("配置了 Secure 就必须是 Secure cookie")
	}

	// CSRF cookie 反过来：**故意**不是 HttpOnly，前端要读它
	csrf := cfg.CSRFCookie("csrf-value", nowPlusHour())
	if csrf.HttpOnly {
		t.Error("CSRF cookie 必须能被前端读到，不能是 HttpOnly")
	}
	if csrf.Name == session.Name {
		t.Error("两个 cookie 不能同名")
	}

	for _, c := range cfg.ClearCookies() {
		if c.MaxAge >= 0 {
			t.Errorf("清除 cookie 的 MaxAge 应该是负数，得到 %d", c.MaxAge)
		}
	}
}

func nowPlusHour() time.Time { return time.Now().Add(time.Hour) }

// TestLoginGuardLimitsPerTenantCode 是 §3.2 ⑦ 的守门测试。
//
// 限流中间件跑在认证之前，那时还不知道租户，所以「按租户分桶」对登录接口做不到。
// 能做的是 **IP + 公司代码**两维：
//
//	IP       挡「一个人从一台机器上一直试」，也是公司代码枚举的主要防线
//	公司代码 挡「换一堆 IP 一起打同一家公司」—— IP 维度对分布式来源无能为力
func TestLoginGuardLimitsPerTenantCode(t *testing.T) {
	g := newLoginGuard()

	// 每次换一个 IP，模拟分布式来源：IP 那一维永远触发不了
	for i := range codeMaxFailures {
		ip := netip.AddrFrom4([4]byte{10, 0, byte(i / 256), byte(i % 256)})
		if g.blocked(&ip, "acme") {
			t.Fatalf("第 %d 次就被挡了，per-code 阈值是 %d", i, codeMaxFailures)
		}
		g.fail(&ip, "acme")
	}

	fresh := netip.AddrFrom4([4]byte{10, 9, 9, 9})
	if !g.blocked(&fresh, "acme") {
		t.Fatal("同一家公司被打了这么多次，换个新 IP 也该挡住 —— 公司代码那一维没生效")
	}
	// ⚠️ 别把别人家一起挡了：这一维是按公司分桶的
	if g.blocked(&fresh, "globex") {
		t.Fatal("挡住 acme 的同时把 globex 也挡了 —— 那是把限流变成了跨租户的可用性武器")
	}
	// 大小写换着写不能绕过（tenants.code 存的一律是小写）
	if !g.blocked(&fresh, "ACME") {
		t.Fatal("公司代码大小写换一下就绕过了计数")
	}
}

// TestLoginSuccessDoesNotResetTenantCounter 说明一个刻意的取舍。
//
// 登录成功会清掉这个 IP 的失败记录（正常人打错几次再打对，不该留着记账），
// **但不清公司代码那一笔** —— 攻击者手上只要有一个能登进去的账号，
// 就能靠反复成功登录把整家公司的计数一直归零，那道防线就废了。
func TestLoginSuccessDoesNotResetTenantCounter(t *testing.T) {
	g := newLoginGuard()
	ip := netip.AddrFrom4([4]byte{10, 0, 0, 1})

	for range codeMaxFailures {
		g.fail(&ip, "acme")
	}
	g.succeed(&ip)

	other := netip.AddrFrom4([4]byte{10, 0, 0, 2})
	if !g.blocked(&other, "acme") {
		t.Fatal("登录成功把公司维度的计数也清了 —— 有一个可用账号就能一直重置防线")
	}
}
