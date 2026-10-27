package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/prstatus"
	"github.com/huangchengsir/pipewright/internal/repocache"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type scopedHTTPVault struct {
	vault.Vault
	active   bool
	released int
	err      error
}

func TestHTTPAnonymousFallbackRetainsActualMaintenanceAdmission(t *testing.T) {
	for _, mode := range []string{"missing", "unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			st := testStoreAuth(t)
			raw := vault.New(st.DB, testMasterKey())
			if mode == "unconfigured" {
				if _, err := st.DB.Exec(`INSERT INTO credentials(id,name,type,scope,username,ciphertext,masked_value,created_at,updated_at) VALUES('credential','fixture','git_token','global','',X'00','masked','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
					t.Fatal(err)
				}
				raw = vault.New(st.DB, nil)
			}
			svc := oauth.New(st.DB, raw, nil)
			managed := oauth.ManagedVault(svc, raw)
			calls := 0
			reader := scopedHTTPRefs{check: func() {
				calls++
				if err := maintenance.Fence(context.Background(), st.DB, "fixture-update"); !errors.Is(err, maintenance.ErrBlocked) {
					t.Fatal("anonymous network read did not block installation", err)
				}
			}}
			h := makeListRefsHandler(scopedHTTPProject{}, managed, reader)
			r := httptest.NewRequest(http.MethodGet, "http://instance.test/refs", nil)
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != 200 || calls != 1 {
				t.Fatal("legacy public fallback failed", w.Code)
			}
			if err := maintenance.Fence(context.Background(), st.DB, "fixture-update"); err != nil {
				t.Fatal("anonymous reader scope leaked", err)
			}
			defer maintenance.Unfence(context.Background(), st.DB, "fixture-update")
			w = httptest.NewRecorder()
			h(w, r)
			if w.Code != 503 || calls != 1 {
				t.Fatal("installation fence was bypassed by anonymous fallback", w.Code)
			}
		})
	}
}

type statusLeaseRuns struct{ run.Service }

func (statusLeaseRuns) Get(context.Context, string) (*run.Run, error) {
	return &run.Run{ProjectID: "p", Trigger: run.Trigger{Commit: "12345678"}}, nil
}

type statusLeaseProjects struct{ project.Service }

func (statusLeaseProjects) Get(context.Context, string) (*project.Project, error) {
	return &project.Project{RepoURL: "https://github.com/fixture/repo.git", CredentialID: "c", PRStatusEnabled: true}, nil
}

type statusLeaseTransport func(*http.Request) (*http.Response, error)

func (f statusLeaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type statusLeaseBody struct {
	io.Reader
	active func() bool
	t      *testing.T
}

func (b statusLeaseBody) Close() error {
	if !b.active() {
		b.t.Fatal("status response closed after credential scope ended")
	}
	return nil
}

func TestPRStatusRetainsScopedCredentialThroughActualReport(t *testing.T) {
	for _, mode := range []string{"success", "provider_failure", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			v := &scopedHTTPVault{}
			if mode == "uncertain" {
				v.err = vault.ErrOAuthUncertain
			}
			calls := 0
			client := &http.Client{Transport: statusLeaseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !v.active {
					t.Fatal("status request lacked a scoped credential")
				}
				status := 201
				if mode == "provider_failure" {
					status = 401
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: statusLeaseBody{Reader: strings.NewReader(`{}`), active: func() bool { return v.active }, t: t}}, nil
			})}
			reporter := prstatus.NewReporter(client).WithBaseURLs("https://fixture.test", "https://fixture.test").WithRetry(1, 0)
			NewPRStatusHook(statusLeaseRuns{}, statusLeaseProjects{}, v, reporter, "", false)(context.Background(), "r", run.StatusSuccess)
			if mode == "uncertain" {
				if calls != 0 || v.released != 0 {
					t.Fatal("uncertain status token reached provider")
				}
				return
			}
			if calls != 1 || v.active || v.released != 1 {
				t.Fatal("status use did not end with actual report")
			}
		})
	}
}

func (v *scopedHTTPVault) GetGitAuth(string) (vault.GitAuth, error) {
	panic("unscoped HTTP Git credential read")
}
func (v *scopedHTTPVault) AcquireGitAuth(context.Context, string) (vault.GitAuth, func(), error) {
	if v.err != nil {
		return vault.GitAuth{}, nil, v.err
	}
	v.active = true
	return vault.GitAuth{Username: "alice", Token: "synthetic-access"}, func() { v.active = false; v.released++ }, nil
}

type scopedHTTPProject struct{ project.Service }

func (scopedHTTPProject) Get(context.Context, string) (*project.Project, error) {
	return &project.Project{ID: "p", RepoURL: "https://fixture.test/repo.git", CredentialID: "credential", DefaultBranch: "main"}, nil
}

type scopedHTTPReader struct {
	SourceReader
	check func()
}

func (r scopedHTTPReader) Tree(context.Context, string, string, string, string, string) (sourceTreeDTO, error) {
	r.check()
	return sourceTreeDTO{Entries: []sourceEntryDTO{}}, nil
}
func (r scopedHTTPReader) Blob(context.Context, string, string, string, string, string) (sourceBlobDTO, error) {
	r.check()
	return sourceBlobDTO{Content: "version: 1\nstages: []"}, nil
}

type scopedHTTPRefs struct {
	RefsLister
	check func()
}

func (r scopedHTTPRefs) ListRefs(context.Context, string, string, string) (*repocache.Refs, error) {
	r.check()
	return &repocache.Refs{}, nil
}
func (r scopedHTTPRefs) ListCommits(context.Context, string, string, string, string, int) ([]repocache.Commit, error) {
	r.check()
	return nil, nil
}

type scopedHTTPAnalyzer struct{ check func() }

func (r scopedHTTPAnalyzer) Analyze(context.Context, string, string, string) ai.RepoAnalysis {
	r.check()
	return ai.RepoAnalysis{}
}

type scopedHTTPRuns struct{ run.Service }

func (scopedHTTPRuns) Get(context.Context, string) (*run.Run, error) {
	return &run.Run{ProjectID: "p", Trigger: run.Trigger{Commit: "12345678"}}, nil
}

type scopedHTTPDiff struct {
	ai.RunDiffer
	check func()
}

func (r scopedHTTPDiff) DiffCommit(context.Context, string, string, string, string) (ai.RunDiff, string) {
	r.check()
	return ai.RunDiff{}, ""
}

func TestHTTPGitConsumersRetainLeaseAndRejectUncertainCredential(t *testing.T) {
	for _, name := range []string{"refs", "commits", "tree", "blob", "pac", "analysis", "diff"} {
		t.Run(name, func(t *testing.T) {
			for _, failed := range []bool{false, true} {
				v := &scopedHTTPVault{}
				if failed {
					v.err = vault.ErrOAuthUncertain
				}
				calls := 0
				check := func() {
					if !v.active {
						t.Fatal("Git reader called without retained credential generation")
					}
					calls++
				}
				projects := scopedHTTPProject{}
				reader := scopedHTTPReader{check: check}
				refs := scopedHTTPRefs{check: check}
				var handler http.HandlerFunc
				switch name {
				case "refs":
					handler = makeListRefsHandler(projects, v, refs)
				case "commits":
					handler = makeListCommitsHandler(projects, v, refs)
				case "tree":
					handler = makeSourceTreeHandler(sourceDeps{projects: projects, vault: v, reader: reader})
				case "blob":
					handler = makeSourceBlobHandler(sourceDeps{projects: projects, vault: v, reader: reader})
				case "pac":
					handler = makePacPreviewHandler(sourceDeps{projects: projects, vault: v, reader: reader})
				case "analysis":
					handler = makeAIGenerateHandler(aiGenerateDeps{projects: projects, vault: v, analyzer: scopedHTTPAnalyzer{check: check}, aiSvc: &stubAIService{proposal: sampleProposal()}})
				case "diff":
					handler = makeRunDiffHandler(runDiffDeps{projects: projects, vault: v, runs: scopedHTTPRuns{}, differ: scopedHTTPDiff{check: check}})
				}
				req := httptest.NewRequest(http.MethodGet, "http://instance.test/resource?path=app.txt", nil)
				w := httptest.NewRecorder()
				handler(w, req)
				if failed {
					if calls != 0 || w.Code != 409 || v.released != 0 {
						t.Fatalf("uncertain credential silently downgraded: calls=%d status=%d releases=%d", calls, w.Code, v.released)
					}
				} else {
					if calls != 1 || v.active || v.released != 1 {
						t.Fatalf("wrong scope lifetime: calls=%d active=%v releases=%d", calls, v.active, v.released)
					}
				}
			}
		})
	}
}
