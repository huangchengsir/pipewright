package project

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/vault"
)

type scopedProbeVault struct {
	vault.Vault
	active   bool
	released int
	err      error
}

func (v *scopedProbeVault) AcquireGitAuth(context.Context, string) (vault.GitAuth, func(), error) {
	if v.err != nil {
		return vault.GitAuth{}, nil, v.err
	}
	v.active = true
	return vault.GitAuth{Type: vault.TypeGitToken, Username: "alice", Token: "synthetic-access"}, func() { v.active = false; v.released++ }, nil
}
func (v *scopedProbeVault) GetGitAuth(string) (vault.GitAuth, error) {
	panic("unscoped project credential read")
}

type leaseProbe struct{ check func() error }

func (p leaseProbe) Probe(context.Context, string, string, string) (string, error) {
	return "main", p.check()
}

func TestProjectProbeRetainsCredentialUntilNetworkEnds(t *testing.T) {
	for _, mode := range []string{"success", "probe_failure", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			v := &scopedProbeVault{}
			if mode == "uncertain" {
				v.err = vault.ErrOAuthUncertain
			}
			calls := 0
			s := &service{vault: v, prober: leaseProbe{check: func() error {
				calls++
				if !v.active {
					t.Fatal("probe ran outside credential scope")
				}
				if mode == "probe_failure" {
					return errors.New("synthetic network failure")
				}
				return nil
			}}}
			_, err := s.probe(context.Background(), "https://fixture.test/repo.git", "c")
			if mode == "uncertain" {
				if calls != 0 || err == nil {
					t.Fatal("uncertain authorization reached probe")
				}
				return
			}
			if calls != 1 || v.active || v.released != 1 {
				t.Fatal("probe did not release actual use", err)
			}
		})
	}
}
