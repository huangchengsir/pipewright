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

	"github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrationFS embed.FS

//go:embed migrations/mysql/*.sql
var mysqlMigrationFS embed.FS

// Store 持有数据库连接。仅本包持有 *sql.DB;其它领域包经 repository 接口取数。
type Store struct {
	DB *sql.DB
	// Dialect 标识底层方言(SQLite 默认 / MySQL);Open 时由驱动判定。
	Dialect Dialect
}

// OpenConfig 是驱动感知的打开参数。Driver 取 "sqlite"(默认)或 "mysql";
// DSN 为对应驱动的连接串(SQLite 为数据库文件路径)。
type OpenConfig struct {
	Driver string
	DSN    string
}

// OpenWithConfig 按驱动选择后端打开数据库并应用对应方言的内嵌迁移。
// 空 Driver 视为 "sqlite",保持向后兼容。
func OpenWithConfig(c OpenConfig) (*Store, error) {
	switch c.Driver {
	case "mysql":
		return openMySQL(c.DSN)
	case "", "sqlite":
		return Open(c.DSN)
	default:
		return nil, fmt.Errorf("store: unknown db driver %q (want sqlite|mysql)", c.Driver)
	}
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

// openMySQL 打开 MySQL(8.0+)连接并应用 mysql 方言迁移。与 SQLite 不同,
// MySQL 用常规连接池(不串行化),外键由 InnoDB 默认强制。驱动经 dialect.go
// 的 mysql 包导入已注册,无需在此重复 blank import。
//
// ClientFoundRows 强制开启:让 UPDATE 的 RowsAffected 返回"匹配行数"而非默认的
// "实际改变行数",与 SQLite 语义一致。否则"把列更新为相同值"会返回 0,被领域层
// (如 SetDeployTerminal / 各 RowsAffected==0→ErrNotFound 校验)误判为行不存在。
func openMySQL(dsn string) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse mysql dsn: %w", err)
	}
	cfg.ClientFoundRows = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	s := &Store{DB: db, Dialect: DialectOf(db)}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrationFS 返回当前方言对应的内嵌迁移文件系统与 glob。
func (s *Store) migrationFS() (fs.FS, string) {
	if s.Dialect == MySQL {
		return mysqlMigrationFS, "migrations/mysql/*.sql"
	}
	return sqliteMigrationFS, "migrations/sqlite/*.sql"
}

// Close 关闭底层数据库连接。
func (s *Store) Close() error { return s.DB.Close() }

// migrate 建立 schema_migrations 跟踪表,并按版本顺序幂等应用内嵌的 *.sql 迁移。
// 领域表由各自 story 在需要时通过新增迁移创建。bootstrap DDL 与每条迁移的执行
// 方式按方言分叉(见 schemaMigrationsDDL / applyMigration)。
func (s *Store) migrate() error {
	if _, err := s.DB.Exec(s.schemaMigrationsDDL()); err != nil {
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
		if err := s.applyMigration(version, string(sqlText)); err != nil {
			return err
		}
	}
	return nil
}

// schemaMigrationsDDL 返回方言对应的版本跟踪表 DDL。MySQL 主键不能用 TEXT,改 VARCHAR。
func (s *Store) schemaMigrationsDDL() string {
	if s.Dialect == MySQL {
		return `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    VARCHAR(255) PRIMARY KEY,
			applied_at VARCHAR(64) NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`
}

// applyMigration 应用单条迁移并记录版本。
//
// SQLite:DDL 与版本记录在同一事务内原子提交,崩溃/部分失败整体回滚,不留半迁移。
// MySQL :DDL 隐式提交,事务对 DDL 无回滚效力——故逐句执行(go-sql-driver 默认不允许
// 单 Exec 多语句)后再记录版本;幂等靠 CREATE TABLE/TRIGGER IF NOT EXISTS。
// 注意:MySQL 的 CREATE INDEX / ALTER ADD COLUMN 无 IF NOT EXISTS,真正的"只应用一次"
// 由 schema_migrations 跟踪保证;仅当崩溃恰好发生在 DDL 已提交但版本未记录之间,重跑才会
// 撞已存在对象(单管理员全新建库场景概率极低,可接受)。
func (s *Store) applyMigration(version, sqlText string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	if s.Dialect == MySQL {
		for _, stmt := range splitStatements(sqlText) {
			if _, err := s.DB.Exec(stmt); err != nil {
				return fmt.Errorf("apply migration %s: %w", version, err)
			}
		}
		if _, err := s.DB.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, now,
		); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		return nil
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	if _, err := tx.Exec(sqlText); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, now,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	return nil
}

// splitStatements 把含多条 SQL 的迁移文本拆成单条语句(供 MySQL 逐条执行)。
// 规则:剥离整行/行尾 `--` 注释;在单引号字符串外按 `;` 切分;丢弃空白语句。
// 我们的 mysql 迁移不在字符串字面量内出现 `;`,且触发器写成单语句 SIGNAL 形式
// (无 BEGIN/END、体内无 `;`),故此简单切分足够且可单测。
func splitStatements(sqlText string) []string {
	var stmts []string
	var b strings.Builder
	inQuote := false
	lineComment := false

	runes := []rune(sqlText)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if lineComment {
			if c == '\n' {
				lineComment = false
				b.WriteRune(c)
			}
			continue
		}
		if !inQuote && c == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			lineComment = true
			i++ // 跳过第二个 '-'
			continue
		}
		if c == '\'' {
			inQuote = !inQuote
			b.WriteRune(c)
			continue
		}
		if c == ';' && !inQuote {
			if stmt := strings.TrimSpace(b.String()); stmt != "" {
				stmts = append(stmts, stmt)
			}
			b.Reset()
			continue
		}
		b.WriteRune(c)
	}
	if stmt := strings.TrimSpace(b.String()); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}

// migrationVersion 从 "migrations/sqlite/0001_baseline.sql" 提取版本键 "0001_baseline"。
func migrationVersion(entry string) string {
	return strings.TrimSuffix(path.Base(entry), ".sql")
}
