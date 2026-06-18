package storetest_test

import (
	"context"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// TestMigrationsApplied 验证两方言都把全部迁移跑完、计数与文件数一致、且重开幂等。
func TestMigrationsApplied(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		var n int
		if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations`).Scan(&n); err != nil {
			t.Fatalf("count migrations: %v", err)
		}
		if n != 46 {
			t.Fatalf("应用迁移数 = %d, 期望 46", n)
		}
		// 核心领域表存在(随手验一张)。
		if _, err := st.DB.ExecContext(ctx, `SELECT 1 FROM audit_log WHERE 1=0`); err != nil {
			t.Fatalf("audit_log 表缺失: %v", err)
		}
	})
}

// TestAuditAppendOnly 验证审计表 append-only:INSERT 可,UPDATE / DELETE 被存储层硬拦。
func TestAuditAppendOnly(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		_, err := st.DB.ExecContext(ctx,
			"INSERT INTO audit_log (id, `timestamp`, actor, action, target_type, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			"a1", now(), "admin", "project_create", "project", now())
		if err != nil {
			t.Fatalf("insert audit: %v", err)
		}
		if _, err := st.DB.ExecContext(ctx, `UPDATE audit_log SET actor = 'x' WHERE id = ?`, "a1"); err == nil {
			t.Fatalf("审计 UPDATE 应被触发器拒绝,但成功了")
		}
		if _, err := st.DB.ExecContext(ctx, `DELETE FROM audit_log WHERE id = ?`, "a1"); err == nil {
			t.Fatalf("审计 DELETE 应被触发器拒绝,但成功了")
		}
		// INSERT 仍可(再插一条不报错)。
		if _, err := st.DB.ExecContext(ctx,
			"INSERT INTO audit_log (id, `timestamp`, actor, action, target_type, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			"a2", now(), "admin", "project_update", "project", now()); err != nil {
			t.Fatalf("第二次 insert audit 应成功: %v", err)
		}
	})
}

// TestUpsertSuffixRoundtrip 验证 UpsertSuffix 在真库上"冲突即更新"语义两方言一致。
func TestUpsertSuffixRoundtrip(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		suffix := store.UpsertSuffix(st.Dialect, []string{"id"}, []string{"provider", "updated_at"})
		q := `INSERT INTO ai_config (id, provider, created_at, updated_at) VALUES (1, ?, ?, ?) ` + suffix

		if _, err := st.DB.ExecContext(ctx, q, "claude", now(), now()); err != nil {
			t.Fatalf("first upsert: %v", err)
		}
		if _, err := st.DB.ExecContext(ctx, q, "openai", now(), now()); err != nil {
			t.Fatalf("second upsert: %v", err)
		}

		var provider string
		var count int
		if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(1), MAX(provider) FROM ai_config`).Scan(&count, &provider); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if count != 1 {
			t.Fatalf("单例表应只 1 行, got %d", count)
		}
		if provider != "openai" {
			t.Fatalf("冲突更新后 provider 应为 openai, got %q", provider)
		}
	})
}

// TestErrorClassification 验证唯一/外键冲突错误在两方言上都被中心分类函数正确识别。
func TestErrorClassification(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()

		// 唯一/主键冲突:ai_config 单例 id=1 重复插入(不带 upsert)→ IsUniqueErr。
		ins := `INSERT INTO ai_config (id, created_at, updated_at) VALUES (1, ?, ?)`
		if _, err := st.DB.ExecContext(ctx, ins, now(), now()); err != nil {
			t.Fatalf("first ai_config insert: %v", err)
		}
		_, err := st.DB.ExecContext(ctx, ins, now(), now())
		if err == nil {
			t.Fatalf("重复主键应报错")
		}
		if !store.IsUniqueErr(err) {
			t.Fatalf("应识别为唯一冲突: %v", err)
		}

		// 外键冲突:projects.credential_id 指向不存在的凭据 → IsForeignKeyErr。
		_, err = st.DB.ExecContext(ctx,
			`INSERT INTO projects (id, name, repo_url, credential_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			"p1", "demo", "https://example.com/r.git", "no-such-cred", now(), now())
		if err == nil {
			t.Fatalf("悬挂外键应报错")
		}
		if !store.IsForeignKeyErr(err) {
			t.Fatalf("应识别为外键冲突: %v", err)
		}
	})
}
