package main

import (
	"regexp"
	"strings"
	"testing"
)

// squash 把连续空白（gofmt 的对齐缩进）压成单空格，好让断言不必逐格对齐。
var rxWS = regexp.MustCompile(`[ \t]+`)

func squash(s string) string { return rxWS.ReplaceAllString(s, " ") }

func TestGenServiceParses(t *testing.T) {
	def := validGenerated()
	src := genService(&def)
	mustParseGo(t, src)
}

func TestGenServiceStructure(t *testing.T) {
	def := validGenerated()
	src := squash(genService(&def))

	for _, want := range []string{
		"package supplier",
		"Safe to edit", // 种子文件头
		"func (s *Service) tenant(ctx context.Context)", // 每方法先取租户句柄
		"authz.MustTenant(ctx)",
		"s.store.ForTenant(id)",
		"func (s *Service) List(ctx context.Context, f ListFilter) ([]Supplier, int64, error)",
		"func (s *Service) Get(ctx context.Context, id uuid.UUID) (Supplier, error)",
		"func (s *Service) Create(ctx context.Context, in Input) (Supplier, error)",
		"func (s *Service) Update(ctx context.Context, id uuid.UUID, version int, in Input) (Supplier, error)",
		"func (s *Service) Delete(ctx context.Context, id uuid.UUID, version int) error",
		"q.ListSuppliers(ctx, repo.ListSuppliersArgs{",
		"q.CreateSupplier(ctx, repo.CreateSupplierArgs{",
		"CreatedBy: actorID(ctx),",
		"Version: int32(version),",
		"repo.IsUniqueViolation(err, \"uk_suppliers_name\")", // 索引名和迁移一致
		"return ErrNameTaken",
		"errs.VersionConflict",
		"audit.SetResourceID",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("service 产出里应包含：%s", want)
		}
	}
}

// TestGenServiceTypes 校验字段的 Go 类型和 sqlc 对齐：可空列指针、decimal/date 映射。
func TestGenServiceTypes(t *testing.T) {
	def := validGenerated()
	src := squash(genService(&def))

	for _, want := range []string{
		"Name string",                       // required → 非指针
		"Status *string",                    // 可选 enum → 指针
		"Credit *decimal.Decimal",           // 可选 decimal → 指针 decimal
		"StartedAt *time.Time",              // 可选 date → 指针 time.Time
		"Remark *string",                    // 可选 text → 指针
		"\"github.com/shopspring/decimal\"", // 有 decimal 字段才 import
	} {
		if !strings.Contains(src, want) {
			t.Errorf("service 类型产出里应包含：%s", want)
		}
	}
}

// TestGenServiceApplyDefaults 可选 + 带默认值的字段要在 applyDefaults 里补默认。
func TestGenServiceApplyDefaults(t *testing.T) {
	def := validGenerated() // status 默认 active
	src := squash(genService(&def))

	for _, want := range []string{
		"func (in *Input) applyDefaults()",
		"if in.Status == nil {",
		"v := \"active\"",
		"in.Status = &v",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("applyDefaults 产出里应包含：%s", want)
		}
	}
}

// TestGenServiceCountVariants 覆盖 count 查询的 0/1/≥2 参数三种调用形态。
func TestGenServiceCountVariants(t *testing.T) {
	// ≥2 参（fixture：keyword + status + started_at）→ Args 结构
	multi := validGenerated()
	if !strings.Contains(genService(&multi), "q.CountSuppliers(ctx, repo.CountSuppliersArgs{") {
		t.Error("多参 count 应走 Args 结构")
	}

	// 1 参（只有 keyword）→ 裸传
	one := ModuleDef{
		Key: "note", Name: "便签", Generated: true, Scoped: true,
		Menu:    Menu{Path: "/notes", Icon: "file"},
		Fields:  []Field{{Name: "title", Type: typeString, Label: "标题", Required: true, Searchable: true, Max: 100}},
		Actions: []string{actList},
	}
	oneSrc := genService(&one)
	mustParseGo(t, oneSrc)
	if !strings.Contains(oneSrc, "q.CountNotes(ctx, keyword)") {
		t.Error("单参 count 应裸传")
	}

	// 0 参（无搜索无筛选）→ 裸调
	zero := ModuleDef{
		Key: "tag", Name: "标签", Generated: true, Scoped: true,
		Menu:    Menu{Path: "/tags", Icon: "tag"},
		Fields:  []Field{{Name: "color", Type: typeString, Label: "颜色", Required: true, Max: 20}},
		Actions: []string{actList},
	}
	zeroSrc := genService(&zero)
	mustParseGo(t, zeroSrc)
	if !strings.Contains(zeroSrc, "q.CountTags(ctx)\n") {
		t.Error("无参 count 应裸调")
	}
}

// TestGenServiceNoUnique 没有唯一字段时 uniqueOr 只兜底，不产 switch。
// TestGenServiceUniqueOr 单个唯一字段用 if（单-case switch 会被 gocritic 拦），多个才用 switch。
func TestGenServiceUniqueOr(t *testing.T) {
	// fixture：只有 name 一个唯一字段 → if
	one := validGenerated()
	src := genService(&one)
	mustParseGo(t, src)
	if strings.Contains(src, "switch {") {
		t.Error("单个唯一字段应用 if，不该产单-case switch（gocritic singleCaseSwitch）")
	}
	if !strings.Contains(src, `if repo.IsUniqueViolation(err, "uk_suppliers_name") {`) {
		t.Error("单个唯一字段应产 if repo.IsUniqueViolation")
	}

	// 两个唯一字段 → switch
	two := validGenerated()
	two.Fields[4].Unique = true // remark 也设唯一
	src2 := genService(&two)
	mustParseGo(t, src2)
	if !strings.Contains(src2, "switch {") {
		t.Error("多个唯一字段应用 switch")
	}
}

func TestGenServiceNoUnique(t *testing.T) {
	def := validGenerated()
	for i := range def.Fields {
		def.Fields[i].Unique = false
	}
	src := genService(&def)
	mustParseGo(t, src)
	if strings.Contains(src, "IsUniqueViolation") {
		t.Error("无唯一字段时不该产 IsUniqueViolation 分支")
	}
	if !strings.Contains(src, "func uniqueOr(err error) error {") {
		t.Error("uniqueOr 仍要保留（Create/Update 会调它）")
	}
}
