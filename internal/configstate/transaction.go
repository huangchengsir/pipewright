// Package configstate provides the transaction and concurrency boundary shared by
// configuration writers and execution admission. It performs no external I/O.
package configstate

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
)

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var ErrReadOnly = errors.New("configstate: read-only transaction")

type transaction struct {
	DBTX
	db       *sql.DB
	readOnly bool
	mu       sync.Mutex
	hooks    []func()
	finished bool
}
type contextKey struct{}
type binding struct {
	db *sql.DB
	q  DBTX
}

// Queryer returns the caller's transaction only for this exact database.
func Queryer(ctx context.Context, db *sql.DB) DBTX {
	if b, ok := ctx.Value(contextKey{}).(binding); ok && b.db == db {
		return b.q
	}
	return db
}

func WithTxContext(ctx context.Context, db *sql.DB, q DBTX) context.Context {
	return context.WithValue(ctx, contextKey{}, binding{db: db, q: q})
}

// AfterCommit registers a callback only on a managed transaction. A false return
// lets an adapter perform its ordinary nontransactional behavior instead.
func AfterCommit(ctx context.Context, fn func()) bool {
	b, ok := ctx.Value(contextKey{}).(binding)
	if !ok || fn == nil {
		return false
	}
	t, ok := b.q.(*transaction)
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return false
	}
	t.hooks = append(t.hooks, fn)
	return true
}

func WithWriteTx(ctx context.Context, db *sql.DB, fn func(DBTX) error) error {
	return withTx(ctx, db, false, fn)
}
func WithReadTx(ctx context.Context, db *sql.DB, fn func(DBTX) error) error {
	return withTx(ctx, db, true, fn)
}
func WithWriteTxContext(ctx context.Context, db *sql.DB, fn func(context.Context, DBTX) error) error {
	return WithWriteTx(ctx, db, func(q DBTX) error { return fn(WithTxContext(ctx, db, q), q) })
}
func WithReadTxContext(ctx context.Context, db *sql.DB, fn func(context.Context, DBTX) error) error {
	return WithReadTx(ctx, db, func(q DBTX) error { return fn(WithTxContext(ctx, db, q), q) })
}

func withTx(ctx context.Context, db *sql.DB, readOnly bool, fn func(DBTX) error) error {
	if b, ok := ctx.Value(contextKey{}).(binding); ok && b.db == db {
		if t, ok := b.q.(*transaction); ok && t.readOnly && !readOnly {
			return ErrReadOnly
		}
		return fn(b.q)
	}
	var q DBTX
	var commit func() error
	var rollback func()
	var closeConn func()
	if store.DialectOf(db) == store.MySQL {
		opts := &sql.TxOptions{ReadOnly: readOnly, Isolation: sql.LevelReadCommitted}
		if readOnly {
			opts.Isolation = sql.LevelRepeatableRead
		}
		tx, err := db.BeginTx(ctx, opts)
		if err != nil {
			return err
		}
		q, commit = tx, tx.Commit
		rollback = func() { _ = tx.Rollback() }
		closeConn = func() {}
	} else {
		conn, err := db.Conn(ctx)
		if err != nil {
			return err
		}
		begin := "BEGIN IMMEDIATE"
		if readOnly {
			begin = "BEGIN"
		}
		startCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err = conn.ExecContext(startCtx, begin)
		cancel()
		if err != nil {
			_ = conn.Close()
			return err
		}
		q = conn
		commit = func() error { _, err := conn.ExecContext(ctx, "COMMIT"); return err }
		rollback = func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_, _ = conn.ExecContext(cleanup, "ROLLBACK")
		}
		closeConn = func() { _ = conn.Close() }
	}
	t := &transaction{DBTX: q, db: db, readOnly: readOnly}
	closed := false
	defer func() {
		if !closed {
			rollback()
			closeConn()
		}
		t.mu.Lock()
		t.finished = true
		t.mu.Unlock()
	}()
	if err := fn(t); err != nil {
		return err
	}
	if err := commit(); err != nil {
		return err
	}
	closeConn()
	closed = true
	t.mu.Lock()
	t.finished = true
	hooks := append([]func(){}, t.hooks...)
	t.mu.Unlock()
	for _, hook := range hooks {
		hook()
	}
	return nil
}
