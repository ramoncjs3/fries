// Package handler 是 HTTP 接口层：定义 huma 的入参/出参契约，调 service，转错误码。
//
// 红线 #6：handler 不写业务逻辑。红线 #7：返回的 error 必须是注册过的 *errs.Code。
package handler

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
)

// PingInput 是 /ping 的入参。
//
// 校验规则写在 tag 上，huma 自动校验并产出 OpenAPI —— 校验不通过返回
// common.validation_failed，errors[] 里带上是哪个参数错了。
type PingInput struct {
	Echo  string `query:"echo" maxLength:"64" doc:"原样回显的内容"`
	Times int    `query:"times" minimum:"1" maximum:"5" default:"1" doc:"回显次数，1~5"`
}

// PingResult 是 /ping 的返回数据。
type PingResult struct {
	Message    string    `json:"message" doc:"回显内容"`
	Times      int       `json:"times" doc:"回显次数"`
	ServerTime time.Time `json:"server_time" doc:"服务端当前时间（UTC，RFC3339）"`
	Version    string    `json:"version" doc:"服务版本"`
}

// errInvalidQuery 是 handler 里做完格式转换后的统一报错方式。
// **返回的必须是注册过的错误码**（红线 #7）。
func errInvalidQuery(field, message string) error {
	return errs.ValidationFailed.WithField("query."+field, message)
}

// RegisterSystem 注册系统级接口。
//
// ping 是公开接口，不需要登录也不需要权限点。
func RegisterSystem(api huma.API, version string) {
	perm.Public(api, huma.Operation{
		OperationID: "ping",
		Method:      "GET",
		Path:        "/ping",
		Summary:     "连通性自检",
		Description: "回显参数并返回服务端时间。用来确认接口链路、响应封套和 request_id 都通了。",
		Tags:        []string{"system"},
	}, func(_ context.Context, in *PingInput) (*httpx.Response[PingResult], error) {
		return httpx.OK(PingResult{
			Message:    in.Echo,
			Times:      in.Times,
			ServerTime: time.Now().UTC(),
			Version:    version,
		}), nil
	})
}
