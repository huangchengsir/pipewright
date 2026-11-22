package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/workspace"
)

type workspaceNameRequest struct {
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

type workspaceApplicationDTO struct {
	projectDTO
	ProductID            string                          `json:"productId,omitempty"`
	BusinessProjectID    string                          `json:"businessProjectId,omitempty"`
	AssignmentRevision   int64                           `json:"assignmentRevision"`
	LatestRun            *workspace.LatestRun            `json:"latestRun,omitempty"`
	ProductionDeployment *workspace.ProductionDeployment `json:"productionDeployment,omitempty"`
}

func toWorkspaceApplicationDTO(item workspace.Application) workspaceApplicationDTO {
	dto := toProjectDTO(&item.Project)
	if item.LatestRun != nil {
		if status := workspaceCardRunStatus(item.LatestRun.Status); status != "" {
			dto.LastRunStatus = &status
		}
	}
	return workspaceApplicationDTO{
		projectDTO: dto, ProductID: item.ProductID, BusinessProjectID: item.BusinessProjectID,
		AssignmentRevision: item.Revision, LatestRun: item.LatestRun, ProductionDeployment: item.ProductionDeployment,
	}
}

// The card keeps the existing six-status enum. LatestRun retains the domain
// status for callers that link to a run detail.
func workspaceCardRunStatus(status string) string {
	switch status {
	case "success":
		return "成功"
	case "failed":
		return "失败"
	case "running", "waiting_approval", "waiting_action":
		return "进行中"
	case "partial_failed":
		return "部分失败"
	case "rolled_back":
		return "已回滚"
	case "queued":
		return "排队中"
	default:
		return ""
	}
}

func workspaceReady(w http.ResponseWriter, svc *workspace.Service) bool {
	if svc != nil {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "internal", "项目工作区服务未初始化")
	return false
}

func writeWorkspaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspace.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_workspace", "请检查项目、产品或应用参数")
	case errors.Is(err, workspace.ErrNotFound):
		writeError(w, http.StatusNotFound, "workspace_not_found", "找不到项目、产品或应用")
	case errors.Is(err, workspace.ErrNotEmpty):
		writeError(w, http.StatusConflict, "workspace_not_empty", "仅可删除空的项目或产品")
	case errors.Is(err, workspace.ErrConflict):
		writeError(w, http.StatusConflict, "workspace_conflict", "数据已变化，请刷新后重试")
	case errors.Is(err, tenancy.ErrForbidden), errors.Is(err, tenancy.ErrConflict):
		accessError(w, err)
	default:
		writeProjectError(w, err)
	}
}

func decodeWorkspace(w http.ResponseWriter, r *http.Request, value any) bool {
	const limit = 1 << 16
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil || strictObjectJSON(raw, value, limit) != nil {
		writeWorkspaceError(w, workspace.ErrInvalid)
		return false
	}
	return true
}

func workspaceAudit(r *http.Request, aud audit.Recorder, action, kind, id string, detail map[string]any) {
	recordAudit(r.Context(), aud, audit.Entry{Actor: auditActor, Action: action, TargetType: kind, TargetID: id, IP: clientIP(r), Detail: detail})
}

func makeGetWorkspaceHandler(svc *workspace.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !workspaceReady(w, svc) {
			return
		}
		result, err := svc.List(r.Context())
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func makeCreateBusinessProjectHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workspaceNameRequest
		if !workspaceReady(w, svc) || !decodeWorkspace(w, r, &req) {
			return
		}
		result, err := svc.CreateBusinessProject(r.Context(), req.Name)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		workspaceAudit(r, aud, "workspace_business_project_create", "business_project", result.ID, nil)
		writeJSON(w, http.StatusCreated, result)
	}
}

func makeRenameBusinessProjectHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workspaceNameRequest
		if !workspaceReady(w, svc) || !decodeWorkspace(w, r, &req) {
			return
		}
		result, err := svc.RenameBusinessProject(r.Context(), chi.URLParam(r, "businessProjectID"), req.Name, req.ExpectedRevision)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		workspaceAudit(r, aud, "workspace_business_project_rename", "business_project", result.ID, nil)
		writeJSON(w, http.StatusOK, result)
	}
}

func makeDeleteBusinessProjectHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !workspaceReady(w, svc) {
			return
		}
		revision, err := strconv.ParseInt(r.URL.Query().Get("expectedRevision"), 10, 64)
		if err != nil {
			writeWorkspaceError(w, workspace.ErrInvalid)
			return
		}
		id := chi.URLParam(r, "businessProjectID")
		if err = svc.DeleteBusinessProject(r.Context(), id, revision); err != nil {
			writeWorkspaceError(w, err)
			return
		}
		workspaceAudit(r, aud, "workspace_business_project_delete", "business_project", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func makeCreateProductHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workspaceNameRequest
		if !workspaceReady(w, svc) || !decodeWorkspace(w, r, &req) {
			return
		}
		result, err := svc.CreateProduct(r.Context(), chi.URLParam(r, "businessProjectID"), req.Name)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		workspaceAudit(r, aud, "workspace_product_create", "product", result.ID, map[string]any{"businessProjectId": result.BusinessProjectID})
		writeJSON(w, http.StatusCreated, result)
	}
}

func makeRenameProductHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workspaceNameRequest
		if !workspaceReady(w, svc) || !decodeWorkspace(w, r, &req) {
			return
		}
		result, err := svc.RenameProduct(r.Context(), chi.URLParam(r, "productID"), req.Name, req.ExpectedRevision)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		workspaceAudit(r, aud, "workspace_product_rename", "product", result.ID, map[string]any{"businessProjectId": result.BusinessProjectID})
		writeJSON(w, http.StatusOK, result)
	}
}

func makeDeleteProductHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !workspaceReady(w, svc) {
			return
		}
		revision, err := strconv.ParseInt(r.URL.Query().Get("expectedRevision"), 10, 64)
		if err != nil {
			writeWorkspaceError(w, workspace.ErrInvalid)
			return
		}
		id := chi.URLParam(r, "productID")
		if err = svc.DeleteProduct(r.Context(), id, revision); err != nil {
			writeWorkspaceError(w, err)
			return
		}
		workspaceAudit(r, aud, "workspace_product_delete", "product", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func makeListWorkspaceApplicationsHandler(svc *workspace.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !workspaceReady(w, svc) {
			return
		}
		params := r.URL.Query()
		page := atoiDefault(params.Get("page"), 1)
		if page > (1<<30)/project.MaxPageSize {
			writeWorkspaceError(w, workspace.ErrInvalid)
			return
		}
		result, err := svc.ListApplications(r.Context(), workspace.ApplicationFilter{
			BusinessProjectID: params.Get("businessProjectId"), ProductID: params.Get("productId"),
			Unassigned: params.Get("unassigned") == "true", Query: params.Get("q"), Status: params.Get("status"),
			Page: page, PageSize: atoiDefault(params.Get("pageSize"), project.DefaultPageSize),
		})
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		items := make([]workspaceApplicationDTO, 0, len(result.Items))
		for _, app := range result.Items {
			items = append(items, toWorkspaceApplicationDTO(app))
		}
		writeJSON(w, http.StatusOK, struct {
			Items    []workspaceApplicationDTO `json:"items"`
			Total    int                       `json:"total"`
			Page     int                       `json:"page"`
			PageSize int                       `json:"pageSize"`
		}{items, result.Total, result.Page, result.PageSize})
	}
}

func makeCreateWorkspaceApplicationHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name          string `json:"name"`
			RepoURL       string `json:"repoUrl"`
			CredentialID  string `json:"credentialId"`
			DefaultBranch string `json:"defaultBranch"`
		}
		if !workspaceReady(w, svc) || !decodeWorkspace(w, r, &req) {
			return
		}
		result, err := svc.CreateApplication(r.Context(), chi.URLParam(r, "productID"), project.CreateInput{
			Name: req.Name, RepoURL: req.RepoURL, CredentialID: req.CredentialID, DefaultBranch: req.DefaultBranch,
		})
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		workspaceAudit(r, aud, audit.ActionProjectCreate, audit.TargetProject, result.Project.ID, map[string]any{"productId": result.ProductID})
		writeJSON(w, http.StatusCreated, toWorkspaceApplicationDTO(result))
	}
}

func makeAssignWorkspaceApplicationHandler(svc *workspace.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ProductID        json.RawMessage `json:"productId"`
			ExpectedRevision *int64          `json:"expectedRevision"`
		}
		if !workspaceReady(w, svc) || !decodeWorkspace(w, r, &req) {
			return
		}
		var productID string
		if req.ExpectedRevision == nil || len(req.ProductID) == 0 || json.Unmarshal(req.ProductID, &productID) != nil {
			writeWorkspaceError(w, workspace.ErrInvalid)
			return
		}
		result, err := svc.AssignApplication(r.Context(), chi.URLParam(r, "applicationID"), productID, *req.ExpectedRevision)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		if result.PreviousProductID != result.ProductID {
			workspaceAudit(r, aud, "workspace_application_assign", audit.TargetProject, result.ApplicationID, map[string]any{"previousProductId": result.PreviousProductID, "productId": result.ProductID})
		}
		writeJSON(w, http.StatusOK, result)
	}
}
