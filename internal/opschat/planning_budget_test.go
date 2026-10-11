package opschat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestContinuationBudgetNeverDropsCurrentUserForAssistant(t *testing.T) {
	id := uuid.NewString()
	f, m := executor(id), model()
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	now := time.Now().UTC()
	callID := seedContainerObservation(t, s, c.ID, f.snapshots[id], Call{CollectedAt: &now, Output: containerObservationRow(strings.Repeat("a", 64), "app")}, now)
	var runID string
	if err := s.db.QueryRow("SELECT run_id FROM ops_chat_calls WHERE id=?", callID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	r, err := s.run(context.Background(), s.db, c.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	r.View.Status = Running
	r.Body.Provider = m.Info()
	r.Body.NeedsDiscovery = true
	r.Body.ModelCalls = 1
	err = s.persist(c.ID, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.saveRun(ctx, tx, r); err != nil {
			return err
		}
		for _, role := range []string{"user", "assistant"} {
			if err := s.append(ctx, tx, Entry{SessionID: c.ID, RunID: runID, Kind: role, Text: strings.Repeat(`"`, 8<<10)}, true, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.requestPlan(context.Background(), r, true); !errors.Is(err, ErrQuota) {
		t.Fatal("question dropped in favor of narration", err)
	}
	m.mu.Lock()
	plans := len(m.plans)
	m.mu.Unlock()
	stored, err := s.run(context.Background(), s.db, c.ID, runID)
	if err != nil || plans != 0 || stored.Body.ModelCalls != 1 || stored.Body.ContinuationStarted {
		t.Fatal("oversized question dispatched or consumed continuation", plans, stored.Body, err)
	}
}
