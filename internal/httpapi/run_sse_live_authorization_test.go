package httpapi

import (
	"context"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type streamRevocationBus struct {
	events   chan run.Event
	canceled bool
}

func (b *streamRevocationBus) Subscribe(string) (<-chan run.Event, func()) {
	return b.events, func() { b.canceled = true }
}

type streamRevocationWriter struct {
	*httptest.ResponseRecorder
	writes     int
	afterFirst func()
	fail       bool
}

func (w *streamRevocationWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.fail {
		return 0, errors.New("synthetic disconnected writer")
	}
	n, e := w.ResponseRecorder.Write(p)
	if w.writes == 1 && w.afterFirst != nil {
		w.afterFirst()
	}
	return n, e
}
func TestRunSSELiveAuthorizationPerFrame(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		live, fail bool
	}{
		{"history-role-revocation", "role", false, false}, {"history-session-revocation", "session", false, false},
		{"live-role-revocation", "role", true, false}, {"live-session-revocation", "session", true, false},
		{"history-write-failure", "none", false, true}, {"normal-employee-terminal", "none", false, false},
		{"normal-instance-admin-terminal", "admin", false, false}, {"normal-disabled-terminal", "off", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTenantAuxFixture(t)
			ctx := context.Background()
			repo := tenancy.NewRepository(f.db)
			accounts := tenancy.NewAccounts(f.db)
			admin, e := accounts.Login(ctx, "admin", "auxiliary-admin-password")
			if e != nil {
				t.Fatal(e)
			}
			var team, roleID string
			var revision uint64
			if e = f.db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
				t.Fatal(e)
			}
			if e = f.db.QueryRow(`SELECT id,revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'`, team).Scan(&roleID, &revision); e != nil {
				t.Fatal(e)
			}
			save := func(enabled bool) {
				t.Helper()
				g := []authz.Grant{}
				if enabled {
					g = append(g, authz.Grant{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}})
				}
				r, e := repo.SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{Name: "Auxiliary", Pages: []string{"page.runs"}, Grants: g, ExpectedRevision: revision})
				if e != nil {
					t.Fatal(e)
				}
				revision = r.Revision
			}
			save(true)
			status := "success"
			if tc.live {
				status = "running"
			}
			if _, e = f.db.Exec(`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_commit,trigger_actor,created_at) VALUES('sse-run','p',?,'manual','main','','synthetic','2026-01-01T00:00:00Z')`, status); e != nil {
				t.Fatal(e)
			}
			if _, e = f.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id) VALUES('run','sse-run',?,?,'p')`, team, admin.Actor.UserID); e != nil {
				t.Fatal(e)
			}
			if !tc.live {
				for i, text := range []string{"first-sensitive", "second-sensitive", "third-sensitive"} {
					if _, e = f.db.Exec(`INSERT INTO run_logs(id,run_id,seq,ts,stream,step_ordinal,text) VALUES(?,'sse-run',?,'2026-01-01T00:00:00Z','stdout',0,?)`, text, i+1, text); e != nil {
						t.Fatal(e)
					}
				}
			}
			requestctx := f.ctx
			if tc.mode == "admin" {
				requestctx = tenancy.WithActor(ctx, admin.Actor, team)
			}
			if tc.mode == "off" {
				if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Explicit: true}, true); e != nil {
					t.Fatal(e)
				}
				requestctx = ctx
			}
			bus := &streamRevocationBus{events: make(chan run.Event, 2)}
			if tc.live {
				bus.events <- run.Event{Kind: run.EventLog, RunID: "sse-run", Log: run.LogLine{Seq: 1, Ts: time.Now(), Text: "second-sensitive"}}
				bus.events <- run.Event{Kind: run.EventStatus, RunID: "sse-run", Status: "success"}
			}
			close(bus.events)
			w := &streamRevocationWriter{ResponseRecorder: httptest.NewRecorder(), fail: tc.fail}
			if tc.mode == "role" {
				w.afterFirst = func() { save(false) }
			}
			if tc.mode == "session" {
				w.afterFirst = func() {
					if _, e := f.db.Exec(`DELETE FROM access_sessions WHERE user_id=(SELECT id FROM access_users WHERE username='auxmember')`); e != nil {
						t.Fatal(e)
					}
				}
			}
			router := chi.NewRouter()
			router.Get("/runs/{id}/events", makeRunEventsHandler(run.New(f.db), bus))
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/runs/sse-run/events", nil).WithContext(requestctx))
			if !bus.canceled {
				t.Fatal("subscription not released")
			}
			if tc.fail || tc.mode == "role" || tc.mode == "session" {
				if w.writes != 1 || strings.Contains(w.Body.String(), "second-sensitive") {
					t.Fatalf("stream continued beyond authorization/write boundary: writes=%d", w.writes)
				}
			} else {
				if w.writes != 4 || !strings.Contains(w.Body.String(), "third-sensitive") || !strings.Contains(w.Body.String(), `"status":"success"`) {
					t.Fatal("normal terminal replay changed", w.writes, w.Body.String())
				}
			}
		})
	}
}
