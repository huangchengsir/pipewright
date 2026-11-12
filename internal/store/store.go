// Package store owns database connections, dialects and embedded schema migrations.
// Domain repositories share its *sql.DB and use parameterized SQL.
//
// 使用纯 Go 的 modernc.org/sqlite 驱动(无 CGO),以支持 CGO_DISABLED 全静态
// 交叉编译——这是平台"双运行模式"的前提。严禁改用需要 CGO 的 mattn/go-sqlite3。
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrationFS embed.FS

//go:embed migrations/mysql/*.sql
var mysqlMigrationFS embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrationFS embed.FS

//go:embed migrations/sqlserver/*.sql
var sqlserverMigrationFS embed.FS

//go:embed migrations/dm/*.sql
var dmMigrationFS embed.FS

// Store holds the shared database/sql pool and its explicit SQL dialect.
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
	s, err := Connect(c)
	if err != nil {
		return nil, err
	}
	if err := s.Migrate(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Connect opens a pool without creating business tables or applying migrations.
// SQLite may create the named file: target preflight must inspect its path before
// calling Connect, and only initialize a new file after the user confirms.
func Connect(c OpenConfig) (*Store, error) {
	return ConnectContext(context.Background(), c)
}

// ConnectContext bounds initial connection and handshake checks to 15 seconds,
// or the caller's earlier cancellation. The returned pool outlives this context.
func ConnectContext(ctx context.Context, c OpenConfig) (*Store, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch c.Driver {
	case "mysql":
		return openMySQL(ctx, c.DSN)
	case "sqlserver":
		return openSQLServer(ctx, c.DSN)
	case "postgres":
		return openPostgres(ctx, c.DSN)
	case "dm":
		return openDM(ctx, c.DSN)
	case "", "sqlite":
		return connectSQLite(ctx, c.DSN)
	default:
		return nil, fmt.Errorf("store: unknown db driver %q (want sqlite|mysql|postgres|sqlserver|dm)", c.Driver)
	}
}

func Open(dbPath string) (*Store, error) {
	return OpenWithConfig(OpenConfig{Driver: "sqlite", DSN: dbPath})
}

// Open 打开(必要时创建)指定路径的 SQLite 数据库,并应用内嵌迁移。
// DSN 加固:busy_timeout 避免锁竞争直接报错;WAL 提升并发;foreign_keys 默认开启
// (SQLite 默认关闭)。SetMaxOpenConns(1) 串行化写,规避 SQLITE_BUSY。
func connectSQLite(ctx context.Context, dbPath string) (*Store, error) {
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{DB: db, Dialect: DialectOf(db)}
	return s, nil
}

// openMySQL 打开 MySQL(8.0+)连接并应用 mysql 方言迁移。与 SQLite 不同,
// MySQL 用常规连接池(不串行化),外键由 InnoDB 默认强制。驱动经 dialect.go
// 的 mysql 包导入已注册,无需在此重复 blank import。
//
// ClientFoundRows 强制开启:让 UPDATE 的 RowsAffected 返回"匹配行数"而非默认的
// "实际改变行数",与 SQLite 语义一致。否则"把列更新为相同值"会返回 0,被领域层
// (如 SetDeployTerminal / 各 RowsAffected==0→ErrNotFound 校验)误判为行不存在。
func openMySQL(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("store: invalid MySQL connection configuration")
	}
	cfg.ClientFoundRows = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	s := &Store{DB: db, Dialect: DialectOf(db)}
	return s, nil
}

// ErrDriverUnavailable is explicit until an approved DM connector is linked.
var ErrDriverUnavailable = errors.New("store: DM driver is not available in this build")

func openPostgres(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("store: PostgreSQL DSN is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("store: invalid PostgreSQL connection configuration")
	}
	cfg.RuntimeParams["client_encoding"] = "UTF8"
	cfg.RuntimeParams["standard_conforming_strings"] = "on"
	db := sql.OpenDB(&bindingConnector{Connector: stdlib.GetConnector(*cfg), dialect: PostgreSQL})
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	var version string
	if err := db.QueryRowContext(ctx, "SHOW server_version_num").Scan(&version); err != nil {
		_ = db.Close()
		return nil, errors.New("store: PostgreSQL connection or version check failed")
	}
	n, err := strconv.Atoi(version)
	if err != nil || n < 160000 {
		_ = db.Close()
		return nil, errors.New("store: PostgreSQL 16 or newer is required")
	}
	s := &Store{DB: db, Dialect: PostgreSQL}
	return s, nil
}

// migrationFS 返回当前方言对应的内嵌迁移文件系统与 glob。
func (s *Store) migrationFS() (fs.FS, string) {
	if s.Dialect == DM {
		return dmMigrationFS, "migrations/dm/*.sql"
	}
	if s.Dialect == SQLServer {
		return sqlserverMigrationFS, "migrations/sqlserver/*.sql"
	}
	if s.Dialect == PostgreSQL {
		return postgresMigrationFS, "migrations/postgres/*.sql"
	}
	if s.Dialect == MySQL {
		return mysqlMigrationFS, "migrations/mysql/*.sql"
	}
	return sqliteMigrationFS, "migrations/sqlite/*.sql"
}

// Close 关闭底层数据库连接。
func (s *Store) Close() error { return s.DB.Close() }

// Migrate explicitly applies the selected embedded schema. Callers that inspect
// an empty migration target must not invoke this until initialization is authorized.
func (s *Store) Migrate() error {
	if s == nil || s.DB == nil {
		return errors.New("store: no database connection")
	}
	if err := s.migrate(); err != nil {
		return err
	}
	// Concurrent first opens converge on one database-local identity.
	if _, err := s.DB.Exec(InsertSQL(s.Dialect, "database_identity", []string{"id", "instance_id"}, []string{"1", "?"}, []string{"id"}, nil), uuid.NewString()); err != nil {
		return fmt.Errorf("store: initialize database identity: %w", err)
	}
	_, err := s.Identity(context.Background())
	return err
}

func (s *Store) Identity(ctx context.Context) (string, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, "SELECT instance_id FROM database_identity WHERE id=1").Scan(&id); err != nil {
		return "", errors.New("store: database identity unavailable")
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", errors.New("store: database identity invalid")
	}
	return id, nil
}

// migrate 建立 schema_migrations 跟踪表,并按版本顺序幂等应用内嵌的 *.sql 迁移。
// 领域表由各自 story 在需要时通过新增迁移创建。bootstrap DDL 与每条迁移的执行
// 方式按方言分叉(见 schemaMigrationsDDL / applyMigration)。
func (s *Store) migrate() error {
	if s.Dialect != SQLite && s.Dialect != MySQL && s.Dialect != PostgreSQL && s.Dialect != SQLServer && s.Dialect != DM {
		return errors.New("store: migrations unavailable for this dialect")
	}
	if s.Dialect == DM {
		release, err := s.lockDMMigrations()
		if err != nil {
			return err
		}
		defer release()
	}
	if s.Dialect == SQLServer {
		release, err := s.lockSQLServerMigrations()
		if err != nil {
			return err
		}
		defer release()
	}
	// A session advisory lock serializes startup for this PostgreSQL schema.
	if s.Dialect == PostgreSQL {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := s.DB.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(232, hashtext(current_schema()))"); err != nil {
			return fmt.Errorf("store: migration lock: %w", err)
		}
		defer func() {
			release, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_, _ = conn.ExecContext(release, "SELECT pg_advisory_unlock(232, hashtext(current_schema()))")
		}()
	}

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
	if s.Dialect == SQLServer {
		return "IF OBJECT_ID(N'schema_migrations', N'U') IS NULL CREATE TABLE schema_migrations (version VARCHAR(255) PRIMARY KEY,applied_at VARCHAR(64) NOT NULL)"
	}

	if s.Dialect == DM {
		return `CREATE TABLE IF NOT EXISTS schema_migrations(version VARCHAR(255) PRIMARY KEY,applied_at VARCHAR(64) NOT NULL)`
	}
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

	if s.Dialect == MySQL || s.Dialect == DM {
		statements := splitStatements(sqlText)
		if s.Dialect == DM {
			statements = strings.Split(strings.ReplaceAll(sqlText, "\r\n", "\n"), "\n-- pipewright:statement\n")
		}
		for _, stmt := range statements {
			if !hasSQLCode(stmt) {
				continue
			}
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
	statements := []string{sqlText}
	if s.Dialect == SQLServer {
		statements = splitSQLServerBatches(sqlText)
	}
	if s.Dialect == PostgreSQL {
		statements, err = splitSQLStatements(sqlText)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("parse migration %s: %w", version, err)
		}
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
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

func hasSQLCode(source string) bool {
	for i := 0; i < len(source); {
		if strings.ContainsRune(" \r\n\t", rune(source[i])) {
			i++
			continue
		}
		if strings.HasPrefix(source[i:], "--") || strings.HasPrefix(source[i:], "/*") {
			end, _, err := sqlToken(source, i)
			if err != nil {
				return true
			}
			i = end
			continue
		}
		return true
	}
	return false
}
