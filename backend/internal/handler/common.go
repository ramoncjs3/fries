package handler

import (
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/errs"
)

// handler 层共用的小工具。这一层只做「HTTP 形状 ↔ service 入参」的翻译，
// 业务判断一律在 service（红线 #6）。

// errInvalidField 报一个字段级错误。location 形如 body.parent_id / query.actor_id，
// 前端靠它把错误挂到对应的表单字段上（DECISIONS.md §4.3）。
func errInvalidField(location, message string) error {
	return errs.ValidationFailed.WithField(location, message)
}

// parsePathID 解析路径上的 UUID。
//
// huma 的 `format:"uuid"` 已经拦过一道，这里是第二道 —— 万一以后有人把 tag 删了，
// 不至于把空字符串当 ID 传到 SQL 里。
func parsePathID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errInvalidField("path.id", "不是合法的 UUID")
	}
	return id, nil
}
