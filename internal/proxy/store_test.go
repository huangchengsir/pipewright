package proxy

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

// TestStoreCRUDRoundtrip 验证路由 落库 → 列 → 启停 → 删 在真库上往返一致(两方言)。
func TestStoreCRUDRoundtrip(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := NewStore(st.DB)

		r := newRoute(CreateInput{ServerID: "srv-1", Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 8080})
		if err := s.insert(ctx, r); err != nil {
			t.Fatalf("insert: %v", err)
		}

		// list by server.
		routes, err := s.list(ctx, "srv-1")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(routes) != 1 {
			t.Fatalf("期望 1 条路由, got %d", len(routes))
		}
		got := routes[0]
		if got.Domain != "app.example.com" || got.UpstreamContainer != "web" || got.UpstreamPort != 8080 {
			t.Fatalf("路由字段不符: %+v", got)
		}
		if !got.Enabled || got.CertStatus != CertStatusPending || got.TLSMode != tlsModeAuto {
			t.Fatalf("默认态不符: enabled=%v cert=%s tls=%s", got.Enabled, got.CertStatus, got.TLSMode)
		}

		// 其它 server 不串。
		other, err := s.list(ctx, "srv-2")
		if err != nil {
			t.Fatalf("list other: %v", err)
		}
		if len(other) != 0 {
			t.Fatalf("srv-2 不应有路由, got %d", len(other))
		}

		// setEnabled false → listEnabledForServer 应空。
		if err := s.setEnabled(ctx, r.ID, false); err != nil {
			t.Fatalf("setEnabled: %v", err)
		}
		enabled, err := s.listEnabledForServer(ctx, "srv-1")
		if err != nil {
			t.Fatalf("listEnabled: %v", err)
		}
		if len(enabled) != 0 {
			t.Fatalf("停用后 enabled 列表应空, got %d", len(enabled))
		}

		// setCertStatus 回写。
		if err := s.setCertStatus(ctx, r.ID, CertStatusIssued, "ok"); err != nil {
			t.Fatalf("setCertStatus: %v", err)
		}
		after, err := s.get(ctx, r.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if after.CertStatus != CertStatusIssued || after.CertDetail != "ok" {
			t.Fatalf("证书态回写不符: %s/%s", after.CertStatus, after.CertDetail)
		}

		// delete.
		if err := s.del(ctx, r.ID); err != nil {
			t.Fatalf("del: %v", err)
		}
		if _, err := s.get(ctx, r.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("删后 get 应 ErrNotFound, got %v", err)
		}
		if err := s.del(ctx, r.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("重复删应 ErrNotFound, got %v", err)
		}
	})
}

// TestStoreUniqueDomain 验证同一 domain 第二次插入被唯一索引拦下 → ErrDomainTaken。
func TestStoreUniqueDomain(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := NewStore(st.DB)

		r1 := newRoute(CreateInput{ServerID: "srv-1", Domain: "dup.example.com", UpstreamContainer: "a", UpstreamPort: 80})
		if err := s.insert(ctx, r1); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		r2 := newRoute(CreateInput{ServerID: "srv-2", Domain: "dup.example.com", UpstreamContainer: "b", UpstreamPort: 90})
		if err := s.insert(ctx, r2); !errors.Is(err, ErrDomainTaken) {
			t.Fatalf("重复域名应 ErrDomainTaken, got %v", err)
		}
	})
}
