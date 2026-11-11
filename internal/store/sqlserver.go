package store

import (
	"context"
	"database/sql"
	"errors"
	mssql "github.com/microsoft/go-mssqldb"
	"strconv"
	"strings"
	"time"
)

func openSQLServer(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("store: SQL Server DSN is required")
	}
	connector, err := mssql.NewConnector(dsn)
	if err != nil {
		return nil, errors.New("store: invalid SQL Server connection configuration")
	}
	// Required for filtered indexes and deterministic expression semantics.
	connector.SessionInitSQL = "SET ANSI_NULLS ON; SET QUOTED_IDENTIFIER ON; SET ANSI_PADDING ON; SET ANSI_WARNINGS ON; SET CONCAT_NULL_YIELDS_NULL ON; SET ARITHABORT ON; SET NUMERIC_ROUNDABORT OFF;"
	db := sql.OpenDB(&bindingConnector{Connector: connector, dialect: SQLServer})
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	var version string
	if db.QueryRowContext(ctx, "SELECT CAST(SERVERPROPERTY('ProductMajorVersion') AS VARCHAR(10))").Scan(&version) != nil {
		db.Close()
		return nil, errors.New("store: SQL Server connection or version check failed")
	}
	n, err := strconv.Atoi(version)
	if err != nil || n < 15 || n > 17 {
		db.Close()
		return nil, errors.New("store: SQL Server 2019, 2022 or 2025 is required")
	}
	return &Store{DB: db, Dialect: SQLServer}, nil
}

// Embedded SQL Server files use a standalone GO as an explicit batch delimiter.
// Trigger bodies remain whole; no business SQL is parsed or rewritten here.
func splitSQLServerBatches(source string) []string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	var out []string
	for _, batch := range strings.Split(source, "\nGO\n") {
		if batch = strings.TrimSpace(batch); batch != "" {
			out = append(out, batch)
		}
	}
	return out
}
func (s *Store) lockSQLServerMigrations() (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return nil, errors.New("store: SQL Server migration lock unavailable")
	}
	var schema string
	if err = conn.QueryRowContext(ctx, "SELECT SCHEMA_NAME()").Scan(&schema); err != nil {
		conn.Close()
		return nil, errors.New("store: SQL Server schema unavailable")
	}
	resource := "pipewright:migrations:" + schema
	var result int
	err = conn.QueryRowContext(ctx, "DECLARE @result int; EXEC @result=sys.sp_getapplock @Resource=?, @LockMode='Exclusive', @LockOwner='Session', @LockTimeout=30000; SELECT @result", resource).Scan(&result)
	if err != nil || result < 0 {
		conn.Close()
		return nil, errors.New("store: SQL Server migration lock unavailable")
	}
	return func() {
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "EXEC sys.sp_releaseapplock @Resource=?, @LockOwner='Session'", resource)
	}, nil
}
