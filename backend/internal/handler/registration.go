package handler

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/service/registration"
)

// Registration 是自助注册接口。都是公开的（陌生人还没有任何身份）。
type Registration struct {
	svc *registration.Service
}

// NewRegistration 造自助注册 handler。
func NewRegistration(svc *registration.Service) *Registration {
	return &Registration{svc: svc}
}

// RegisterRegistration 注册自助注册路由。两条都是 Public。
func RegisterRegistration(api huma.API, h *Registration) {
	perm.Public(api, huma.Operation{
		OperationID: "register",
		Method:      http.MethodPost,
		Path:        "/auth/register",
		Summary:     "自助注册：申请开一个组织",
		Description: "校验后发一封验证邮件。⚠️ 需平台在设置里开启「允许自助注册」，否则拒绝。" +
			"邮箱/公司代码是否已存在不在这一步暴露（防枚举）。",
		Tags: []string{tagAuth},
	}, h.register)

	perm.Public(api, huma.Operation{
		OperationID: "register-verify",
		Method:      http.MethodPost,
		Path:        "/auth/register/verify",
		Summary:     "自助注册：验证邮箱、完成建组织",
		Description: "校验邮件里的一次性 token，建组织和首个管理员，返回登录要用的公司代码。",
		Tags:        []string{tagAuth},
	}, h.verify)
}

// RegisterInput 是注册申请入参。
type RegisterInput struct {
	Body struct {
		Email       string `json:"email" format:"email" maxLength:"255" doc:"管理员邮箱，验证信发这里"`
		CompanyName string `json:"company_name" minLength:"1" maxLength:"100" doc:"组织名"`
		Code        string `json:"code" minLength:"2" maxLength:"32" doc:"公司代码，登录时要填"`
		Password    string `json:"password" minLength:"1" maxLength:"200" doc:"管理员密码"`
	}
}

func (h *Registration) register(ctx context.Context, in *RegisterInput) (*httpx.Response[struct{}], error) {
	audit.SetAction(ctx, "registration", "register")
	audit.Detail(ctx, "email", in.Body.Email)
	audit.Detail(ctx, "code", in.Body.Code)

	if err := h.svc.Register(ctx, registration.RegisterInput{
		Email:       in.Body.Email,
		CompanyName: in.Body.CompanyName,
		Code:        in.Body.Code,
		Password:    in.Body.Password,
	}); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}

// RegisterVerifyInput 是「验证邮箱完成注册」入参。
type RegisterVerifyInput struct {
	Body struct {
		Token string `json:"token" minLength:"1" maxLength:"200" doc:"验证邮件里的一次性 token"`
	}
}

func (h *Registration) verify(ctx context.Context, in *RegisterVerifyInput) (*httpx.Response[registration.VerifiedResult], error) {
	audit.SetAction(ctx, "registration", "verify")

	result, err := h.svc.Verify(ctx, in.Body.Token)
	if err != nil {
		return nil, err
	}
	return httpx.OK(result), nil
}
