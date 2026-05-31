package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/repocache"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type fakeRefsLister struct {
	refs *repocache.Refs
	err  error
}

func (f fakeRefsLister) ListRefs(_ context.Context, _, _ string) (*repocache.Refs, error) {
	return f.refs, f.err
}

func setupRefsServer(t *testing.T, lister RefsLister) (string, *http.Client, string, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	projID := seedSourceProject(t, st.DB, "https://example.com/p.git")

	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithProjects(psvc), WithRefs(lister)))
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv.URL, client, csrf, projID
}

func TestListRefsEndpointReturnsBranchesAndTags(t *testing.T) {
	lister := fakeRefsLister{refs: &repocache.Refs{
		Branches: []repocache.Ref{{Name: "main", Commit: "aaa111"}, {Name: "dev", Commit: "bbb222"}},
		Tags:     []repocache.Ref{{Name: "v1.0", Commit: "ccc333", IsTag: true}},
	}}
	srv, client, _, projID := setupRefsServer(t, lister)

	resp, err := client.Get(srv + "/api/projects/" + projID + "/refs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body refsResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Branches) != 2 || body.Branches[0].Name != "main" || body.Branches[0].Commit != "aaa111" {
		t.Fatalf("branches 不对: %+v", body.Branches)
	}
	if len(body.Tags) != 1 || body.Tags[0].Name != "v1.0" {
		t.Fatalf("tags 不对: %+v", body.Tags)
	}
}

func TestListRefsEndpointDisabledWhenNoLister(t *testing.T) {
	srv, client, _, projID := setupRefsServer(t, nil)
	resp, err := client.Get(srv + "/api/projects/" + projID + "/refs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("代码管理区未启用应 503,实际 %d", resp.StatusCode)
	}
}
