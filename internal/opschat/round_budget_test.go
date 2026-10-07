package opschat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

func TestFullRoundEscapedOutputFitsReservedCipherStorage(t *testing.T) {
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	f := executor(ids...)
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, _ []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
		return &target.LimitedResult{Stdout: strings.Repeat("\x01", l.Bytes), Truncated: true}, nil
	}
	m := model()
	for i := 0; i < 3; i++ {
		m.plan.Actions = append(m.plan.Actions, Action{ToolID: "host_ports", Args: json.RawMessage("{}"), TargetIndexes: []int{0, 1, 2, 3, 4, 5, 6, 7}})
	}
	s, _, db := fixture(t, f, m)
	c := chat(t, s, ids...)
	fillStorage(t, s, db, c.ID, (4<<20)-1024)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, PartialFailed)
	waitJobs(t, s)
	bytes, calls := 0, 0
	for _, c := range snap.Calls {
		if c.RunID == r.ID {
			calls++
			bytes += len(c.Output)
			if !c.Truncated {
				t.Fatal("missing aggregate truncation flag")
			}
		}
	}
	// A final 1KiB is reserved for failure details rather than raw collection.
	if calls != 24 || bytes > 512<<10 || bytes < (512<<10)-1024 || snap.Session.ActiveRunID != "" {
		t.Fatal(calls, bytes, snap.Session.ActiveRunID)
	}
	actual, err := s.StorageBytes(context.Background(), c.ID)
	if err != nil || actual > 8<<20 || actual < 7<<20 {
		t.Fatal(actual, err)
	}
}
