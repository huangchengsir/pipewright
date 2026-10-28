package target

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestSSHExecutionBoundaryClassificationPreservesLegacyErrors(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, e = (sshDialer{}).Run(ctx, addr, SSHConfig{User: "fixture", Password: "fixture"}, []string{"fixture"})
	if !errors.Is(e, ErrNotStarted) || !errors.Is(e, ErrUnreachable) || errors.Is(e, ErrOutcomeUnknown) {
		t.Fatal(e)
	}
	for _, phase := range []string{"handshake", "channel", "exec", "output"} {
		t.Run(phase, func(t *testing.T) {
			addr, reached, done := stalledLimitedSSH(t, phase, false)
			ctx, cancel := context.WithCancel(context.Background())
			type result struct{ err error }
			resultC := make(chan result, 1)
			go func() {
				_, e := (sshDialer{}).RunLimited(ctx, addr, SSHConfig{User: "fixture", Password: "fixture"}, []string{"fixture"}, ExecutionLimits{Bytes: 1024, Lines: 20})
				resultC <- result{e}
			}()
			select {
			case <-reached:
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("fixture not reached")
			}
			cancel()
			select {
			case r := <-resultC:
				want := ErrNotStarted
				if phase == "exec" || phase == "output" {
					want = ErrOutcomeUnknown
				}
				if !errors.Is(r.err, want) || !errors.Is(r.err, context.Canceled) {
					t.Fatal(phase, r.err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("transport did not exit")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("fixture did not exit")
			}
		})
	}
}
