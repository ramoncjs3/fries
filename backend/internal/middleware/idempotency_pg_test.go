package middleware_test

import (
	"testing"
	"time"

	"github.com/ramoncjs3/fries/internal/middleware"
	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// TestPgIdempotencyStore 验 PG 幂等键存储的四条语义：新键放行、重复挡住、释放后能重来、过期后能重认领。
//
// 这是多副本部署的前提（SCALING.md §1）：内存版每副本一份认不出别的副本见过的键，
// 这版靠库里一条原子 INSERT ... ON CONFLICT 让所有副本共享同一份记录。
func TestPgIdempotencyStore(t *testing.T) {
	pool := testdb.Start(t)
	ctx := t.Context()
	q := repo.New(pool).Unscoped()

	t.Run("新键放行、重复挡住", func(t *testing.T) {
		s := middleware.NewPgIdempotencyStore(q, time.Hour)
		key := "acme/u1 POST /orders k-fresh"

		fresh, err := s.Remember(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if !fresh {
			t.Fatal("第一次见到的键应放行（fresh=true）")
		}

		fresh, err = s.Remember(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if fresh {
			t.Fatal("同一个键第二次应被挡住（fresh=false）—— 这正是幂等")
		}
	})

	t.Run("释放后能重来", func(t *testing.T) {
		s := middleware.NewPgIdempotencyStore(q, time.Hour)
		key := "acme/u1 POST /orders k-forget"

		if fresh, _ := s.Remember(ctx, key); !fresh {
			t.Fatal("首次应放行")
		}
		s.Forget(ctx, key) // 请求失败会调它，客户端改完得能用同一个键重试
		fresh, err := s.Remember(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if !fresh {
			t.Fatal("释放后同键应能重新认领")
		}
	})

	t.Run("过期后能重认领", func(t *testing.T) {
		// 负 ttl：expires_at 落在过去，WHERE expires_at<=now() 稳成立，走重新认领 —— 模拟键已过期。
		// 不用 0：宿主机时钟和容器 DB 时钟可能有微小偏差，0 会让「过去」变得不确定。
		s := middleware.NewPgIdempotencyStore(q, -time.Hour)
		key := "acme/u1 POST /orders k-expire"

		if fresh, _ := s.Remember(ctx, key); !fresh {
			t.Fatal("首次应放行")
		}
		fresh, err := s.Remember(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if !fresh {
			t.Fatal("键已过期，应能重新认领（fresh=true）")
		}
	})

	t.Run("清过期键", func(t *testing.T) {
		s := middleware.NewPgIdempotencyStore(q, -time.Hour) // 立即过期
		if _, err := s.Remember(ctx, "acme/u1 POST /orders k-cleanup"); err != nil {
			t.Fatal(err)
		}
		n, err := s.DeleteExpired(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n < 1 {
			t.Fatalf("应清掉至少 1 条过期键，清了 %d", n)
		}
	})
}
