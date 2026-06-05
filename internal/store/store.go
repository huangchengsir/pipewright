// Package store owns all SQLite access. No other package touches the database
// directly — they go through repository interfaces defined here.
//
// 使用纯 Go 的 modernc.org/sqlite 驱动(无 CGO),以支持 CGO_DISABLED 全静态
// 交叉编译——这是平台"双运行模式"的前提。严禁改用需要 CGO 的 mattn/go-sqlite3。
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrationFS embed.FS

// Store 持有数据库连接。仅本包持有 *sql.DB;其它领域包经 repository 接口取数。
type Store struct {
	DB *sql.DB
	// Dialect 标识底层方言(SQLite 默认 / MySQL);Open 时由驱动判定。
	Dialect Dialect
}

// Open 打开(必要时创建)指定路径的 SQLite 数据库,并应用内嵌迁移。
// DSN 加固:busy_timeout 避免锁竞争直接报错;WAL 提升并发;foreign_keys 默认开启
// (SQLite 默认关闭)。SetMaxOpenConns(1) 串行化写,规避 SQLITE_BUSY。
func Open(dbPath string) (*Store, error) {
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{DB: db, Dialect: DialectOf(db)}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrationFS 返回当前方言对应的内嵌迁移文件系统与 glob。
// MySQL 分支在 P5 接入 mysql 迁移集后启用。
func (s *Store) migrationFS() (fs.FS, string) {
	return sqliteMigrationFS, "migrations/sqlite/*.sql"
}

// Close 关闭底层数据库连接。
func (s *Store) Close() error { return s.DB.Close() }

// migrate 建立 schema_migrations 跟踪表,并按版本顺序幂等应用内嵌的 *.sql 迁移。
// 本阶段不创建任何领域表;领域表由各自 story 在需要时通过新增迁移创建。
func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	fsys, glob := s.migrationFS()
	entries, err := fs.Glob(fsys, glob)
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(entries)

	for _, entry := range entries {
		version := migrationVersion(entry)

		var applied int
		if err := s.DB.QueryRow(
			`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if applied > 0 {
			continue
		}

		sqlText, err := fs.ReadFile(fsys, entry)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		// 迁移 DDL 与版本记录在同一事务内,保证原子:崩溃/部分失败则整体回滚,
		// 不会留下"已改但未记录"的半迁移导致下次重跑。
		tx, err := s.DB.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err := tx.Exec(string(sqlText)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}

// migrationVersion 从 "migrations/0001_baseline.sql" 提取版本键 "0001_baseline"。
func migrationVersion(entry string) string {
	return strings.TrimSuffix(path.Base(entry), ".sql")
}
