package store

import (
	"path/filepath"
	"testing"
)

// TestOpenAppliesMigrations 验证:Open 建立 schema_migrations 跟踪表、应用全部内嵌迁移,
// 且 schema_migrations 跟踪表本身始终存在(领域表由各 story 的迁移按需创建)。
func TestOpenAppliesMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var name string
	if err := s.DB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&name); err != nil {
		t.Fatalf("schema_migrations table missing: %v", err)
	}

	var migCount int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&migCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migCount < 1 {
		t.Fatalf("expected >=1 migration recorded, got %d", migCount)
	}

	// schema_migrations 跟踪表必须在用户表集合中。
	tables := userTables(t, s)
	if !contains(tables, "schema_migrations") {
		t.Fatalf("schema_migrations not in user tables: %v", tables)
	}
}

// TestOpenIdempotent 验证:重复 Open 同一库不会重复应用迁移(应用次数稳定)。
func TestOpenIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	var firstCount int
	if err := s1.DB.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&firstCount); err != nil {
		t.Fatalf("count migrations (first): %v", err)
	}
	_ = s1.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	var secondCount int
	if err := s2.DB.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&secondCount); err != nil {
		t.Fatalf("count migrations (second): %v", err)
	}
	if secondCount != firstCount {
		t.Fatalf("reopen changed migration count: first=%d second=%d (not idempotent)", firstCount, secondCount)
	}
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

func userTables(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.DB.Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
	)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	return tables
}
