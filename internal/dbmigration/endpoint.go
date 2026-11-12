package dbmigration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huangchengsir/pipewright/internal/store"
)

type Endpoint struct {
	Dialect  string `json:"dialect"`
	Server   string `json:"server,omitempty"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	FilePath string `json:"filePath,omitempty"`
	FileID   string `json:"fileId,omitempty"`
	Identity string `json:"identity,omitempty"`
}

// Describe reads actual endpoint metadata without creating identity/schema rows.
// sqlitePath is the resolved application database filename, never a remote DSN.
func Describe(ctx context.Context, s *store.Store, sqlitePath string) (Endpoint, error) {
	out := Endpoint{Dialect: s.Dialect.String()}
	switch s.Dialect {
	case store.SQLite:
		if strings.Contains(sqlitePath, "?") || strings.HasPrefix(sqlitePath, "file:") || sqlitePath == ":memory:" {
			return out, ErrUnsupported
		}
		absolute, err := filepath.Abs(sqlitePath)
		if err != nil {
			return out, ErrIdentity
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return out, ErrIdentity
		}
		f, err := os.Open(resolved)
		if err != nil {
			return out, ErrIdentity
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return out, ErrIdentity
		}
		out.FilePath = filepath.Clean(resolved)
		out.FileID, err = fileIdentity(f)
		if err != nil {
			return out, ErrIdentity
		}
	case store.MySQL:
		if err := s.DB.QueryRowContext(ctx, "SELECT @@server_uuid,DATABASE()").Scan(&out.Server, &out.Database); err != nil {
			return out, ErrIdentity
		}
	case store.DM:
		if s.DB.QueryRowContext(ctx, "SELECT INSTANCE_NAME||'@'||HOST_NAME FROM V$INSTANCE").Scan(&out.Server) != nil || s.DB.QueryRowContext(ctx, "SELECT NAME FROM V$DATABASE").Scan(&out.Database) != nil || s.DB.QueryRowContext(ctx, "SELECT SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID()) FROM DUAL").Scan(&out.Schema) != nil {
			return out, ErrIdentity
		}
	case store.SQLServer:
		if err := s.DB.QueryRowContext(ctx, "SELECT CAST(SERVERPROPERTY('ServerName') AS NVARCHAR(256)),DB_NAME(),SCHEMA_NAME()").Scan(&out.Server, &out.Database, &out.Schema); err != nil {
			return out, ErrIdentity
		}
	case store.PostgreSQL:
		var host sql.NullString
		var port sql.NullInt64
		if err := s.DB.QueryRowContext(ctx, "SELECT current_database(),current_schema(),inet_server_addr()::text,inet_server_port()").Scan(&out.Database, &out.Schema, &host, &port); err != nil {
			return out, ErrIdentity
		}
		out.Server = fmt.Sprintf("%s:%d", host.String, port.Int64)
	default:
		return out, ErrUnsupported
	}
	// A missing identity is expected for a genuinely empty target. Callers also
	// require CheckEmpty; missing identity alone never authorizes initialization.
	if id, err := s.Identity(ctx); err == nil {
		out.Identity = id
	}
	return out, nil
}
func SameEndpoint(a, b Endpoint) bool {
	if a.Identity != "" && a.Identity == b.Identity {
		return true
	}
	if a.Dialect != b.Dialect {
		return false
	}
	if a.Dialect == "sqlite" {
		return a.FileID != "" && a.FileID == b.FileID || a.FilePath != "" && a.FilePath == b.FilePath
	}
	return a.Server != "" && a.Server == b.Server && a.Database == b.Database && a.Schema == b.Schema
}

func CheckEmpty(ctx context.Context, s *store.Store) error {
	var query string
	switch s.Dialect {
	case store.SQLite:
		query = "SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'"
	case store.MySQL:
		query = "SELECT (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE())+(SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema=DATABASE())+(SELECT COUNT(*) FROM information_schema.events WHERE event_schema=DATABASE())"
	case store.DM:
		query = "SELECT COUNT(*) FROM ALL_OBJECTS WHERE OWNER=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())"
	case store.SQLServer:
		query = "SELECT COUNT(*) FROM sys.objects WHERE schema_id=SCHEMA_ID() AND is_ms_shipped=0"
	case store.PostgreSQL:
		query = "SELECT (SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema())+(SELECT COUNT(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema())"
	default:
		return ErrUnsupported
	}
	var count int64
	if err := s.DB.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return ErrUnsupported
	}
	if count != 0 {
		return ErrTargetNotEmpty
	}
	return nil
}
