// Package audit 记审计日志。
//
// 分两层，缺一不可（DECISIONS.md §6）：
//   - **中间件层**（自动，全部接口）：谁 / 何时 / 什么资源 / 什么动作 / IP / UA / 结果码 / 耗时
//   - **handler 层**：哪条记录、参数摘要。因为「新增」的记录 ID 在响应里而不在请求里，
//     中间件只看得到字节流，拿不到 —— 必须在 handler 里补
//
// handler 里这样补：
//
//	audit.SetResourceID(ctx, user.ID)
//	audit.Detail(ctx, "username", in.Body.Username)
package audit

import (
	"context"
	"maps"
	"sync"

	"github.com/google/uuid"
)

// maxDetailValueLen 限制单个摘要字段的长度，别让审计表被大 body 撑爆。
const maxDetailValueLen = 200

// maxDetailFields 限制摘要字段数。
const maxDetailFields = 20

// Recorder 收集这次请求的审计信息。中间件放进 context，handler 往里填。
//
// 用指针放进 context 是有意的：handler 在内层填，中间件在外层读。
type Recorder struct {
	mu         sync.Mutex
	resource   string
	action     string
	resourceID *uuid.UUID
	tenantID   *uuid.UUID
	actor      *Actor
	detail     map[string]any
	skipped    bool
}

type recorderKey struct{}

// WithRecorder 造一个 Recorder 并放进 context。
func WithRecorder(ctx context.Context) (context.Context, *Recorder) {
	r := &Recorder{detail: map[string]any{}}
	return context.WithValue(ctx, recorderKey{}, r), r
}

// FromContext 取出当前请求的 Recorder。
func FromContext(ctx context.Context) (*Recorder, bool) {
	if ctx == nil {
		return nil, false
	}
	r, ok := ctx.Value(recorderKey{}).(*Recorder)
	return r, ok && r != nil
}

// SetAction 标记这次请求操作的资源和动作。授权中间件会自动填，
// 登录这类没有权限点的接口由 handler 自己填。
func SetAction(ctx context.Context, resource, action string) {
	if r, ok := FromContext(ctx); ok {
		r.mu.Lock()
		r.resource, r.action = resource, action
		r.mu.Unlock()
	}
}

// Actor 是这次操作的主体，登录成功时由 handler 补。
type Actor struct {
	Type string
	ID   uuid.UUID
	Name string
}

// SetActor 显式指定操作主体。
//
// 只有登录用得上：登录**成功之前**请求上下文里还没有主体，中间件读不到是谁，
// 那条最该看清楚是谁的审计反而会记成匿名。
func SetActor(ctx context.Context, actor Actor) {
	if r, ok := FromContext(ctx); ok {
		r.mu.Lock()
		r.actor = &actor
		r.mu.Unlock()
	}
}

// SetResourceID 补上「操作的是哪条记录」。新增操作必须在 handler 里调它。
func SetResourceID(ctx context.Context, id uuid.UUID) {
	if r, ok := FromContext(ctx); ok {
		r.mu.Lock()
		r.resourceID = &id
		r.mu.Unlock()
	}
}

// SetTenantID 显式指定这条审计属于哪个租户。
//
// 平时不用调它 —— 中间件会从认证主体上取。只有一种情况必须手动补：
// **登录失败**（MULTI-TENANCY.md §7.1）。那时还没有主体，而「公司代码有效、
// 密码错误」这条恰恰要记在**那个租户名下**，客户才能看到「有人在爆破我们的账号」。
// 公司代码本身无效时不要调，让它保持 nil（平台级事件）。
func SetTenantID(ctx context.Context, id uuid.UUID) {
	if r, ok := FromContext(ctx); ok {
		r.mu.Lock()
		r.tenantID = &id
		r.mu.Unlock()
	}
}

// Detail 往参数摘要里加一项。**别塞密码、token 这类东西** —— 想塞也会被 Mask 挡掉。
func Detail(ctx context.Context, key string, value any) {
	r, ok := FromContext(ctx)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.detail) >= maxDetailFields {
		return
	}
	r.detail[key] = maskValue(key, value)
}

// Skip 标记这次请求不记审计（探针、静态资源之类）。
func Skip(ctx context.Context) {
	if r, ok := FromContext(ctx); ok {
		r.mu.Lock()
		r.skipped = true
		r.mu.Unlock()
	}
}

// Collected 是 Recorder 收集到的全部信息。
type Collected struct {
	Resource   string
	Action     string
	ResourceID *uuid.UUID
	TenantID   *uuid.UUID
	Actor      *Actor
	Detail     map[string]any
	Skipped    bool
}

// Snapshot 读出收集到的信息，中间件写库前调它。
func (r *Recorder) Snapshot() Collected {
	r.mu.Lock()
	defer r.mu.Unlock()

	detail := make(map[string]any, len(r.detail))
	maps.Copy(detail, r.detail)

	return Collected{
		Resource:   r.resource,
		Action:     r.action,
		ResourceID: r.resourceID,
		TenantID:   r.tenantID,
		Actor:      r.actor,
		Detail:     detail,
		Skipped:    r.skipped,
	}
}
