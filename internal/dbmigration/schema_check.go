package dbmigration

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/store"
	"slices"
	"strings"
)

// Check the actual table/column inventory before copying. Unknown business tables
// or columns must not silently disappear just because this binary cannot name them.
func checkManifest(ctx context.Context, tx *sql.Tx, d store.Dialect) error {
	expected := map[string][]string{
		"schema_migrations":          {"version", "applied_at"},
		"database_identity":          {"id", "instance_id"},
		"database_migration_batches": {"migration_id", "table_name", "batch_no", "row_count", "content_sha256", "committed_at"},
	}
	for _, table := range Manifest() {
		for _, col := range table.Columns {
			expected[table.Name] = append(expected[table.Name], col.Name)
		}
	}
	var query string
	switch d {
	case store.SQLite:
		query = "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"
	case store.MySQL:
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE'"
	case store.PostgreSQL:
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'"
	case store.DM:
		query = "SELECT TABLE_NAME FROM ALL_TABLES WHERE OWNER=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())"
	case store.SQLServer:
		query = "SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=SCHEMA_NAME() AND TABLE_TYPE='BASE TABLE'"
	default:
		return ErrUnsupported
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("dbmigration: schema metadata query: %w", ErrMismatch)
	}
	var names []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			rows.Close()
			return ErrMismatch
		}
		if d == store.DM {
			name = strings.ToLower(name)
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("dbmigration: schema metadata iteration: %w", ErrMismatch)
	}
	if len(names) != len(expected) {
		return fmt.Errorf("dbmigration: schema table count %d expected %d: %w", len(names), len(expected), ErrMismatch)
	}
	for _, table := range names {
		columns, ok := expected[table]
		if !ok {
			return fmt.Errorf("dbmigration: unexpected schema table: %w", ErrMismatch)
		}
		q := "SELECT * FROM " + store.Identifier(d, table) + " WHERE 1=0"
		rows, err = tx.QueryContext(ctx, q)
		if err != nil {
			return ErrMismatch
		}
		actual, err := rows.Columns()
		if d == store.DM {
			for i := range actual {
				actual[i] = strings.ToLower(actual[i])
			}
		}
		rows.Close()
		if err != nil || !sameColumnInventory(actual, columns) {
			return fmt.Errorf("dbmigration: schema column inventory for %s: %w", table, ErrMismatch)
		}
	}
	var count int
	var latest string
	if tx.QueryRowContext(ctx, "SELECT COUNT(*),MAX(version) FROM schema_migrations").Scan(&count, &latest) != nil || count != 65 || latest != "0066_database_migration" {
		return fmt.Errorf("dbmigration: schema migration versions: %w", ErrMismatch)
	}
	return nil
}

// Physical column order may differ after ALTER TABLE versus SQLite rebuilds.
// Copy always names columns explicitly; require the complete exact inventory.
func sameColumnInventory(actual, expected []string) bool {
	a := append([]string(nil), actual...)
	b := append([]string(nil), expected...)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
