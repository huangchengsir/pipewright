package main

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func (r *previewRepoResolver) Acquire(ctx context.Context, projectID string) (context.Context, string, string, func(), error) {
	proj, err := r.projects.Get(ctx, projectID)
	if err != nil {
		return ctx, "", "", nil, err
	}
	check := func(current context.Context) error {
		actual, e := r.projects.Get(current, projectID)
		if e != nil {
			return e
		}
		if actual.RepoURL != proj.RepoURL || actual.CredentialID != proj.CredentialID {
			return tenancy.ErrConflict
		}
		return vault.AuthorizeUse(current, r.vault, proj.CredentialID)
	}
	ctx = tenancy.WithActionBoundary(ctx, check)
	gitAuth, release, err := vault.AcquireGitAuth(ctx, r.vault, proj.CredentialID)
	if err != nil {
		return ctx, "", "", release, err
	}
	if err = check(ctx); err != nil {
		return ctx, "", "", release, err
	}
	return ctx, proj.RepoURL, gitAuth.Token, release, nil
}
