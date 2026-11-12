package store

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestSQLServerContextSocketReleasedHandshakeRemainsReusable(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	socket := newSQLServerContextSocket(ctx, client)
	defer socket.Close()
	socket.release()
	cancel()
	// A successful handshake must not leave either its cancellation hook or its
	// deadline on the pooled connection used by subsequent requests.
	time.Sleep(120 * time.Millisecond)
	if e := socket.SetDeadline(time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := server.Write([]byte("x")); done <- e }()
	buf := make([]byte, 1)
	if _, e := socket.Read(buf); e != nil || string(buf) != "x" {
		t.Fatal("pooled connection lost after connect context", e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestSQLServerContextSocketProbeCannotExtendDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	socket := newSQLServerContextSocket(ctx, client)
	defer socket.Close()
	if e := socket.SetDeadline(time.Time{}); e != nil {
		t.Fatal(e)
	}
	if e := socket.SetReadDeadline(time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if _, e := socket.Read(make([]byte, 1)); e == nil {
		t.Fatal("blackhole query succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("driver extended probe deadline")
	}
}
func TestSQLServerContextSocketProbeCancellationClosesTransport(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	socket := newSQLServerContextSocket(ctx, client)
	defer socket.Close()
	cancel()
	if e := server.SetReadDeadline(time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := server.Read(make([]byte, 1)); e == nil {
		t.Fatal("cancelled transport remained open")
	}
}

func TestSQLServerContextConnectorPreservesDSNKeepAlive(t *testing.T) {
	for _, test := range []struct {
		name, query string
		want        time.Duration
	}{
		{"default", "", 30 * time.Second},
		{"custom", "?keepalive=7", 7 * time.Second},
		{"zero-driver-default", "?keepalive=0", 30 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			connector, err := newSQLServerContextConnector("sqlserver://user:password@localhost:1433"+test.query, nil)
			if err != nil {
				t.Fatal("connection configuration rejected")
			}
			actual := connector.contextDialer(context.Background()).networkDialer().KeepAlive
			if actual != test.want {
				t.Fatalf("KeepAlive=%v want %v", actual, test.want)
			}
		})
	}
}

func TestSQLServerContextConnectorKeepsDisabledPolicyAndInvalidDSNBehavior(t *testing.T) {
	// The upstream Config supports negative durations, while its DSN parser uses
	// ParseUint and rejects a negative string. Preserve both contracts unchanged.
	connector := &sqlServerContextConnector{keepAlive: -time.Second}
	if got := connector.contextDialer(context.Background()).networkDialer().KeepAlive; got != -time.Second {
		t.Fatal("disabled keepalive was replaced", got)
	}
	if _, err := newSQLServerContextConnector("sqlserver://user:password@localhost:1433?keepalive=-1", nil); err == nil {
		t.Fatal("upstream-invalid DSN was accepted")
	}
}
