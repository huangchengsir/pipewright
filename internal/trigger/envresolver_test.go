package trigger

import (
	"context"
	"testing"
)

// TestEnvironmentResolver 证分支→环境解析复用与 webhook 同一套 matchBranch glob:
// 精确命中、release/* 跨 `/` 命中、无命中、无配置项目全覆盖。
func TestEnvironmentResolver(t *testing.T) {
	svc, db, _, projID := newSvc(t)
	ctx := context.Background()

	// 经 Save 落分支映射(与 webhook 配置同一存储)。
	if _, err := svc.Save(ctx, projID, SaveInput{
		UnmatchedPolicy: PolicyRecord,
		BranchMappings: []BranchMapping{
			{ID: "m1", BranchPattern: "main", Environment: "production", TargetServerIDs: []string{"prod-1"}},
			{ID: "m2", BranchPattern: "release/*", Environment: "staging", TargetServerIDs: []string{"stg-1", "stg-2"}},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	res := NewEnvironmentResolver(db)

	t.Run("精确命中", func(t *testing.T) {
		env, servers := res.ResolveEnv(ctx, projID, "main")
		if env != "production" || len(servers) != 1 || servers[0] != "prod-1" {
			t.Fatalf("env=%q servers=%v", env, servers)
		}
	})

	t.Run("release/* 跨斜杠命中", func(t *testing.T) {
		env, servers := res.ResolveEnv(ctx, projID, "release/1.2")
		if env != "staging" || len(servers) != 2 {
			t.Fatalf("env=%q servers=%v", env, servers)
		}
	})

	t.Run("无命中分支", func(t *testing.T) {
		if env, _ := res.ResolveEnv(ctx, projID, "feature/x"); env != "" {
			t.Fatalf("无命中应返回空环境,得 %q", env)
		}
	})

	t.Run("空分支", func(t *testing.T) {
		if env, _ := res.ResolveEnv(ctx, projID, "  "); env != "" {
			t.Fatalf("空分支应返回空环境,得 %q", env)
		}
	})

	t.Run("无触发配置的项目", func(t *testing.T) {
		other := seedProject(t, db) // 未 Save 任何映射
		if env, _ := res.ResolveEnv(ctx, other, "main"); env != "" {
			t.Fatalf("无配置项目应返回空环境,得 %q", env)
		}
	})

	t.Run("nil 接收者安全", func(t *testing.T) {
		var nilRes *EnvironmentResolver
		if env, _ := nilRes.ResolveEnv(ctx, projID, "main"); env != "" {
			t.Fatalf("nil resolver 应返回空,得 %q", env)
		}
	})
}
