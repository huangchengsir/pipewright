package deployattempt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func capturedLocalImage() ExpectedImage {
	id := "sha256:" + strings.Repeat("a", 64)
	return ExpectedImage{Reference: id, Digest: id, DigestKind: DigestConfig, IdentitySource: IdentityScriptPreActivation, Platform: Platform{OS: "linux", Architecture: "amd64"}}
}

func TestLocalExpectedCaptureContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ExpectedImage)
		want   bool
	}{
		{"complete preactivation image", func(*ExpectedImage) {}, true},
		{"optional variant", func(e *ExpectedImage) { e.Platform.Variant = "v8" }, true},
		{"legacy config is not capture proof", func(e *ExpectedImage) { e.IdentitySource = "" }, false},
		{"unknown identity source", func(e *ExpectedImage) { e.IdentitySource = "container_inspect" }, false},
		{"repository remains a different identity", func(e *ExpectedImage) { e.Repository = "registry.invalid/app" }, false},
		{"manifest remains a different identity", func(e *ExpectedImage) { e.DigestKind = DigestManifest }, false},
		{"mutable reference", func(e *ExpectedImage) { e.Reference = "app:latest" }, false},
		{"short ID", func(e *ExpectedImage) { e.Digest = "sha256:abcd"; e.Reference = e.Digest }, false},
		{"missing OS", func(e *ExpectedImage) { e.Platform.OS = "" }, false},
		{"missing architecture", func(e *ExpectedImage) { e.Platform.Architecture = "" }, false},
		{"invalid variant", func(e *ExpectedImage) { e.Platform.Variant = "v8\n" }, false},
		{"pending build is not durable identity", func(e *ExpectedImage) { e.Digest = ""; e.Reference = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := capturedLocalImage()
			tc.change(&e)
			if got := ValidLocalExpected(e); got != tc.want {
				t.Fatalf("ValidLocalExpected=%v want=%v", got, tc.want)
			}
			if e.IdentitySource != "" && validateExpected(e) != tc.want {
				t.Fatal("ledger validation diverged from capture contract")
			}
		})
	}
}

func TestLocalExpectationIsDurableBeforeActivationAndActualMismatchRetained(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ledger := seedLedger(t, st)
		ctx := context.Background()
		attempt, err := ledger.Begin(ctx, BeginInput{RunID: "r", Initiator: "deploy"})
		if err != nil {
			t.Fatal(err)
		}
		in := targetInput()
		in.ExpectedImage = capturedLocalImage()
		target, err := ledger.StartTarget(ctx, attempt.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		// Mutating the caller's copy or recording a different runtime image does
		// not rewrite the expectation captured before activation.
		in.ExpectedImage.Digest = "sha256:" + strings.Repeat("f", 64)
		actual := ActualImage{ContainerID: strings.Repeat("1", 64), ConfigID: in.ExpectedImage.Digest, Platform: Platform{OS: "linux", Architecture: "amd64"}}
		for _, receipt := range []Receipt{{Kind: ActivationStarted}, {Kind: Activated, ActualImage: actual}, {Kind: DeployFailed, ActualImage: actual, Reason: "image_config_id_mismatch"}} {
			if _, err = ledger.AppendReceipt(ctx, target.ID, receipt); err != nil {
				t.Fatal(err)
			}
		}
		got, err := ledger.GetTarget(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ExpectedImage != capturedLocalImage() || got.Receipts[1].ActualImage.ConfigID != actual.ConfigID || got.Outcome != DeployFailed || len(got.Receipts) != 3 {
			t.Fatalf("lost immutable expected/actual evidence: %+v", got)
		}
		bad := targetInput()
		bad.ExpectedImage = capturedLocalImage()
		bad.ExpectedImage.IdentitySource = "container_inspect"
		if _, err = ledger.StartTarget(ctx, attempt.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted unproven source", err)
		}
		bad.ExpectedImage.IdentitySource = IdentityScriptPreActivation
		bad.ExpectedImage.Digest, bad.ExpectedImage.Reference = "", ""
		if _, err = ledger.StartTarget(ctx, attempt.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted pending image as expected", err)
		}
	})
}
