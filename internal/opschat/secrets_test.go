package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
)

type registeringModel struct {
	*fakeModel
	regMu         sync.Mutex
	masker        *mask.Masker
	db            *sql.DB
	key           string
	err           error
	registrations int
}

func (m *registeringModel) RegisterSecrets(ctx context.Context) error {
	m.regMu.Lock()
	defer m.regMu.Unlock()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) <= 0 {
		return errors.New("missing local registration deadline")
	}
	// The fixture has one SQLite connection: this fails if called inside a transaction.
	var one int
	if err := m.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	m.registrations++
	if m.err != nil {
		return m.err
	}
	m.masker.RegisterSecret(m.key)
	return nil
}

func TestManualToolsRegisterDisabledAndRotatedProviderSecrets(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := &registeringModel{fakeModel: model(), key: "disabled-provider-api-secret"}
	m.info = ProviderInfo{}
	s, _, db := fixture(t, f, m)
	m.masker, m.db = s.masker, db
	c := chat(t, s, id)
	if s.Capabilities().ModelAvailable {
		t.Fatal("disabled provider is available")
	}
	for _, key := range []string{"disabled-provider-api-secret", "rotated-provider-api-secret"} {
		m.regMu.Lock()
		m.key = key
		m.regMu.Unlock()
		f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
			return &target.LimitedResult{Stdout: "provider key: " + key}, nil
		}
		r := submitTool(t, s, c, "host_ports", "{}")
		snap := waitRun(t, s, c.ID, r.ID, Succeeded)
		waitJobs(t, s)
		for _, call := range snap.Calls {
			if strings.Contains(call.Output, key) {
				t.Fatal("provider key leaked into persisted result")
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.plans) != 0 || len(m.analyses) != 0 || m.registrations < 5 {
		t.Fatal("manual tools did not register without model inference")
	}
}

func TestProviderSecretRegistrationFailureStopsPersistenceAndDispatch(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := &registeringModel{fakeModel: model(), key: "provider-secret"}
	s, _, db := fixture(t, f, m)
	m.masker, m.db = s.masker, db
	c := chat(t, s, id)
	m.err = errors.New("provider-secret: raw adapter failure")
	if _, err := s.Create(context.Background(), CreateInput{}); err != ErrUnavailable {
		t.Fatal(err)
	}
	draft := "provider-secret"
	if _, err := s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, Draft: &draft}); err != ErrUnavailable {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "host_ports", Args: json.RawMessage("{}")}); err != ErrUnavailable {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM ops_chat_runs").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if current, err := s.Get(context.Background(), c.ID); err != ErrUnavailable || current != nil {
		t.Fatal("public GET did not fail closed", current, err)
	}
	current, err := s.session(context.Background(), db, c.ID)
	if err != nil || current.View.Revision != c.Revision || current.Body.Draft != "" {
		t.Fatal(current, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.invocations) != 0 {
		t.Fatal("registration failure allowed SSH")
	}
}
