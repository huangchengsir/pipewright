package target

import (
	"context"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"sync"
)

// secretStdioConn adapts one fixed SSH exec channel to HTTP without requesting
// TCP/stream-local forwarding, a PTY, environment variables or a config file.
// net.Pipe supplies real net.Conn deadlines; closure also interrupts SSH pumps.
type secretStdioConn struct {
	net.Conn
	peer    net.Conn
	client  *ssh.Client
	session *ssh.Session
	once    sync.Once
	done    chan struct{}
}

func (c *secretStdioConn) Close() error {
	c.once.Do(func() {
		_ = c.Conn.Close()
		_ = c.peer.Close()
		// This SSH client belongs only to this single HTTP request. Closing its
		// socket first prevents channel-close writes or copy pumps from hanging.
		_ = c.client.Close()
		_ = c.session.Close()
	})
	return nil
}
func newSecretStdioConn(ctx context.Context, client *ssh.Client) (*secretStdioConn, error) {
	if ctx.Err() != nil {
		return nil, ErrUnreachable
	}
	session, e := client.NewSession()
	if e != nil {
		return nil, ErrUnreachable
	}
	local, peer := net.Pipe()
	c := &secretStdioConn{Conn: local, peer: peer, client: client, session: session, done: make(chan struct{})}
	session.Stdin = peer
	session.Stdout = peer
	session.Stderr = io.Discard
	// The enclosing pull always cancels this bounded context on exit. Its socket
	// and both pipe ends are closed even if exec acceptance or body I/O blocks.
	context.AfterFunc(ctx, func() { _ = c.Close() })
	command := quoteArgs([]string{"docker", "--host", "unix:///var/run/docker.sock", "--config", "/dev/null", "system", "dial-stdio"})
	if e = session.Start(command); e != nil {
		_ = c.Close()
		close(c.done)
		return nil, ErrUnreachable
	}
	go func() { _ = session.Wait(); close(c.done); _ = c.Close() }()
	return c, nil
}
