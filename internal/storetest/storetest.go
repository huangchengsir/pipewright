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

	"github.com/go-sql-driver/mysql"
	"github.com/huangchengsir/pipewright/internal/store"
)

var schemaSeq atomic.Int64

// MySQLDSNEnv 是承载 MySQL 测试 DSN 的环境变量名。
const MySQLDSNEnv = "PIPEWRIGHT_TEST_MYSQL_DSN"

// OpenForDialect 返回一个已应用迁移的干净库。MySQL 未配置时 t.Skip。
func OpenForDialect(t *testing.T, d store.Dialect) *store.Store {
	t.Helper()
	if d == store.MySQL {
		return openMySQL(t)
	}
	st, err := store.Open(t.TempDir() + "/test.db")
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

	schema := "pwtest_" + strings.ToLower(randToken()) + itoa(schemaSeq.Add(1))
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

// randToken 由测试名/序列驱动唯一性,不用随机源(确定性,且 schemaSeq 保证不撞)。
func randToken() string { return "s" }

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
