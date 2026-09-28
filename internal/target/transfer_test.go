package target

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestDialSSHCancelDuringHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			close(accepted)
			defer conn.Close()
			buf := make([]byte, 32)
			for {
				if _, err := conn.Read(buf); err != nil {
					return
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, cleanup, err := dialSSH(ctx, listener.Addr().String(), &ssh.ClientConfig{
		User: "test", Timeout: 2 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	cleanup()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake returned %v, want deadline", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("handshake did not stop after context cancellation")
	}
	select {
	case <-accepted:
	default:
		t.Fatal("test listener was not reached")
	}
}

func TestResolveTimeoutClampsToConfiguredConnectionLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx = WithTransferTimeouts(ctx, 3*time.Second, 0)
	if got := resolveTimeout(ctx); got <= 0 || got > 3*time.Second {
		t.Fatalf("connection timeout = %s, want at most 3s", got)
	}
}
