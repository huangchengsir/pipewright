package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func seedContainerObservation(t *testing.T, s *Service, session string, snapshot target.ConnectionSnapshot, call Call, created time.Time) string {
	t.Helper()
	call.ID, call.RunID, call.SessionID, call.ServerID = uuid.NewString(), uuid.NewString(), session, snapshot.Server.ID
	if call.ToolID == "" {
		call.ToolID = "docker_containers"
	}
	if call.Status == "" {
		call.Status = Succeeded
	}
	if call.TargetHash == "" {
		call.TargetHash = snapshot.Fingerprint()
	}
	err := s.transaction(context.Background(), false, func(tx *sql.Tx) error {
		raw, err := s.seal(runBody{Targets: []target.ConnectionSnapshot{snapshot}})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)`, call.RunID, session, uuid.NewString(), "hash", Succeeded, raw, created.UnixMilli()); err != nil {
			return err
		}
		raw, err = s.seal(callBody{View: call, Snapshot: snapshot})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, call.ID, session, call.RunID, call.ServerID, call.ToolID, call.Status, "hash", call.TargetHash, raw, created.UnixMilli(), 0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return call.ID
}

func containerObservationRow(id, name string) string {
	raw, _ := json.Marshal(map[string]any{"id": id, "names": name, "image": "redis:7", "state": "running", "ports": "never-export-ports", "env": map[string]string{"TOKEN": "never-export-env"}, "serverId": "never-export-server-id", "address": "never-export-address", "serverName": "never-export-server-name"})
	return string(raw)
}

func getContainerObservations(t *testing.T, s *Service, session string, targets ...target.ConnectionSnapshot) []ContainerObservation {
	t.Helper()
	observations, err := s.containerObservations(context.Background(), session, targets)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(observations)
	if err != nil || len(raw) > 8<<10 {
		t.Fatal("observation byte cap exceeded", len(raw), err)
	}
	return observations
}

func TestContainerObservationsLocalLatestSessionAndTargetIsolation(t *testing.T) {
	ids := []string{uuid.NewString(), uuid.NewString()}
	f, m := executor(ids...), model()
	s, _, _ := fixture(t, f, m)
	c, other := chat(t, s, ids...), chat(t, s, ids...)
	targets := []target.ConnectionSnapshot{f.snapshots[ids[0]], f.snapshots[ids[1]]}
	now := time.Now().UTC()
	old, newest := now.Add(-2*time.Minute), now.Add(-time.Minute)
	seedContainerObservation(t, s, c.ID, targets[0], Call{CollectedAt: &old, Output: containerObservationRow(strings.Repeat("a", 64), "older")}, old)
	seedContainerObservation(t, s, c.ID, targets[0], Call{CollectedAt: &newest, Output: containerObservationRow(strings.Repeat("b", 64), "latest")}, newest)
	seedContainerObservation(t, s, c.ID, targets[1], Call{CollectedAt: &old, Output: containerObservationRow(strings.Repeat("c", 64), "second-target")}, old)
	seedContainerObservation(t, s, other.ID, targets[0], Call{CollectedAt: &now, Output: containerObservationRow(strings.Repeat("d", 64), "wrong-session")}, now)
	seedContainerObservation(t, s, c.ID, targets[0], Call{CollectedAt: &now, Status: Failed, Output: containerObservationRow(strings.Repeat("e", 64), "failed")}, now)
	seedContainerObservation(t, s, c.ID, targets[0], Call{CollectedAt: &now, ToolID: "docker_inspect", Output: containerObservationRow(strings.Repeat("f", 64), "wrong-tool")}, now)
	observations := getContainerObservations(t, s, c.ID, targets[1], targets[0])
	if len(observations) != 2 || observations[0].TargetAlias != "target_0" || observations[1].TargetAlias != "target_1" || len(observations[0].Containers) != 1 || observations[0].Containers[0].Name != "second-target" || observations[1].Containers[0].Name != "latest" || !observations[1].CollectedAt.Equal(newest) {
		t.Fatal("wrong target/session/latest observation", observations)
	}
	raw, _ := json.Marshal(observations)
	for _, forbidden := range append(ids, "never-export", "wrong-session", "failed", "wrong-tool", targets[0].Server.Host, targets[0].Server.Name) {
		if forbidden != "" && strings.Contains(string(raw), forbidden) {
			t.Fatal("private or unrelated data exported", forbidden, string(raw))
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(f.invocations) != 0 || len(m.plans) != 0 || len(m.analyses) != 0 {
		t.Fatal("local observation lookup invoked SSH/model")
	}
}

func TestContainerObservationsUnavailableAndEmptySuccess(t *testing.T) {
	for _, scenario := range []string{"no_results", "expired", "connection_changed", "missing_time", "future_time", "empty", "truncated_empty", "nonzero"} {
		t.Run(scenario, func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			s, _, _ := fixture(t, f, nil)
			c := chat(t, s, id)
			snapshot := f.snapshots[id]
			now := time.Now().UTC()
			when := now.Add(-time.Minute)
			call := Call{CollectedAt: &when, Output: containerObservationRow(strings.Repeat("a", 64), "app")}
			want := scenario
			switch scenario {
			case "expired":
				when = now.Add(-16 * time.Minute)
			case "missing_time":
				call.CollectedAt, want = nil, "expired"
			case "future_time":
				when, want = now.Add(time.Minute), "expired"
			case "connection_changed":
				call.TargetHash = "different-fingerprint"
			case "empty", "truncated_empty":
				call.Output, call.Truncated, want = "", scenario == "truncated_empty", ""
			case "nonzero":
				exit := 1
				call.ExitCode, want = &exit, "no_results"
			}
			if scenario != "no_results" {
				seedContainerObservation(t, s, c.ID, snapshot, call, now)
			}
			observation := getContainerObservations(t, s, c.ID, snapshot)[0]
			if observation.Unavailable != want || len(observation.Containers) != 0 || observation.Containers == nil {
				t.Fatal(observation)
			}
			if want == "" && (observation.CollectedAt == nil || observation.Truncated != call.Truncated) {
				t.Fatal("empty successful inventory was not preserved", observation)
			}
		})
	}
}

func TestContainerObservationsFrozenFingerprintAndBoundedHistory(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, _ := fixture(t, f, nil)
	c := chat(t, s, id)
	snapshot := f.snapshots[id]
	now := time.Now().UTC()
	when := now.Add(-time.Minute)
	seedContainerObservation(t, s, c.ID, snapshot, Call{CollectedAt: &when, Output: containerObservationRow(strings.Repeat("a", 64), "matching")}, when)
	changed := snapshot
	changed.Server.Host = "192.0.2.99"
	if got := getContainerObservations(t, s, c.ID, changed)[0]; got.Unavailable != "connection_changed" || len(got.Containers) != 0 {
		t.Fatal("changed connection reused facts", got)
	}
	for i := 0; i < 8; i++ {
		created := now.Add(time.Duration(i) * time.Millisecond)
		seedContainerObservation(t, s, c.ID, changed, Call{CollectedAt: &when, Output: containerObservationRow(strings.Repeat("b", 64), "changed")}, created)
	}
	if got := getContainerObservations(t, s, c.ID, snapshot)[0]; got.Unavailable != "connection_changed" || len(got.Containers) != 0 {
		t.Fatal("query scanned beyond its eight-call bound", got)
	}
	// A forged matching hash cannot substitute a different frozen snapshot.
	separate := chat(t, s, id)
	seedContainerObservation(t, s, separate.ID, changed, Call{TargetHash: snapshot.Fingerprint(), CollectedAt: &when, Output: containerObservationRow(strings.Repeat("c", 64), "forged")}, now)
	if got := getContainerObservations(t, s, separate.ID, snapshot)[0]; got.Unavailable != "connection_changed" {
		t.Fatal("frozen snapshot was not checked", got)
	}
}

func TestContainerObservationsInvalidRowsSecretsAndCopy(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, v, db := fixture(t, f, nil)
	c := chat(t, s, id)
	snapshot := f.snapshots[id]
	for _, secret := range []string{strings.Repeat("a", 64), "known-observation-secret"} {
		if _, err := v.Create(vault.CreateInput{Name: uuid.NewString(), Type: vault.TypeSSHPassword, Secret: secret}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.registerSecrets(); err != nil {
		t.Fatal(err)
	}
	output := strings.Join([]string{
		"shell warning: never-export-warning", containerObservationRow(strings.Repeat("a", 64), "secret-id"),
		containerObservationRow(strings.Repeat("B", 64), "uppercase-id"), containerObservationRow("short", "short-id"),
		`{"id":null,"names":"invalid","image":"x","state":"running"}`,
		`{"id":"` + strings.Repeat("c", 64) + `","names":"x","image":{},"state":"running"}`,
		containerObservationRow(strings.Repeat("b", 64), "known-observation-secret"),
		containerObservationRow(strings.Repeat("b", 64), "duplicate-id"),
	}, "\n")
	now := time.Now().UTC()
	callID := seedContainerObservation(t, s, c.ID, snapshot, Call{CollectedAt: &now, Output: output}, now)
	observation := getContainerObservations(t, s, c.ID, snapshot)[0]
	if !observation.Truncated || len(observation.Containers) != 1 || observation.Containers[0].ID != strings.Repeat("b", 64) || observation.Containers[0].Name == "known-observation-secret" {
		t.Fatal("invalid/redacted IDs became facts", observation)
	}
	raw, _ := json.Marshal(observation)
	if strings.Contains(string(raw), "never-export") || strings.Contains(string(raw), "known-observation-secret") || strings.Contains(string(raw), strings.Repeat("a", 64)) {
		t.Fatal("unprojected data or secret exported", string(raw))
	}
	observation.Containers[0].Name = "mutated"
	*observation.CollectedAt = now.Add(time.Hour)
	stored, err := s.call(context.Background(), db, c.ID, callID)
	if err != nil || stored.Body.View.Output != output || !stored.Body.View.CollectedAt.Equal(now) {
		t.Fatal("projection modified stored call", err)
	}
}

func TestContainerObservationsInventoryAndEncodedByteCaps(t *testing.T) {
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	f := executor(ids...)
	s, _, _ := fixture(t, f, nil)
	c := chat(t, s, ids...)
	now := time.Now().UTC()
	var targets []target.ConnectionSnapshot
	for _, id := range ids {
		snapshot := f.snapshots[id]
		targets = append(targets, snapshot)
		var lines []string
		for n := 0; n < 25; n++ {
			lines = append(lines, containerObservationRow(fmt.Sprintf("%064x", n+1), strings.Repeat("<&", 100)))
		}
		seedContainerObservation(t, s, c.ID, snapshot, Call{CollectedAt: &now, Output: strings.Join(lines, "\n")}, now)
	}
	observations := getContainerObservations(t, s, c.ID, targets...)
	if len(observations) != 8 {
		t.Fatal("budget silently omitted targets")
	}
	for i, observation := range observations {
		if observation.TargetAlias != fmt.Sprintf("target_%d", i) || !observation.Truncated || len(observation.Containers) > 20 || observation.CollectedAt == nil {
			t.Fatal("missing inventory/budget marker", observation)
		}
		for _, fact := range observation.Containers {
			if !immutableContainer.MatchString(fact.ID) {
				t.Fatal("budget cropped an ID", fact)
			}
		}
	}
	if _, err := s.containerObservations(context.Background(), c.ID, append(targets, targets[0])); err != ErrInvalid {
		t.Fatal("target bound not enforced", err)
	}
	if empty := getContainerObservations(t, s, c.ID); empty == nil || len(empty) != 0 {
		t.Fatal("zero-target context should be an empty projection")
	}
}

func TestContainerObservationsAdapterTextBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
		accepted           bool
		truncated          bool
	}{
		{"name-256", "names", strings.Repeat("n", 256), true, false},
		{"name-257", "names", strings.Repeat("n", 257), false, true},
		{"unicode-name", "names", "\u6d4b\u8bd5", true, false},
		{"unicode-name-byte-limit", "names", strings.Repeat("\u754c", 86), false, true},
		{"name-newline", "names", "bad\nname", false, true},
		{"image-tab", "image", "bad\timage", false, true},
		{"state-nul", "state", "bad\x00state", false, true},
		{"state-c1-control", "state", "bad\u0085state", false, true},
		{"image-control-after-limit", "image", strings.Repeat("i", 513) + "\n", false, true},
		{"image-512", "image", strings.Repeat("i", 512), true, false},
		{"image-513", "image", strings.Repeat("i", 513), true, true},
		{"state-128", "state", strings.Repeat("s", 128), true, false},
		{"state-129", "state", strings.Repeat("s", 129), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			s, _, _ := fixture(t, f, nil)
			c := chat(t, s, id)
			snapshot := f.snapshots[id]
			row := map[string]string{"id": strings.Repeat("a", 64), "names": "app", "image": "image", "state": "running"}
			row[tc.field] = tc.value
			raw, _ := json.Marshal(row)
			now := time.Now().UTC()
			seedContainerObservation(t, s, c.ID, snapshot, Call{CollectedAt: &now, Output: string(raw)}, now)
			observation := getContainerObservations(t, s, c.ID, snapshot)[0]
			if (len(observation.Containers) == 1) != tc.accepted || observation.Truncated != tc.truncated {
				t.Fatal("adapter boundary mismatch", observation)
			}
			if tc.accepted {
				fact := observation.Containers[0]
				if len(fact.Name) > 256 || len(fact.Image) > 512 || len(fact.State) > 128 || (tc.field == "names" && fact.Name != tc.value) {
					t.Fatal("identity shortened or adapter limit exceeded", fact)
				}
			}
		})
	}
}
