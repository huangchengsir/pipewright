package storetest_test

import (
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestMySQLBinaryDatabaseOAuthForeignKeys(t *testing.T) {
	s := storetest.OpenEmptyForDialect(t, store.MySQL)
	var database string
	if e := s.DB.QueryRow("SELECT DATABASE()").Scan(&database); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec("ALTER DATABASE " + store.Identifier(store.MySQL, database) + " COLLATE utf8mb4_0900_bin"); e != nil {
		t.Fatal(e)
	}
	if e := s.Migrate(); e != nil {
		t.Fatal(e)
	}
	var parent string
	if e := s.DB.QueryRow("SELECT COLLATION_NAME FROM information_schema.columns WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='credentials' AND COLUMN_NAME='id'").Scan(&parent); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"oauth_credentials", "oauth_credential_uses", "oauth_authorizations"} {
		var child string
		if e := s.DB.QueryRow("SELECT COLLATION_NAME FROM information_schema.columns WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='credential_id'", table).Scan(&child); e != nil || child != parent {
			t.Fatal("foreign-key collation mismatch", table, e)
		}
	}
}
