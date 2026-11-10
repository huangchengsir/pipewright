package kubeprobe

import (
	"context"
	"errors"
	"net"
	"runtime"
	"syscall"
	"time"
)

// A negative probe is only scheduled by the controller after its exact-target
// positive control has a verified healthy Job receipt. Here we recognize only
// explicit TCP refusal or expiry of this probe's dial deadline. This is a
// reachability observation, not proof of which firewall produced the result.
func deniedTCPConnection(parent, work context.Context, err error) bool {
	if parent.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var failure *net.OpError
	if !errors.As(err, &failure) || failure.Op != "dial" || (failure.Net != "tcp" && failure.Net != "tcp4" && failure.Net != "tcp6") {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return false
	}
	// On Windows syscall.ECONNREFUSED is a synthetic application error;
	// Winsock's real WSAECONNREFUSED is 10061. Never accept it on other OSes.
	if errors.Is(failure.Err, syscall.ECONNREFUSED) || runtime.GOOS == "windows" && errors.Is(failure.Err, syscall.Errno(10061)) {
		return true
	}
	if !failure.Timeout() {
		return false
	}
	deadline, ok := work.Deadline()
	// Dial's deadline may fire just before context's timer publishes Err().
	// Require the real deadline to have elapsed, not any earlier timeout.
	return ok && !time.Now().Before(deadline) && (work.Err() == nil || work.Err() == context.DeadlineExceeded)
}
