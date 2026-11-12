package store

import (
	"strings"
	"testing"
)

func TestBindSQLLexicalBoundaries(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT ?, '?' , ?", "SELECT $1, '?' , $2"},
		{"SELECT 'it''s ?; --', ? /* ? /* ? */ ? */ -- ?\n, ?", "SELECT 'it''s ?; --', $1 /* ? /* ? */ ? */ -- ?\n, $2"},
		{`SELECT "?", "a""?", ` + "`?`" + `, ?`, `SELECT "?", "a""?", ` + "`?`" + `, $1`},
		{`SELECT $$?;$$, $body$? ' ;$body$, ?`, `SELECT $$?;$$, $body$? ' ;$body$, $1`},
		{`SELECT E'escaped\' ?;', ?`, `SELECT E'escaped\' ?;', $1`},
		{`SELECT '\', ?`, `SELECT '\', $1`},
		{"SELECT data ?? 'a', ?", "SELECT data ? 'a', $1"},
	}
	for _, c := range cases {
		got, err := Bind(PostgreSQL, c.in)
		if err != nil || got != c.want {
			t.Errorf("Bind(%q)=%q,%v want %q", c.in, got, err, c.want)
		}
	}
	for _, q := range []string{"SELECT 'bad", `SELECT "bad`, "SELECT /*bad", "SELECT $body$bad"} {
		if _, err := Bind(PostgreSQL, q); err == nil {
			t.Errorf("unterminated SQL accepted: %s", q)
		}
	}
	if got, err := Bind(DM, "SELECT ?, '?' FROM DUAL WHERE x=?"); err != nil || got != "SELECT :1, '?' FROM DUAL WHERE x=:2" {
		t.Fatalf("DM: %q %v", got, err)
	}
	for _, d := range []Dialect{SQLite, MySQL} {
		if got, err := Bind(d, "SELECT ??, '?' "); err != nil || got != "SELECT ??, '?' " {
			t.Fatalf("existing dialect changed")
		}
	}
	if _, err := Bind(Unknown, "SELECT 1"); err == nil {
		t.Fatal("unknown dialect accepted")
	}
}
func TestSplitSQLKeepsTriggerFunction(t *testing.T) {
	q := `CREATE FUNCTION f() RETURNS trigger AS $fn$ BEGIN RAISE EXCEPTION '?;'; END; $fn$ LANGUAGE plpgsql; SELECT '?;'; /* ; */ SELECT 1;`
	parts, err := splitSQLStatements(q)
	if err != nil || len(parts) != 3 || !strings.Contains(parts[0], "END;") {
		t.Fatalf("split=%v,%v", parts, err)
	}
}
func TestStructuredInsert(t *testing.T) {
	cols := []string{"id", "scope", "name", "state"}
	values := []string{"?", "?", "?", "'pending'"}
	conflict := []string{"id", "scope"}
	update := []string{"name", "state"}
	pg := InsertSQL(PostgreSQL, "sample", cols, values, conflict, update)
	if pg != "INSERT INTO sample (id, scope, name, state) VALUES (?, ?, ?, 'pending') ON CONFLICT(id, scope) DO UPDATE SET name = excluded.name, state = excluded.state" {
		t.Fatal(pg)
	}
	dm := InsertSQL(DM, "sample", cols, values, conflict, update)
	for _, part := range []string{"MERGE INTO sample target", "SELECT ? AS id, ? AS scope, ? AS name, 'pending' AS state FROM DUAL", "target.id=incoming.id AND target.scope=incoming.scope", "WHEN MATCHED THEN UPDATE SET target.name=incoming.name, target.state=incoming.state", "WHEN NOT MATCHED THEN INSERT (id, scope, name, state)"} {
		if !strings.Contains(dm, part) {
			t.Fatalf("missing %s: %s", part, dm)
		}
	}
	if strings.Contains(InsertSQL(DM, "sample", cols, values, conflict, nil), "WHEN MATCHED") {
		t.Fatal("do-nothing updates existing row")
	}
}
func TestPostgresMigrationSet(t *testing.T) {
	sq := migrationVersions(t, sqliteMigrationFS, "migrations/sqlite/*.sql")
	pg := migrationVersions(t, postgresMigrationFS, "migrations/postgres/*.sql")
	if len(sq) != len(pg) {
		t.Fatalf("migration versions differ: SQLite %d PG %d", len(sq), len(pg))
	}
	for v := range sq {
		if !pg[v] {
			t.Errorf("missing PG migration %s", v)
		}
	}
}

func TestPostgresParseErrorDoesNotEchoCredentials(t *testing.T) {
	const secret = "must-not-appear-in-errors"
	_, err := OpenWithConfig(OpenConfig{Driver: "postgres", DSN: "postgres://user:" + secret + "@%zz/db"})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatal("connection configuration error leaked input")
	}
}
