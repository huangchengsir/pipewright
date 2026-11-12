package dbmigration

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/huangchengsir/pipewright/internal/store"
)

// ConnectionFields is a write-only form. No response, log, audit or journal
// status serializes it; only the resulting Connection goes into sealed storage.
type ConnectionFields struct {
	Driver   string `json:"driver"`
	DSN      string `json:"dsn,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	File     string `json:"file,omitempty"`
}

func BuildConnection(in ConnectionFields) (Connection, error) {
	switch in.Driver {
	case "sqlite", "mysql", "postgres", "sqlserver", "dm":
	default:
		return Connection{}, ErrUnsupported
	}
	if in.DSN != "" {
		return Connection{Driver: in.Driver, DSN: in.DSN}, nil
	}
	if in.Driver == "sqlite" {
		if strings.TrimSpace(in.File) == "" {
			return Connection{}, ErrInvalidConnection
		}
		return Connection{Driver: "sqlite", DSN: in.File}, nil
	}
	host := strings.TrimSpace(in.Host)
	if host == "" || in.Username == "" || strings.ContainsAny(host, "/@?#\r\n\t ") {
		return Connection{}, ErrInvalidConnection
	}
	if in.Port == 0 {
		in.Port = map[string]int{"mysql": 3306, "postgres": 5432, "sqlserver": 1433, "dm": 5236}[in.Driver]
	}
	if in.Port < 1 || in.Port > 65535 {
		return Connection{}, ErrInvalidConnection
	}
	plainHost := strings.Trim(host, "[]")
	if strings.Contains(plainHost, ":") && net.ParseIP(plainHost) == nil {
		return Connection{}, ErrInvalidConnection
	}
	if in.Schema != "" && (in.Driver == "postgres" || in.Driver == "dm") {
		for i, r := range in.Schema {
			if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
				return Connection{}, ErrInvalidConnection
			}
		}
	}
	address := net.JoinHostPort(plainHost, strconv.Itoa(in.Port))
	if in.Driver == "mysql" {
		if in.Database == "" || strings.Contains(in.Username, ":") {
			return Connection{}, ErrInvalidConnection
		}
		cfg := mysql.NewConfig()
		cfg.User = in.Username
		cfg.Passwd = in.Password
		cfg.Net = "tcp"
		cfg.Addr = address
		cfg.DBName = in.Database
		return Connection{Driver: in.Driver, DSN: cfg.FormatDSN()}, nil
	}
	u := url.URL{Scheme: in.Driver, User: url.UserPassword(in.Username, in.Password), Host: address}
	query := url.Values{}
	switch in.Driver {
	case "postgres":
		if in.Database == "" {
			return Connection{}, ErrInvalidConnection
		}
		u.Path = "/" + in.Database
		if in.Schema != "" {
			query.Set("search_path", in.Schema)
		}
	case "sqlserver":
		if in.Database == "" {
			return Connection{}, ErrInvalidConnection
		}
		query.Set("database", in.Database)
	case "dm":
		if in.Schema != "" {
			query.Set("schema", in.Schema)
		}
	}
	u.RawQuery = query.Encode()
	return Connection{Driver: in.Driver, DSN: u.String()}, nil
}

type DatabaseInfo struct {
	Driver   string `json:"driver"`
	Version  string `json:"version"`
	Server   string `json:"server,omitempty"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	File     string `json:"file,omitempty"`
	Identity string `json:"identity,omitempty"`
}

func (r *Runtime) queryInfo(ctx context.Context) (DatabaseInfo, error) {
	if r.Source == nil {
		return DatabaseInfo{}, ErrIdentity
	}
	connection := r.Current.Connection
	if r.Recovery {
		s, e := r.Control.State()
		if e != nil || s == nil {
			return DatabaseInfo{}, ErrIdentity
		}
		connection = s.Source.Connection
	}
	endpoint, e := Describe(ctx, r.Source, connection.DSN)
	if e != nil {
		return DatabaseInfo{}, e
	}
	info := DatabaseInfo{Driver: endpoint.Dialect, Server: endpoint.Server, Database: endpoint.Database, Schema: endpoint.Schema, File: endpoint.FilePath, Identity: endpoint.Identity}
	var query string
	switch r.Source.Dialect {
	case store.SQLite:
		query = "SELECT sqlite_version()"
	case store.MySQL:
		query = "SELECT VERSION()"
	case store.PostgreSQL:
		query = "SHOW server_version"
	case store.SQLServer:
		query = "SELECT CAST(SERVERPROPERTY('ProductVersion') AS VARCHAR(64))"
	case store.DM:
		query = "SELECT BANNER FROM V$VERSION WHERE BANNER LIKE 'DM Database Server%'"
	}
	if r.Source.DB.QueryRowContext(ctx, query).Scan(&info.Version) != nil {
		info.Version = ""
	}
	if len(info.Version) > 128 {
		info.Version = info.Version[:128]
	}
	return info, nil
}

// Cached before copying begins, so status never contends with a SQLite snapshot
// connection and can display durable per-table progress throughout the transfer.
func (r *Runtime) Info(context.Context) (DatabaseInfo, error) {
	if r.info.Driver == "" {
		return DatabaseInfo{}, ErrIdentity
	}
	return r.info, nil
}

var ErrInvalidConnection = errors.New("dbmigration: invalid connection fields")
var ErrConnection = errors.New("dbmigration: connection failed")
