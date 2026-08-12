package audit

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/repo"
)

// Record 是一条要落库的审计。
type Record struct {
	// TenantID 是这条审计属于哪个租户。**可以是 nil**（MULTI-TENANCY.md §7.1）：
	// 未认证请求（登录失败、健康检查、直接打接口）本来就没有租户，nil = 平台级事件。
	//
	// ⚠️ 有一个细节别做错：**公司代码有效、密码错误**的登录失败要记在**那个租户名下**，
	// 客户才能看到「有人在爆破我们的账号」。只有公司代码本身无效时才记 nil。
	TenantID   *uuid.UUID
	RequestID  string
	ActorType  string
	ActorID    *uuid.UUID
	ActorName  string
	Resource   string
	Action     string
	ResourceID *uuid.UUID
	Method     string
	Path       string
	IP         *netip.Addr
	UserAgent  string
	HTTPStatus int
	Duration   time.Duration
	Detail     map[string]any
}

// 主体类型。和 audit_logs.actor_type 的 CHECK 约束一一对应。
const (
	ActorUser      = "user"
	ActorService   = "service"
	ActorAnonymous = "anonymous"
	ActorSystem    = "system"
	// ActorPlatform 是平台管理员。**不属于任何租户** —— 他的审计记在平台级那条链上
	// （tenant_id 为 NULL），客户看不到，也不该看到（MULTI-TENANCY.md §9.2）。
	ActorPlatform = "platform"
)

// Writer 把审计写进库。
//
// 同步写：本项目写入量很低，异步换来的那点延迟不值得冒丢日志的风险。
// 哈希链和分区都在 DB 侧做，应用只管插。
// ⚠️ 它拿的是**不带租户**的句柄，因为未认证请求也要写审计，那时租户就是 NULL。
// 租户由每条 Record 自己带（见 Record.TenantID），不靠 ForTenant 注入。
type Writer struct {
	q *repo.UnscopedQueries
}

// NewWriter 造一个审计写入器。
func NewWriter(store *repo.Store) *Writer {
	return &Writer{q: store.Unscoped()}
}

// Write 写一条审计。
func (w *Writer) Write(ctx context.Context, rec Record) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("生成审计 ID: %w", err)
	}

	detail, err := json.Marshal(rec.Detail)
	if err != nil {
		// 摘要序列化失败不能拖累整条审计，退化成空对象
		detail = []byte(`{}`)
	}

	return w.q.InsertAuditLog(ctx, repo.InsertAuditLogParams{
		ID:         id,
		TenantID:   rec.TenantID,
		OccurredAt: time.Now().UTC(),
		RequestID:  rec.RequestID,
		ActorType:  rec.ActorType,
		ActorID:    rec.ActorID,
		ActorName:  rec.ActorName,
		Resource:   rec.Resource,
		Action:     rec.Action,
		ResourceID: rec.ResourceID,
		Method:     rec.Method,
		Path:       rec.Path,
		IP:         rec.IP,
		UserAgent:  rec.UserAgent,
		HTTPStatus: int32(rec.HTTPStatus),
		DurationMs: int32(rec.Duration.Milliseconds()),
		Detail:     detail,
	})
}

// Discard 是不写任何东西的审计写入器。只给 `server --selfcheck` 用。
type Discard struct{}

// Write 实现审计写入，什么都不做。
func (Discard) Write(context.Context, Record) error { return nil }

// ChainRow 是验哈希链时用到的一行。
type ChainRow struct {
	ID         uuid.UUID
	TenantID   *uuid.UUID
	OccurredAt time.Time
	ActorType  string
	ActorID    *uuid.UUID
	Resource   string
	Action     string
	ResourceID *uuid.UUID
	HTTPStatus int32
	Detail     []byte
	PrevHash   []byte
	Hash       []byte
}

// PlatformChainKey 是平台级 / 未认证事件那条哈希链在**链头表里的键**。
//
// audit_logs.tenant_id 可空，而 NULL 做不了主键，所以链头表用一个全零哨兵 UUID
// 归类这批（MULTI-TENANCY.md §10.3）。和迁移里 audit_chain() 触发器的 coalesce 一致。
//
// ⚠️ **它不是一个租户，别拿去调 ForTenant** —— 那会 panic，也应该 panic。
// 验平台级那条链走 VerifyPlatformChain。
var PlatformChainKey = uuid.Nil

// VerifyChain 从头验一遍某个租户的哈希链，返回第一条对不上的记录。
//
// 哈希由 DB 触发器算（应用算就等于应用能伪造），这里独立复算一遍 ——
// 有人绕过应用直接改库，这一步就会指出是哪一行。
//
// **链是每个租户各一条**（§2.4）：共用一条的话，验 A 租户的完整性要读到 B 租户的记录，
// 既慢又不该。验平台级那条传 PlatformChainKey。
func VerifyChain(ctx context.Context, q *repo.TenantQueries) (broken *uuid.UUID, checked int, err error) {
	rows, err := q.ListAuditChain(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("读审计链: %w", err)
	}
	return verifyRows(toChainRows(rows))
}

// VerifyPlatformChain 验平台级那条链（`tenant_id IS NULL` 的那批：登录失败、
// 健康检查这些未认证请求，§7.1）。
//
// 它不属于任何租户，所以走不带租户的句柄 —— 这是那批查询里为数不多的正当用法之一。
// 将来只有平台管理端调它（第 ⑤ 步）。
func VerifyPlatformChain(ctx context.Context, q *repo.UnscopedQueries) (broken *uuid.UUID, checked int, err error) {
	rows, err := q.ListPlatformAuditChain(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("读平台审计链: %w", err)
	}
	return verifyRows(toPlatformChainRows(rows))
}

// verifyRows 是两条链共用的复算逻辑。
func verifyRows(rows []ChainRow) (broken *uuid.UUID, checked int, err error) {
	var prev []byte
	for _, row := range rows {
		if string(chainHash(prev, row)) != string(row.Hash) {
			id := row.ID
			return &id, checked, nil
		}
		prev = row.Hash
		checked++
	}
	return nil, checked, nil
}

func toChainRows(rows []repo.ListAuditChainRow) []ChainRow {
	out := make([]ChainRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, ChainRow{
			ID: row.ID, TenantID: row.TenantID, OccurredAt: row.OccurredAt,
			ActorType: row.ActorType, ActorID: row.ActorID,
			Resource: row.Resource, Action: row.Action, ResourceID: row.ResourceID,
			HTTPStatus: row.HTTPStatus, Detail: row.Detail, Hash: row.Hash,
		})
	}
	return out
}

func toPlatformChainRows(rows []repo.ListPlatformAuditChainRow) []ChainRow {
	out := make([]ChainRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, ChainRow{
			ID: row.ID, TenantID: row.TenantID, OccurredAt: row.OccurredAt,
			ActorType: row.ActorType, ActorID: row.ActorID,
			Resource: row.Resource, Action: row.Action, ResourceID: row.ResourceID,
			HTTPStatus: row.HTTPStatus, Detail: row.Detail, Hash: row.Hash,
		})
	}
	return out
}

// chainHash 必须和迁移里 audit_chain() 触发器算的完全一致，改一边就要改另一边。
// 触发器最初在 00004_audit.sql，多租户之后改在 00007_multi_tenancy.sql。
func chainHash(prev []byte, row ChainRow) []byte {
	h := sha256.New()
	h.Write(prev)
	h.Write([]byte(row.ID.String() + "|" +
		row.OccurredAt.UTC().Format("2006-01-02T15:04:05.000000") + "|" +
		// 租户进哈希：不然把一行从 A 的链搬到 B 的链，两边都验得过
		uuidText(row.TenantID) + "|" +
		row.ActorType + "|" + uuidText(row.ActorID) + "|" +
		row.Resource + "|" + row.Action + "|" +
		uuidText(row.ResourceID) + "|" +
		fmt.Sprintf("%d", row.HTTPStatus) + "|" + string(row.Detail)))
	return h.Sum(nil)
}

func uuidText(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
