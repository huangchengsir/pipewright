package target

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Each server deliberately withholds one different SSH setup response. Parent
// cancellation must close the socket, not just cancel an already-completed dial.
func TestInteractiveCancellationAtEachCreationStage(t *testing.T) {
	for _, stage := range []string{"handshake", "channel", "pty", "start"} {
		t.Run(stage, func(t *testing.T) {
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.NewSignerFromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			cfg := &ssh.ServerConfig{NoClientAuth: true}
			cfg.AddHostKey(signer)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			arrived := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				if stage == "handshake" {
					close(arrived)
					var b [256]byte
					for {
						if _, err := raw.Read(b[:]); err != nil {
							return
						}
					}
				}
				server, chans, reqs, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if stage == "channel" {
						close(arrived)
						server.Wait()
						return
					}
					channel, requests, err := ch.Accept()
					if err != nil {
						return
					}
					defer channel.Close()
					for req := range requests {
						if (stage == "pty" && req.Type == "pty-req") || (stage == "start" && req.Type == "exec") {
							close(arrived)
							server.Wait()
							return
						}
						req.Reply(true, nil)
					}
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				s, err := (sshDialer{}).RunInteractive(ctx, listener.Addr().String(), SSHConfig{User: "test", Password: "fixture"}, []string{"/bin/sh"})
				if s != nil {
					s.Close()
				}
				result <- err
			}()
			select {
			case <-arrived:
			case <-time.After(3 * time.Second):
				t.Fatal("stage not reached")
			}
			cancel()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("unexpected successful setup")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation failed to unblock setup")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("SSH socket remained open")
			}
		})
	}
}

func closeSignalFixture(t *testing.T, afterExec ...func()) (string, <-chan string, <-chan struct{}) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	signals := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		server, chans, reqs, err := ssh.NewServerConn(raw, cfg)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(reqs)
		for newChannel := range chans {
			channel, requests, err := newChannel.Accept()
			if err != nil {
				return
			}
			go io.Copy(io.Discard, channel)
			for req := range requests {
				if req.Type == "signal" {
					var payload struct{ Signal string }
					if ssh.Unmarshal(req.Payload, &payload) == nil {
						signals <- payload.Signal
					}
				}
				_ = req.Reply(true, nil)
				if req.Type == "exec" && len(afterExec) > 0 {
					afterExec[0]()
				}
			}
			_ = channel.Close()
		}
	}()
	return listener.Addr().String(), signals, done
}

func TestInteractiveExplicitCloseSignalsBeforeTransportTeardown(t *testing.T) {
	addr, signals, done := closeSignalFixture(t)
	sess, err := (sshDialer{}).RunInteractive(context.Background(), addr, SSHConfig{User: "fixture", Password: "fixture"}, []string{"/bin/sh"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err = sess.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case signal := <-signals:
		if signal != string(ssh.SIGKILL) {
			t.Fatal(signal)
		}
	case <-time.After(time.Second):
		t.Fatal("remote did not receive SIGKILL before connection teardown")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSH connection not released")
	}
}

func TestInteractiveParentCancelAfterSetupSignalsBeforeTeardown(t *testing.T) {
	addr, signals, done := closeSignalFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess, err := (sshDialer{}).RunInteractive(ctx, addr, SSHConfig{User: "fixture", Password: "fixture"}, []string{"/bin/sh"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// A successful resize demonstrates the established transport remains usable
	// after the creation cancellation hook has been detached.
	if err = sess.Resize(120, 40); err != nil {
		t.Fatal("established terminal unusable", err)
	}
	cancel()
	select {
	case signal := <-signals:
		if signal != string(ssh.SIGKILL) {
			t.Fatal(signal)
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancellation closed TCP before remote SIGKILL")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = sess.Close() }()
	}
	joined := make(chan struct{})
	go func() { wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("concurrent Close calls did not converge")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled SSH connection remained open")
	}
}

func TestInteractiveCancellationAtSetupHandoff(t *testing.T) {
	for range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		// Cancellation races the final exec reply and ownership handoff. An opener
		// canceled during setup may fail; any successfully handed-off session must
		// still close through the signal-first established-session path.
		addr, signals, done := closeSignalFixture(t, func() { go cancel() })
		sess, err := (sshDialer{}).RunInteractive(ctx, addr, SSHConfig{User: "fixture", Password: "fixture"}, []string{"/bin/sh"})
		cancel()
		if err == nil {
			select {
			case signal := <-signals:
				if signal != string(ssh.SIGKILL) {
					t.Fatal(signal)
				}
			case <-time.After(time.Second):
				t.Fatal("successful handoff lost cancellation signal")
			}
			_ = sess.Close()
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("handoff cancellation leaked SSH connection")
		}
	}
}

// blockedWriteConn models an established peer whose network stops consuming
// writes. Close must release the pending SSH signal write through raw cleanup.
type blockedWriteConn struct {
	net.Conn
	blocked atomic.Bool
	once    sync.Once
	closed  chan struct{}
}

func (c *blockedWriteConn) Write(p []byte) (int, error) {
	if c.blocked.Load() {
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}
func (c *blockedWriteConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestInteractiveCloseBoundsBlockedSignalWrite(t *testing.T) {
	addr, _, done := closeSignalFixture(t)
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	gate := &blockedWriteConn{Conn: raw, closed: make(chan struct{})}
	defer gate.Close()
	transport, chans, reqs, err := ssh.NewClientConn(gate, addr, &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(transport, chans, reqs)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, outW := io.Pipe()
	defer out.Close()
	s := &interactiveSession{stdin: stdin, out: out, outW: outW, session: session, client: client, stop: make(chan struct{}), cleanup: func() { _ = gate.Close() }}
	gate.blocked.Store(true)
	started := time.Now()
	closed := make(chan struct{})
	go func() { _ = s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("stalled signal kept Close blocked")
	}
	if elapsed := time.Since(started); elapsed < 200*time.Millisecond {
		t.Fatalf("signal grace bypassed: %v", elapsed)
	}
	// Concurrent and repeated Close calls must converge without double-close.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Close() }()
	}
	wg.Wait()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stalled SSH connection not released")
	}
}
