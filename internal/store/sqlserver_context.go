package store

import (
	"context"
	"database/sql/driver"
	"net"
	"sync"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// ConnectProbeContext preserves ConnectContext's capability checks. SQL Server
// and DM additionally bind transport I/O to the caller's entire probe lifetime: its
// driver otherwise permits prelogin reads and cancellation drains past ctx.
// The caller must close the returned Store and cancel its bounded context.
func ConnectProbeContext(ctx context.Context, cfg OpenConfig) (*Store, error) {
	if cfg.Driver != "sqlserver" && cfg.Driver != "dm" {
		return ConnectContext(ctx, cfg)
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, context.DeadlineExceeded
	}
	if cfg.Driver == "dm" {
		return openDMWithLifetime(ctx, cfg.DSN, ctx)
	}
	return openSQLServerWithLifetime(ctx, cfg.DSN, ctx)
}

type sqlServerContextConnector struct {
	*mssql.Connector
	lifetime  context.Context
	keepAlive time.Duration
}

func (c *sqlServerContextConnector) Connect(ctx context.Context) (driver.Conn, error) {
	// A connector may be used concurrently by the pool; never mutate its Dialer.
	connector := *c.Connector
	lifetime := ctx
	if c.lifetime != nil {
		lifetime = c.lifetime
	}
	dialer := c.contextDialer(lifetime)
	connector.Dialer = dialer
	conn, err := connector.Connect(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = lifetime.Err()
	}
	dialer.finish(err == nil, c.lifetime != nil)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if actual, ok := conn.(*mssql.Conn); ok && actual != nil {
			actual.Close()
		}
		return nil, err
	}
	return conn, nil
}

type sqlServerContextDialer struct {
	lifetime    context.Context
	keepAlive   time.Duration
	mu          sync.Mutex
	connections []*sqlServerContextSocket
}

func (d *sqlServerContextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	raw, err := d.networkDialer().DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	socket := newSQLServerContextSocket(d.lifetime, raw)
	d.mu.Lock()
	d.connections = append(d.connections, socket)
	d.mu.Unlock()
	return socket, nil
}
func (d *sqlServerContextDialer) finish(success, keepLifetime bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, socket := range d.connections {
		if !success {
			socket.Close()
		} else if !keepLifetime {
			socket.release()
		}
	}
}

// Driver socket-timeout settings may shorten, but never extend, this deadline.
// release is used only after ordinary handshakes; probes retain the guard until
// the pool closes the socket, including while query cancellation is draining.
type sqlServerContextSocket struct {
	net.Conn
	mu       sync.Mutex
	deadline time.Time
	active   bool
	stop     func() bool
}

func newSQLServerContextSocket(ctx context.Context, raw net.Conn) *sqlServerContextSocket {
	deadline, _ := ctx.Deadline()
	socket := &sqlServerContextSocket{Conn: raw, deadline: deadline, active: true}
	if !deadline.IsZero() {
		_ = raw.SetDeadline(deadline)
	}
	socket.stop = context.AfterFunc(ctx, func() { _ = raw.Close() })
	return socket
}
func (c *sqlServerContextSocket) limit(t time.Time) time.Time {
	if c.active && !c.deadline.IsZero() && (t.IsZero() || c.deadline.Before(t)) {
		return c.deadline
	}
	return t
}
func (c *sqlServerContextSocket) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetDeadline(c.limit(t))
}
func (c *sqlServerContextSocket) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetReadDeadline(c.limit(t))
}
func (c *sqlServerContextSocket) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetWriteDeadline(c.limit(t))
}
func (c *sqlServerContextSocket) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stop()
	c.active = false
	_ = c.Conn.SetDeadline(time.Time{})
}
func (c *sqlServerContextSocket) Close() error {
	c.stop()
	return c.Conn.Close()
}

func newSQLServerContextConnector(dsn string, lifetime context.Context) (*sqlServerContextConnector, error) {
	config, err := msdsn.Parse(dsn)
	if err != nil {
		return nil, err
	}
	connector := mssql.NewConnectorConfig(config)
	// Preserve the ordinary connector's required session settings and TCP policy.
	connector.SessionInitSQL = "SET ANSI_NULLS ON; SET QUOTED_IDENTIFIER ON; SET ANSI_PADDING ON; SET ANSI_WARNINGS ON; SET CONCAT_NULL_YIELDS_NULL ON; SET ARITHABORT ON; SET NUMERIC_ROUNDABORT OFF;"
	return &sqlServerContextConnector{Connector: connector, lifetime: lifetime, keepAlive: config.KeepAlive}, nil
}
func (c *sqlServerContextConnector) contextDialer(ctx context.Context) *sqlServerContextDialer {
	return &sqlServerContextDialer{lifetime: ctx, keepAlive: c.keepAlive}
}
func (d *sqlServerContextDialer) networkDialer() *net.Dialer {
	keepAlive := d.keepAlive
	// Match go-mssqldb's createDialer: zero uses 30s; a negative value disables it.
	if keepAlive == 0 {
		keepAlive = 30 * time.Second
	}
	return &net.Dialer{KeepAlive: keepAlive}
}
