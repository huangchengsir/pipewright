package kubeprobe

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestNetworkDenyActualTCPRefusedAndOpen(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	spec := Spec{Kind: "network-deny", Target: address, TimeoutSeconds: 2}
	if Run(context.Background(), spec) == nil {
		t.Error("successful TCP dial was accepted as denied")
	}
	spec.Kind = "network-allow"
	if Run(context.Background(), spec) != nil {
		t.Error("open TCP positive control failed")
	}
	listener.Close()
	spec.Kind = "network-deny"
	if Run(context.Background(), spec) != nil {
		t.Error("explicit TCP connection refused was not accepted after paired-control contract")
	}
	spec.Kind = "network-allow"
	if Run(context.Background(), spec) == nil {
		t.Error("refused connection was accepted as positive control")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec.Kind = "network-deny"
	if Run(ctx, spec) == nil {
		t.Error("cancellation was accepted as policy result")
	}
}

// This context models the real race where net.Dial's deadline has fired but
// the context timer has not yet published DeadlineExceeded.
type unpublishedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c unpublishedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func TestNetworkDenyOnlyClosedTCPErrorsAndOwnElapsedDeadline(t *testing.T) {
	dial := func(err error) *net.OpError { return &net.OpError{Op: "dial", Net: "tcp", Err: err} }
	past := unpublishedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	future := unpublishedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Hour)}
	if !deniedTCPConnection(context.Background(), past, dial(os.ErrDeadlineExceeded)) {
		t.Error("dial/context deadline publication race rejected")
	}
	if deniedTCPConnection(context.Background(), future, dial(os.ErrDeadlineExceeded)) {
		t.Error("unelapsed deadline accepted")
	}
	if deniedTCPConnection(context.Background(), context.Background(), dial(os.ErrDeadlineExceeded)) {
		t.Error("unbounded unrelated timeout accepted")
	}
	for _, err := range []error{syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.ECONNRESET, syscall.EACCES, errors.New("connection refused"), &net.DNSError{Err: "timeout", IsTimeout: true}, context.Canceled} {
		if deniedTCPConnection(context.Background(), past, dial(err)) {
			t.Error("non-policy error accepted")
		}
	}
	for _, err := range []error{syscall.ECONNREFUSED, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNREFUSED}, &net.OpError{Op: "dial", Net: "udp", Err: syscall.ECONNREFUSED}} {
		if deniedTCPConnection(context.Background(), past, err) {
			t.Error("non-TCP-dial refusal accepted")
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, parent := range []context.Context{canceled, expired} {
		if deniedTCPConnection(parent, past, dial(os.ErrDeadlineExceeded)) || deniedTCPConnection(parent, past, dial(syscall.ECONNREFUSED)) {
			t.Error("parent cancellation treated as successful negative result")
		}
	}
	if !deniedTCPConnection(context.Background(), past, dial(&os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED})) {
		t.Error("wrapped explicit TCP refusal rejected")
	}
}
