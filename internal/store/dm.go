package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"hash/fnv"
	"io"
	"reflect"
	"strings"
	"time"
)

// These hooks are supplied only by the user's local vendor overlay. The public
// module never imports or redistributes the proprietary dm module.
var dmConnectorFactory func(string) (driver.Connector, error)
var dmDecodeValue func(context.Context, driver.Value) (driver.Value, error)
var dmErrorNumber func(error) (int32, bool)
var dmDriverSelfTest func() error

const maxDMLobBytes = 64 << 20

func openDM(ctx context.Context, dsn string) (*Store, error) {
	if dmConnectorFactory == nil {
		return nil, ErrDriverUnavailable
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("store: DM DSN is required")
	}
	connector, err := dmConnectorFactory(dsn)
	if err != nil {
		return nil, errors.New("store: invalid DM connection configuration")
	}
	db := sql.OpenDB(&bindingConnector{Connector: connector, dialect: DM})
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	success := false
	defer func() {
		if !success {
			db.Close()
		}
	}()
	var mode, emptyIsNull, caseInsensitive int
	if db.QueryRowContext(ctx, "SELECT SF_GET_PARA_VALUE(2,'COMPATIBLE_MODE') FROM DUAL").Scan(&mode) != nil || mode != 0 {
		return nil, errors.New("store: DM8 standard mode COMPATIBLE_MODE=0 is required")
	}
	if db.QueryRowContext(ctx, "SELECT CASE WHEN CAST('' AS VARCHAR(10)) IS NULL THEN 1 ELSE 0 END, CASE WHEN 'a'='A' THEN 1 ELSE 0 END FROM DUAL").Scan(&emptyIsNull, &caseInsensitive) != nil || emptyIsNull != 0 || caseInsensitive != 0 {
		return nil, errors.New("store: DM must preserve empty text and case-sensitive identifiers")
	}
	var text string
	if db.QueryRowContext(ctx, "SELECT CAST(? AS VARCHAR(128)) FROM DUAL", "Pipewright 中文🙂").Scan(&text) != nil || text != "Pipewright 中文🙂" {
		return nil, errors.New("store: DM Unicode capability check failed")
	}
	rows, err := db.QueryContext(ctx, "SELECT BANNER FROM V$VERSION")
	if err != nil {
		return nil, errors.New("store: DM version check unavailable")
	}
	version8 := false
	for rows.Next() {
		var banner string
		if rows.Scan(&banner) != nil {
			rows.Close()
			return nil, errors.New("store: DM version check failed")
		}
		if strings.Contains(banner, "V8") {
			version8 = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !version8 {
		return nil, errors.New("store: DM8 is required")
	}
	success = true
	return &Store{DB: db, Dialect: DM}, nil
}

// dmRows adapts vendor LOB values to legal database/sql values while preserving
// result sets and metadata. Conversion is bounded and never normalizes NULL.
type dmRows struct {
	driver.Rows
	ctx context.Context
}

func adaptDMRows(ctx context.Context, rows driver.Rows, err error) (driver.Rows, error) {
	if err != nil || rows == nil {
		return rows, err
	}
	return &dmRows{Rows: rows, ctx: ctx}, nil
}
func (r *dmRows) Next(values []driver.Value) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if err := r.Rows.Next(values); err != nil {
		return err
	}
	if dmDecodeValue == nil {
		return ErrDriverUnavailable
	}
	for i, value := range values {
		decoded, err := dmDecodeValue(r.ctx, value)
		if err != nil {
			return err
		}
		values[i] = decoded
	}
	return nil
}
func (r *dmRows) HasNextResultSet() bool {
	if v, ok := r.Rows.(driver.RowsNextResultSet); ok {
		return v.HasNextResultSet()
	}
	return false
}
func (r *dmRows) NextResultSet() error {
	if v, ok := r.Rows.(driver.RowsNextResultSet); ok {
		return v.NextResultSet()
	}
	return io.EOF
}
func (r *dmRows) ColumnTypeDatabaseTypeName(i int) string {
	if v, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return v.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}
func (r *dmRows) ColumnTypeLength(i int) (int64, bool) {
	if v, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return v.ColumnTypeLength(i)
	}
	return 0, false
}
func (r *dmRows) ColumnTypeNullable(i int) (bool, bool) {
	if v, ok := r.Rows.(driver.RowsColumnTypeNullable); ok {
		return v.ColumnTypeNullable(i)
	}
	return false, false
}
func (r *dmRows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if v, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return v.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}
func (r *dmRows) ColumnTypeScanType(i int) reflect.Type {
	switch strings.ToUpper(r.ColumnTypeDatabaseTypeName(i)) {
	case "BLOB", "IMAGE":
		return reflect.TypeOf([]byte{})
	case "CLOB", "TEXT":
		return reflect.TypeOf("")
	}
	if v, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return v.ColumnTypeScanType(i)
	}
	return reflect.TypeOf((*any)(nil)).Elem()
}

type dmStmt struct {
	driver.Stmt
	ctx context.Context
}

func (s *dmStmt) Query(args []driver.Value) (driver.Rows, error) {
	r, e := s.Stmt.Query(args)
	return adaptDMRows(s.ctx, r, e)
}
func (s *dmStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if v, ok := s.Stmt.(driver.StmtQueryContext); ok {
		r, e := v.QueryContext(ctx, args)
		return adaptDMRows(ctx, r, e)
	}
	values, e := legacyValues(ctx, args)
	if e != nil {
		return nil, e
	}
	r, e := s.Stmt.Query(values)
	return adaptDMRows(ctx, r, e)
}
func (s *dmStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if v, ok := s.Stmt.(driver.StmtExecContext); ok {
		return v.ExecContext(ctx, args)
	}
	values, e := legacyValues(ctx, args)
	if e != nil {
		return nil, e
	}
	return s.Stmt.Exec(values)
}
func (s *dmStmt) CheckNamedValue(v *driver.NamedValue) error {
	if err := normalizeBooleanArgument(v); err != nil {
		return err
	}
	if c, ok := s.Stmt.(driver.NamedValueChecker); ok {
		return c.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func (s *dmStmt) ColumnConverter(i int) driver.ValueConverter {
	if c, ok := s.Stmt.(driver.ColumnConverter); ok {
		return c.ColumnConverter(i)
	}
	return driver.DefaultParameterConverter
}

func (s *Store) lockDMMigrations() (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return nil, errors.New("store: DM migration lock unavailable")
	}
	var schema string
	if err = conn.QueryRowContext(ctx, "SELECT SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID()) FROM DUAL").Scan(&schema); err != nil {
		conn.Close()
		return nil, errors.New("store: DM schema identity unavailable")
	}
	h := fnv.New32a()
	h.Write([]byte("pipewright:migrations:" + schema))
	id := int64(h.Sum32() & 0x3fffffff)
	var result int
	if err = conn.QueryRowContext(ctx, "SELECT DBMS_LOCK.REQUEST(?,6,30,FALSE) FROM DUAL", id).Scan(&result); err != nil || result != 0 {
		conn.Close()
		return nil, errors.New("store: DM migration lock unavailable; DBMS_LOCK must already be enabled and accessible")
	}
	return func() {
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "SELECT DBMS_LOCK.RELEASE(?) FROM DUAL", id)
	}, nil
}
