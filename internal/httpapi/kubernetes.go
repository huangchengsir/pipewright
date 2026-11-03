package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
)

type kubernetesObserver interface {
	Probe(context.Context, kubernetes.Target) (kubernetes.Receipt, error)
	Inspect(context.Context, string, string) (kubernetes.Receipt, error)
	PrepareRetire(context.Context, string, int64) (kubernetes.Receipt, error)
}

func WithKubernetes(s *kubernetes.Service, c kubernetesObserver) Option {
	return func(o *options) { o.kubernetes = s; o.kubernetesObserver = c }
}

type k8sAPI struct {
	s     *kubernetes.Service
	c     kubernetesObserver
	audit audit.Recorder
}
type k8sRevision struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}
type k8sCredentialWrite struct {
	k8sRevision
	kubernetes.CredentialInput
}
type k8sConnectionWrite struct {
	k8sRevision
	kubernetes.ConnectionInput
}
type k8sGrantWrite struct {
	k8sRevision
	kubernetes.GrantInput
}
type k8sEnableWrite struct {
	k8sRevision
	Enabled bool `json:"enabled"`
}
type k8sExecutorSelection struct {
	Kind     string `json:"kind"`
	ServerID string `json:"serverId,omitempty"`
}
type k8sProbeWrite struct {
	GrantID   string `json:"grantId"`
	Container string `json:"container"`
}

// Operation DTO deliberately excludes private intent, ownership, approval material,
// credential revisions, API server URLs and runtime proofs.
type k8sOperationDTO struct {
	ID           string                   `json:"id"`
	RunID        string                   `json:"runId"`
	NodeID       string                   `json:"nodeId"`
	Attempt      int                      `json:"attempt"`
	Revision     int64                    `json:"revision"`
	ConnectionID string                   `json:"connectionId"`
	ProjectID    string                   `json:"projectId"`
	Environment  string                   `json:"environment"`
	Namespace    string                   `json:"namespace"`
	Deployment   string                   `json:"deployment"`
	Container    string                   `json:"container"`
	Executor     k8sExecutorSelection     `json:"executor"`
	Image        string                   `json:"image"`
	IntentHash   string                   `json:"intentHash"`
	Business     kubernetes.BusinessState `json:"business"`
	Cleanup      kubernetes.CleanupState  `json:"cleanup"`
	Retired      bool                     `json:"retired"`
	ReasonCode   string                   `json:"reasonCode,omitempty"`
	CanReconcile bool                     `json:"canReconcile"`
	CanRetire    bool                     `json:"canRetire"`
	Receipt      *k8sReceiptDTO           `json:"receipt,omitempty"`
	CreatedAt    int64                    `json:"createdAt"`
	UpdatedAt    int64                    `json:"updatedAt"`
}
type k8sReceiptDTO struct {
	UID                string `json:"uid"`
	Generation         int64  `json:"generation"`
	ObservedGeneration int64  `json:"observedGeneration"`
	Revision           string `json:"revision,omitempty"`
	Image              string `json:"image"`
	DesiredReplicas    int64  `json:"desiredReplicas"`
	UpdatedReplicas    int64  `json:"updatedReplicas"`
	AvailableReplicas  int64  `json:"availableReplicas"`
	Paused             bool   `json:"paused"`
	Reason             string `json:"reason,omitempty"`
}

func k8sReceipt(r kubernetes.Receipt) *k8sReceiptDTO {
	return &k8sReceiptDTO{r.UID, r.Generation, r.ObservedGeneration, r.Revision, r.Image, r.DesiredReplicas, r.UpdatedReplicas, r.AvailableReplicas, r.Paused, r.Reason}
}
func k8sOperation(o kubernetes.Operation) k8sOperationDTO {
	t := o.Intent.Authorization.Target
	d := k8sOperationDTO{ID: o.ID, RunID: o.RunID, NodeID: o.NodeID, Attempt: o.Attempt, Revision: o.Revision, ConnectionID: t.ConnectionID, ProjectID: t.ProjectID, Environment: t.Environment, Namespace: t.Namespace, Deployment: t.Deployment, Container: t.Container, Executor: k8sExecutorSelection{t.Executor.Kind, t.Executor.ServerID}, Image: o.Intent.Image, IntentHash: o.IntentHash, Business: o.Business, Cleanup: o.Cleanup, Retired: o.Retired, ReasonCode: o.ReasonCode, CanReconcile: o.OwnerFenced && !o.Retired, CanRetire: o.OwnerFenced && o.Cleanup == kubernetes.CleanupVerified && o.Business == kubernetes.OutcomeUnknown && !o.Retired, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt}
	if o.Receipt != nil {
		d.Receipt = k8sReceipt(*o.Receipt)
	}
	return d
}
func mountKubernetes(r chi.Router, s *kubernetes.Service, c kubernetesObserver, a audit.Recorder) {
	api := k8sAPI{s, c, a}
	for _, kind := range []string{"credentials", "connections"} {
		base := "/kubernetes/" + kind
		r.Get(base, api.handle(kind, "list"))
		r.Get(base+"/{id}", api.handle(kind, "get"))
		r.Post(base, api.handle(kind, "create"))
		r.Patch(base+"/{id}", api.handle(kind, "update"))
		r.Delete(base+"/{id}", api.handle(kind, "delete"))
	}
	r.Post("/kubernetes/credentials/preview", api.handle("credentials", "preview"))
	r.Post("/kubernetes/credentials/{id}/enabled", api.handle("credentials", "enabled"))
	r.Get("/kubernetes/connections/{id}/grants", api.handle("grants", "list"))
	r.Post("/kubernetes/connections/{id}/grants", api.handle("grants", "create"))
	r.Patch("/kubernetes/connections/{id}/grants/{grantId}", api.handle("grants", "update"))
	r.Delete("/kubernetes/connections/{id}/grants/{grantId}", api.handle("grants", "delete"))
	r.Post("/kubernetes/connections/{id}/probe", api.handle("connections", "probe"))
	r.Get("/runs/{runId}/kubernetes/operations", api.handle("operations", "list"))
	r.Post("/runs/{runId}/kubernetes/operations/{id}/reconcile", api.handle("operations", "reconcile"))
	r.Post("/runs/{runId}/kubernetes/operations/{id}/retire", api.handle("operations", "retire"))
}
func k8sErrorCode(e error) (int, string) {
	switch {
	case errors.Is(e, kubernetes.ErrSession):
		return 401, "kubernetes_session_invalid"
	case errors.Is(e, kubernetes.ErrRecentPassword):
		return 403, "recent_password_required"
	case errors.Is(e, kubernetes.ErrInvalid), errors.Is(e, errStrictJSON):
		return 400, "kubernetes_invalid_input"
	case errors.Is(e, kubernetes.ErrNotFound):
		return 404, "kubernetes_not_found"
	case errors.Is(e, kubernetes.ErrDenied):
		return 403, "kubernetes_not_authorized"
	case errors.Is(e, kubernetes.ErrConflict):
		return 409, "kubernetes_revision_conflict"
	case errors.Is(e, kubernetes.ErrInUse), errors.Is(e, configstate.ErrMaintenance):
		return 409, "kubernetes_in_use"
	case errors.Is(e, kubernetes.ErrHeld):
		return 409, "kubernetes_workload_held"
	case errors.Is(e, kubernetes.ErrOwner):
		return 409, "kubernetes_owner_unconfirmed"
	case errors.Is(e, kubernetes.ErrRuntimeUnverified):
		return 422, "secret_runtime_unverified"
	case errors.Is(e, kubernetes.ErrDecrypt):
		return 503, "kubernetes_credential_unavailable"
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return 503, "kubernetes_observation_interrupted"
	default:
		return 500, "kubernetes_operation_failed"
	}
}
func k8sDecode(w http.ResponseWriter, r *http.Request, out any, limit int) error {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(limit)))
	defer clear(raw)
	if e != nil {
		return kubernetes.ErrInvalid
	}
	if strictObjectJSON(raw, out, limit) != nil || !k8sExactKeys(raw, reflect.TypeOf(out)) {
		return kubernetes.ErrInvalid
	}
	return nil
}

// Exact field names, including nested embedded input DTOs. encoding/json alone
// accepts case aliases; these administrator envelopes intentionally do not.
func k8sExactKeys(raw []byte, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return false
		}
		allowed := map[string]reflect.Type{}
		var collect func(reflect.Type)
		collect = func(v reflect.Type) {
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.Anonymous {
					collect(f.Type)
					continue
				}
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name != "" && name != "-" {
					allowed[name] = f.Type
				}
			}
		}
		collect(t)
		valid := true
		for k, v := range fields {
			ft, ok := allowed[k]
			if !ok || !k8sExactKeys(v, ft) {
				valid = false
			}
			clear(v)
		}
		return valid
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return false
		}
		valid := true
		for _, v := range values {
			if !k8sExactKeys(v, t.Elem()) {
				valid = false
			}
			clear(v)
		}
		return valid
	default:
		return true
	}
}
func (a k8sAPI) handle(kind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if a.s == nil {
			writeError(w, 503, "kubernetes_unavailable", "kubernetes_unavailable")
			return
		}
		if len(r.URL.RawQuery) > 0 {
			writeError(w, 400, "kubernetes_invalid_input", "kubernetes_invalid_input")
			return
		}
		ctx := r.Context()
		id := chi.URLParam(r, "id")
		gid := chi.URLParam(r, "grantId")
		runID := chi.URLParam(r, "runId")
		var out any
		var err error
		status := 200
		var expected int64
		session, ok := sessionFromContext(ctx)
		if !ok || session.Token == "" {
			writeError(w, 401, "kubernetes_session_invalid", "kubernetes_session_invalid")
			return
		}
		admin := func(fn func(context.Context) error) error { return a.s.WithAdminMutation(ctx, session.Token, fn) }
		if r.Method == http.MethodGet {
			switch kind {
			case "credentials":
				if action == "list" {
					out, err = a.s.ListCredentials(ctx)
				} else {
					out, err = a.s.GetCredential(ctx, id)
				}
			case "connections":
				if action == "list" {
					out, err = a.s.ListConnections(ctx)
				} else {
					out, err = a.s.GetConnection(ctx, id)
				}
			case "grants":
				if _, err = a.s.GetConnection(ctx, id); err == nil {
					out, err = a.s.ListGrants(ctx, id)
				}
			case "operations":
				var ops []kubernetes.Operation
				ops, err = a.s.ListOperations(ctx, runID)
				dtos := []k8sOperationDTO{}
				for _, o := range ops {
					dtos = append(dtos, k8sOperation(o))
				}
				out = dtos
			}
		} else {
			// Validate the session before accepting any credential import or creating an observer.
			err = admin(func(context.Context) error { return nil })
			if err == nil {
				switch {
				case kind == "credentials" && action == "preview":
					var in kubernetes.CredentialInput
					err = k8sDecode(w, r, &in, 384<<10)
					if err == nil {
						out, err = kubernetes.PreviewCredential(in)
					}
				case kind == "credentials" && (action == "create" || action == "update"):
					var in k8sCredentialWrite
					err = k8sDecode(w, r, &in, 384<<10)
					expected = in.ExpectedRevision
					if err == nil {
						err = admin(func(ctx context.Context) error {
							var c kubernetes.Credential
							c, err = a.s.SaveCredential(ctx, id, expected, in.CredentialInput)
							out = c
							if err == nil {
								id = c.ID
							}
							return err
						})
					}
				case kind == "credentials" && action == "enabled":
					var in k8sEnableWrite
					err = k8sDecode(w, r, &in, 32<<10)
					expected = in.ExpectedRevision
					if err == nil {
						err = admin(func(ctx context.Context) error {
							out, err = a.s.SetCredentialEnabled(ctx, id, expected, in.Enabled)
							return err
						})
					}
				case kind == "connections" && (action == "create" || action == "update"):
					var in k8sConnectionWrite
					err = k8sDecode(w, r, &in, 32<<10)
					expected = in.ExpectedRevision
					if err == nil {
						err = admin(func(ctx context.Context) error {
							var c kubernetes.Connection
							c, err = a.s.SaveConnection(ctx, id, expected, in.ConnectionInput)
							out = c
							if err == nil {
								id = c.ID
							}
							return err
						})
					}
				case kind == "grants" && (action == "create" || action == "update"):
					var in k8sGrantWrite
					err = k8sDecode(w, r, &in, 32<<10)
					expected = in.ExpectedRevision
					if err == nil {
						err = admin(func(ctx context.Context) error {
							if gid != "" {
								g, e := a.s.GetGrant(ctx, gid)
								if e != nil {
									return e
								}
								if g.ConnectionID != id {
									return kubernetes.ErrNotFound
								}
							}
							var g kubernetes.Grant
							g, err = a.s.SaveGrant(ctx, id, gid, expected, in.GrantInput)
							out = g
							if err == nil {
								gid = g.ID
							}
							return err
						})
					}
				case action == "delete":
					var in k8sRevision
					err = k8sDecode(w, r, &in, 32<<10)
					expected = in.ExpectedRevision
					if err == nil {
						err = admin(func(ctx context.Context) error {
							switch kind {
							case "credentials":
								return a.s.DeleteCredential(ctx, id, expected)
							case "connections":
								return a.s.DeleteConnection(ctx, id, expected)
							case "grants":
								g, e := a.s.GetGrant(ctx, gid)
								if e != nil {
									return e
								}
								if g.ConnectionID != id {
									return kubernetes.ErrNotFound
								}
								return a.s.DeleteGrant(ctx, gid, expected)
							}
							return kubernetes.ErrInvalid
						})
					}
					out = map[string]bool{"deleted": err == nil}
				case action == "probe":
					var in k8sProbeWrite
					err = k8sDecode(w, r, &in, 32<<10)
					if err == nil {
						var g kubernetes.Grant
						g, err = a.s.GetGrant(ctx, in.GrantID)
						if err == nil && g.ConnectionID != id {
							err = kubernetes.ErrNotFound
						}
						if err == nil {
							var c kubernetes.Connection
							c, err = a.s.GetConnection(ctx, id)
							if err == nil {
								t := kubernetes.Target{ProjectID: g.ProjectID, Environment: g.Environment, ConnectionID: id, CredentialID: c.CredentialID, Context: c.Context, Namespace: g.Namespace, Deployment: g.Deployment, Container: in.Container, Executor: kubernetes.Executor{Kind: g.Executor.Kind, ServerID: g.Executor.ServerID}}
								var authz kubernetes.Authorization
								authz, err = a.s.AuthorizeConfigured(ctx, t)
								if err == nil {
									if a.c == nil {
										err = kubernetes.ErrRuntimeUnverified
									} else {
										bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
										var receipt kubernetes.Receipt
										receipt, err = a.c.Probe(bounded, authz.Target)
										cancel()
										out = k8sReceipt(receipt)
									}
								}
							}
						}
					}
				case kind == "operations":
					var in k8sRevision
					err = k8sDecode(w, r, &in, 32<<10)
					expected = in.ExpectedRevision
					if err == nil {
						var o kubernetes.Operation
						o, err = a.s.GetOperation(ctx, id)
						if err == nil && o.RunID != runID {
							err = kubernetes.ErrNotFound
						}
						if err == nil && o.Revision != expected {
							err = kubernetes.ErrConflict
						}
						if err == nil && !o.OwnerFenced {
							err = kubernetes.ErrOwner
						}
						if err == nil {
							if a.c == nil {
								err = kubernetes.ErrRuntimeUnverified
							} else {
								bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
								var receipt kubernetes.Receipt
								if action == "retire" {
									receipt, err = a.c.PrepareRetire(bounded, id, expected)
								} else {
									receipt, err = a.c.Inspect(bounded, id, "reconcile")
								}
								cancel()
								if err == nil {
									err = admin(func(ctx context.Context) error {
										if action == "retire" {
											o, err = a.s.Retire(ctx, id, expected, receipt)
										} else {
											o, err = a.s.ApplyReconciliation(ctx, id, expected, receipt)
										}
										out = k8sOperation(o)
										return err
									})
								}
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
			target := id
			if kind == "grants" {
				target = gid
			}
			recordAudit(context.WithoutCancel(ctx), a.audit, audit.Entry{Actor: auditActor, Action: "kubernetes_" + kind + "_" + action, TargetType: "kubernetes_" + kind, TargetID: target, Detail: map[string]any{"expectedRevision": expected, "outcome": result}, IP: clientIP(r)})
			if action == "create" {
				status = 201
			}
		}
		if err != nil {
			code, name := k8sErrorCode(err)
			writeError(w, code, name, name)
			return
		}
		writeJSON(w, status, out)
	}
}
