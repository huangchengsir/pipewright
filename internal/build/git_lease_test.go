package build

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type scopedBuildVault struct {
	vault.Vault
	active             bool
	released           int
	primary, anonymous error
	ids                []string
}

func (v *scopedBuildVault) AcquireGitAuth(_ context.Context, id string) (vault.GitAuth, func(), error) {
	v.ids = append(v.ids, id)
	err := v.primary
	if id == "" {
		err = v.anonymous
	}
	if err != nil {
		return vault.GitAuth{}, nil, err
	}
	v.active = true
	token := "synthetic-access"
	if id == "" {
		token = ""
	}
	return vault.GitAuth{Token: token}, func() { v.active = false; v.released++ }, nil
}
func (v *scopedBuildVault) GetGitAuth(string) (vault.GitAuth, error) {
	panic("unscoped build credential read")
}

type leaseCheckingCloner struct{ check func(string) error }

func (c leaseCheckingCloner) Clone(_ context.Context, _ string, _ string, token string, _ string, _ string, _ string) (*CloneResolved, error) {
	if err := c.check(token); err != nil {
		return nil, err
	}
	return &CloneResolved{CommitFull: "12345678"}, nil
}

func TestBuildCloneRetainsScopeThroughSuccessFailureAndAnonymousFallback(t *testing.T) {
	for _, mode := range []string{"success", "clone_failure", "missing", "unconfigured", "uncertain", "fenced_fallback"} {
		t.Run(mode, func(t *testing.T) {
			v := &scopedBuildVault{}
			switch mode {
			case "missing", "fenced_fallback":
				v.primary = vault.ErrNotFound
			case "unconfigured":
				v.primary = vault.ErrVaultUnconfigured
			case "uncertain":
				v.primary = vault.ErrOAuthUncertain
			}
			if mode == "fenced_fallback" {
				v.anonymous = configstate.ErrMaintenance
			}
			calls := 0
			b := &Builder{vault: v, cloner: leaseCheckingCloner{check: func(token string) error {
				calls++
				if !v.active {
					t.Fatal("Clone ran without a live scoped use")
				}
				if (mode == "missing" || mode == "unconfigured") && token != "" {
					t.Fatal("fallback reused a failed credential")
				}
				if mode == "clone_failure" {
					return ErrCloneFailed
				}
				return nil
			}}}
			_, err := b.cloneWithGitAuth(context.Background(), &project.Project{RepoURL: "https://fixture.test/repo.git", CredentialID: "c"}, &run.Run{}, t.TempDir())
			blocked := mode == "uncertain" || mode == "fenced_fallback"
			if blocked {
				if err == nil || calls != 0 || v.released != 0 {
					t.Fatal("blocked credential/fence reached clone", err)
				}
			} else if calls != 1 || v.active || v.released != 1 {
				t.Fatal("Clone scope did not end after real call", err)
			}
			if mode == "clone_failure" && !errors.Is(err, ErrCloneFailed) {
				t.Fatal(err)
			}
			if mode == "missing" || mode == "unconfigured" || mode == "fenced_fallback" {
				if len(v.ids) != 2 || v.ids[1] != "" {
					t.Fatal("anonymous fallback did not obtain its own maintenance scope")
				}
			}
		})
	}
}
