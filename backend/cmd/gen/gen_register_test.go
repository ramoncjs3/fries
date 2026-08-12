package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registerFixture 造一个带三个登记锚点的最小仓库树。
func registerFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("backend/cmd/server/app.go", `package main

import (
	"context"

	usersvc "github.com/ramoncjs3/fries/internal/service/user"
)

func (a *app) build() {
	handler.RegisterUser(api, handler.NewUser(users))
	a.registerOps()
}
`)
	write("backend/internal/tenantsql/tenantsql.go", `package tenantsql

var tenantTables = map[string]bool{
	"users": true, "roles": true,
	"departments": true,
}
`)
	write("frontend/src/routes/index.tsx", `import { lazy } from "react";

const UserListPage = lazy(() => import("@/features/user/ListPage"));

function lazyPage(element) {
  return element;
}

export const router = createBrowserRouter([
  {
    children: [
      { index: true, element: lazyPage(<HomePage />) },
      { path: "/users", element: lazyPage(<UserListPage />) },
    ],
  },
]);
`)
	return root
}

func TestRegisterModule(t *testing.T) {
	root := registerFixture(t)
	def := refModule() // key=product, table=products, entity=Product, 有 read 动作

	res, err := registerModule(root, &def)
	if err != nil {
		t.Fatalf("registerModule 报错：%v", err)
	}
	if len(res) != 3 {
		t.Fatalf("应登记 3 处，得到 %d", len(res))
	}
	for _, r := range res {
		if r.status != "written" {
			t.Errorf("%s 应为 written，得到 %s", r.path, r.status)
		}
	}

	app := readFile(t, filepath.Join(root, "backend/cmd/server/app.go"))
	for _, want := range []string{
		`productsvc "github.com/ramoncjs3/fries/internal/service/product"`,
		"handler.RegisterProduct(api, handler.NewProduct(productsvc.New(a.store)))",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.go 应含：%s\n%s", want, app)
		}
	}
	// 装配行插在 registerOps 之前。
	if strings.Index(app, "RegisterProduct") > strings.Index(app, "a.registerOps()") {
		t.Error("RegisterProduct 应插在 a.registerOps() 之前")
	}

	ts := readFile(t, filepath.Join(root, "backend/internal/tenantsql/tenantsql.go"))
	// gofmt 会把 true 那列对齐（"products":<空格>true），所以只认 key。
	if !strings.Contains(ts, `"products":`) {
		t.Errorf("tenantsql 应含 products：\n%s", ts)
	}

	routes := readFile(t, filepath.Join(root, "frontend/src/routes/index.tsx"))
	for _, want := range []string{
		`const ProductListPage = lazy(() => import("@/features/product/ListPage"));`,
		`path: "/products",`,
		`path: "/products/new",`,
		`path: "/products/:id",`,
		`resource="product" action="read"`, // 有 read 动作 → 详情用 read
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("routes 应含：%s\n%s", want, routes)
		}
	}

	// 幂等：再跑一次全 skipped，文件不变。
	before := app + ts + routes
	res2, err := registerModule(root, &def)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res2 {
		if !strings.HasPrefix(r.status, "skipped") {
			t.Errorf("重跑应 skipped，得到 %s（%s）", r.status, r.path)
		}
	}
	after := readFile(t, filepath.Join(root, "backend/cmd/server/app.go")) +
		readFile(t, filepath.Join(root, "backend/internal/tenantsql/tenantsql.go")) +
		readFile(t, filepath.Join(root, "frontend/src/routes/index.tsx"))
	if before != after {
		t.Error("幂等被破坏：重跑改动了文件")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
