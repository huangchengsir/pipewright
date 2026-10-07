package target

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// This fixture binds only loopback and never runs an OS command or accesses a credential.
func stalledLimitedSSH(t *testing.T, phase string) (string, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reached, done := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		defer close(done)
		defer listener.Close()
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		// A hard fixture deadline bounds cleanup even if the client regresses.
		_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
		if phase == "handshake" {
			close(reached)
			_, _ = io.Copy(io.Discard, raw)
			return
		}
		conn, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			return
		}
		defer conn.Close()
		requestsDone := make(chan struct{})
		go func() { defer close(requestsDone); ssh.DiscardRequests(requests) }()
		defer func() { _ = conn.Close(); <-requestsDone }()
		channel, ok := <-channels
		if !ok {
			return
		}
		if phase == "channel" {
			close(reached) // Never acknowledge session channel creation.
			_ = conn.Wait()
			return
		}
		stream, sessionRequests, err := channel.Accept()
		if err != nil {
			return
		}
		defer stream.Close()
		for request := range sessionRequests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			if phase == "exec" {
				close(reached) // Never acknowledge the exec request.
				_ = conn.Wait()
				return
			}
			_ = request.Reply(true, nil)
			// Exceed the SSH channel window so the client must drain before we signal.
			_, _ = io.WriteString(stream, strings.Repeat("stdout-line\n", 327680))
			_, _ = io.WriteString(stream.Stderr(), strings.Repeat("stderr-line\n", 8192))
			close(reached) // Drain excess output, then stall without EOF or exit status.
			_ = conn.Wait()
			return
		}
	}()
	return listener.Addr().String(), reached, done
}

func TestRunLimitedLoopbackStallsCancelAndGoroutinesConverge(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for repeat := 0; repeat < 3; repeat++ {
		for _, phase := range []string{"handshake", "channel", "exec", "output"} {
			for _, timeout := range []bool{false, true} {
				t.Run(phase+map[bool]string{false: "/cancel", true: "/deadline"}[timeout], func(t *testing.T) {
					addr, reached, serverDone := stalledLimitedSSH(t, phase)
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					if timeout {
						cancel()
						ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
					}
					defer cancel()
					type result struct {
						res *LimitedResult
						err error
					}
					finished := make(chan result, 1)
					go func() {
						res, err := (sshDialer{}).RunLimited(ctx, addr, SSHConfig{User: "fixture", Password: "fixture-only"}, []string{"fixture-command"}, ExecutionLimits{Bytes: 1024, Lines: 20})
						finished <- result{res, err}
					}()
					select {
					case <-reached:
					case <-time.After(time.Second):
						t.Fatal("loopback SSH did not reach stall phase")
					}
					started := time.Now()
					if !timeout {
						cancel()
					}
					select {
					case got := <-finished:
						want := error(context.Canceled)
						if timeout {
							want = context.DeadlineExceeded
						}
						if !errors.Is(got.err, want) {
							t.Fatalf("stalled transport returned %v, want %v", got.err, want)
						}
						if phase == "output" && (got.res == nil || !got.res.Truncated || len(got.res.Stdout)+len(got.res.Stderr) > 1024) {
							t.Fatal("stalled output exceeded bounded capture", got.res)
						}
					case <-time.After(time.Second):
						t.Fatal("RunLimited retained a blocked transport goroutine")
					}
					if time.Since(started) > time.Second {
						t.Fatal("cancellation did not converge promptly")
					}
					select {
					case <-serverDone:
					case <-time.After(time.Second):
						t.Fatal("loopback server transport did not close")
					}
				})
			}
		}
	}
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > baseline+2 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline+2 {
		t.Fatalf("goroutines did not converge after 24 cancelled SSH transports: baseline=%d got=%d", baseline, got)
	}
}

type delayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c delayedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestRunLimitedHonorsDeadlineBeforeContextTimerNotification(t *testing.T) {
	ctx := delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	if ctx.Err() != nil {
		t.Fatal("fixture must delay context cancellation notification")
	}
	// An expired operation must not start a connection, even without Done being closed.
	res, err := (sshDialer{}).RunLimited(ctx, "not-a-network-address", SSHConfig{User: "fixture", Password: "fixture-only"}, []string{"fixture-command"}, ExecutionLimits{Bytes: 1024, Lines: 20})
	if res != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired operation returned %v, want deadline", err)
	}
	if err := limitedContextErr(delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("future deadline returned %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := limitedContextErr(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit cancellation returned %v", err)
	}
	if err := limitedContextErr(context.Background()); err != nil {
		t.Fatalf("unbounded context returned %v", err)
	}
}
