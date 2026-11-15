package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/chain"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/cron"
	"github.com/huangchengsir/pipewright/internal/metrics"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/previewenv"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/repocache"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runner"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/trigger"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type tenantAuxAnalyzer struct {
	calls  int
	during func()
}

func (a *tenantAuxAnalyzer) Analyze(context.Context, string, string, string) ai.RepoAnalysis {
	a.calls++
	if a.during != nil {
		a.during()
	}
	return ai.RepoAnalysis{Cloned: true, Signals: []string{"private-analysis"}}
}

type tenantAuxAI struct {
	stubAIService
	calls      int
	during     func()
	riskCalls  int
	duringRisk func()
}

func (a *tenantAuxAI) Generate(context.Context, ai.GenerateInput) (*ai.Proposal, error) {
	a.calls++
	if a.during != nil {
		a.during()
	}
	return sampleProposal(), nil
}

func (a *tenantAuxAI) AnnotateRisks(context.Context, ai.AnnotateRisksInput) (*ai.RiskReport, error) {
	a.riskCalls++
	if a.duringRisk != nil {
		a.duringRisk()
	}
	return &ai.RiskReport{AIEnhanced: true, AIReason: "synthetic-permitted-risk-result"}, nil
}

type tenantAuxSource struct {
	calls  int
	during func()
}

func (s *tenantAuxSource) Tree(context.Context, string, string, string, string, string) (sourceTreeDTO, error) {
	s.calls++
	if s.during != nil {
		s.during()
	}
	return sourceTreeDTO{Entries: []sourceEntryDTO{{Name: "private-file", Path: "private-file", Type: "file"}}}, nil
}
func (s *tenantAuxSource) Blob(context.Context, string, string, string, string, string) (sourceBlobDTO, error) {
	s.calls++
	if s.during != nil {
		s.during()
	}
	return sourceBlobDTO{Content: "private-source"}, nil
}

type tenantAuxProber struct {
	calls  int
	during func()
}

func (p *tenantAuxProber) Probe(context.Context, string, string, string) (string, error) {
	p.calls++
	if p.during != nil {
		p.during()
	}
	return "main", nil
}

type tenantAuxFixture struct {
	prober    *tenantAuxProber
	setGrants func(bool, ...authz.Grant)
	ctx       context.Context
	db        *sql.DB
	request   func(string, string, string) *httptest.ResponseRecorder
	grant     func(bool, bool)
	readScope func(authz.Scope)
	analyzer  *tenantAuxAnalyzer
	model     *tenantAuxAI
	source    *tenantAuxSource
}

func newTenantAuxFixture(t *testing.T) *tenantAuxFixture {
	t.Helper()
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, testMasterKey())
	cred, e := v.Create(vault.CreateInput{Name: "Test Git", Type: "git_token", Secret: "synthetic-local-test-token"})
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"p", "outside", "foreign"} {
		if _, e = db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,'https://example.invalid/repo','main',?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, id, cred.ID); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"p", "outside", "foreign"} {
		if e = configstate.EnsureProject(ctx, db, db, id); e != nil {
			t.Fatal(e)
		}
	}
	if e = auth.NewService(db, nil).Bootstrap("admin", "auxiliary-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='project' AND resource_id='foreign'`, other.ID); e != nil {
		t.Fatal(e)
	}
	for _, record := range []struct{ id, team string }{{"s1", team}, {"s2", team}, {"foreign-server", other.ID}} {
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('server',?,?,?)`, record.id, record.team, admin.User.ID); e != nil {
			t.Fatal(e)
		}
	}
	f := &tenantAuxFixture{db: db, analyzer: &tenantAuxAnalyzer{}, model: &tenantAuxAI{}, source: &tenantAuxSource{}, prober: &tenantAuxProber{}}
	roleID := ""
	var roleRevision uint64
	saveGrants := func(read, edit bool, projectScope authz.Scope, more ...authz.Grant) {
		t.Helper()
		grants := slices.Clone(more)
		if read {
			for _, a := range []string{"project.read", "pipeline.read"} {
				grants = append(grants, authz.Grant{Action: a, Scope: projectScope})
			}
			for _, a := range []string{"credential.metadata", "credential.use"} {
				grants = append(grants, authz.Grant{Action: a, Scope: authz.Scope{Kind: authz.ScopeTeam}})
			}
		}
		if read {
			grants = append(grants, authz.Grant{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1"}}})
		}
		if edit {
			grants = append(grants, authz.Grant{Action: "pipeline.edit", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}})
		}
		role, e := repo.SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{Name: "Auxiliary", ExpectedRevision: roleRevision, Pages: []string{"page.projects", "page.pipeline", "page.vault", "page.servers"}, Grants: grants})
		if e != nil {
			t.Fatal(e)
		}
		roleID = role.ID
		roleRevision = role.Revision
	}
	f.grant = func(read, edit bool) { saveGrants(read, edit, authz.Scope{Kind: authz.ScopeTeam}) }
	f.readScope = func(scope authz.Scope) { saveGrants(true, false, scope) }
	f.setGrants = func(read bool, more ...authz.Grant) {
		saveGrants(read, false, authz.Scope{Kind: authz.ScopeTeam}, more...)
	}
	f.grant(true, true)
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "auxmember", DisplayName: "Member", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: roleID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "auxmember", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "auxiliary-member-password")
	if e != nil {
		t.Fatal(e)
	}
	f.ctx = tenancy.WithActor(ctx, login.Actor, team)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithVault(v), WithProjects(project.New(db, v, f.prober)), WithPipelines(pipeline.New(db)), WithPipelineSettings(pipeline.NewSettingsService(db, v)), WithTriggers(trigger.New(db, v)), WithAISettings(f.model), WithAIGenerate(f.analyzer), WithSource(f.source), WithRefs(f.source), WithMetricsHistory(metrics.New(db)), WithCron(cron.NewService(db)), WithChain(chain.NewService(db)), WithConcurrency(run.NewConcurrencyService(db)), WithParameters(run.NewParameterService(db)), WithRunnerConfig(runner.New(db, nil)), WithConfigManagement(db, nil, false), WithPromotion(promotion.NewStore(db)), WithPreviewEnvs(previewenv.New(db)))
	f.request = func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", team)
		r.Header.Set(headerCsrf, login.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	return f
}
func TestTenantPipelineAuxiliaryDeniedZeroEffects(t *testing.T) {
	f := newTenantAuxFixture(t)
	f.grant(true, false)
	for _, id := range []string{"p", "foreign", "missing"} {
		for _, endpoint := range []string{"ai-generate", "ai-apply", "import"} {
			w := f.request("POST", "/api/projects/"+id+"/pipeline/"+endpoint, `{}`)
			if w.Code != 403 {
				t.Fatal(id, endpoint, w.Code, w.Body.String())
			}
		}
	}
	if f.analyzer.calls != 0 || f.model.calls != 0 || f.source.calls != 0 {
		t.Fatal("denial performed external work")
	}
	var count int
	if e := f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_configs`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatal("denial persisted pipeline")
	}
	for _, endpoint := range []string{"source/tree", "source/blob?path=x", "pac/preview"} {
		w := f.request("GET", "/api/projects/foreign/"+endpoint, "")
		if w.Code != 404 && w.Code != 403 {
			t.Fatal(endpoint, w.Code, w.Body.String())
		}
	}
	if f.source.calls != 0 {
		t.Fatal("cross-team read contacted repository")
	}
}
func TestTenantPipelineAuxiliaryGenerateRevocation(t *testing.T) {
	for _, point := range []string{"analyzer", "model", "unchanged"} {
		t.Run(point, func(t *testing.T) {
			f := newTenantAuxFixture(t)
			if point == "analyzer" {
				f.analyzer.during = func() { f.grant(true, false) }
			}
			if point == "model" {
				f.model.during = func() { f.grant(true, false) }
			}
			w := f.request("POST", "/api/projects/p/pipeline/ai-generate", `{}`)
			if point == "unchanged" {
				if w.Code != 200 || !strings.Contains(w.Body.String(), "private-analysis") {
					t.Fatal(w.Code, w.Body.String())
				}
				return
			}
			if w.Code != 403 || strings.Contains(w.Body.String(), "private-analysis") {
				t.Fatal("revoked result", w.Code, w.Body.String())
			}
			if point == "analyzer" && f.model.calls != 0 {
				t.Fatal("called model after revoke")
			}
		})
	}
}
func TestTenantSourceReadRechecksOwnershipAndConfiguration(t *testing.T) {
	for _, endpoint := range []string{"source/tree", "source/blob?path=x", "pac/preview"} {
		for _, change := range []string{"revoke", "config", "unchanged"} {
			t.Run(endpoint+"/"+change, func(t *testing.T) {
				f := newTenantAuxFixture(t)
				if change == "revoke" {
					f.source.during = func() { f.grant(false, false) }
				}
				if change == "config" {
					f.source.during = func() {
						if _, e := f.db.Exec(`UPDATE projects SET default_branch='other' WHERE id='p'`); e != nil {
							t.Fatal(e)
						}
					}
				}
				w := f.request("GET", "/api/projects/p/"+endpoint, "")
				if f.source.calls != 1 {
					t.Fatal("reader calls", f.source.calls, w.Code, w.Body.String())
				}
				if change == "unchanged" {
					if w.Code != 200 {
						t.Fatal(w.Code, w.Body.String())
					}
					return
				}
				if change == "config" && w.Code != 409 {
					t.Fatal("snapshot conflict", w.Code, w.Body.String())
				}
				if change == "revoke" && w.Code != 403 && w.Code != 404 {
					t.Fatal("revoked result", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "private-") {
					t.Fatal("stale source leaked")
				}
			})
		}
	}
}

func TestTenantMetricsHistoryHTTPAndAtomicRecord(t *testing.T) {
	f := newTenantAuxFixture(t)
	svc := metrics.New(f.db)
	now := time.Now().UTC()
	if e := svc.Record(f.ctx, []metrics.Sample{{ServerID: "s1", At: now}}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"s2", "foreign-server", "missing"} {
		if e := svc.Record(f.ctx, []metrics.Sample{{ServerID: "s1", At: now}, {ServerID: id, At: now}}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("mixed unauthorized batch", id, e)
		}
		w := f.request("GET", "/api/metrics/history?serverId="+id, "")
		if w.Code != 403 {
			t.Fatal(id, w.Code, w.Body.String())
		}
	}
	var n int
	if e := f.db.QueryRow(`SELECT COUNT(*) FROM metric_samples`).Scan(&n); e != nil || n != 1 {
		t.Fatal("partial denied batch", n, e)
	}
	if w := f.request("GET", "/api/metrics/history?serverId=s1", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"points":[{`) {
		t.Fatal("authorized history", w.Code, w.Body.String())
	}
	f.grant(false, false)
	if _, e := svc.QueryRange(f.ctx, "s1", now.Add(-time.Hour)); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked history", e)
	}
	if e := svc.Record(f.ctx, []metrics.Sample{{ServerID: "s1", At: now}}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked sampler", e)
	}
	if e := svc.Record(context.Background(), []metrics.Sample{{ServerID: "s1", At: now}}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("anonymous sampler", e)
	}
	if e := f.db.QueryRow(`SELECT COUNT(*) FROM metric_samples`).Scan(&n); e != nil || n != 1 {
		t.Fatal("revoked write", n, e)
	}
}

func TestTenantProjectConfigurationHTTPReadSurfaces(t *testing.T) {
	f := newTenantAuxFixture(t)
	f.readScope(authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}})
	checkProjects := func(t *testing.T, query, wantTotal string, wantIDs ...string) {
		t.Helper()
		w := f.request("GET", "/api/projects?"+query, "")
		var projects []projectDTO
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &projects) != nil || w.Header().Get("X-Total-Count") != wantTotal {
			t.Fatalf("project list: status=%d total=%s", w.Code, w.Header().Get("X-Total-Count"))
		}
		ids := make([]string, 0, len(projects))
		for _, p := range projects {
			ids = append(ids, p.ID)
		}
		slices.Sort(ids)
		if !slices.Equal(ids, wantIDs) {
			t.Fatalf("unexpected project IDs %v", ids)
		}
	}
	t.Run("projects/selected-first-page", func(t *testing.T) { checkProjects(t, "page=1&pageSize=1", "1", "p") })
	t.Run("projects/selected-past-last-page", func(t *testing.T) { checkProjects(t, "page=2&pageSize=1", "1") })
	endpoints := []string{
		"pipeline", "pipeline/settings", "pipeline/validation", "trigger", "cron",
		"chain", "concurrency", "parameters", "runner", "config-revision",
		"source/tree", "source/blob?path=fixture.txt", "environments", "preview-config",
	}
	request := func(project, endpoint string) *httptest.ResponseRecorder {
		return f.request("GET", "/api/projects/"+project+"/"+endpoint, "")
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			t.Run("selected", func(t *testing.T) {
				w := request("p", endpoint)
				if w.Code != 200 {
					t.Fatalf("selected route returned %d", w.Code)
				}
				if endpoint == "pipeline/validation" && strings.Contains(w.Body.String(), "project_credential_missing") {
					t.Fatal("visible credential incorrectly reported missing")
				}
			})
			for _, scope := range []struct{ name, id string }{
				{"same-team-outside-selection", "outside"}, {"other-team", "foreign"}, {"unknown", "missing"},
			} {
				t.Run(scope.name, func(t *testing.T) {
					before := f.source.calls
					w := request(scope.id, endpoint)
					if (w.Code != 403 && w.Code != 404) || strings.Contains(w.Body.String(), "private-") || w.Header().Get("X-Config-Revision") != "" {
						t.Fatalf("scope denied: status=%d", w.Code)
					}
					if f.source.calls != before {
						t.Fatal("denied scope touched repository")
					}
				})
			}
		})
	}
	f.readScope(authz.Scope{Kind: authz.ScopeTeam})
	t.Run("projects/team-excludes-foreign", func(t *testing.T) { checkProjects(t, "page=1&pageSize=100", "2", "outside", "p") })
	f.grant(false, false)
	t.Run("projects/revoked-same-session", func(t *testing.T) { checkProjects(t, "page=1&pageSize=100", "0") })
	for _, endpoint := range endpoints {
		t.Run(endpoint+"/revoked-same-session", func(t *testing.T) {
			before := f.source.calls
			w := request("p", endpoint)
			if (w.Code != 403 && w.Code != 404) || w.Header().Get("X-Config-Revision") != "" {
				t.Fatalf("revoked route returned %d", w.Code)
			}
			if f.source.calls != before {
				t.Fatal("revoked request touched repository")
			}
		})
	}
	for _, table := range []string{"pipeline_configs", "pipeline_settings", "pipeline_triggers", "project_crons", "project_chain_targets", "project_concurrency", "project_parameters", "project_runners"} {
		var count int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("permission read modified %s: %d, %v", table, count, err)
		}
	}
}

func TestTenantConfigRevisionDropsPayloadOnConcurrentRevocation(t *testing.T) {
	f := newTenantAuxFixture(t)
	f.readScope(authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}})
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		f.grant(false, false)
		w.Write([]byte("private configuration payload"))
	})
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p/pipeline", nil).WithContext(f.ctx)
	w := httptest.NewRecorder()
	configRevisionMiddleware(f.db, false)(next).ServeHTTP(w, req)
	if !called || w.Code != 403 || w.Header().Get("X-Config-Revision") != "" || strings.Contains(w.Body.String(), "private configuration payload") {
		t.Fatalf("revoked response escaped buffer: called=%v status=%d", called, w.Code)
	}
}

func (s *tenantAuxSource) ListRefs(context.Context, string, string, string) (*repocache.Refs, error) {
	s.calls++
	if s.during != nil {
		s.during()
	}
	return &repocache.Refs{Branches: []repocache.Ref{{Name: "fixture-branch"}}}, nil
}

func (s *tenantAuxSource) ListCommits(context.Context, string, string, string, string, int) ([]repocache.Commit, error) {
	s.calls++
	if s.during != nil {
		s.during()
	}
	return []repocache.Commit{{Subject: "fixture-commit"}}, nil
}

func TestTenantProjectRepositoryPickersRespectLiveScope(t *testing.T) {
	f := newTenantAuxFixture(t)
	selected := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}
	for _, tc := range []struct{ suffix, marker string }{{"refs", "fixture-branch"}, {"commits", "fixture-commit"}} {
		t.Run(tc.suffix, func(t *testing.T) {
			request := func(id string) *httptest.ResponseRecorder {
				return f.request(http.MethodGet, "/api/projects/"+id+"/"+tc.suffix, "")
			}
			f.readScope(selected)
			before := f.source.calls
			t.Run("selected", func(t *testing.T) {
				if w := request("p"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.marker) || f.source.calls != before+1 {
					t.Fatalf("selected picker = %d %s", w.Code, w.Body.String())
				}
			})
			for scenario, id := range map[string]string{"other-project": "outside", "cross-team": "foreign", "missing": "absent"} {
				t.Run(scenario, func(t *testing.T) {
					before := f.source.calls
					if w := request(id); (w.Code != http.StatusForbidden && w.Code != http.StatusNotFound) || strings.Contains(w.Body.String(), tc.marker) || f.source.calls != before {
						t.Fatalf("denied picker = %d %s, calls delta=%d", w.Code, w.Body.String(), f.source.calls-before)
					}
				})
			}
			t.Run("revoke-during-read", func(t *testing.T) {
				f.source.during = func() { f.grant(false, false) }
				t.Cleanup(func() { f.source.during = nil })
				before := f.source.calls
				if w := request("p"); (w.Code != http.StatusForbidden && w.Code != http.StatusNotFound) || strings.Contains(w.Body.String(), tc.marker) || f.source.calls != before+1 {
					t.Fatalf("revoked picker = %d %s", w.Code, w.Body.String())
				}
			})
		})
	}
}

func TestTenantProjectEditorWritesStayWithinScope(t *testing.T) {
	f := newTenantAuxFixture(t)
	for _, tc := range []struct{ name, table, body string }{
		{"pipeline", "pipeline_configs", "{\"stages\":[{\"name\":\"Fixture source\",\"kind\":\"source\",\"jobs\":[{\"name\":\"Fixture git\",\"type\":\"git_source\",\"config\":{}}]}]}"},
		{"pipeline/settings", "pipeline_settings", "{\"build\":{\"model\":\"dockerfile\",\"artifactType\":\"image\"},\"environments\":[]}"},
		{"cron", "project_crons", "{\"expression\":\"0 0 * * *\",\"branch\":\"main\",\"enabled\":false}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutate := func(id string) *httptest.ResponseRecorder {
				return f.request(http.MethodPut, "/api/projects/"+id+"/"+tc.name, tc.body)
			}
			count := func(want int) {
				t.Helper()
				var n int
				if err := f.db.QueryRow("SELECT COUNT(*) FROM " + tc.table).Scan(&n); err != nil || n != want {
					t.Fatalf("%s persisted rows = %d, want %d: %v", tc.table, n, want, err)
				}
			}
			denied := func(id string) {
				t.Helper()
				w := mutate(id)
				if (w.Code != http.StatusForbidden && w.Code != http.StatusNotFound) || w.Header().Get("X-Config-Revision") != "" {
					t.Fatalf("denied write (%s) = %d %s", id, w.Code, w.Body.String())
				}
			}
			f.grant(true, false)
			t.Run("read-only", func(t *testing.T) { denied("p"); count(0) })
			f.grant(true, true)
			for scenario, id := range map[string]string{"other-project": "outside", "cross-team": "foreign", "missing": "absent"} {
				t.Run(scenario, func(t *testing.T) { denied(id); count(0) })
			}
			t.Run("selected", func(t *testing.T) {
				if w := mutate("p"); w.Code != http.StatusOK || w.Header().Get("X-Config-Revision") == "" {
					t.Fatalf("selected write = %d %s", w.Code, w.Body.String())
				}
				count(1)
				var project string
				if err := f.db.QueryRow("SELECT project_id FROM " + tc.table).Scan(&project); err != nil || project != "p" {
					t.Fatalf("actual owner = %q: %v", project, err)
				}
			})
			f.grant(true, false)
			t.Run("revoked", func(t *testing.T) {
				before, err := configstate.ReadRevision(context.Background(), f.db, "p")
				if err != nil {
					t.Fatal(err)
				}
				denied("p")
				after, err := configstate.ReadRevision(context.Background(), f.db, "p")
				if after != before || err != nil {
					t.Fatalf("denied write changed revision from %d to %d: %v", before, after, err)
				}
				count(1)
			})
		})
	}
}

func TestTenantProjectCreateCloneAndDeleteActions(t *testing.T) {
	f := newTenantAuxFixture(t)
	var credential string
	if err := f.db.QueryRow("SELECT credential_id FROM projects WHERE id='p'").Scan(&credential); err != nil {
		t.Fatal(err)
	}
	create := authz.Grant{Action: "project.create", Scope: authz.Scope{Kind: authz.ScopeTeam}}
	remove := authz.Grant{Action: "project.delete", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}}
	input, err := json.Marshal(map[string]string{
		"name": "Fixture created", "repoUrl": "https://example.invalid/project.git", "credentialId": credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertResponse := func(t *testing.T, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := f.request(method, path, body)
		if w.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	assertProjectCount := func(t *testing.T, want int) {
		t.Helper()
		var n int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&n); n != want || err != nil {
			t.Fatalf("projects = %d, want %d: %v", n, want, err)
		}
	}

	t.Run("create-action", func(t *testing.T) {
		f.setGrants(true)
		assertResponse(t, http.MethodPost, "/api/projects", string(input), http.StatusForbidden)
		if f.prober.calls != 0 {
			t.Fatal("missing action triggered checkout test")
		}
		assertProjectCount(t, 3)
		f.setGrants(false, create)
		assertResponse(t, http.MethodPost, "/api/projects", string(input), http.StatusForbidden)
		if f.prober.calls != 0 {
			t.Fatal("missing credential use triggered checkout test")
		}
		assertProjectCount(t, 3)
		f.setGrants(true, create)
		f.prober.during = func() { f.setGrants(true) }
		assertResponse(t, http.MethodPost, "/api/projects", string(input), http.StatusForbidden)
		f.prober.during = nil
		assertProjectCount(t, 3)
		f.setGrants(true, create)
		w := assertResponse(t, http.MethodPost, "/api/projects", string(input), http.StatusCreated)
		var added projectDTO
		if err := json.Unmarshal(w.Body.Bytes(), &added); err != nil || added.ID == "" {
			t.Fatalf("missing created project: %v", err)
		}
		var owner string
		if err := f.db.QueryRow("SELECT owner_id FROM access_ownership WHERE resource_kind='project' AND resource_id=?", added.ID).Scan(&owner); err != nil || owner == "" {
			t.Fatalf("missing created project owner: %v", err)
		}
		assertProjectCount(t, 4)
	})

	t.Run("clone-check", func(t *testing.T) {
		f.setGrants(false)
		before := f.prober.calls
		assertResponse(t, http.MethodPost, "/api/projects/test-clone", string(input), http.StatusForbidden)
		if f.prober.calls != before {
			t.Fatal("denied clone check reached repository adapter")
		}
		f.setGrants(true)
		assertResponse(t, http.MethodPost, "/api/projects/test-clone", string(input), http.StatusOK)
		f.prober.during = func() { f.setGrants(false) }
		defer func() { f.prober.during = nil }()
		assertResponse(t, http.MethodPost, "/api/projects/test-clone", string(input), http.StatusForbidden)
		if f.prober.calls != before+2 {
			t.Fatal("authorized clone check did not reach the adapter")
		}
		assertProjectCount(t, 4)
	})

	t.Run("delete-action", func(t *testing.T) {
		f.setGrants(true)
		assertResponse(t, http.MethodDelete, "/api/projects/p", "", http.StatusForbidden)
		f.setGrants(true, remove)
		for _, project := range []string{"outside", "foreign", "absent"} {
			assertResponse(t, http.MethodDelete, "/api/projects/"+project, "", http.StatusForbidden)
		}
		assertProjectCount(t, 4)
		assertResponse(t, http.MethodDelete, "/api/projects/p", "", http.StatusNoContent)
		assertProjectCount(t, 3)
	})
}

func TestTenantPipelineRiskAnalysisRequiresLiveEditorScope(t *testing.T) {
	f := newTenantAuxFixture(t)
	self := "/api/projects/p/pipeline/analyze-risks"
	f.grant(true, false)
	if w := f.request("POST", self, ""); w.Code != 403 || f.model.riskCalls != 0 {
		t.Fatalf("read-only role reached risk action: %d %s", w.Code, w.Body.String())
	}
	f.grant(true, true)
	for _, project := range []string{"outside", "foreign", "missing"} {
		w := f.request("POST", "/api/projects/"+project+"/pipeline/analyze-risks", "")
		if w.Code != 403 || f.model.riskCalls != 0 {
			t.Fatalf("unselected project reached risk action: %d %s", w.Code, w.Body.String())
		}
	}
	if w := f.request("POST", self, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic-permitted-risk-result") || f.model.riskCalls != 1 {
		t.Fatalf("selected editor did not get risk result: %d %s", w.Code, w.Body.String())
	}
	f.model.duringRisk = func() { f.grant(true, false) }
	if w := f.request("POST", self, ""); w.Code != 403 || strings.Contains(w.Body.String(), "synthetic-permitted-risk-result") || f.model.riskCalls != 2 {
		t.Fatalf("revoked editor retained risk result: %d %s", w.Code, w.Body.String())
	}
}
