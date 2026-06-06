// Package storetest 提供跨方言(SQLite / MySQL)的测试夹具。
//
// SQLite 始终可用(t.TempDir 文件库)。MySQL 需设环境变量 PIPEWRIGHT_TEST_MYSQL_DSN
// (指向一个可建库的 MySQL 8.0.29+ 账号,如 root:pw@tcp(127.0.0.1:3306)/pipewright);
// 未设则 MySQL 子测试 t.Skip——CI 默认无此变量即只测 SQLite,专用 job 设后启用。
//
// MySQL 隔离:每次 OpenForDialect 建一个唯一命名的 schema 并切入,Cleanup 时 DROP,
// 避免相互污染。
package storetest

import (
	"database/sql"
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

// Dialect 返回当前 env 驱动的方言:设了 PIPEWRIGHT_TEST_MYSQL_DSN → MySQL,否则 SQLite。
// 各包测试 helper 据此让整个套件随环境变量在两方言间切换。
func Dialect() store.Dialect {
	if strings.TrimSpace(os.Getenv(MySQLDSNEnv)) != "" {
		return store.MySQL
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
	if Dialect() == store.MySQL {
		return OpenDB(t), ""
	}
	path := t.TempDir() + "/test.db"
	st := openSQLitePath(t, path)
	return st.DB, path
}

// SkipIfMySQL 跳过仅适用于 SQLite 文件存储的测试(如读原始 .db 文件 grep 明文)。
// 这类测试断言的"密文先于落库"属性由 vault 层保证、与方言无关,故跳过不损失实质覆盖。
func SkipIfMySQL(t *testing.T) {
	t.Helper()
	if Dialect() == store.MySQL {
		t.Skip("SQLite 文件专有测试:MySQL 无对应原始文件,跳过(密文属性由 vault 层保证)")
	}
}

// OpenForDialect 返回一个已应用迁移的干净库。MySQL 未配置时 t.Skip。
func OpenForDialect(t *testing.T, d store.Dialect) *store.Store {
	t.Helper()
	if d == store.MySQL {
		return openMySQL(t)
	}
	return openSQLitePath(t, t.TempDir()+"/test.db")
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
}

func openMySQL(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv(MySQLDSNEnv)
	if strings.TrimSpace(dsn) == "" {
		t.Skipf("%s 未设置,跳过 MySQL 子测试", MySQLDSNEnv)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", MySQLDSNEnv, err)
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
	st, err := store.OpenWithConfig(store.OpenConfig{Driver: "mysql", DSN: cfg.FormatDSN()})
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
