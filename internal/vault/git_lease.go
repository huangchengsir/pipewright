package vault

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	secretmask "github.com/huangchengsir/pipewright/internal/mask"
)

var (
	ErrGitLeaseRequired    = errors.New("vault: managed Git credential requires a scoped lease")
	ErrOAuthReauthorize    = errors.New("vault: OAuth credential requires authorization")
	ErrOAuthUncertain      = errors.New("vault: OAuth exchange result is uncertain")
	ErrOAuthBusy           = errors.New("vault: OAuth credential has active users or an unresolved exchange")
	ErrOAuthIdentity       = errors.New("vault: OAuth account or application changed")
	ErrOAuthDetachRequired = errors.New("vault: explicitly detach OAuth before replacing its secret")
)

type GitAuthAcquirer interface {
	AcquireGitAuth(context.Context, string) (GitAuth, func(), error)
}

// AcquireGitAuth retains old test adapters while requiring managed production
// credentials to hold a generation reference until the actual Git call ends.
func AcquireGitAuth(ctx context.Context, v Vault, id string) (GitAuth, func(), error) {
	if v == nil {
		return GitAuth{}, func() {}, nil
	}
	if a, ok := v.(GitAuthAcquirer); ok {
		auth, release, err := a.AcquireGitAuth(ctx, id)
		if err == nil {
			secretmask.RegisterContextSecret(ctx, auth.Token)
		}
		return auth, release, err
	}
	if id == "" {
		return GitAuth{}, func() {}, nil
	}
	a, err := v.GetGitAuth(id)
	if err == nil {
		secretmask.RegisterContextSecret(ctx, a.Token)
	}
	return a, func() {}, err
}

func IsManagedGitError(err error) bool {
	return errors.Is(err, ErrGitLeaseRequired) || errors.Is(err, ErrOAuthReauthorize) ||
		errors.Is(err, ErrOAuthUncertain) || errors.Is(err, ErrOAuthBusy) ||
		errors.Is(err, ErrOAuthIdentity) || errors.Is(err, ErrOAuthDetachRequired) ||
		errors.Is(err, configstate.ErrMaintenance) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
