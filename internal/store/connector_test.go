package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type recordedCall struct {
	query string
	args  []driver.NamedValue
}
type recordingConnector struct{ conn *recordingConn }

func (c recordingConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c recordingConnector) Driver() driver.Driver                        { return recordingDriver{c.conn} }

type recordingDriver struct{ conn *recordingConn }

func (d recordingDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

type recordingConn struct {
	calls                             []recordedCall
	opts                              driver.TxOptions
	resets, pings, commits, rollbacks int
	skip                              bool
	invalid                           bool
}

func (c *recordingConn) Close() error                          { return nil }
func (c *recordingConn) Prepare(q string) (driver.Stmt, error) { return &recordingStmt{c, q}, nil }
func (c *recordingConn) Begin() (driver.Tx, error)             { return recordingTx{c}, nil }
func (c *recordingConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.opts = o
	return recordingTx{c}, nil
}
func (c *recordingConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.Prepare(q)
}
func (c *recordingConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.skip {
		return nil, driver.ErrSkip
	}
	c.calls = append(c.calls, recordedCall{q, append([]driver.NamedValue{}, a...)})
	return driver.RowsAffected(1), nil
}
func (c *recordingConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.calls = append(c.calls, recordedCall{q, a})
	return &recordingRows{}, nil
}
func (c *recordingConn) CheckNamedValue(v *driver.NamedValue) error { return driver.ErrSkip }
func (c *recordingConn) ResetSession(ctx context.Context) error     { c.resets++; return ctx.Err() }
func (c *recordingConn) Ping(ctx context.Context) error             { c.pings++; return ctx.Err() }
func (c *recordingConn) IsValid() bool                              { return !c.invalid }

type recordingStmt struct {
	c *recordingConn
	q string
}

func (s *recordingStmt) Close() error  { return nil }
func (s *recordingStmt) NumInput() int { return -1 }
func (s *recordingStmt) Exec(args []driver.Value) (driver.Result, error) {
	nv := make([]driver.NamedValue, len(args))
	for i, v := range args {
		nv[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	s.c.calls = append(s.c.calls, recordedCall{s.q, nv})
	return driver.RowsAffected(1), nil
}
func (s *recordingStmt) Query([]driver.Value) (driver.Rows, error) {
	s.c.calls = append(s.c.calls, recordedCall{query: s.q})
	return &recordingRows{}, nil
}

type recordingRows struct{ done bool }

func (*recordingRows) Columns() []string { return []string{"value"} }
func (*recordingRows) Close() error      { return nil }
func (r *recordingRows) Next(v []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	v[0] = int64(7)
	return nil
}

type recordingTx struct{ c *recordingConn }

func (t recordingTx) Commit() error   { t.c.commits++; return nil }
func (t recordingTx) Rollback() error { t.c.rollbacks++; return nil }

type failedValuer struct{ err error }

func (v failedValuer) Value() (driver.Value, error) { return nil, v.err }

func TestConnectorAllDatabaseSQLPaths(t *testing.T) {
	raw := &recordingConn{}
	db := sql.OpenDB(&bindingConnector{recordingConnector{raw}, PostgreSQL})
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if DialectOf(db) != PostgreSQL {
		t.Fatal("dialect identity lost")
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO sample VALUES(?, ?, ?, ?)", true, sql.NullBool{Bool: false, Valid: true}, nil, []byte{0, 255}); err != nil {
		t.Fatal(err)
	}
	call := raw.calls[len(raw.calls)-1]
	if call.query != "INSERT INTO sample VALUES($1, $2, $3, $4)" || call.args[0].Value != int64(1) || call.args[1].Value != int64(0) || call.args[2].Value != nil || !reflect.DeepEqual(call.args[3].Value, []byte{0, 255}) {
		t.Fatalf("binding or value changed: %#v", call)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT ?", 7).Scan(&n); err != nil || n != 7 {
		t.Fatalf("query row: %d %v", n, err)
	}
	stmt, err := db.PrepareContext(ctx, "INSERT INTO sample VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.ExecContext(ctx, true); err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	if got := raw.calls[len(raw.calls)-1]; got.query != "INSERT INTO sample VALUES ($1)" || got.args[0].Value != int64(1) {
		t.Fatalf("prepare: %#v", got)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, "UPDATE sample SET value=?", false); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if raw.opts.Isolation != driver.IsolationLevel(sql.LevelSerializable) || !raw.opts.ReadOnly {
		t.Fatal("transaction options lost")
	}
	if err = tx.QueryRowContext(ctx, "SELECT ?", 7).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if raw.commits != 1 || raw.pings != 1 || raw.resets == 0 {
		t.Fatal("driver lifecycle lost")
	}
	expected := errors.New("valuer failure")
	if _, err = db.ExecContext(ctx, "SELECT ?", failedValuer{expected}); !errors.Is(err, expected) {
		t.Fatalf("valuer error not preserved: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	before := len(raw.calls)
	if _, err = db.ExecContext(canceled, "UPDATE sample SET value=?", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if len(raw.calls) != before {
		t.Fatal("canceled write executed")
	}
}
func TestConnectorErrSkipPrepareFallback(t *testing.T) {
	raw := &recordingConn{skip: true}
	db := sql.OpenDB(&bindingConnector{recordingConnector{raw}, PostgreSQL})
	defer db.Close()
	if _, err := db.Exec("UPDATE sample SET value=?", int32(3)); err != nil {
		t.Fatal(err)
	}
	if len(raw.calls) != 1 || raw.calls[0].query != "UPDATE sample SET value=$1" || raw.calls[0].args[0].Value != int64(3) {
		t.Fatalf("fallback changed behavior: %#v", raw.calls)
	}
}
func TestConnectorUnknownDriverDoesNotBecomeSQLite(t *testing.T) {
	db := sql.OpenDB(recordingConnector{&recordingConn{}})
	defer db.Close()
	if DialectOf(db) != Unknown {
		t.Fatal("unknown driver treated as SQLite")
	}
}
func TestConnectorDoesNotRewriteSQL(t *testing.T) {
	raw := &recordingConn{}
	db := sql.OpenDB(&bindingConnector{recordingConnector{raw}, PostgreSQL})
	defer db.Close()
	q := "SELECT '?' AS label /* LIMIT ?; */ WHERE value = ?"
	if _, err := db.Exec(q, 1); err != nil {
		t.Fatal(err)
	}
	if got := raw.calls[0].query; got != strings.Replace(q, "value = ?", "value = $1", 1) {
		t.Fatalf("rewrote SQL: %s", got)
	}
}
