package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

func assertTargetNames(t *testing.T, page EntryPage, run, kind string, want map[string]string) {
	t.Helper()
	found := false
	for _, e := range page.Entries {
		if e.RunID == run && e.Kind == kind {
			found = true
			if !reflect.DeepEqual(e.TargetNames, want) {
				t.Fatalf("%s names=%v want=%v", kind, e.TargetNames, want)
			}
		}
	}
	if !found {
		t.Fatalf("missing %s entry for %s", kind, run)
	}
}

func TestTargetNamesAutomaticManualFrozenAndRestart(t *testing.T) {
	ctx := context.Background()
	a, b, current := uuid.NewString(), uuid.NewString(), uuid.NewString()
	nameA, nameB := "\u6d4b\u8bd5", "frozen-backup"
	f := executor(a, b, current)
	for id, name := range map[string]string{a: nameA, b: nameB} {
		snap := f.snapshots[id]
		snap.Server.Name = name
		f.snapshots[id] = snap
	}
	m := model()
	m.plan.Text = "target_0 compares target_1"
	for _, tool := range []string{"host_ports", "host_processes"} {
		m.plan.Actions = append(m.plan.Actions, Action{ToolID: tool, Args: json.RawMessage(`{}`), TargetIndexes: []int{0, 1}})
	}
	s, v, db := fixture(t, f, m)
	c := chat(t, s, a, b)
	r, err := s.Submit(ctx, c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "compare resources", AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	auto := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	assistantNames := map[string]string{"target_0": nameA, "target_1": nameB}
	autoNames := map[string]string{"target_A": nameA, "target_B": nameB}
	assertTargetNames(t, auto.Entries, r.ID, "assistant", assistantNames)
	assertTargetNames(t, auto.Entries, r.ID, "analysis", autoNames)
	calls, err := s.calls(ctx, db, c.ID, r.ID)
	if err != nil || len(calls) != 4 {
		t.Fatal(calls, err)
	}
	// Subset order differs from ordinals, with a repeated server on a distinct call.
	ids := []string{calls[3].Body.View.ID, calls[0].Body.View.ID, calls[2].Body.View.ID}
	manual, err := s.Analyze(ctx, c.ID, AnalysisInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: ids, AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	manualSnap := waitRun(t, s, c.ID, manual.ID, Succeeded)
	waitJobs(t, s)
	manualNames := map[string]string{"target_A": nameB, "target_B": nameA}
	assertTargetNames(t, manualSnap.Entries, manual.ID, "analysis", manualNames)
	stored, err := s.run(ctx, db, c.ID, manual.ID)
	if err != nil || stored.Body.Analysis == nil || stored.Body.Analysis.Items != nil || !reflect.DeepEqual(stored.Body.Analysis.CallIDs, ids) {
		t.Fatal("manual references not preserved", stored.Body.Analysis, err)
	}
	// Neither later selection nor renamed/deleted inventory can rebind old replies.
	selected := []string{current}
	if _, err = s.Patch(ctx, c.ID, PatchInput{Revision: c.Revision, ServerIDs: &selected}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	for id, snap := range f.snapshots {
		snap.Server.Name = "renamed-current-inventory"
		f.snapshots[id] = snap
	}
	delete(f.snapshots, a)
	f.mu.Unlock()
	page, err := s.Entries(ctx, c.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	assertTargetNames(t, page, r.ID, "assistant", assistantNames)
	assertTargetNames(t, page, r.ID, "analysis", autoNames)
	assertTargetNames(t, page, manual.ID, "analysis", manualNames)
	m.mu.Lock()
	requests, err := json.Marshal(struct {
		Plans    []PlanRequest
		Analyses []AnalysisRequest
	}{m.plans, m.analyses})
	if len(m.plans) != 1 || len(m.analyses) != 2 || m.analyses[1].Items[0].TargetAlias != "target_A" || m.analyses[1].Items[1].TargetAlias != "target_B" {
		t.Error("unexpected captured alias ordering")
	}
	m.mu.Unlock()
	if err != nil || strings.Contains(string(requests), nameA) || strings.Contains(string(requests), nameB) || strings.Contains(string(requests), "targetNames") {
		t.Fatal("local names entered model requests", string(requests), err)
	}
	messages, err := s.contextMessages(ctx, c.ID)
	encoded, _ := json.Marshal(messages)
	if err != nil || strings.Contains(string(encoded), nameA) || strings.Contains(string(encoded), nameB) {
		t.Fatal("local names entered planning history", string(encoded), err)
	}
	assertNoPersistedTargetNames(t, s, db, c.ID)
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Read projection works after restart without a model or server inventory.
	next, err := New(Options{DB: db, Vault: v})
	if err != nil {
		t.Fatal(err)
	}
	if err = next.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer next.Close(ctx)
	restored, err := next.Snapshot(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertTargetNames(t, restored.Entries, r.ID, "assistant", assistantNames)
	assertTargetNames(t, restored.Entries, manual.ID, "analysis", manualNames)
	replay, err := next.Events(ctx, c.ID, EventCursor(c.ID, 0), 100)
	if err != nil {
		t.Fatal(err)
	}
	assertTargetNames(t, replay, r.ID, "analysis", autoNames)
	assertTargetNames(t, replay, manual.ID, "analysis", manualNames)
}

func assertNoPersistedTargetNames(t *testing.T, s *Service, db *sql.DB, session string) {
	t.Helper()
	for _, table := range []string{"ops_chat_entries", "ops_chat_runs"} {
		rows, err := db.Query("SELECT body FROM "+table+" WHERE session_id=?", session)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var raw []byte
			var body map[string]json.RawMessage
			if err = rows.Scan(&raw); err == nil {
				err = s.open(raw, &body)
			}
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for key := range body {
				if strings.EqualFold(key, "targetNames") {
					rows.Close()
					t.Fatal("display mapping persisted", table)
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func seedTargetNameRun(t *testing.T, s *Service, session string, body runBody, calls []Call) string {
	t.Helper()
	run := uuid.NewString()
	err := s.transaction(context.Background(), false, func(tx *sql.Tx) error {
		raw, err := s.seal(body)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", run, session, uuid.NewString(), "hash", Succeeded, raw, time.Now().UnixMilli()); err != nil {
			return err
		}
		for i, call := range calls {
			call.RunID, call.SessionID = run, session
			if call.ID == "" {
				call.ID = uuid.NewString()
			}
			raw, err = s.seal(callBody{View: call})
			if err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)", call.ID, session, run, call.ServerID, "host_ports", Succeeded, "hash", "hash", raw, time.Now().UnixMilli(), i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

type targetNameQueryCounter struct {
	querier
	runs, calls int
}

func (q *targetNameQueryCounter) count(query string) {
	if strings.Contains(query, "FROM ops_chat_runs") {
		q.runs++
	}
	if strings.Contains(query, "FROM ops_chat_calls") {
		q.calls++
	}
}
func (q *targetNameQueryCounter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q.count(query)
	return q.querier.QueryRowContext(ctx, query, args...)
}
func (q *targetNameQueryCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.count(query)
	return q.querier.QueryContext(ctx, query, args...)
}

func TestTargetNamesOldEntriesAndStreamRevisionsCachePerKind(t *testing.T) {
	ctx := context.Background()
	s, _, db := fixture(t, executor(), nil)
	c := chat(t, s)
	s.masker.RegisterSecret("projectionsecret987")
	run := seedTargetNameRun(t, s, c.ID, runBody{Targets: []target.ConnectionSnapshot{{Server: target.Server{ID: "frozen", Name: "planning-name"}}}}, []Call{{ServerID: "frozen", ServerName: "analysis-name projectionsecret987"}})
	err := s.transaction(ctx, false, func(tx *sql.Tx) error {
		for i := 0; i < 10; i++ {
			for _, kind := range []string{"assistant", "analysis"} {
				if err := s.append(ctx, tx, Entry{SessionID: c.ID, RunID: run, Kind: kind, Text: "target_A and target_0", TargetNames: map[string]string{"target_A": "never-persist-this"}}, true, kind == "assistant"); err != nil {
					return err
				}
				var seq int64
				if err := tx.QueryRow("SELECT seq FROM ops_chat_sessions WHERE id=?", c.ID).Scan(&seq); err != nil {
					return err
				}
				if err := s.append(ctx, tx, Entry{SessionID: c.ID, RunID: run, Kind: "entry_updated", ReplaceSeq: seq}, false, false); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, events := range []bool{false, true} {
		var page EntryPage
		var counter *targetNameQueryCounter
		err = s.transaction(ctx, false, func(tx *sql.Tx) error {
			counter = &targetNameQueryCounter{querier: tx}
			var err error
			page, err = s.entryPage(ctx, counter, c.ID, "", 100, events)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if counter.runs != 2 || counter.calls != 1 {
			t.Fatalf("per-entry query fanout: events=%v runs=%d calls=%d", events, counter.runs, counter.calls)
		}
		assertTargetNames(t, page, run, "assistant", map[string]string{"target_0": "planning-name"})
		assertTargetNames(t, page, run, "analysis", map[string]string{"target_A": "analysis-name [MASKED]"})
		for _, e := range page.Entries {
			if e.RunID == run && e.Text != "target_A and target_0" {
				t.Fatal("projection rewrote stored text", e)
			}
		}
		if events {
			found := false
			for _, e := range page.Entries {
				found = found || e.ReplaceSeq != 0 && len(e.TargetNames) != 0
			}
			if !found {
				t.Fatal("entry_updated not enriched after dereference")
			}
		}
		mutated := false
		for _, entry := range page.Entries {
			if entry.Kind != "assistant" {
				continue
			}
			if !mutated {
				entry.TargetNames["target_0"] = "caller-mutated"
				mutated = true
			} else if entry.TargetNames["target_0"] == "caller-mutated" {
				t.Fatal("public projections share cached mutable maps")
			}
		}
	}
	assertNoPersistedTargetNames(t, s, db, c.ID)
}

func TestPublicEntryTargetNamesClosedAliasesCopiedAndScrubbed(t *testing.T) {
	s, _, _ := fixture(t, executor(), nil)
	s.masker.RegisterSecret("projectionsecret987")
	input := map[string]string{"target_0": "projectionsecret987", "target_A": strings.Repeat("\u6d4b", 400), "target_7": "last", "target_H": "last-letter", "target_8": "invalid", "target_I": "invalid", "target_A-extra": "invalid", "shell": "invalid", "target_00": "invalid", "target_a": "invalid", "target_B": ""}
	e := s.publicEntry(Entry{TargetNames: input})
	if len(e.TargetNames) != 4 || e.TargetNames["target_0"] != "[MASKED]" || len(e.TargetNames["target_A"]) > 1024 || !utf8.ValidString(e.TargetNames["target_A"]) {
		t.Fatal(e.TargetNames)
	}
	e.TargetNames["target_0"] = "changed"
	if input["target_0"] != "projectionsecret987" {
		t.Fatal("input map mutated")
	}
	s.masker.RegisterSecret("target_H")
	if _, ok := s.publicEntry(Entry{TargetNames: input}).TargetNames["target_H"]; ok {
		t.Fatal("registered secret exposed in alias key")
	}
	raw, err := json.Marshal(Entry{})
	if err != nil || strings.Contains(string(raw), "targetNames") {
		t.Fatal("empty field not omitted", string(raw), err)
	}
}

func TestTargetNamesMissingBindingsAndBounds(t *testing.T) {
	ctx := context.Background()
	s, _, db := fixture(t, executor(), nil)
	c := chat(t, s)
	missing, err := s.entryTargetNames(ctx, db, c.ID, entryTargetKey{run: uuid.NewString(), kind: "analysis"})
	if err != nil || len(missing) != 0 {
		t.Fatal(missing, err)
	}
	run := seedTargetNameRun(t, s, c.ID, runBody{}, []Call{{ServerID: "unnamed"}, {ServerID: "named", ServerName: "second"}, {ServerID: "unnamed", ServerName: "later-name-not-first"}})
	names, err := s.entryTargetNames(ctx, db, c.ID, entryTargetKey{run: run, kind: "analysis"})
	if err != nil || !reflect.DeepEqual(names, map[string]string{"target_B": "second"}) {
		t.Fatal("unnamed binding shifted aliases", names, err)
	}
	for _, test := range []struct {
		name  string
		body  runBody
		calls []Call
		kind  string
	}{
		{name: "missing-manual-call", body: runBody{Analysis: &AnalysisPreview{CallIDs: []string{uuid.NewString()}}}, kind: "analysis"},
		{name: "too-many-manual-calls", body: runBody{Analysis: &AnalysisPreview{CallIDs: make([]string, 25)}}, kind: "analysis"},
		{name: "too-many-targets", body: runBody{Targets: make([]target.ConnectionSnapshot, 9)}, kind: "assistant"},
		{name: "unknown-kind", kind: "user"},
		{name: "unknown-server", calls: []Call{{ServerName: "unbound-name"}}, kind: "analysis"},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := seedTargetNameRun(t, s, c.ID, test.body, test.calls)
			names, err := s.entryTargetNames(ctx, db, c.ID, entryTargetKey{run: run, kind: test.kind})
			if err != nil || len(names) != 0 {
				t.Fatal(names, err)
			}
		})
	}
	for _, count := range []int{24, 25} {
		calls := make([]Call, count)
		for i := range calls {
			calls[i] = Call{ServerID: "one-server", ServerName: "frozen"}
		}
		run := seedTargetNameRun(t, s, c.ID, runBody{}, calls)
		names, err := s.entryTargetNames(ctx, db, c.ID, entryTargetKey{run: run, kind: "analysis"})
		if err != nil || count == 24 && len(names) != 1 || count == 25 && len(names) != 0 {
			t.Fatal("call cap", count, names, err)
		}
	}
	for _, count := range []int{8, 9} {
		calls := make([]Call, count)
		for i := range calls {
			calls[i] = Call{ServerID: uuid.NewString(), ServerName: "frozen"}
		}
		run := seedTargetNameRun(t, s, c.ID, runBody{}, calls)
		names, err := s.entryTargetNames(ctx, db, c.ID, entryTargetKey{run: run, kind: "analysis"})
		if err != nil || count == 8 && len(names) != 8 || count == 9 && len(names) != 0 {
			t.Fatal("server cap", count, names, err)
		}
	}
}
