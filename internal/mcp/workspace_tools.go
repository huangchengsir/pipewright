package mcp

import (
	"context"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/workspace"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// WorkspaceReader is implemented directly by workspace.Service. Its native
// authorization and exact application filter run before counting/pagination.
// Never replace it with an unscoped directory lookup or browser principal.
type WorkspaceReader interface {
	ListApplications(context.Context, workspace.ApplicationFilter) (workspace.ApplicationList, error)
}

func addWorkspaceTools(s *sdk.Server, d Dependencies, verify VerifyFunc) {
	addTool(s, "workspace_application_get", true, schema(map[string]any{"projectId": stringSchema(36)}, "projectId"), verify, d.RegisterSecrets, func(ctx context.Context, t *auth.MCPToken, in ProjectInput) (any, error) {
		return d.workspaceApplication(ctx, t, in.ProjectID, verify)
	})
}
func (d Dependencies) workspaceApplication(ctx context.Context, t *auth.MCPToken, id string, verify VerifyFunc) (any, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return nil, ErrInvalidArgument
	}
	// Legacy single-admin tokens do not install a tenancy predicate. Their fixed
	// project allowlist must be checked explicitly before the native read as well.
	if t == nil || !t.Allows(id, false) {
		return nil, auth.ErrMCPForbidden
	}
	if d.Workspace == nil || d.BindToken == nil || verify == nil {
		return nil, ErrUnavailable
	}
	result, err := d.Workspace.ListApplications(ctx, workspace.ApplicationFilter{ApplicationID: id, IdentityOnly: true, Page: 1, PageSize: 1})
	if err != nil {
		return nil, err
	}
	if len(result.Items) == 0 {
		return nil, project.ErrNotFound
	}
	if len(result.Items) != 1 {
		return nil, ErrUnavailable
	}
	app := result.Items[0]
	if app.Project.ID != id || app.Assignment.ApplicationID != id {
		return nil, ErrUnavailable
	}
	// Do not release a previously computed result if token ownership or its fixed
	// scope changed while the read was in progress. The SDK also re-verifies after
	// the handler; current native resource permission checks remain authoritative.
	fresh, err := verify(ctx)
	if err != nil || fresh == nil {
		return nil, auth.ErrMCPTokenInvalid
	}
	if fresh.ID != t.ID || fresh.OwnerUserID != t.OwnerUserID || fresh.OwnerTeamID != t.OwnerTeamID || fresh.OwnerModeEpoch != t.OwnerModeEpoch || fresh.OwnerGeneration != t.OwnerGeneration || fresh.AuthzRevision != t.AuthzRevision || !fresh.Allows(id, false) {
		return nil, auth.ErrMCPForbidden
	}
	if (app.ProductID == "") != (app.BusinessProjectID == "") {
		return nil, ErrUnavailable
	}
	if app.ProductID != "" {
		for _, parent := range []string{app.ProductID, app.BusinessProjectID} {
			p, e := uuid.Parse(parent)
			if e != nil || p.String() != parent {
				return nil, ErrUnavailable
			}
		}
	}
	return map[string]any{"projectId": id, "applicationId": id, "name": d.safeText(app.Project.Name), "productId": app.ProductID, "businessProjectId": app.BusinessProjectID, "assignmentRevision": app.Revision, "untrustedData": true}, nil
}

var _ WorkspaceReader = (*workspace.Service)(nil)
