package pacloader

import (
	"context"
	"errors"
	"testing"
)

type scopedPACTokens struct {
	fakeTokens
	active     bool
	releases   int
	acquireErr error
}

func (f *scopedPACTokens) AcquireGitAuth(context.Context, string) (string, string, func(), error) {
	if f.acquireErr != nil {
		return "", "", nil, f.acquireErr
	}
	f.active = true
	return "alice", "synthetic-access", func() { f.active = false; f.releases++ }, nil
}

type scopedPACBlob struct {
	tokens *scopedPACTokens
	t      *testing.T
}

func (f scopedPACBlob) FetchBlob(context.Context, string, string, string, string, string) (string, bool, error) {
	if !f.tokens.active {
		f.t.Fatal("PAC read did not hold credential generation")
	}
	return validYAML, false, nil
}
func TestPACRetainsGitLeaseAndDoesNotFallbackAfterManagedFailure(t *testing.T) {
	tokens := &scopedPACTokens{}
	stored := &storedLoader{cfg: storedCfg()}
	loader := New(stored, fakeProjects{info: ProjectInfo{RepoURL: "https://fixture.test/repo.git", CredentialID: "c", DefaultBranch: "main", PacEnabled: true}}, tokens, scopedPACBlob{tokens, t}, false)
	cfg, source, err := loader.Get(context.Background(), "p", "main")
	if err != nil || cfg == nil || !source.FromRepo || tokens.active || tokens.releases != 1 {
		t.Fatal("PAC lease did not cover actual blob read", err)
	}
	tokens.acquireErr = errors.New("managed authorization required")
	if _, _, err = loader.Get(context.Background(), "p", "main"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatal("managed credential failure fell back to stored deployment configuration")
	}
	if stored.called != 0 {
		t.Fatal("stored pipeline executed after managed credential failure")
	}
}

func TestPACAnonymousReadRetainsScopedMaintenanceAdmission(t *testing.T) {
	tokens := &scopedPACTokens{}
	loader := New(&storedLoader{cfg: storedCfg()}, fakeProjects{info: ProjectInfo{RepoURL: "https://fixture.test/public.git", DefaultBranch: "main", PacEnabled: true}}, tokens, scopedPACBlob{tokens, t}, false)
	_, source, err := loader.Get(context.Background(), "p", "main")
	if err != nil || !source.FromRepo || tokens.active || tokens.releases != 1 {
		t.Fatal("anonymous PAC read bypassed scoped maintenance admission", err)
	}
}
