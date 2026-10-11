package opschat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

type failedQuestionAnalysisModel struct{ *continuationModel }

func (m *failedQuestionAnalysisModel) Analyze(_ context.Context, in AnalysisRequest) (string, error) {
	m.mu.Lock()
	m.analyses = append(m.analyses, in)
	m.mu.Unlock()
	return "", errors.New("fake analysis failure")
}

func TestConfirmationDoesNotBindAfterAutomaticAnalysisFailure(t *testing.T) {
	id := uuid.NewString()
	f := continuationExecutor(t, id)
	firstPlan := discoveryPlan(0)
	firstPlan.NeedsDiscovery = false
	m := &failedQuestionAnalysisModel{&continuationModel{fakeModel: model(), steps: []Plan{firstPlan, {Text: "Please specify the inquiry."}}}}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	first, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Inspect Redis keys", AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, first.ID, Succeeded)
	waitJobs(t, s)
	previous, err := s.run(context.Background(), s.db, c.ID, first.ID)
	if err != nil || !previous.Body.AnalysisStarted || previous.Body.Analysis != nil {
		t.Fatal("not the persisted automatic analysis state", previous.Body, err)
	}
	c, err = s.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, second.ID, Succeeded)
	waitJobs(t, s)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.plans) != 2 || m.plans[1].Question != "yes" || len(m.analyses) != 1 || len(execInvocations(f)) != 0 {
		t.Fatal("failed analysis inherited or replayed", m.plans)
	}
}

func TestConfirmationBindsOriginalQuestionWithoutAuthorizingExec(t *testing.T) {
	id := uuid.NewString()
	f := continuationExecutor(t, id)
	m := &continuationModel{fakeModel: model(), steps: []Plan{{Text: "Review the command first."}, containerExecPlan()}}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	const question = "Redis container internal key distribution"
	first, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: question, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, first.ID, Succeeded)
	waitJobs(t, s)
	c, err = s.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "\u786e\u8ba4", AnalyzeResults: true, Provider: m.Info()}
	second, err := s.Submit(context.Background(), c.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, second.ID, AwaitingConfirmation)
	waitJobs(t, s)
	if len(execInvocations(f)) != 0 || len(snap.Confirmations) != 1 {
		t.Fatal("chat confirmation authorized execution")
	}
	m.mu.Lock()
	req := m.plans[1]
	m.mu.Unlock()
	if req.Question != question || req.Messages[len(req.Messages)-1].Text != in.Text {
		t.Fatal("original question or literal user message lost", req)
	}
	record, err := s.run(context.Background(), s.db, c.ID, second.ID)
	if err != nil || record.Body.Question != question || record.Body.Turn.Text != in.Text {
		t.Fatal(record.Body, err)
	}
	if duplicate, err := s.Submit(context.Background(), c.ID, in); err != nil || duplicate.ID != second.ID {
		t.Fatal("duplicate changed binding", duplicate, err)
	}
	approval := snap.Confirmations[0]
	if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: second.ID, Nonce: approval.Nonce, Calls: approval.Calls}); err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, second.ID, Succeeded)
	waitJobs(t, s)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.plans) != 2 || len(m.analyses) != 1 || m.analyses[0].Question != question || len(execInvocations(f)) != 1 {
		t.Fatal("analysis/replay mismatch", m.analyses)
	}
}

func TestConfirmationQuestionBindingBoundaries(t *testing.T) {
	for _, scenario := range []string{"bind", "legacy", "consecutive", "new-question", "no-history", "other-session", "manual-entry", "manual-turn", "analysis", "intervening-analysis", "retry", "reordered-targets", "failed", "cancelled", "stale", "future", "provider", "target", "target-count", "connection", "completed-exec", "failed-discovery", "successful-discovery", "empty-question", "confirmation-only"} {
		t.Run(scenario, func(t *testing.T) {
			ids := []string{uuid.NewString(), uuid.NewString()}
			f, m := executor(ids...), model()
			s, _, _ := fixture(t, f, m)
			c := chat(t, s, ids[0])
			const original = "Inspect Redis keys"
			previous := runRecord{View: Run{ID: uuid.NewString(), SessionID: c.ID, Status: Succeeded, CreatedAt: time.Now().UTC().Add(-time.Minute)}, Body: runBody{Turn: TurnInput{Text: original}, Question: original, Provider: m.Info(), Targets: []target.ConnectionSnapshot{f.snapshots[ids[0]]}}}
			next := runBody{Turn: TurnInput{Text: "\u786e\u8ba4"}, Provider: m.Info(), Targets: []target.ConnectionSnapshot{f.snapshots[ids[0]]}}
			kind, tool, callStatus := "user", "", Succeeded
			want := next.Turn.Text
			switch scenario {
			case "bind":
				want = original
			case "legacy":
				previous.Body.Question = ""
				want = original
			case "consecutive":
				previous.Body.Turn.Text = "yes"
				want = original
			case "new-question":
				next.Turn.Text = "Inspect disk usage"
				want = next.Turn.Text
			case "other-session":
				previous.View.SessionID = chat(t, s, ids[0]).ID
			case "manual-entry":
				kind = "tool_request"
			case "manual-turn":
				previous.Body.Turn.ToolID = "host_ports"
			case "analysis":
				previous.Body.Analysis = &AnalysisPreview{}
			case "retry":
				previous.Body.RetryActions = containerExecPlan().Actions
			case "reordered-targets":
				previous.Body.Targets = append(previous.Body.Targets, f.snapshots[ids[1]])
				next.Targets = []target.ConnectionSnapshot{f.snapshots[ids[1]], f.snapshots[ids[0]]}
			case "failed":
				previous.View.Status = Failed
			case "cancelled":
				previous.View.CancelRequested = true
			case "stale":
				previous.View.CreatedAt = time.Now().Add(-16 * time.Minute)
			case "future":
				previous.View.CreatedAt = time.Now().Add(time.Minute)
			case "provider":
				next.Provider.ConfigHash = strings.Repeat("f", 64)
			case "target":
				next.Targets[0] = f.snapshots[ids[1]]
			case "target-count":
				next.Targets = nil
			case "connection":
				next.Targets[0].Server.Host = "192.0.2.99"
			case "completed-exec":
				tool = "docker_exec"
			case "failed-discovery":
				tool = "docker_containers"
				callStatus = Failed
			case "successful-discovery":
				tool = "docker_containers"
				want = original
			case "empty-question":
				previous.Body.Question = ""
				previous.Body.Turn.Text = ""
			case "confirmation-only":
				previous.Body.Question = "yes"
				previous.Body.Turn.Text = "yes"
			}
			if scenario != "no-history" {
				err := s.persist(previous.View.SessionID, func(ctx context.Context, tx *sql.Tx) error {
					raw, err := s.seal(previous.Body)
					if err != nil {
						return err
					}
					if _, err = tx.Exec(`INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,cancel_requested,body,created_at) VALUES(?,?,?,?,?,?,?,?)`, previous.View.ID, previous.View.SessionID, uuid.NewString(), "hash", previous.View.Status, previous.View.CancelRequested, raw, previous.View.CreatedAt.UnixMilli()); err != nil {
						return err
					}
					if tool != "" {
						call := Call{ID: uuid.NewString(), SessionID: previous.View.SessionID, RunID: previous.View.ID, ServerID: ids[0], ToolID: tool, Status: callStatus, TargetHash: previous.Body.Targets[0].Fingerprint()}
						raw, err = s.seal(callBody{View: call, Snapshot: previous.Body.Targets[0]})
						if err != nil {
							return err
						}
						if _, err = tx.Exec(`INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, call.ID, call.SessionID, call.RunID, call.ServerID, call.ToolID, call.Status, "hash", call.TargetHash, raw, time.Now().UnixMilli(), 0); err != nil {
							return err
						}
					}
					if err := s.append(ctx, tx, Entry{SessionID: previous.View.SessionID, RunID: previous.View.ID, Kind: kind, Text: previous.Body.Turn.Text}, true, true); err != nil {
						return err
					}
					if scenario == "intervening-analysis" {
						analysisID := uuid.NewString()
						raw, err := s.seal(runBody{Analysis: &AnalysisPreview{}, Question: original})
						if err != nil {
							return err
						}
						if _, err = tx.Exec(`INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)`, analysisID, c.ID, uuid.NewString(), "hash", Succeeded, raw, time.Now().UnixMilli()); err != nil {
							return err
						}
						return s.append(ctx, tx, Entry{SessionID: c.ID, RunID: analysisID, Kind: "analysis", Text: "Finished analysis."}, true, false)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			err := s.transaction(context.Background(), false, func(tx *sql.Tx) error {
				got, err := s.turnQuestion(context.Background(), tx, c.ID, next)
				if err == nil && got != want {
					t.Errorf("got %q want %q", got, want)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(f.invocations) != 0 || len(m.plans) != 0 {
				t.Fatal("question binding invoked remote work")
			}
		})
	}
}

func TestShortConfirmationDoesNotMatchNewInstructions(t *testing.T) {
	for _, text := range []string{"\u786e\u8ba4", "\u786e\u8ba4\u6267\u884c\uff01", " YES! ", "okay."} {
		if !shortConfirmation(text) {
			t.Fatal(text)
		}
	}
	for _, text := range []string{"", "confirm remove files", "\u786e\u8ba4\u67e5\u8be2\u5176\u4ed6\u670d\u52a1\u5668", "yes and restart", "Inspect Redis"} {
		if shortConfirmation(text) {
			t.Fatal(text)
		}
	}
}
