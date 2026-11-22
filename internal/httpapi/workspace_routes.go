package httpapi

import (
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/workspace"
)

// WithWorkspace wires the organization layer over existing applications.
func WithWorkspace(svc *workspace.Service) Option {
	return func(o *options) { o.workspace = svc }
}

func mountWorkspace(r chi.Router, svc *workspace.Service, aud audit.Recorder) {
	if svc == nil {
		return
	}
	r.Get("/workspace", makeGetWorkspaceHandler(svc))
	r.Post("/workspace/business-projects", makeCreateBusinessProjectHandler(svc, aud))
	r.Patch("/workspace/business-projects/{businessProjectID}", makeRenameBusinessProjectHandler(svc, aud))
	r.Delete("/workspace/business-projects/{businessProjectID}", makeDeleteBusinessProjectHandler(svc, aud))
	r.Post("/workspace/business-projects/{businessProjectID}/products", makeCreateProductHandler(svc, aud))
	r.Patch("/workspace/products/{productID}", makeRenameProductHandler(svc, aud))
	r.Delete("/workspace/products/{productID}", makeDeleteProductHandler(svc, aud))
	r.Get("/workspace/applications", makeListWorkspaceApplicationsHandler(svc))
	r.Post("/workspace/products/{productID}/applications", makeCreateWorkspaceApplicationHandler(svc, aud))
	r.Put("/workspace/applications/{applicationID}/assignment", makeAssignWorkspaceApplicationHandler(svc, aud))
}
