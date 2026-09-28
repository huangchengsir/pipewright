package target

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

type transferTimeoutKey struct{}
type transferTimeouts struct{ connect, idle time.Duration }

// WithTransferTimeouts separates connection establishment from transfer inactivity.
// Idle progress is measured at successful network writes, never local file reads.
func WithTransferTimeouts(ctx context.Context, connect, idle time.Duration) context.Context {
	return context.WithValue(ctx, transferTimeoutKey{}, transferTimeouts{connect, idle})
}

type transferConn struct {
	net.Conn
	idle atomic.Int64
}

func (c *transferConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if idle := time.Duration(c.idle.Load()); n > 0 && idle > 0 {
		_ = c.Conn.SetDeadline(time.Now().Add(idle))
	}
	return n, err
}

// The raw socket is closed on cancellation even during handshake or channel open.
func dialSSH(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, func(), error) {
	conn, err := (&net.Dialer{Timeout: cfg.Timeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, func() {}, classifyDialErr(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	cleanup := func() { stop(); _ = conn.Close() }
	_ = conn.SetDeadline(time.Now().Add(cfg.Timeout))
	tc := &transferConn{Conn: conn}
	sc, chans, reqs, err := ssh.NewClientConn(tc, addr, cfg)
	if err != nil {
		cleanup()
		if ctx.Err() != nil {
			return nil, func() {}, ctx.Err()
		}
		return nil, func() {}, classifyHandshakeErr(err)
	}
	opts, _ := ctx.Value(transferTimeoutKey{}).(transferTimeouts)
	tc.idle.Store(int64(opts.idle))
	if opts.idle > 0 {
		_ = conn.SetDeadline(time.Now().Add(opts.idle))
	} else {
		_ = conn.SetDeadline(time.Time{})
	}
	return ssh.NewClient(sc, chans, reqs), cleanup, nil
}
