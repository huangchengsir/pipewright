package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
)

func WithKubernetesReleases(c *kubernetes.ReleaseController, owner string) Option {
	return func(o *options) { o.kubernetesReleases = c; o.kubernetesReleaseOwner = owner }
}

func WithKubernetesRollback(start kubernetes.ReleaseRollbackStarter) Option {
	return func(o *options) { o.kubernetesRollback = start }
}

type k8sReleaseAPI struct {
	rollback kubernetes.ReleaseRollbackStarter
	s        *kubernetes.Service
	c        *kubernetes.ReleaseController
	owner    string
	audit    audit.Recorder
}
type k8sReleaseProfileWrite struct {
	ExpectedRevision int64                         `json:"expectedRevision"`
	Enabled          bool                          `json:"enabled"`
	Profile          kubernetes.ReleaseToolProfile `json:"profile"`
}
type k8sReleaseGrantWrite struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	kubernetes.ReleaseGrantInput
}
type k8sReleasePolicyWrite struct {
	ProjectID   string               `json:"projectId"`
	Environment string               `json:"environment"`
	Executor    k8sExecutorSelection `json:"executor"`
}
type k8sReleaseContinueWrite struct {
	RequestID        string `json:"requestId,omitempty"`
	ExpectedRevision int64  `json:"expectedRevision"`
	PlanID           string `json:"planId"`
}
type k8sReleaseDTO struct {
	Results     []kubernetes.ReleaseResourceResult `json:"results"`
	ID          string                             `json:"id"`
	RunID       string                             `json:"runId"`
	NodeKey     string                             `json:"nodeKey"`
	Revision    int64                              `json:"revision"`
	State       string                             `json:"state"`
	Cleanup     kubernetes.CleanupState            `json:"cleanup"`
	OwnerFenced bool                               `json:"ownerFenced"`
	Cursor      int                                `json:"cursor"`
	ReasonCode  string                             `json:"reasonCode,omitempty"`
	Preview     kubernetes.ReleasePreview          `json:"preview"`
	CreatedAt   int64                              `json:"createdAt"`
	UpdatedAt   int64                              `json:"updatedAt"`
}

func releaseDTO(r kubernetes.ReleaseRecord) k8sReleaseDTO {
	return k8sReleaseDTO{ID: r.ID, RunID: r.RunID, NodeKey: r.NodeKey, Revision: r.Revision, State: r.State, Cleanup: r.Cleanup, OwnerFenced: r.OwnerFenced, Cursor: r.Cursor, ReasonCode: r.ReasonCode, Preview: kubernetes.PreviewRelease(r), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func mountKubernetesReleases(r chi.Router, s *kubernetes.Service, c *kubernetes.ReleaseController, owner string, rollback kubernetes.ReleaseRollbackStarter, a audit.Recorder) {
	api := k8sReleaseAPI{rollback: rollback, s: s, c: c, owner: owner, audit: a}
	r.Get("/kubernetes/connections/{id}/release-profile", api.handle("profile", "get"))
	r.Put("/kubernetes/connections/{id}/release-profile", api.handle("profile", "save"))
	r.Get("/kubernetes/connections/{id}/release-grants", api.handle("grants", "list"))
	r.Post("/kubernetes/connections/{id}/release-grants", api.handle("grants", "save"))
	r.Patch("/kubernetes/connections/{id}/release-grants/{grantId}", api.handle("grants", "save"))
	r.Delete("/kubernetes/connections/{id}/release-grants/{grantId}", api.handle("grants", "delete"))
	r.Post("/kubernetes/connections/{id}/release-capabilities", api.handle("capabilities", "discover"))
	r.Post("/kubernetes/connections/{id}/release-options", api.handle("capabilities", "options"))
	r.Get("/runs/{runId}/kubernetes/releases", api.handle("releases", "list"))
	r.Get("/runs/{runId}/kubernetes/releases/{releaseId}", api.handle("releases", "get"))
	r.Get("/runs/{runId}/kubernetes/releases/{releaseId}/rollback-requests/{requestId}", api.handle("releases", "rollback-request"))
	r.Post("/runs/{runId}/kubernetes/releases/{releaseId}/rollback", api.handle("releases", "rollback"))
	r.Post("/runs/{runId}/kubernetes/releases/{releaseId}/continue", api.handle("releases", "continue"))
	r.Post("/runs/{runId}/kubernetes/releases/{releaseId}/reconcile", api.handle("releases", "reconcile"))
	r.Post("/runs/{runId}/kubernetes/releases/{releaseId}/cleanup", api.handle("releases", "cleanup"))
}
func (a k8sReleaseAPI) handle(kind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if a.s == nil {
			writeError(w, 503, "kubernetes_unavailable", "kubernetes_unavailable")
			return
		}
		session, ok := sessionFromContext(r.Context())
		if !ok || session.Token == "" {
			writeError(w, 401, "kubernetes_session_invalid", "kubernetes_session_invalid")
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, 400, "kubernetes_invalid_input", "kubernetes_invalid_input")
			return
		}
		ctx := r.Context()
		id, gid, runID, releaseID := chi.URLParam(r, "id"), chi.URLParam(r, "grantId"), chi.URLParam(r, "runId"), chi.URLParam(r, "releaseId")
		var out any
		var err error
		var expected int64
		status := 200
		admin := func(fn func(context.Context) error) error { return a.s.WithAdminMutation(ctx, session.Token, fn) }
		if r.Method == "GET" {
			switch kind {
			case "profile":
				out, err = a.s.GetReleaseProfile(ctx, id)
			case "grants":
				out, err = a.s.ListReleaseGrants(ctx, id)
			case "releases":
				if action == "list" {
					var records []kubernetes.ReleaseRecord
					records, err = a.s.ListReleases(ctx, runID)
					views := []k8sReleaseDTO{}
					for _, record := range records {
						view := releaseDTO(record)
						if err == nil {
							view.Results, err = a.s.ReleaseResults(ctx, record.ID)
						}
						views = append(views, view)
					}
					out = views
				} else {
					var record kubernetes.ReleaseRecord
					record, err = a.s.GetRelease(ctx, releaseID)
					if err == nil && record.RunID != runID {
						err = kubernetes.ErrNotFound
					}
					if action == "rollback-request" && err == nil {
						started, e := a.s.FindReleaseRollbackRequest(ctx, releaseID, chi.URLParam(r, "requestId"))
						err = e
						out = map[string]any{"runId": started.RunID, "release": releaseDTO(started.Release)}
						break
					}
					view := releaseDTO(record)
					if err == nil {
						view.Results, err = a.s.ReleaseResults(ctx, record.ID)
					}
					out = view
				}
			}
		} else {
			err = admin(func(context.Context) error { return nil })
			if err == nil {
				switch kind {
				case "profile":
					in := k8sReleaseProfileWrite{Enabled: true}
					err = k8sDecode(w, r, &in, 32<<10)
					expected = in.ExpectedRevision
					if err == nil {
						err = admin(func(ctx context.Context) error {
							var e error
							out, e = a.s.SaveReleaseProfile(ctx, id, expected, in.Profile, in.Enabled)
							return e
						})
					}
				case "grants":
					if action == "delete" {
						var in k8sRevision
						err = k8sDecode(w, r, &in, 16<<10)
						expected = in.ExpectedRevision
						if err == nil {
							err = admin(func(ctx context.Context) error { return a.s.DeleteReleaseGrant(ctx, id, gid, expected) })
						}
						out = map[string]bool{"deleted": err == nil}
					} else {
						in := k8sReleaseGrantWrite{ReleaseGrantInput: kubernetes.ReleaseGrantInput{RequireApproval: true, Enabled: true}}
						err = k8sDecode(w, r, &in, 4<<20)
						expected = in.ExpectedRevision
						if err == nil {
							err = admin(func(ctx context.Context) error {
								var e error
								out, e = a.s.SaveReleaseGrant(ctx, id, gid, expected, in.ReleaseGrantInput)
								return e
							})
						}
						if gid == "" {
							status = 201
						}
					}
				case "capabilities":
					var in k8sReleasePolicyWrite
					err = k8sDecode(w, r, &in, 16<<10)
					var auth kubernetes.ReleaseAuthorization
					if err == nil {
						err = admin(func(ctx context.Context) error {
							connection, e := a.s.GetConnection(ctx, id)
							if e != nil {
								return e
							}
							auth, e = a.s.ReleasePolicyCatalog(ctx, kubernetes.ReleaseAuthorizationRequest{ProjectID: in.ProjectID, Environment: in.Environment, ConnectionID: id, CredentialID: connection.CredentialID, Context: connection.Context, Executor: kubernetes.Executor{Kind: in.Executor.Kind, ServerID: in.Executor.ServerID}})
							return e
						})
					}
					if err == nil {
						if action == "options" {
							out = map[string]any{"version": 2, "connectionId": id, "projectId": in.ProjectID, "environment": in.Environment, "resources": auth.Request.Resources, "profile": auth.Profile, "requireApproval": auth.RequireApproval}
						} else if a.c == nil {
							err = kubernetes.ErrRuntimeUnverified
						} else {
							bounded, cancel := context.WithTimeout(ctx, 180*time.Second)
							out, err = a.c.Capabilities(bounded, auth, a.owner)
							cancel()
						}
					}
				case "releases":
					var in k8sReleaseContinueWrite
					err = k8sDecode(w, r, &in, 16<<10)
					expected = in.ExpectedRevision
					if err == nil {
						record, e := a.s.GetRelease(ctx, releaseID)
						err = e
						if err == nil && (record.RunID != runID) {
							err = kubernetes.ErrNotFound
						}
						if err == nil && action != "rollback" && (record.Revision != in.ExpectedRevision || in.PlanID != "" && record.PlanID != in.PlanID) {
							err = kubernetes.ErrConflict
						}
						if err == nil {
							switch action {
							case "rollback":
								if a.rollback == nil {
									err = kubernetes.ErrRuntimeUnverified
									break
								}
								bounded, cancel := context.WithTimeout(ctx, 180*time.Second)
								var started kubernetes.ReleaseRollbackStart
								started, err = a.rollback(bounded, kubernetes.ReleaseRollbackRequest{SourceID: releaseID, ExpectedRevision: in.ExpectedRevision, PlanID: in.PlanID, RequestID: in.RequestID}, func(_ context.Context, fn func(context.Context) error) error { return admin(fn) })
								cancel()
								if err == nil {
									out = map[string]any{"runId": started.RunID, "release": releaseDTO(started.Release)}
									status = 202
								}
							case "continue":
								err = admin(func(ctx context.Context) error {
									var e error
									out, e = a.s.RequestReleaseContinue(ctx, releaseID, in.ExpectedRevision, in.PlanID)
									return e
								})
								status = 202
							case "reconcile", "cleanup":
								if a.c == nil {
									err = kubernetes.ErrRuntimeUnverified
									break
								}
								bounded, cancel := context.WithTimeout(ctx, 180*time.Second)
								var updated kubernetes.ReleaseRecord
								if action == "reconcile" {
									updated, err = a.c.ReconcileRelease(bounded, releaseID, in.ExpectedRevision, func(_ context.Context, fn func(context.Context) error) error { return admin(fn) })
								} else {
									updated, err = a.c.CleanupRelease(bounded, releaseID, in.ExpectedRevision)
								}
								cancel()
								if err == nil {
									view := releaseDTO(updated)
									view.Results, err = a.s.ReleaseResults(ctx, updated.ID)
									out = view
								}
							default:
								err = kubernetes.ErrInvalid
							}
						}
					}
				default:
					err = kubernetes.ErrInvalid
				}
			}
			result := "ok"
			if err != nil {
				_, result = k8sErrorCode(err)
			}
			recordAudit(context.WithoutCancel(ctx), a.audit, audit.Entry{Actor: auditActor, Action: "kubernetes_release_" + kind + "_" + action, TargetType: "kubernetes_release_" + kind, TargetID: id + gid + releaseID, Detail: map[string]any{"expectedRevision": expected, "outcome": result}, IP: clientIP(r)})
		}
		if err != nil {
			code, name := k8sErrorCode(err)
			if errors.Is(err, kubernetes.ErrManifest) || errors.Is(err, kubernetes.ErrVariable) || errors.Is(err, kubernetes.ErrDiscovery) {
				code, name = 400, "kubernetes_release_invalid"
			}
			writeError(w, code, name, name)
			return
		}
		writeJSON(w, status, out)
	}
}
