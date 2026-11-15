package httpapi

import (
	"context"
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantArtifactDownloadHTTP(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	admin, e := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = f.db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	svc := run.New(f.db)
	r := &run.Run{ID: "artifact-run"}
	if _, e = f.db.Exec(`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_commit,trigger_actor,created_at) VALUES(?,'p','success','manual','main','','admin','2026-11-13T00:00:00Z')`, r.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('run',?,?,?)`, r.ID, team, admin.Actor.UserID); e != nil {
		t.Fatal(e)
	}
	store, e := artifactstore.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	key, size, e := store.Put(strings.NewReader("private artifact bytes"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := svc.AddArtifact(ctx, run.Artifact{RunID: r.ID, Type: "archive", Name: "result", Reference: key, SizeBytes: size, Metadata: map[string]any{"stored": true}})
	if e != nil {
		t.Fatal(e)
	}
	router := chi.NewRouter()
	router.Get("/runs/{id}/artifacts/{artifactId}/download", makeArtifactDownloadHandler(svc, store))
	request := func() *httptest.ResponseRecorder {
		q := httptest.NewRequest("GET", "/runs/"+r.ID+"/artifacts/"+a.ID+"/download", nil).WithContext(f.ctx)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, q)
		return w
	}
	repo := tenancy.NewRepository(f.db)
	var roleID string
	var revision uint64
	if e = f.db.QueryRow(`SELECT id,revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'`, team).Scan(&roleID, &revision); e != nil {
		t.Fatal(e)
	}
	save := func(download bool) {
		t.Helper()
		grants := []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}
		if download {
			grants = append(grants, authz.Grant{Action: "artifact.download", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		}
		role, e := repo.SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{Name: "Auxiliary", ExpectedRevision: revision, Pages: []string{"page.runs"}, Grants: grants})
		if e != nil {
			t.Fatal(e)
		}
		revision = role.Revision
	}
	save(false)
	w := request()
	if w.Code != 403 || strings.Contains(w.Body.String(), "private artifact") {
		t.Fatal("metadata reader", w.Code, w.Body.String())
	}
	save(true)
	w = request()
	if w.Code != 200 || w.Body.String() != "private artifact bytes" {
		t.Fatal("authorized bytes", w.Code, w.Body.String())
	}
	save(false)
	w = request()
	if w.Code != 403 || strings.Contains(w.Body.String(), "private artifact") {
		t.Fatal("revoked download", w.Code, w.Body.String())
	}
}
