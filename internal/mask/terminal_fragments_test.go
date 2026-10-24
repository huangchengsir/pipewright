package mask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTerminalFragmentsCoverPrefixesSuffixesAndInternalSlices(t *testing.T) {
	secret := "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	m := NewMasker()
	m.RegisterSecret(secret)
	for _, fragment := range []string{secret[:8], secret[8:16], secret[len(secret)-8:], secret[15:31]} {
		for _, text := range []string{fragment, "before " + fragment, "before " + fragment + " after"} {
			out, err := m.ScrubTerminalFragments(context.Background(), text)
			if err != nil || out != Placeholder {
				t.Fatal("registered fragment was not fully masked", err)
			}
		}
	}
	fragment := secret[15:31]
	if m.Scrub(fragment) != fragment || m.ScrubTruncated(fragment) != fragment {
		t.Fatal("legacy masking semantics changed")
	}
	if out, err := m.ScrubTerminalFragments(context.Background(), "unrelated log output"); err != nil || out != "unrelated log output" {
		t.Fatal(out, err)
	}
}
func TestTerminalFragmentsPreserveCompleteSecretsAndSafeEvidence(t *testing.T) {
	m := NewMasker()
	secret := "complete-registered-credential"
	m.RegisterSecret(secret)
	out, err := m.ScrubTerminalFragments(context.Background(), "ERROR before "+secret+" safe details after")
	if err != nil || out != "ERROR before [MASKED] safe details after" {
		t.Fatal("complete-secret redaction lost safe evidence", err)
	}
	if out, err := m.ScrubTerminalFragments(context.Background(), secret); err != nil || out != Placeholder {
		t.Fatal("complete secret echoed", err)
	}
}
func TestTerminalFragmentsPreserveShortFullSecretAndTailProtection(t *testing.T) {
	m := NewMasker()
	m.RegisterSecret("tiny")
	m.RegisterRegistrySecret("u:x")
	m.RegisterSecret("unique-prefix-credential")
	for _, tc := range []struct{ input, want string }{{"a tiny value", "a [MASKED] value"}, {"x", Placeholder}, {"log uniq", "log [MASKED]"}} {
		out, err := m.ScrubTerminalFragments(context.Background(), tc.input)
		if err != nil || out != tc.want {
			t.Fatal("short full secret or truncated prefix escaped", err)
		}
	}
	if out, err := m.ScrubTerminalFragments(context.Background(), "pre ique post"); err != nil || out != "pre ique post" {
		t.Fatal("unexpected short-fragment contract", err)
	}
}
func TestTerminalFragmentsFailClosedOnCancellationBudgetAndInvalidText(t *testing.T) {
	m := NewMasker()
	m.RegisterSecret("registered long credential")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := m.ScrubTerminalFragments(canceled, "log output"); !errors.Is(err, context.Canceled) || out != "" {
		t.Fatal("cancellation did not fail closed")
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if out, err := m.ScrubTerminalFragments(expired, "log output"); !errors.Is(err, context.DeadlineExceeded) || out != "" {
		t.Fatal("deadline did not fail closed")
	}
	if out, err := m.scrubTerminalFragments(context.Background(), "log output", 3); !errors.Is(err, ErrTerminalScanUnavailable) || out != "" {
		t.Fatal("budget did not fail closed")
	}
	for _, value := range []string{strings.Repeat("中", 5462), string([]byte{0xff})} {
		if out, err := m.ScrubTerminalFragments(context.Background(), value); !errors.Is(err, ErrTerminalScanUnavailable) || out != "" {
			t.Fatal("invalid field did not fail closed")
		}
	}
	m.mu.Lock()
	out, err := m.ScrubTerminalFragments(context.Background(), "log output")
	m.mu.Unlock()
	if !errors.Is(err, ErrTerminalScanUnavailable) || out != "" {
		t.Fatal("busy registry did not fail closed")
	}
}
func TestTerminalFragmentsUTF8ByteBoundaryAndWholeBudget(t *testing.T) {
	m := NewMasker()
	boundary := strings.Repeat("界", 5461) + "a"
	if out, err := m.ScrubTerminalFragments(context.Background(), boundary); err != nil || out != boundary {
		t.Fatal("valid UTF8 boundary rejected", err)
	}
	if _, err := m.ScrubTerminalFragments(context.Background(), boundary+"b"); !errors.Is(err, ErrTerminalScanUnavailable) {
		t.Fatal("byte overflow accepted")
	}
	m.RegisterSecret(strings.Repeat("Z", terminalSecretScanBytes+1))
	if out, err := m.ScrubTerminalFragments(context.Background(), "safe input"); !errors.Is(err, ErrTerminalScanUnavailable) || out != "" {
		t.Fatal("whole registry budget did not fail closed")
	}
}

func TestTerminalFragmentsRegistryCountBudget(t *testing.T) {
	m := NewMasker()
	for i := 0; i <= terminalSecretScanCount; i++ {
		m.RegisterSecret(fmt.Sprintf("credential-%04d", i))
	}
	if out, err := m.ScrubTerminalFragments(context.Background(), "safe text"); !errors.Is(err, ErrTerminalScanUnavailable) || out != "" {
		t.Fatal("count budget did not fail closed")
	}
}
