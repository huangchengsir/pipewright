package httpapi

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func acquireRequestGitAuth(ctx context.Context, v vault.Vault, id string) (string, string, func(), error) {
	auth, release, err := vault.AcquireGitAuth(ctx, v, id)
	if err != nil {
		// Existing unconfigured/public-repository adapters retain anonymous fallback.
		// Managed lifecycle failures and maintenance never silently become anonymous.
		if vault.IsManagedGitError(err) || ctx.Err() != nil || errors.Is(err, configstate.ErrMaintenance) {
			return "", "", func() {}, err
		}
		anonymous, done, fallbackErr := vault.AcquireGitAuth(ctx, v, "")
		if fallbackErr != nil {
			return "", "", func() {}, fallbackErr
		}
		return anonymous.Username, anonymous.Token, done, nil
	}
	return auth.Username, auth.Token, release, nil
}
