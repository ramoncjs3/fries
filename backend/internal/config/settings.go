package config

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/repo"
)

// SettingsChannel 是**租户级**配置变更的 PG 通知频道，由 settings 表的触发器发出。
//
// ⚠️ 通知的负载是那一行的 tenant_id（MULTI-TENANCY.md §9.4）。
// 不带租户的话，一个租户改配置会让所有实例把**所有租户**的缓存全刷一遍。
const SettingsChannel = "settings_changed"

// PlatformSettingsChannel 是平台级配置变更的通知频道。负载是空的 —— 平台配置只有一份。
const PlatformSettingsChannel = "platform_settings_changed"

// 配置项的 key。**只在这里写字符串**，别处一律用常量。
//
// 分两类，别放错（MULTI-TENANCY.md §7.2）：
//   - 租户级：每家公司自己定，存 settings 表，主键 (tenant_id, key)
//   - 平台级：整个平台一份，存 platform_settings 表，只有平台管理员能改
const (
	// —— 租户级 ——
	KeyPasswordMinLength  = "security.password_min_length"
	KeyPasswordRequireMix = "security.password_require_mix"
	KeyPasswordMaxAgeDays = "security.password_max_age_days"
	KeyLoginMaxFailures   = "security.login_max_failures"
	KeyLoginLockMinutes   = "security.login_lock_minutes"

	// —— 平台级 ——
	// 审计按月分区、过期整分区 DROP，分区是跨租户的，没法按租户设保留期。
	KeyAuditRetentionDays = "audit.retention_days"
	// 产品名。客户自己的名字在 tenants.name 里，不是这个。
	KeySystemName = "ui.system_name"
	// 是否允许自助注册（陌生人自己注册开一个组织）。**默认关**：开着意味着
	// 任何人都能建租户，必须配合邮箱验证 + 限流，滥用面不小（MULTI-TENANCY.md §9.2）。
	KeyAllowSelfRegistration = "registration.self_service"
)

// Security 是安全策略，**每个租户一份**，后台改完立即生效（DECISIONS.md §5、§6）。
type Security struct {
	PasswordMinLength  int
	PasswordRequireMix bool
	PasswordMaxAgeDays int
	LoginMaxFailures   int
	LoginLockMinutes   int
}

// defaultSecurity 是兜底默认值：库里没有这一行，或者值坏掉了，就用它。
// **每个 key 都必须有兜底** —— 配置读不到不该让系统起不来或者变得不安全。
//
// 多租户下它还多了一个用处：**新开的租户一条 settings 行都没有**，
// 走的就是这里。所以开租户时不需要给它铺一份默认配置。
//
// ⚠️ 值**从注册表派生**，不在这里重写一遍。原来两处各写一份，
// 而它们必须永远相等 —— 那是这个项目反复踩过的「两份清单会漂」
// （MEMORY.md 记过好几条）。现在改一处就够，改错了 checkRegistry 会拦。
func defaultSecurity() Security {
	return Security{
		PasswordMinLength:  defaultOf[int](KeyPasswordMinLength),
		PasswordRequireMix: defaultOf[bool](KeyPasswordRequireMix),
		PasswordMaxAgeDays: defaultOf[int](KeyPasswordMaxAgeDays),
		LoginMaxFailures:   defaultOf[int](KeyLoginMaxFailures),
		LoginLockMinutes:   defaultOf[int](KeyLoginLockMinutes),
	}
}

// Settings 是 DB 配置层的内存缓存。
//
// 读全部走缓存，写走 DB → 触发器 NOTIFY → 所有实例刷新（DECISIONS.md §5）。
//
// ⚠️ 多租户下它**不再是一份**（MULTI-TENANCY.md §7.2）：租户级配置按租户各一份，
// 取值时必须说明是哪个租户。平台级配置仍然只有一份。
type Settings struct {
	store *repo.Store

	mu sync.RWMutex
	// tenants 是租户级配置：租户 → key → 值。**缓存里没有那个租户就是走默认值**，
	// 不是错误 —— 新租户本来就一条配置行都没有。
	tenants map[uuid.UUID]map[string]json.RawMessage
	// platform 是平台级配置，整个进程一份。
	platform map[string]json.RawMessage
}

// NewSettings 造一个配置缓存并立即加载一次。
func NewSettings(ctx context.Context, store *repo.Store) (*Settings, error) {
	s := &Settings{
		store:    store,
		tenants:  map[uuid.UUID]map[string]json.RawMessage{},
		platform: map[string]json.RawMessage{},
	}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// NewDefaultSettings 造一个不连库的配置缓存，全部走默认值。
// 只给 `server --selfcheck` 用 —— 自检是纯内存的，不碰数据库（DECISIONS.md §3.7）。
func NewDefaultSettings() *Settings {
	return &Settings{
		tenants:  map[uuid.UUID]map[string]json.RawMessage{},
		platform: map[string]json.RawMessage{},
	}
}

// Reload 重新加载**全部**配置：平台级一份 + 每个租户各一份。
//
// 遍历的是**全部**租户而不是启用中的 —— 理由见 db/queries/tenant.sql 的 ListTenants：
// 按状态过滤会让「停用再启用」的租户拿不回自己的配置和权限。
//
// 启动时跑一次；LISTEN 断线重连之后也跑一次（断开期间的通知已经丢了）。
// 日常的单个租户变更走 ReloadTenant，不用整体重刷。
func (s *Settings) Reload(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	if err := s.ReloadPlatform(ctx); err != nil {
		return err
	}

	tenants, err := s.store.Platform().ListTenants(ctx)
	if err != nil {
		return fmt.Errorf("加载租户列表: %w", err)
	}
	loaded := make(map[uuid.UUID]map[string]json.RawMessage, len(tenants))
	for _, t := range tenants {
		values, err := s.loadTenant(ctx, t.ID)
		if err != nil {
			return err
		}
		loaded[t.ID] = values
	}

	s.mu.Lock()
	s.tenants = loaded
	s.mu.Unlock()
	return nil
}

// ReloadTenant 只刷一个租户的配置。收到 settings_changed 通知时用，负载就是租户 id。
func (s *Settings) ReloadTenant(ctx context.Context, tenantID uuid.UUID) error {
	if s.store == nil {
		return nil
	}
	values, err := s.loadTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.tenants[tenantID] = values
	s.mu.Unlock()
	return nil
}

// ReloadPlatform 只刷平台级配置。
func (s *Settings) ReloadPlatform(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	rows, err := s.store.Platform().ListPlatformSettings(ctx)
	if err != nil {
		return fmt.Errorf("加载平台配置: %w", err)
	}
	values := make(map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	s.mu.Lock()
	s.platform = values
	s.mu.Unlock()
	return nil
}

func (s *Settings) loadTenant(ctx context.Context, tenantID uuid.UUID) (map[string]json.RawMessage, error) {
	rows, err := s.store.ForTenant(tenantID).ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("加载租户 %s 的配置: %w", tenantID, err)
	}
	values := make(map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values, nil
}

// Set 改一个**租户级**配置项。写库之后触发器会 NOTIFY，所有实例（含自己）刷新缓存。
//
// 两道校验都拦在这里，因为**这是租户级配置唯一的写入口**：
//
//	① key 必须在注册表里，而且是租户级的  ← registry.go，防「写进任意键」
//	② 值必须落在平台划的区间内（§10.5）    ← bounds.go
//
// 拦在这里而不是 handler 里是有意的：将来多接一个入口（导入、脚本、AI 工具）
// 也自动受保护，那些入口不用记得自己校验一遍。
func (s *Settings) Set(ctx context.Context, tenantID uuid.UUID, key string, value any, updatedBy *uuid.UUID) error {
	if !writable(key, perm.RealmTenant) {
		return ErrUnknownKey
	}
	if err := checkKind(key, value); err != nil {
		return err
	}
	if err := s.checkTenantBound(key, value); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("序列化配置 %s: %w", key, err)
	}
	if err := s.store.ForTenant(tenantID).UpsertSetting(ctx, repo.UpsertSettingArgs{
		Key:       key,
		Value:     raw,
		UpdatedBy: updatedBy,
	}); err != nil {
		return fmt.Errorf("写配置 %s: %w", key, err)
	}
	return s.ReloadTenant(ctx, tenantID)
}

// SetPlatform 改一个平台级配置项。只有平台管理员能走到这里。
//
// 白名单同样拦在这里，而且平台端**更需要它**：`limits.*` 的键名敲错一个字母，
// 那条界就静默消失（没有对应行 = 不受限，见 bounds.go），
// 页面上看着还像保存成功了。所以平台端能写的只有「注册表里的平台级项」
// 加「BoundKey 拼出来的那 6 个」，别的一律拒绝。
func (s *Settings) SetPlatform(ctx context.Context, key string, value any, updatedBy *uuid.UUID) error {
	if !writable(key, perm.RealmPlatform) {
		return ErrUnknownKey
	}
	// 上下界允许清空（nil = 不限制），别的项必须类型对得上。
	if value != nil {
		if err := checkKind(key, value); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("序列化平台配置 %s: %w", key, err)
	}
	if err := s.store.Platform().UpsertPlatformSetting(ctx, repo.UpsertPlatformSettingParams{
		Key:       key,
		Value:     raw,
		UpdatedBy: updatedBy,
	}); err != nil {
		return fmt.Errorf("写平台配置 %s: %w", key, err)
	}
	return s.ReloadPlatform(ctx)
}

// Security 返回某个租户的安全策略。
//
// 租户在缓存里没有（新开的、或者一条配置都没改过）就整份走默认值 —— 这是正常路径。
func (s *Settings) Security(tenantID uuid.UUID) Security {
	def := defaultSecurity()
	return Security{
		PasswordMinLength:  s.Int(tenantID, KeyPasswordMinLength, def.PasswordMinLength),
		PasswordRequireMix: s.Bool(tenantID, KeyPasswordRequireMix, def.PasswordRequireMix),
		PasswordMaxAgeDays: s.Int(tenantID, KeyPasswordMaxAgeDays, def.PasswordMaxAgeDays),
		LoginMaxFailures:   s.Int(tenantID, KeyLoginMaxFailures, def.LoginMaxFailures),
		LoginLockMinutes:   s.Int(tenantID, KeyLoginLockMinutes, def.LoginLockMinutes),
	}
}

// PlatformValue 读一个平台级配置的原值，读不到就返回默认值。
//
// 给配置页面用：页面要按注册表逐项显示，不关心它是 int 还是 string，
// 所以这里不做类型断言，原样把 JSON 解出来的值给出去。
// 业务代码要取值仍然走 PlatformInt / PlatformString / PlatformBool —— 那些有类型。
func (s *Settings) PlatformValue(key string, def any) any {
	s.mu.RLock()
	raw, ok := s.platform[key]
	s.mu.RUnlock()
	if !ok {
		return def
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return def
	}
	return v
}

// PlatformKeys 返回缓存里全部平台级配置的 key。
//
// 给启动时的孤儿检查用（见 cmd/server 的 warnIfOrphanBounds）：
// 库里可能躺着一条谁也不认识的 `limits.xxx.min` —— 那种行**看着像在生效，其实没有**。
func (s *Settings) PlatformKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Sorted(maps.Keys(s.platform))
}

// TenantValues 返回某个租户的全部租户级配置，给配置管理页面用。
func (s *Settings) TenantValues(tenantID uuid.UUID) map[string]json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.tenants[tenantID])
}

// AllowSelfRegistration 返回是否允许陌生人自助注册开组织（平台级，默认关）。
func (s *Settings) AllowSelfRegistration() bool {
	return s.PlatformBool(KeyAllowSelfRegistration, defaultOf[bool](KeyAllowSelfRegistration))
}

// AuditRetention 返回审计日志保留时长（平台级）。
func (s *Settings) AuditRetention() time.Duration {
	return time.Duration(s.PlatformInt(KeyAuditRetentionDays, defaultOf[int](KeyAuditRetentionDays))) * 24 * time.Hour
}

// SystemName 返回产品名（平台级）。
func (s *Settings) SystemName() string {
	return s.PlatformString(KeySystemName, defaultOf[string](KeySystemName))
}

// Int 读某个租户的整数配置，读不到或类型不对就用默认值。
func (s *Settings) Int(tenantID uuid.UUID, key string, def int) int {
	var v int
	if s.decodeTenant(tenantID, key, &v) {
		return v
	}
	return def
}

// Bool 读某个租户的布尔配置。
func (s *Settings) Bool(tenantID uuid.UUID, key string, def bool) bool {
	var v bool
	if s.decodeTenant(tenantID, key, &v) {
		return v
	}
	return def
}

// String 读某个租户的字符串配置。
func (s *Settings) String(tenantID uuid.UUID, key, def string) string {
	var v string
	if s.decodeTenant(tenantID, key, &v) {
		return v
	}
	return def
}

// PlatformInt 读一个平台级整数配置。
func (s *Settings) PlatformInt(key string, def int) int {
	var v int
	if s.decodePlatform(key, &v) {
		return v
	}
	return def
}

// PlatformString 读一个平台级字符串配置。
func (s *Settings) PlatformString(key, def string) string {
	var v string
	if s.decodePlatform(key, &v) {
		return v
	}
	return def
}

// PlatformBool 读一个平台级布尔配置。
func (s *Settings) PlatformBool(key string, def bool) bool {
	var v bool
	if s.decodePlatform(key, &v) {
		return v
	}
	return def
}

func (s *Settings) decodeTenant(tenantID uuid.UUID, key string, into any) bool {
	s.mu.RLock()
	raw, ok := s.tenants[tenantID][key]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	return json.Unmarshal(raw, into) == nil
}

func (s *Settings) decodePlatform(key string, into any) bool {
	s.mu.RLock()
	raw, ok := s.platform[key]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	return json.Unmarshal(raw, into) == nil
}
