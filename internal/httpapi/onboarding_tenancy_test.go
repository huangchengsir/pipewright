package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/onboarding"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOnboardingInstanceSnapshotDeniedToEmployees(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	employee, err := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var epoch uint64
	var team string
	if err = f.db.QueryRow(`SELECT mode_epoch,default_team_id FROM access_control WHERE id=1`).Scan(&epoch, &team); err != nil {
		t.Fatal(err)
	}
	service := onboarding.New(f.db, onboarding.Capabilities{Runtime: onboarding.RuntimeAvailable})
	handler := New(nil, tenancy.NewAuthenticator(accounts, epoch), WithTenancyAccounts(accounts), WithOnboarding(service))
	for _, tc := range []struct {
		token string
		code  int
	}{{employee.Token, 403}, {admin.Token, 200}} {
		req := httptest.NewRequest("GET", "/api/onboarding/status?projectId=foreign", nil)
		req.Header.Set("X-Pipewright-Team", team)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: tc.token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.code == 200 {
			var snapshot onboarding.Snapshot
			if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{"p": true, "outside": true, "foreign": true}
			if snapshot.ProjectCount == nil || *snapshot.ProjectCount != len(want) || len(snapshot.Projects) != len(want) {
				t.Fatalf("unexpected administrator snapshot size: %s", w.Body.String())
			}
			for _, project := range snapshot.Projects {
				if !want[project.ID] {
					t.Fatalf("unexpected administrator project %q", project.ID)
				}
				delete(want, project.ID)
			}
		}
	}
	// Domain callers cannot accidentally bypass the HTTP restriction.
	denied := service.Status(f.ctx, "foreign")
	if denied.ProjectCount != nil || denied.RunCount != nil || len(denied.Projects) != 0 || denied.SelectedProject != nil || denied.Success != nil || denied.LatestRun != nil || denied.Runtime != onboarding.RuntimeUnknown {
		t.Fatalf("employee received instance data: %#v", denied)
	}
}
