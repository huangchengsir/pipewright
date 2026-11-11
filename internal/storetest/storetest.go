// Package storetest provides isolated SQLite, MySQL and PostgreSQL test fixtures.
//
// SQLite 始终可用(t.TempDir 文件库)。MySQL 需设环境变量 PIPEWRIGHT_TEST_MYSQL_DSN
// (指向一个可建库的 MySQL 8.0.29+ 账号,如 root:pw@tcp(127.0.0.1:3306)/pipewright);
// 未设则 MySQL 子测试 t.Skip——CI 默认无此变量即只测 SQLite,专用 job 设后启用。
//
// MySQL 隔离:每次 OpenForDialect 建一个唯一命名的 schema 并切入,Cleanup 时 DROP,
// 避免相互污染。
package storetest

import (
	"context"
	"database/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	mssql "github.com/microsoft/go-mssqldb"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/huangchengsir/pipewright/internal/store"
)

var schemaSeq atomic.Int64

// MySQLDSNEnv 是承载 MySQL 测试 DSN 的环境变量名。
const MySQLDSNEnv = "PIPEWRIGHT_TEST_MYSQL_DSN"
const PostgresDSNEnv = "PIPEWRIGHT_TEST_POSTGRES_DSN"
const DMDSNEnv = "PIPEWRIGHT_TEST_DM_DSN"
const SQLServerDSNEnv = "PIPEWRIGHT_TEST_SQLSERVER_DSN"
const DriverEnv = "PIPEWRIGHT_TEST_DB_DRIVER"

// Dialect uses PIPEWRIGHT_TEST_DB_DRIVER, or the single configured external DSN.
// An ambiguous selection fails before any database is opened.
// 各包测试 helper 据此让整个套件随环境变量在两方言间切换。
func Dialect() store.Dialect {
	switch os.Getenv(DriverEnv) {
	case "sqlite":
		return store.SQLite
	case "mysql":
		return store.MySQL
	case "postgres":
		return store.PostgreSQL
	case "sqlserver":
		return store.SQLServer
	case "dm":
		return store.DM
	case "":
	default:
		panic("storetest: unsupported PIPEWRIGHT_TEST_DB_DRIVER")
	}
	selected := store.SQLite
	for _, item := range []struct {
		env     string
		dialect store.Dialect
	}{{MySQLDSNEnv, store.MySQL}, {PostgresDSNEnv, store.PostgreSQL}, {SQLServerDSNEnv, store.SQLServer}, {DMDSNEnv, store.DM}} {
		if strings.TrimSpace(os.Getenv(item.env)) != "" {
			if selected != store.SQLite {
				panic("storetest: multiple database DSNs require PIPEWRIGHT_TEST_DB_DRIVER")
			}
			selected = item.dialect
		}
	}
	if selected != store.SQLite {
		return selected
	}
	return store.SQLite
}

// Open 打开一个已应用迁移的全新隔离库,方言由 Dialect() 决定。
func Open(t *testing.T) *store.Store {
	t.Helper()
	return OpenForDialect(t, Dialect())
}

// OpenDB 同 Open,但只返回底层 *sql.DB(多数包 helper 只需 DB)。
func OpenDB(t *testing.T) *sql.DB {
	t.Helper()
	return Open(t).DB
}

// OpenDBWithPath 返回 (DB, sqlite 文件路径)。仅用于需读原始 DB 文件做明文泄漏断言的
// SQLite 专有测试;MySQL 下路径为 "" 且应配合 SkipIfMySQL 跳过。
func OpenDBWithPath(t *testing.T) (*sql.DB, string) {
	t.Helper()
	if Dialect() != store.SQLite {
		return OpenDB(t), ""
	}
	path := t.TempDir() + "/test.db"
	st := openSQLitePath(t, path)
	return st.DB, path
}

// SkipIfMySQL retains its historical name; it skips SQLite file-only tests
// for any external database, including PostgreSQL.
// 这类测试断言的"密文先于落库"属性由 vault 层保证、与方言无关,故跳过不损失实质覆盖。
func SkipIfMySQL(t *testing.T) {
	t.Helper()
	if Dialect() != store.SQLite {
		t.Skip("SQLite file-only test: external stores have no local database file")
	}
}

// OpenForDialect 返回一个已应用迁移的干净库。MySQL 未配置时 t.Skip。
func OpenForDialect(t *testing.T, d store.Dialect) *store.Store {
	t.Helper()
	switch d {
	case store.DM:
		return openDM(t, true)
	case store.SQLServer:
		return openSQLServer(t, true)
	case store.MySQL:
		return openMySQL(t, true)
	case store.PostgreSQL:
		return openPostgres(t, true)
	case store.SQLite:
		return openSQLitePath(t, t.TempDir()+"/test.db")
	default:
		t.Fatal("storetest: requested dialect is unavailable")
		return nil
	}
}

func openSQLitePath(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ForEachDialect 对每个方言各跑一遍 body(MySQL 未配置则该子测试 Skip)。
func ForEachDialect(t *testing.T, body func(t *testing.T, st *store.Store)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { body(t, OpenForDialect(t, store.SQLite)) })
	t.Run("mysql", func(t *testing.T) { body(t, OpenForDialect(t, store.MySQL)) })
	t.Run("postgres", func(t *testing.T) { body(t, OpenForDialect(t, store.PostgreSQL)) })
	t.Run("sqlserver", func(t *testing.T) { body(t, OpenForDialect(t, store.SQLServer)) })
	t.Run("dm", func(t *testing.T) { body(t, OpenForDialect(t, store.DM)) })
}

func openMySQL(t *testing.T, initialize bool) *store.Store {
	t.Helper()
	dsn := os.Getenv(MySQLDSNEnv)
	if strings.TrimSpace(dsn) == "" {
		t.Skipf("%s 未设置,跳过 MySQL 子测试", MySQLDSNEnv)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("storetest: invalid MySQL test configuration")
	}

	// 用无库连接建一个唯一 schema。
	base := *cfg
	base.DBName = ""
	admin, err := sql.Open("mysql", base.FormatDSN())
	if err != nil {
		t.Fatalf("open mysql admin: %v", err)
	}
	defer admin.Close()

	// schema 名须跨「并行包测试进程」全局唯一:pid 区分进程,纳秒+自增区分进程内。
	schema := "pwtest_" + itoa(int64(os.Getpid())) + "_" + itoa(time.Now().UnixNano()) + "_" + itoa(schemaSeq.Add(1))
	if _, err := admin.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4"); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		a, e := sql.Open("mysql", base.FormatDSN())
		if e == nil {
			_, _ = a.Exec("DROP DATABASE IF EXISTS `" + schema + "`")
			_ = a.Close()
		}
	})

	cfg.DBName = schema
	open := store.OpenWithConfig
	if !initialize {
		open = store.Connect
	}
	st, err := open(store.OpenConfig{Driver: "mysql", DSN: cfg.FormatDSN()})
	if err != nil {
		t.Fatalf("open mysql schema %s: %v", schema, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// PostgreSQL tests own a unique schema, never the supplied database or public.
func openPostgres(t *testing.T, initialize bool) *store.Store {
	t.Helper()
	dsn := os.Getenv(PostgresDSNEnv)
	if strings.TrimSpace(dsn) == "" {
		t.Skipf("%s is not set", PostgresDSNEnv)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("storetest: invalid PostgreSQL configuration")
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "pwtest_" + itoa(int64(os.Getpid())) + "_" + itoa(time.Now().UnixNano()) + "_" + itoa(schemaSeq.Add(1))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		db := stdlib.OpenDB(*cfg)
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned PostgreSQL schema: %v", err)
		}
	})
	isolatedDSN := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("storetest: invalid PostgreSQL URL")
		}
		query := u.Query()
		query.Set("search_path", schema)
		u.RawQuery = query.Encode()
		isolatedDSN = u.String()
	}
	open := store.OpenWithConfig
	if !initialize {
		open = store.Connect
	}
	st, err := open(store.OpenConfig{Driver: "postgres", DSN: isolatedDSN})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// OpenEmptyForDialect owns an isolated empty destination for migration tests.
func OpenEmptyForDialect(t *testing.T, d store.Dialect) *store.Store {
	t.Helper()
	switch d {
	case store.DM:
		return openDM(t, false)
	case store.SQLServer:
		return openSQLServer(t, false)
	case store.MySQL:
		return openMySQL(t, false)
	case store.PostgreSQL:
		return openPostgres(t, false)
	case store.SQLite:
		st, err := store.Connect(store.OpenConfig{Driver: "sqlite", DSN: t.TempDir() + "/target.db"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	default:
		t.Fatal("storetest: requested empty dialect unavailable")
		return nil
	}
}

// SQL Server tests own a unique database; no existing database is altered.
func openSQLServer(t *testing.T, initialize bool) *store.Store {
	t.Helper()
	dsn := os.Getenv(SQLServerDSNEnv)
	if strings.TrimSpace(dsn) == "" {
		t.Skip(SQLServerDSNEnv + " is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "sqlserver" {
		t.Fatal("storetest: SQL Server fixture requires a sqlserver URL")
	}
	base := *u
	q := base.Query()
	q.Set("database", "master")
	base.RawQuery = q.Encode()
	connector, err := mssql.NewConnector(base.String())
	if err != nil {
		t.Fatal("storetest: invalid SQL Server configuration")
	}
	admin := sql.OpenDB(connector)
	defer admin.Close()
	name := "pwtest_" + itoa(int64(os.Getpid())) + "_" + itoa(time.Now().UnixNano()) + "_" + itoa(schemaSeq.Add(1))
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE "+store.Identifier(store.SQLServer, name)+" COLLATE Latin1_General_100_BIN2"); err != nil {
		t.Fatalf("create owned SQL Server test database: %v", err)
	}
	t.Cleanup(func() {
		db := sql.OpenDB(connector)
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "ALTER DATABASE "+store.Identifier(store.SQLServer, name)+" SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE "+store.Identifier(store.SQLServer, name)); err != nil {
			t.Errorf("drop owned SQL Server test database: %v", err)
		}
	})
	q = u.Query()
	q.Set("database", name)
	u.RawQuery = q.Encode()
	open := store.OpenWithConfig
	if !initialize {
		open = store.Connect
	}
	st, err := open(store.OpenConfig{Driver: "sqlserver", DSN: u.String()})
	if err != nil {
		t.Fatalf("open owned SQL Server test database: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func openDM(t *testing.T, initialize bool) *store.Store {
	t.Helper()
	dsn := os.Getenv(DMDSNEnv)
	if strings.TrimSpace(dsn) == "" {
		t.Skip(DMDSNEnv + " is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "dm" {
		t.Fatal("storetest: DM fixture requires a dm URL")
	}
	admin, err := store.Connect(store.OpenConfig{Driver: "dm", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var user string
	if err = admin.DB.QueryRow("SELECT USER FROM DUAL").Scan(&user); err != nil {
		t.Fatal(err)
	}
	schema := strings.ToUpper("pwtest_" + itoa(int64(os.Getpid())) + "_" + itoa(time.Now().UnixNano()) + "_" + itoa(schemaSeq.Add(1)))
	quoted := func(value string) string { return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\"" }
	if _, err = admin.DB.Exec("CREATE SCHEMA " + quoted(schema) + " AUTHORIZATION " + quoted(user)); err != nil {
		t.Fatalf("create owned DM schema: %v", err)
	}
	t.Cleanup(func() {
		db, e := store.Connect(store.OpenConfig{Driver: "dm", DSN: dsn})
		if e != nil {
			t.Error("DM cleanup connection unavailable")
			return
		}
		defer db.Close()
		if _, e = db.DB.Exec("DROP SCHEMA " + quoted(schema) + " CASCADE"); e != nil {
			t.Errorf("drop owned DM schema: %v", e)
		}
	})
	q := u.Query()
	q.Set("schema", schema)
	u.RawQuery = q.Encode()
	open := store.OpenWithConfig
	if !initialize {
		open = store.Connect
	}
	st, err := open(store.OpenConfig{Driver: "dm", DSN: u.String()})
	if err != nil {
		t.Fatalf("open owned DM schema: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
