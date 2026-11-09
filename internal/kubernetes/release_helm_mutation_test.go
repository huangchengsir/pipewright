package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"strings"
	"testing"
)

func TestHelmOwnedPOSTRequiresSealedSameReleaseAndUndriftedFields(t *testing.T) {
	f, auth, desired, fact, live := ownedMutationFixture(t, false, true)
	ctx := context.Background()
	parent, e := f.s.GetRelease(ctx, fact.ReleaseID)
	if e != nil {
		t.Fatal(e)
	}
	helm := parent.Plan.Helm
	releaseMap(live["data"])["outside"] = "external"
	policy, e := f.s.ownedPostMutationForHelm(ctx, auth, desired, live, helm)
	if e != nil || policy == nil || policy.Strategy != releaseHelmOwnedPostMerge || policy.OriginActionID != fact.ID {
		t.Fatal("same Helm POST provenance was lost", e)
	}
	if _, ok := releaseMap(policy.Patch["data"])["remove"]; !ok {
		t.Fatal("managed omission not explicitly deleted")
	}
	if _, ok := releaseMap(policy.Patch["data"])["outside"]; ok {
		t.Fatal("unowned key became write authority")
	}
	if p, e := f.s.ownedPostMutation(ctx, auth, desired, live); e != nil || p != nil {
		t.Fatal("ordinary plan borrowed Helm provenance")
	}
	for _, dimension := range []string{"name", "namespace", "storage", "scope", "uid", "ssa", "drift"} {
		t.Run(dimension, func(t *testing.T) {
			otherHelm := *helm
			otherAuth := auth
			current := releaseObjectCopy(live)
			switch dimension {
			case "name":
				otherHelm.Name = "other"
			case "namespace":
				otherHelm.Namespace = "other"
			case "storage":
				otherHelm.StorageAnchor.Name += "other"
			case "scope":
				otherAuth.Request.Environment = "other"
			case "uid":
				releaseMap(current["metadata"])["uid"] = "other"
			case "ssa":
				releaseMap(current["metadata"])["managedFields"] = []any{map[string]any{"operation": "Apply", "manager": releaseFieldManager}}
			case "drift":
				releaseMap(current["data"])["message"] = "foreign"
			}
			p, e := f.s.ownedPostMutationForHelm(ctx, otherAuth, desired, current, &otherHelm)
			if p != nil || dimension == "drift" && !errors.Is(e, ErrSuperseded) {
				t.Fatal("foreign or drifted source granted compatibility patch", e)
			}
		})
	}
	desired.Mutation = policy
	record := ReleaseRecord{Plan: ReleasePlan{MutationVersion: 1, Helm: helm}}
	record.Plan.Helm.Rendered = []ReleaseResource{desired}
	if r, e := releaseHelmPendingMutation(record, desired.ID, releaseObjectCopy(desired.Object)); e != nil || r == nil {
		t.Fatal("frozen CLI full request rejected", e)
	}
	wrong := releaseObjectCopy(desired.Object)
	releaseMap(wrong["data"])["message"] = "foreign"
	if _, e := releaseHelmPendingMutation(record, desired.ID, wrong); !errors.Is(e, ErrDenied) {
		t.Fatal("changed CLI input was replaced by approved patch", e)
	}
	// A patch-shaped CLI request cannot be laundered through the frozen full intent.
	if _, e := releaseHelmPendingMutation(record, desired.ID, releaseObjectCopy(policy.Patch)); !errors.Is(e, ErrDenied) {
		t.Fatal("partial pending body accepted", e)
	}
}
func TestHelmPermitOptionalFieldsPreserveHistoricalWireBytes(t *testing.T) {
	p := kubeprotocol.Permit{RequestID: "request", ActionID: "action", Digest: "opaque", Expires: 123, Allow: true}
	raw, e := json.Marshal(p)
	if e != nil || string(raw) != `{"requestId":"request","actionId":"action","digest":"opaque","expires":123,"allow":true}` {
		t.Fatal("historical permit bytes changed", string(raw), e)
	}
}

type helmCASRPCTransport struct {
	*releasePhaseTransport
	current      map[string]any
	uploads      map[string][]byte
	permit       *kubeprotocol.Permit
	replacement  []byte
	advertise    bool
	capabilities []string
}

func (f *helmCASRPCTransport) Docker(ctx context.Context, args []string, in []byte) (*target.SecretResult, error) {
	if len(args) > 0 && args[0] == "exec" && len(in) > 0 {
		var frame kubeprotocol.Frame
		if json.Unmarshal(in, &frame) == nil && frame.Version == 2 {
			reply := kubeprotocol.Reply{Version: 2, Code: "ok"}
			switch frame.Command {
			case "ping":
				if f.advertise {
					reply.Capabilities = []string{kubeprotocol.PermitCASMergeCapability, kubeprotocol.HelmStorageJSONCapability}
					if f.capabilities != nil {
						reply.Capabilities = f.capabilities
					}
				}
			case "upload":
				f.uploads[frame.StreamID] = append(f.uploads[frame.StreamID], frame.Data...)
			case "init":
			case "read":
				if frame.Request.Path == "/version" {
					reply.Result = &kubeprotocol.APIResult{Status: 200, Body: []byte(`{"major":"1","minor":"34"}`)}
				} else {
					body, _ := json.Marshal(f.current)
					reply.Result = &kubeprotocol.APIResult{Status: 200, Body: body}
				}
			case "dry-run":
				var patch map[string]any
				json.Unmarshal(frame.Request.Body, &patch)
				body, _ := json.Marshal(applyFixtureMerge(f.current, patch))
				reply.Result = &kubeprotocol.APIResult{Status: 200, Body: body}
			case "permit":
				copy := *frame.Permit
				f.permit = &copy
				f.replacement = append([]byte(nil), f.uploads[copy.BodyStream]...)
			default:
				return nil, ErrTool
			}
			return fakeResult(reply), nil
		}
	}
	return f.releasePhaseTransport.Docker(ctx, args, in)
}
func newHelmCASRPCTransport(advertise bool, current map[string]any) *helmCASRPCTransport {
	return &helmCASRPCTransport{releasePhaseTransport: &releasePhaseTransport{fakePrivateDocker: &fakePrivateDocker{}, helperVersion: true, helperPing: true}, uploads: map[string][]byte{}, advertise: advertise, current: current}
}
func TestHelmCASRequiresExplicitToolCapability(t *testing.T) {
	for _, advertise := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-helper", true: "capable-helper"}[advertise], func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			tr := newHelmCASRPCTransport(advertise, nil)
			runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
			session, e := runtime.OpenReleasePreview(context.Background(), p.Authorization, "cap-owner")
			if e != nil {
				t.Fatal(e)
			}
			defer session.Close(context.Background())
			e = session.requireHelmCASCapability(context.Background())
			if advertise && e != nil || !advertise && !errors.Is(e, ErrVersion) {
				t.Fatal("capability was inferred from protocol version", e)
			}
			if tr.permit != nil {
				t.Fatal("capability probe sent business permit")
			}
		})
	}
}
func TestHelmActualHostPermitBindsOriginalFullIntentToFrozenCASBody(t *testing.T) {
	f, auth, desired, fact, live := ownedMutationFixture(t, false, true)
	ctx := context.Background()
	origin, e := f.s.GetRelease(ctx, fact.ReleaseID)
	if e != nil {
		t.Fatal(e)
	}
	policy, e := f.s.ownedPostMutationForHelm(ctx, auth, desired, live, origin.Plan.Helm)
	if e != nil || policy == nil {
		t.Fatal(e)
	}
	desired.Mutation = policy
	desired.Expected = releaseAdmittedExpected(desired, applyFixtureMerge(live, policy.Patch))
	helm := *origin.Plan.Helm
	helm.Action = "upgrade"
	helm.Revision = 2
	helm.Rendered = []ReleaseResource{desired}
	plan := ReleasePlan{Version: 2, MutationVersion: 1, Mode: "helm", Authorization: auth, Helm: &helm, Resources: []ReleaseResource{desired}, Baseline: []ReleaseResource{releaseResourceFromObject(desired.ID, live, "declaration")}, TimeoutSeconds: 300}
	for _, scenario := range []string{"release", "namespace", "scope"} {
		bad := plan
		h := helm
		bad.Helm = &h
		if scenario == "release" {
			h.Name = "other"
		} else if scenario == "namespace" {
			h.Namespace = "other"
		} else {
			bad.Authorization.Request.Environment = "other"
		}
		if _, e := f.s.CreateRelease(ctx, "foreign-"+scenario, "node", "other-owner", bad); e == nil {
			t.Fatal("foreign sealed Helm plan borrowed CAS provenance", scenario)
		}
	}
	record, e := f.s.CreateRelease(ctx, "helm-cas-update", "node", "next-owner", plan)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.db.ExecContext(ctx, `DELETE FROM k8s_resource_gates`); e != nil {
		t.Fatal(e)
	}
	record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.AcquireReleaseGates(ctx, record.ID, record.OwnerEpoch); e != nil {
		t.Fatal(e)
	}
	tr := newHelmCASRPCTransport(true, live)
	runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
	session, e := runtime.OpenReleaseWriter(ctx, record.ID, record.OwnerEpoch)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close(ctx)
	body, _ := json.Marshal(desired.Object)
	request := kubeprotocol.Request{Method: "PATCH", Path: "/api/v1/namespaces/app/configmaps/config", ContentType: "application/apply-patch+yaml", Body: body}
	raw, _ := json.Marshal(request)
	pending := kubeprotocol.PendingWrite{ID: "pending-helm", Request: request, Digest: releaseByteDigest(raw)}
	wrong := pending
	wrong.Digest = strings.Repeat("a", 64)
	if _, e := session.permitHelmPending(ctx, record, wrong, 0); !errors.Is(e, ErrDenied) {
		t.Fatal("mismatched pending digest accepted", e)
	}
	action, e := session.permitHelmPending(ctx, record, pending, 0)
	if e != nil {
		t.Fatal("host did not issue sealed CAS permit", e)
	}
	if tr.permit == nil || tr.permit.Digest != pending.Digest || tr.permit.ContentType != releaseMergeContentType || tr.permit.FieldManager != releaseFieldManager || tr.permit.CreateOnly {
		t.Fatal("permit lost original digest or narrowed wire policy")
	}
	var approved map[string]any
	json.Unmarshal(tr.replacement, &approved)
	m := releaseMap(approved["metadata"])
	if m["uid"] != "owned-uid" || m["resourceVersion"] != "17" || releaseMap(m["annotations"])[ReleaseActionAnnotation] != action.ID {
		t.Fatal("uploaded permit body lost UID/RV/action fence")
	}
	if action.Intent.MutationVersion != 1 || action.Intent.ContentType != releaseMergeContentType || action.State != "outcome_unknown" {
		t.Fatal("write boundary was not durably fenced")
	}
}

func TestHelmTransportRequiresBothCASAndStorageJSONCapability(t *testing.T) {
	for _, capability := range []string{kubeprotocol.PermitCASMergeCapability, kubeprotocol.HelmStorageJSONCapability, "unknown"} {
		t.Run(capability, func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			tr := newHelmCASRPCTransport(true, nil)
			tr.capabilities = []string{capability}
			runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
			session, err := runtime.OpenReleasePreview(context.Background(), p.Authorization, "codec-cap-owner")
			if err != nil {
				t.Fatal("runtime fixture failed")
			}
			defer session.Close(context.Background())
			err = session.requireHelmCASCapability(context.Background())
			if !errors.Is(err, ErrVersion) || tr.permit != nil {
				t.Fatal("partial/unknown helper capability accepted")
			}
		})
	}
}

func TestHelmHookNativeOpenAPICapabilityIsConditionalAndExplicit(t *testing.T) {
	for _, scenario := range []string{"no-hooks-old-tool", "hooks-old-tool", "hooks-openapi-only", "hooks-resync-only", "hooks-capable-tool"} {
		t.Run(scenario, func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			tr := newHelmCASRPCTransport(true, nil)
			tr.capabilities = []string{kubeprotocol.PermitCASMergeCapability, kubeprotocol.HelmStorageJSONCapability}
			hooks := scenario != "no-hooks-old-tool"
			if scenario == "hooks-openapi-only" || scenario == "hooks-capable-tool" {
				tr.capabilities = append(tr.capabilities, kubeprotocol.HelmHookOpenAPICapability)
			}
			if scenario == "hooks-resync-only" || scenario == "hooks-capable-tool" {
				tr.capabilities = append(tr.capabilities, kubeprotocol.HelmHookWatchResyncCapability)
			}
			runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
			session, err := runtime.OpenReleasePreview(context.Background(), p.Authorization, "hook-cap-owner")
			if err != nil {
				t.Fatal("runtime fixture failed")
			}
			defer session.Close(context.Background())
			err = session.requireHelmCapabilities(context.Background(), hooks)
			if scenario == "hooks-old-tool" || scenario == "hooks-openapi-only" || scenario == "hooks-resync-only" {
				if !errors.Is(err, ErrVersion) {
					t.Fatal("hook schema capability inferred from protocol")
				}
			} else if err != nil {
				t.Fatal("capability incorrectly required for no-hooks or capable tool")
			}
			if tr.permit != nil {
				t.Fatal("schema capability sent mutation permit")
			}
		})
	}
}
