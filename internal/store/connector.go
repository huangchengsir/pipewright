package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
)

// bindingConnector preserves database/sql's DB, Conn and Tx boundary. It never
// retries or translates SQL syntax; Prepare and direct execution share Bind.
type bindingConnector struct {
	driver.Connector
	dialect Dialect
}
type bindingDriver struct {
	driver.Driver
	dialect Dialect
}
type bindingConn struct {
	driver.Conn
	dialect Dialect
}

func (c *bindingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &bindingConn{conn, c.dialect}, nil
}
func (c *bindingConnector) Driver() driver.Driver {
	return &bindingDriver{c.Connector.Driver(), c.dialect}
}
func (c *bindingConnector) Close() error {
	if closer, ok := c.Connector.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
func (d *bindingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &bindingConn{conn, d.dialect}, nil
}
func (c *bindingConn) Prepare(query string) (driver.Stmt, error) {
	bound, err := Bind(c.dialect, query)
	if err != nil {
		return nil, err
	}
	stmt, err := c.Conn.Prepare(bound)
	if err == nil && c.dialect == DM {
		return &dmStmt{Stmt: stmt, ctx: context.Background()}, nil
	}
	return stmt, err
}
func (c *bindingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	bound, err := Bind(c.dialect, query)
	if err != nil {
		return nil, err
	}
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := conn.PrepareContext(ctx, bound)
		if err == nil && c.dialect == DM {
			return &dmStmt{Stmt: stmt, ctx: ctx}, nil
		}
		return stmt, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stmt, err := c.Conn.Prepare(bound)
	if err == nil && ctx.Err() != nil {
		_ = stmt.Close()
		return nil, ctx.Err()
	}
	if err == nil && c.dialect == DM {
		return &dmStmt{Stmt: stmt, ctx: ctx}, nil
	}
	return stmt, err
}
func (c *bindingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	bound, err := Bind(c.dialect, query)
	if err != nil {
		return nil, err
	}
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, bound, args)
	}
	if conn, ok := c.Conn.(driver.Execer); ok {
		values, err := legacyValues(ctx, args)
		if err != nil {
			return nil, err
		}
		return conn.Exec(bound, values)
	}
	return nil, driver.ErrSkip
}
func (c *bindingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	bound, err := Bind(c.dialect, query)
	if err != nil {
		return nil, err
	}
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		rows, err := conn.QueryContext(ctx, bound, args)
		if c.dialect == DM {
			return adaptDMRows(ctx, rows, err)
		}
		return rows, err
	}
	if conn, ok := c.Conn.(driver.Queryer); ok {
		values, err := legacyValues(ctx, args)
		if err != nil {
			return nil, err
		}
		rows, err := conn.Query(bound, values)
		if c.dialect == DM {
			return adaptDMRows(ctx, rows, err)
		}
		return rows, err
	}
	return nil, driver.ErrSkip
}
func legacyValues(ctx context.Context, args []driver.NamedValue) ([]driver.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, errors.New("store: driver does not support named parameters")
		}
		out[i] = a.Value
	}
	return out, nil
}
func (c *bindingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err := conn.BeginTx(ctx, opts)
		if err == nil && c.dialect == DM {
			exec, ok := c.Conn.(driver.ExecerContext)
			if !ok {
				tx.Rollback()
				return nil, errors.New("store: DM transaction capability unavailable")
			}
			command := "SP_SET_SESSION_READONLY(0)"
			if opts.ReadOnly {
				command = "SP_SET_SESSION_READONLY(1)"
			}
			if _, err = exec.ExecContext(ctx, command, nil); err != nil {
				tx.Rollback()
				return nil, errors.New("store: DM transaction read-only setting failed")
			}
		}
		return tx, err
	}
	if opts.ReadOnly || opts.Isolation != 0 {
		return nil, errors.New("store: driver does not support transaction options")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := c.Conn.Begin()
	if err == nil && ctx.Err() != nil {
		_ = tx.Rollback()
		return nil, ctx.Err()
	}
	return tx, err
}
func (c *bindingConn) CheckNamedValue(v *driver.NamedValue) error {
	// Pipewright stores booleans as integers in all schema variants. Do not
	// normalize nil, byte slices, strings, Valuer errors or driver-specific types.
	if c.dialect == PostgreSQL || c.dialect == DM {
		if err := normalizeBooleanArgument(v); err != nil {
			return err
		}
	}

	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func (c *bindingConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return ctx.Err()
}
func (c *bindingConn) ResetSession(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.SessionResetter); ok {
		if err := conn.ResetSession(ctx); err != nil {
			return err
		}
	}
	if c.dialect == DM {
		exec, ok := c.Conn.(driver.ExecerContext)
		if !ok {
			return driver.ErrBadConn
		}
		if _, err := exec.ExecContext(ctx, "SP_SET_SESSION_READONLY(0)", nil); err != nil {
			return driver.ErrBadConn
		}
	}
	return ctx.Err()
}
func (c *bindingConn) IsValid() bool {
	if conn, ok := c.Conn.(driver.Validator); ok {
		return conn.IsValid()
	}
	return true
}

func normalizeBooleanArgument(v *driver.NamedValue) error {
	if _, ok := v.Value.(driver.Valuer); ok {
		converted, err := driver.DefaultParameterConverter.ConvertValue(v.Value)
		if err != nil {
			return err
		}
		v.Value = converted
	}
	if ptr, ok := v.Value.(*bool); ok {
		if ptr == nil {
			v.Value = nil
		} else {
			v.Value = *ptr
		}
	}
	if b, ok := v.Value.(bool); ok {
		if b {
			v.Value = int64(1)
		} else {
			v.Value = int64(0)
		}
	}
	return nil
}
