package run

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"io"
)

// AuthorizeArtifactDownload is distinct from reading artifact metadata.
func AuthorizeArtifactDownload(ctx context.Context, svc Service, id string) error {
	a, ok := svc.(interface {
		AuthorizeArtifactDownload(context.Context, string) error
	})
	if !ok {
		return tenancy.ErrForbidden
	}
	return a.AuthorizeArtifactDownload(ctx, id)
}
func (s *service) AuthorizeArtifactDownload(ctx context.Context, id string) error {
	return tenancy.Check(ctx, s.db, "artifact.download", "run", id)
}

type authorizedArtifactReader struct {
	ctx    context.Context
	svc    Service
	ids    []string
	source io.Reader
}

func (r authorizedArtifactReader) Read(p []byte) (int, error) {
	for _, id := range r.ids {
		if err := AuthorizeArtifactDownload(r.ctx, r.svc, id); err != nil {
			return 0, err
		}
	}
	return r.source.Read(p)
}

// Keep live revocation effective between chunks without buffering the artifact.
func ArtifactDownloadReader(ctx context.Context, svc Service, id string, source io.Reader, inheritedRunIDs ...string) io.Reader {
	ids := append([]string{id}, inheritedRunIDs...)
	return authorizedArtifactReader{ctx, svc, ids, source}
}
