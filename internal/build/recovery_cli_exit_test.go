package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/runretry"
)

// This child test process gives execCommander a real *exec.ExitError, rather
// than the nil-error/nonzero-code tuple used by old in-memory CLI fixtures.
func TestRecoveryMissingInspectProcess(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_TEST_INSPECT_EXIT_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "Error: No such object: "+strings.Repeat("a", 64))
	code, _ := strconv.Atoi(os.Getenv("PIPEWRIGHT_TEST_INSPECT_EXIT_CODE"))
	os.Exit(code)
}

type realMissingInspectCommander struct {
	Commander
	binary string
	mode   string
}

func (c realMissingInspectCommander) Run(ctx context.Context, _ string, _ []string, _ string) (string, string, int, error) {
	out, stderr, code, err := (execCommander{}).Run(ctx, c.binary, []string{"-test.run=^TestRecoveryMissingInspectProcess$"}, "")
	switch c.mode {
	case "unknown":
		return out, stderr, code, errors.New("transport completion unknown")
	case "canceled":
		return out, stderr, code, context.Canceled
	case "wrong-object":
		stderr = strings.ReplaceAll(stderr, strings.Repeat("a", 64), strings.Repeat("b", 64))
	case "stdout":
		out = "unverified output"
	}
	return out, stderr, code, err
}

func TestRecoveryMissingInspectHonorsRealExitError(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIPEWRIGHT_TEST_INSPECT_EXIT_HELPER", "1")
	for _, mode := range []string{"known-exit-one", "exit-two", "unknown", "canceled", "wrong-object", "stdout"} {
		t.Run(mode, func(t *testing.T) {
			exit := "1"
			if mode == "exit-two" {
				exit = "2"
			}
			t.Setenv("PIPEWRIGHT_TEST_INSPECT_EXIT_CODE", exit)
			d := &shellDriver{bin: "docker", cmdr: realMissingInspectCommander{binary: binary, mode: mode}}
			absent, _, err := d.inspectOwned(context.Background(), runretry.ResourceRef{Kind: "container", Name: "owned", ID: strings.Repeat("a", 64), Owner: "random-owner"})
			if mode == "known-exit-one" {
				if err != nil || !absent {
					t.Fatalf("real successful inspect-absence evidence rejected: absent=%t err=%v", absent, err)
				}
				return
			}
			if absent || !errors.Is(err, runretry.ErrUnknown) {
				t.Fatalf("unproven exit accepted: absent=%t err=%v", absent, err)
			}
		})
	}
}
