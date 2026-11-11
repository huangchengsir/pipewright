package dbmigration

import (
	"errors"
	"github.com/go-sql-driver/mysql"
	"net/url"
	"testing"
)

func TestConnectionFieldsPreserveCredentialsAndIPv6(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql", "sqlserver", "dm"} {
		t.Run(driver, func(t *testing.T) {
			input := ConnectionFields{Driver: driver, Host: "[::1]", Database: "test database", Username: "user", Password: "a:/?#@%+汉字😀  ", Schema: "private_schema"}
			connection, e := BuildConnection(input)
			if e != nil {
				t.Fatal(e)
			}
			if driver == "mysql" {
				c, e := mysql.ParseDSN(connection.DSN)
				if e != nil {
					t.Fatal("DSN parse failed")
				}
				if c.Passwd != input.Password || c.User != input.Username || c.DBName != input.Database || c.Addr != "[::1]:3306" {
					t.Fatal("field corruption")
				}
				return
			}
			u, e := url.Parse(connection.DSN)
			if e != nil {
				t.Fatal("URL parse failed")
			}
			password, _ := u.User.Password()
			if password != input.Password || u.Hostname() != "::1" || u.User.Username() != input.Username {
				t.Fatal("field corruption")
			}
			if driver == "postgres" && (u.Path != "/"+input.Database || u.Query().Get("search_path") != input.Schema) {
				t.Fatal("PostgreSQL fields corrupted")
			}
			if driver == "sqlserver" && u.Query().Get("database") != input.Database {
				t.Fatal("SQL Server database corrupted")
			}
			if driver == "dm" && u.Query().Get("schema") != input.Schema {
				t.Fatal("DM schema corrupted")
			}
		})
	}
}
func TestConnectionFieldsRejectAmbiguousHostAndSchema(t *testing.T) {
	for _, input := range []ConnectionFields{{Driver: "postgres", Host: "db:5432", Username: "user", Database: "db"}, {Driver: "postgres", Host: "db", Username: "user", Database: "db", Port: 65536}, {Driver: "dm", Host: "db", Username: "user", Schema: "a,b"}, {Driver: "postgres", Host: "db", Username: "user", Database: "db", Schema: "1schema"}, {Driver: "mysql", Host: "db", Username: "user:password", Database: "db"}} {
		if _, e := BuildConnection(input); !errors.Is(e, ErrInvalidConnection) {
			t.Fatal("ambiguous fields accepted")
		}
	}
}
