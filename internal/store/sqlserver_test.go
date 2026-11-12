package store

import (
	"context"
	mssql "github.com/microsoft/go-mssqldb"
	"reflect"
	"strings"
	"testing"
)

func TestSQLServerExplicitSQL(t *testing.T) {
	q, err := Bind(SQLServer, "SELECT [literal?]]name], N'?', ? /* ? */ WHERE id=?")
	if err != nil || q != "SELECT [literal?]]name], N'?', @p1 /* ? */ WHERE id=@p2" {
		t.Fatal(q, err)
	}
	q, err = Bind(PostgreSQL, "SELECT ARRAY[?], ?")
	if err != nil || q != "SELECT ARRAY[$1], $2" {
		t.Fatal(q, err)
	}
	q = InsertSQL(SQLServer, "example", []string{"id", "value"}, []string{"?", "?"}, []string{"id"}, []string{"value"})
	if !strings.Contains(q, "WITH (HOLDLOCK)") || strings.Contains(q, "DUAL") || !strings.HasSuffix(q, ";") {
		t.Fatal(q)
	}
	page, args := Page(SQLServer, 20, 40)
	if page != " OFFSET ? ROWS FETCH NEXT ? ROWS ONLY" || !reflect.DeepEqual(args, []any{40, 20}) {
		t.Fatal(page, args)
	}
	if !IsUniqueErr(mssql.Error{Number: 2627}) || IsUniqueErr(mssql.Error{Number: 50000}) {
		t.Fatal("unique classification")
	}
	if !IsForeignKeyErr(mssql.Error{Number: 547, Message: "FOREIGN KEY conflict"}) || IsForeignKeyErr(mssql.Error{Number: 547, Message: "CHECK conflict"}) {
		t.Fatal("constraint classification")
	}
	_, err = openSQLServer(context.Background(), "sqlserver://user:supersecret@invalid:badport/db")
	if err == nil || strings.Contains(err.Error(), "supersecret") {
		t.Fatal("unsafe connection error")
	}

}
