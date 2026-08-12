package repo

// 通用状态值。users / roles / departments / service_accounts 的 status 列
// 都用这一组，DB 侧有对应的 CHECK 约束。
//
// **不要在各模块里各写各的字符串** —— 拼错了编译期发现不了，
// 写进库还会被 CHECK 拦下来变成 500。
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)
