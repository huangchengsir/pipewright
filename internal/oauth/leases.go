package oauth

import (
	"context"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/maintenance"
	secretmask "github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type managedVault struct {
	vault.Vault
	service *service
}

// ManagedVault decorates the single application vault before any Git consumer is
// constructed. Non-Git operations retain the existing local-only contract.
func ManagedVault(svc Service, v vault.Vault) vault.Vault {
	if s, ok := svc.(*service); ok {
		return &managedVault{Vault: v, service: s}
	}
	return v
}

func (v *managedVault) RegisterMaskSecrets(ctx context.Context, masker *secretmask.Masker) error {
	return vault.RegisterMaskSecrets(ctx, v.Vault, masker)
}

func (v *managedVault) AcquireGitAuth(ctx context.Context, id string) (vault.GitAuth, func(), error) {
	leave, err := maintenance.Enter(ctx, v.service.db, "git_operation")
	if err != nil {
		return vault.GitAuth{}, nil, err
	}
	if id == "" {
		return vault.GitAuth{}, leave, nil
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	l := v.service.lifecycle
	g := l.gate(id)
	for {
		g.mu.Lock()
		acquired, err := l.repo.TryAcquire(wait, id, l.owner, l.now().UTC())
		if err != nil {
			g.mu.Unlock()
			leave()
			return vault.GitAuth{}, nil, err
		}
		switch acquired.Action {
		case "use":
			g.mu.Unlock()
			var once sync.Once
			release := func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				// Each call may retry only the exact returned scope's bookkeeping.
				// Failure keeps its current-process completion evidence and SQL hold.
				_ = l.repo.Release(cleanup, acquired.UseID, l.owner)
				once.Do(func() {
					g.mu.Lock()
					g.signal()
					g.mu.Unlock()
					leave()
				})
			}
			return acquired.Auth, release, nil
		case "refresh":
			b := acquired.Binding
			g.inFlight[b.ClaimID] = true
			g.mu.Unlock()
			err = v.service.refresh(wait, b)
			if err != nil {
				cleanup, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
				state := vault.OAuthUncertain
				if err == vault.ErrOAuthIdentity {
					state = vault.OAuthReauthorize
				}
				_ = l.repo.FinishFailure(cleanup, b, state)
				cancelCleanup()
			}
			g.mu.Lock()
			delete(g.inFlight, b.ClaimID)
			g.signal()
			g.mu.Unlock()
			if err != nil {
				leave()
				return vault.GitAuth{}, nil, err
			}
		default:
			if (acquired.Action == "wait_refresh" || acquired.Action == "wait_authorization") && !g.inFlight[acquired.Binding.ClaimID] {
				// A different/dead owner cannot be declared exited from timestamps. Retain
				// the exact claim and block; an explicit same-account reconnect may resolve.
				g.mu.Unlock()
				leave()
				return vault.GitAuth{}, nil, vault.ErrOAuthUncertain
			}
			changed := g.changed
			g.mu.Unlock()
			select {
			case <-wait.Done():
				leave()
				if ctx.Err() != nil {
					return vault.GitAuth{}, nil, ctx.Err()
				}
				return vault.GitAuth{}, nil, vault.ErrOAuthBusy
			case <-changed:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func (v *managedVault) List() ([]vault.Credential, error) {
	list, err := v.Vault.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		info := list[i].OAuth
		if info == nil || info.Status != vault.OAuthRefreshing {
			continue
		}
		b, e := v.service.lifecycle.repo.Binding(context.Background(), list[i].ID)
		if e != nil {
			return nil, e
		}
		if b == nil {
			continue
		}
		g := v.service.lifecycle.gate(list[i].ID)
		g.mu.Lock()
		known := g.inFlight[b.ClaimID]
		g.mu.Unlock()
		if !known {
			info.Status = vault.OAuthUncertain
		}
	}
	return list, nil
}
