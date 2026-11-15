package kubernetes

import (
	"context"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// PreviewCredentialFor checks the intended mutation before parsing user input.
// The pure parser remains available to trusted internal validation paths.
func (s *Service) PreviewCredentialFor(ctx context.Context, id string, in CredentialInput) (CredentialPreview, error) {
	authorize := func() error {
		if id == "" {
			return tenancy.CheckNew(ctx, s.db, "kubernetes_credential.create", "kubernetes_credential", CredentialPrefix+uuid.NewString())
		}
		return tenancy.Check(ctx, s.db, "kubernetes_credential.edit", "kubernetes_credential", id)
	}
	if err := authorize(); err != nil {
		return CredentialPreview{}, err
	}
	out, err := PreviewCredential(in)
	if err != nil {
		return CredentialPreview{}, err
	}
	if err = authorize(); err != nil {
		return CredentialPreview{}, err
	}
	return out, nil
}
