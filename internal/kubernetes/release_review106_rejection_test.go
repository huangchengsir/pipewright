package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"testing"
)

type review106RejectionTransport struct {
	*releasePhaseTransport
	status   int
	failPoll bool
	writes   int
	request  *kubeprotocol.Request
	permit   *kubeprotocol.Permit
}

func (tr *review106RejectionTransport) Docker(ctx context.Context, args []string, in []byte) (*target.SecretResult, error) {
	if len(args) > 0 && args[0] == "exec" && len(in) > 0 {
		var f kubeprotocol.Frame
		if json.Unmarshal(in, &f) == nil && f.Version == 2 {
			r := kubeprotocol.Reply{Version: 2, Code: "ok"}
			switch f.Command {
			case "ping", "upload", "init":
			case "read":
				r.Result = &kubeprotocol.APIResult{Status: 200, Body: []byte("{\"major\":\"1\",\"minor\":\"34\"}")}
			case "write":
				tr.writes++
				copy := *f.Request
				tr.request = &copy
				r.Code = "accepted"
			case "permit":
				copy := *f.Permit
				tr.permit = &copy
			case "poll":
				if tr.failPoll && tr.permit != nil {
					return nil, ErrTool
				}
				if tr.permit == nil {
					b, _ := json.Marshal(tr.request)
					r.Pending = &kubeprotocol.PendingWrite{ID: "request-review", Digest: releaseByteDigest(b), Request: *tr.request}
				} else {
					r.Result = &kubeprotocol.APIResult{Status: tr.status, ActionID: tr.permit.ActionID, RequestID: tr.permit.RequestID, Body: []byte("{\"kind\":\"Status\",\"status\":\"Failure\"}")}
				}
			default:
				return nil, ErrTool
			}
			return fakeResult(r), nil
		}
	}
	return tr.releasePhaseTransport.Docker(ctx, args, in)
}
func TestReview106RejectionAndUnknownBoundaries(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429, 408, 500, 503, -1} {
		t.Run(string(rune('0'+status/100))+"xx-"+strconvI(status), func(t *testing.T) {
			ctx := context.Background()
			f, p := releaseStoreFixture(t, storetest.Open(t))
			id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "cfg"}
			scope := ReleaseAccess{ID: id, Actions: []string{"get", "create"}}
			if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			req := p.Authorization.Request
			req.Resources = []ReleaseAccess{scope}
			auth, e := f.s.AuthorizeRelease(ctx, req)
			if e != nil {
				t.Fatal(e)
			}
			obj := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "cfg", "namespace": "app"}, "data": map[string]any{"key": "value"}}
			p = ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{releaseResourceFromObject(id, obj, "declaration")}, TimeoutSeconds: 300}
			record, e := f.s.CreateRelease(ctx, "reject-review", "node", "owner", p)
			if e != nil {
				t.Fatal(e)
			}
			record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			if e = f.s.AcquireReleaseGates(ctx, record.ID, record.OwnerEpoch); e != nil {
				t.Fatal(e)
			}
			tr := &review106RejectionTransport{releasePhaseTransport: &releasePhaseTransport{fakePrivateDocker: &fakePrivateDocker{}, helperVersion: true, helperPing: true}, status: status, failPoll: status == -1}
			runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
			session, e := runtime.OpenReleaseWriter(ctx, record.ID, record.OwnerEpoch)
			if e != nil {
				t.Fatal(e)
			}
			defer session.Close(ctx)
			body, _ := json.Marshal(obj)
			action, e := session.ExecuteAction(ctx, record.ID, record.OwnerEpoch, record.Revision, ReleaseActionIntent{Version: 2, ParentID: record.PlanID, StepKey: "rejected-write", Resource: id, Verb: "create", Body: body, AuthorizationFingerprint: auth.Fingerprint}, "/api/v1/namespaces/app/configmaps")
			if status >= 400 && status < 500 && status != 408 {
				if errors.Is(e, ErrOutcomeUnknown) || action.State != "rejected" {
					t.Fatalf("definitive API rejection retained as unknown: HTTP=%d state=%s err=%v", status, action.State, e)
				}
			} else {
				if !errors.Is(e, ErrOutcomeUnknown) || action.State != "outcome_unknown" {
					t.Fatalf("ambiguous API result lost unknown fence: status=%d state=%s", status, action.State)
				}
			}
			if tr.writes != 1 {
				t.Fatal("write was automatically retried")
			}
			persisted, readErr := f.s.GetReleaseAction(ctx, action.ID)
			if readErr != nil || persisted.State != action.State {
				t.Fatal("actual action classification not persisted", readErr)
			}
		})
	}
}
