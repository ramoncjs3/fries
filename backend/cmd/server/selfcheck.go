package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
)

// runSelfcheck 是启动自检：**纯内存、不连库、秒级**（DECISIONS.md §3.7）。
//
// 它把整个应用装配一遍，然后检查那些「写错了就该拒绝启动」的事。
// 任何一项不过就返回非 0 退出码 —— 部署时这一步红了，服务就不该起来。
func runSelfcheck(cfg *config.Config, logger *slog.Logger) int {
	// 自检不连库：pool 传 nil，依赖数据库的组件都换成内存替身。
	a, err := newApp(context.Background(), cfg, logger, nil, version)
	if err != nil {
		fmt.Printf("✗ 装配应用失败\n  %v\n", err) //nolint:forbidigo // 自检就是打给人看的
		return 1
	}

	checks := []struct {
		name string
		fn   func() error
	}{
		{"配置校验", func() error { return cfg.Validate() }},
		{"错误码注册表", checkErrorCodes},
		{"错误响应格式", checkProblemFormat},
		{"接口契约", a.checkOperations},
		{"权限点声明", a.checkPermissions},
		{"配置项注册表", config.CheckRegistry},
		{"OpenAPI 文档", a.checkOpenAPI},
		{"运维路由", a.checkOpsRoutes},
	}

	failed := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			failed++
			fmt.Printf("✗ %s\n  %s\n", c.name, strings.ReplaceAll(err.Error(), "\n", "\n  ")) //nolint:forbidigo // 自检就是打给人看的
			continue
		}
		fmt.Printf("✓ %s\n", c.name) //nolint:forbidigo // 同上
	}

	if failed > 0 {
		fmt.Printf("\n自检未通过：%d 项失败\n", failed) //nolint:forbidigo // 同上
		return 1
	}
	fmt.Println("\n自检通过") //nolint:forbidigo // 同上
	return 0
}

// checkErrorCodes 确认 §4.6 那 16 个内置错误码都注册了，且注册表自洽。
func checkErrorCodes() error {
	var problems []string

	for _, want := range errs.Builtin() {
		got, ok := errs.Lookup(want.Code)
		if !ok {
			problems = append(problems, fmt.Sprintf("内置错误码 %s 没进注册表", want.Code))
			continue
		}
		if got != want {
			problems = append(problems, fmt.Sprintf("错误码 %s 被另一个实例占用了", want.Code))
		}
	}

	for _, c := range errs.All() {
		if c.Message == "" {
			problems = append(problems, fmt.Sprintf("错误码 %s 没有中文文案", c.Code))
		}
		if c.Status < 400 || c.Status > 599 {
			problems = append(problems, fmt.Sprintf("错误码 %s 的状态码 %d 不合法", c.Code, c.Status))
		}
	}

	return join(problems)
}

// checkProblemFormat 确认 huma 的错误构造确实被我们接管了 ——
// 少了这一步，错误响应会退回 huma 默认格式，前端拿不到 code。
func checkProblemFormat() error {
	got := huma.NewError(http.StatusConflict, "冲突了", errs.VersionConflict)
	p, ok := got.(*httpx.Problem)
	if !ok {
		return fmt.Errorf("huma.NewError 没有被覆盖，返回的是 %T", got)
	}
	if p.Code != errs.VersionConflict.Code {
		return fmt.Errorf("错误码没透传，期望 %s，得到 %s", errs.VersionConflict.Code, p.Code)
	}
	if p.Status != http.StatusConflict {
		return fmt.Errorf("状态码应取自错误码，期望 409，得到 %d", p.Status)
	}
	if ct := p.ContentType("application/json"); ct != "application/problem+json" {
		return fmt.Errorf("错误响应的 Content-Type 应为 application/problem+json，得到 %s", ct)
	}

	// 5xx 不许把内部细节带出去（红线 #5）
	internal := huma.NewError(http.StatusInternalServerError, "pq: password authentication failed")
	ip, ok := internal.(*httpx.Problem)
	if !ok {
		return fmt.Errorf("huma.NewError 没有被覆盖，返回的是 %T", internal)
	}
	if ip.Detail != errs.Internal.Message || len(ip.Errors) > 0 {
		return fmt.Errorf("5xx 响应泄露了内部细节：detail=%q errors=%d", ip.Detail, len(ip.Errors))
	}
	return nil
}

// checkOperations 检查每个接口的契约元信息齐不齐。
//
// OperationID 是前端生成类型和 SDK 的锚点，Summary 是文档里那一行说明，缺了就是欠账。
func (a *app) checkOperations() error {
	var problems []string
	seen := map[string]string{}

	for path, item := range a.api.OpenAPI().Paths {
		for method, op := range operationsOf(item) {
			where := fmt.Sprintf("%s %s", method, path)
			if op.OperationID == "" {
				problems = append(problems, where+" 缺 OperationID")
				continue
			}
			if op.Summary == "" {
				problems = append(problems, where+" 缺 Summary")
			}
			if prev, dup := seen[op.OperationID]; dup {
				problems = append(problems, fmt.Sprintf("OperationID %q 重复：%s 和 %s", op.OperationID, prev, where))
			}
			seen[op.OperationID] = where
		}
	}

	if len(seen) == 0 {
		problems = append(problems, "一个接口都没注册")
	}
	sort.Strings(problems)
	return join(problems)
}

// checkPermissions 是 DECISIONS.md §3.7 的三条防漏配检查：
//
//  1. 每个路由都有访问要求声明（漏了就是「谁都能调」）
//  2. 声明的权限点存在于模块注册表（打错字就挡不住人）
//  3. 每个模块声明的权限点都有路由实现（**反向检查** —— 防止角色配置页勾了一个
//     根本没接口的权限，看着有权限其实用不了）
//
// 任一不满足服务直接启动失败，并打印是哪个路由 / 哪个权限点。
func (a *app) checkPermissions() error {
	var problems []string

	routes := perm.Routes()
	declared := map[string]bool{}
	byOperation := map[string]perm.Route{}
	for _, r := range routes {
		byOperation[r.OperationID] = r
		switch r.Access {
		case perm.AccessPublic, perm.AccessAuthenticated:
		case perm.AccessPermission:
			if !perm.Has(r.Point.Resource, r.Point.Action) {
				problems = append(problems,
					fmt.Sprintf("路由 %s %s 用了没声明的权限点 %s", r.Method, r.Path, r.Point))
				continue
			}
			declared[r.Point.String()] = true
		default:
			problems = append(problems,
				fmt.Sprintf("路由 %s %s 的访问要求 %q 不认识", r.Method, r.Path, r.Access))
		}
	}

	// 每个 huma 操作都必须是走 perm 注册器进来的
	for path, item := range a.api.OpenAPI().Paths {
		for method, op := range operationsOf(item) {
			if _, ok := byOperation[op.OperationID]; !ok {
				problems = append(problems, fmt.Sprintf(
					"接口 %s %s 没有声明访问要求 —— 用 perm.Public / perm.Authenticated / perm.Guard 注册，别直接调 huma.Register",
					method, path))
			}
		}
	}

	// 反向检查：声明了权限点却没有接口实现
	for _, point := range perm.Points() {
		if !declared[point.String()] {
			problems = append(problems, fmt.Sprintf(
				"权限点 %s（%s）声明了却没有任何路由用它 —— 要么补接口，要么从模块声明里删掉",
				point, point.Name))
		}
	}

	sort.Strings(problems)
	return join(problems)
}

// checkOpenAPI 确认 OpenAPI 文档生成得出来（schema 写错了这里就炸）。
func (a *app) checkOpenAPI() error {
	spec, err := a.api.OpenAPI().YAML()
	if err != nil {
		return fmt.Errorf("生成 OpenAPI 失败：%w", err)
	}
	if len(spec) == 0 {
		return fmt.Errorf("生成的 OpenAPI 是空的")
	}
	return nil
}

// checkOpsRoutes 确认运维探针都在。
func (a *app) checkOpsRoutes() error {
	want := []string{opsPaths.Health, opsPaths.Ready, opsPaths.Metrics}
	have := map[string]bool{}
	for _, r := range a.echo.Router().Routes() {
		have[r.Path] = true
	}

	var problems []string
	for _, p := range want {
		if !have[p] {
			problems = append(problems, "缺少路由 "+p)
		}
	}
	return join(problems)
}

// operationsOf 把 PathItem 上挂着的操作按方法收集起来。
func operationsOf(item *huma.PathItem) map[string]*huma.Operation {
	all := map[string]*huma.Operation{
		http.MethodGet:     item.Get,
		http.MethodPost:    item.Post,
		http.MethodPut:     item.Put,
		http.MethodPatch:   item.Patch,
		http.MethodDelete:  item.Delete,
		http.MethodHead:    item.Head,
		http.MethodOptions: item.Options,
		http.MethodTrace:   item.Trace,
	}
	for method, op := range all {
		if op == nil {
			delete(all, method)
		}
	}
	return all
}

// join 把若干问题拼成一个 error，没问题则返回 nil。
func join(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(problems, "\n"))
}

// dumpOpenAPISpec 把 OpenAPI 文档打到 stdout。
//
// 和自检一样**不连库** —— 生成前端类型不该要求先起数据库。
// 前端的 TS 类型由它产出（DECISIONS.md §1：真相唯一在 Go 侧）。
func dumpOpenAPISpec(cfg *config.Config, logger *slog.Logger) int {
	a, err := newApp(context.Background(), cfg, logger, nil, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "装配应用失败：%v\n", err)
		return 1
	}

	spec, err := a.api.OpenAPI().MarshalJSON()
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成 OpenAPI 失败：%v\n", err)
		return 1
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, spec, "", "  "); err != nil {
		fmt.Fprintf(os.Stderr, "格式化 OpenAPI 失败：%v\n", err)
		return 1
	}
	pretty.WriteByte('\n')

	if _, err := os.Stdout.Write(pretty.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "写 OpenAPI 失败：%v\n", err)
		return 1
	}
	return 0
}
