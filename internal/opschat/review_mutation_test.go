package opschat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

func TestApprovalRecheckedAfterWaitingForSSHSlot(t *testing.T) {
	for _, change := range []string{"expiry", "instance", "binding"} {
		t.Run(change, func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			s, _, db := fixture(t, f, nil)
			c := chat(t, s, id)
			r := submitTool(t, s, c, "systemd_action", `{"unit":"nginx.service","action":"restart"}`)
			a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
			waitJobs(t, s)
			release, err := s.takeSlot(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if release != nil {
					release()
				}
			}()
			if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
				t.Fatal(err)
			}
			// Wait until the worker passed its earlier audit/approval checks and is
			// blocked in takeSlot. The second check must observe our change below.
			deadline := time.Now().Add(time.Second)
			for {
				var intents int
				if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='ops_chat_intent'").Scan(&intents); err != nil {
					t.Fatal(err)
				}
				if intents == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not reach dispatch wait")
				}
				time.Sleep(time.Millisecond)
			}
			// Early validation and Capture complete before entering the held slot.
			time.Sleep(20 * time.Millisecond)
			switch change {
			case "expiry":
				_, err = db.Exec("UPDATE ops_chat_approvals SET expires_at=? WHERE run_id=?", time.Now().Add(-time.Second).UnixMilli(), r.ID)
			case "instance":
				_, err = db.Exec("UPDATE ops_chat_approvals SET instance_id=? WHERE run_id=?", uuid.NewString(), r.ID)
			case "binding":
				record, e := s.approval(context.Background(), db, c.ID, r.ID)
				if e != nil {
					t.Fatal(e)
				}
				record.Body.Calls[0].Object = "different.service"
				body, e := s.seal(record.Body)
				if e != nil {
					t.Fatal(e)
				}
				_, err = db.Exec("UPDATE ops_chat_approvals SET body=? WHERE run_id=?", body, r.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			release()
			release = nil
			snap := waitRun(t, s, c.ID, r.ID, Failed)
			waitJobs(t, s)
			if f.countMutation() != 0 || snap.Calls[0].Status != Interrupted || snap.Session.ActiveRunID != "" {
				t.Fatal("changed approval dispatched after slot wait", snap.Calls, f.countMutation())
			}
			record, err := s.call(context.Background(), db, c.ID, snap.Calls[0].ID)
			if err != nil || record.Body.Dispatched {
				t.Fatal("undispatched blocked action marked unknown", record, err)
			}
		})
	}
}

func TestReadonlyMutationVerificationAllowedAfterApprovalExpires(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[1] == "show" {
			return &target.LimitedResult{Stdout: "active\n"}, nil
		}
		if _, err := db.Exec("UPDATE ops_chat_approvals SET expires_at=?", time.Now().Add(-time.Second).UnixMilli()); err != nil {
			t.Error(err)
		}
		return &target.LimitedResult{}, nil
	}
	c := chat(t, s, id)
	r := submitTool(t, s, c, "systemd_action", `{"unit":"nginx.service","action":"restart"}`)
	a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
	waitJobs(t, s)
	if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	if f.countMutation() != 1 || snap.Calls[0].Status != Succeeded {
		t.Fatal("expiry blocked read-only verification", snap.Calls)
	}
}

func TestDockerMutationVerificationRequiresExplicitBooleanState(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "pause", "unpause"} {
		for _, raw := range []string{
			`null`, `{}`, `{"Running":false}`, `{"Paused":false}`,
			`{"Running":null,"Paused":false}`, `{"Running":false,"Paused":null}`,
			`{"Running":"false","Paused":false}`, `{"Running":false,"Paused":0}`,
			`[]`, `{"Running":false,"Paused":false} trailing`,
		} {
			t.Run(action+"/"+raw, func(t *testing.T) {
				id := uuid.NewString()
				f := executor(id)
				f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
					if argv[1] == "inspect" && argv[len(argv)-2] == "{{.Id}}" {
						return &target.LimitedResult{Stdout: strings.Repeat("a", 64)}, nil
					}
					if argv[1] == "inspect" {
						return &target.LimitedResult{Stdout: raw}, nil
					}
					return &target.LimitedResult{}, nil
				}
				s, _, _ := fixture(t, f, nil)
				c := chat(t, s, id)
				args, _ := json.Marshal(ToolArgs{Container: "app", Action: action})
				r := submitTool(t, s, c, "docker_action", string(args))
				a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
				waitJobs(t, s)
				if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
					t.Fatal(err)
				}
				snap := waitRun(t, s, c.ID, r.ID, Unknown)
				waitJobs(t, s)
				if snap.Calls[0].Status != Unknown || f.countMutation() != 1 {
					t.Fatal(snap.Calls)
				}
			})
		}
	}
}
