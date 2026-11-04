package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/target"
)

func accessReviewJSON(t Target, status map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "metadata": map[string]any{"creationTimestamp": nil}, "spec": map[string]any{"resourceAttributes": patchAccessAttributes(t)}, "status": status})
	return raw
}

func TestAccessReviewRequiresExplicitConsistentDecision(t *testing.T) {
	target := Target{Namespace: "ns", Deployment: "app"}
	for _, tc := range []struct {
		name   string
		status map[string]any
		want   error
	}{
		{"allow", map[string]any{"allowed": true}, nil},
		{"allow-explicit", map[string]any{"allowed": true, "denied": false, "reason": "private reason", "evaluationError": ""}, nil},
		{"deny", map[string]any{"allowed": false, "denied": true}, ErrPatchDenied},
		{"no-permission", map[string]any{"allowed": false}, ErrPatchDenied},
		{"missing", map[string]any{}, ErrAccessReview},
		{"null", map[string]any{"allowed": nil}, ErrAccessReview},
		{"string", map[string]any{"allowed": "true"}, ErrAccessReview},
		{"number", map[string]any{"allowed": 1}, ErrAccessReview},
		{"contradiction", map[string]any{"allowed": true, "denied": true}, ErrAccessReview},
		{"null-denied", map[string]any{"allowed": true, "denied": nil}, ErrAccessReview},
		{"null-reason", map[string]any{"allowed": true, "reason": nil}, ErrAccessReview},
		{"evaluation-error", map[string]any{"allowed": true, "evaluationError": "private error"}, ErrAccessReview},
		{"deny-evaluation-error", map[string]any{"allowed": false, "evaluationError": "private error"}, ErrAccessReview},
		{"null-error", map[string]any{"allowed": true, "evaluationError": nil}, ErrAccessReview},
		{"unknown-field", map[string]any{"allowed": true, "extra": true}, ErrAccessReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := decodePatchAccessReview(accessReviewJSON(target, tc.status), target); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestAccessReviewRejectsUntrustedEnvelopeAndTarget(t *testing.T) {
	target := Target{Namespace: "ns", Deployment: "app"}
	good := accessReviewJSON(target, map[string]any{"allowed": true})
	edits := map[string]func(map[string]any){
		"kind":             func(v map[string]any) { v["kind"] = "SubjectAccessReview" },
		"version":          func(v map[string]any) { v["apiVersion"] = "authorization.k8s.io/v1beta1" },
		"missing-status":   func(v map[string]any) { delete(v, "status") },
		"null-spec":        func(v map[string]any) { v["spec"] = nil },
		"non-resource":     func(v map[string]any) { v["spec"].(map[string]any)["nonResourceAttributes"] = map[string]any{} },
		"unknown-envelope": func(v map[string]any) { v["Allowed"] = true },
		"null-metadata":    func(v map[string]any) { v["metadata"] = nil },
	}
	for _, key := range []string{"namespace", "name", "verb", "group", "version", "resource"} {
		edits["wrong-"+key] = func(v map[string]any) {
			v["spec"].(map[string]any)["resourceAttributes"].(map[string]any)[key] = "wrong"
		}
		edits["missing-"+key] = func(v map[string]any) { delete(v["spec"].(map[string]any)["resourceAttributes"].(map[string]any), key) }
	}
	edits["subresource"] = func(v map[string]any) {
		v["spec"].(map[string]any)["resourceAttributes"].(map[string]any)["subresource"] = "status"
	}
	for name, change := range edits {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(good, &doc); err != nil {
				t.Fatal(err)
			}
			change(doc)
			raw, _ := json.Marshal(doc)
			if err := decodePatchAccessReview(raw, target); err != ErrAccessReview {
				t.Fatal("untrusted review accepted", err)
			}
		})
	}
	malformed := map[string]string{
		"truncated": string(good[:len(good)-1]), "trailing": string(good) + " {}", "empty": "", "null": "null", "array": "[]",
		"duplicate":          strings.Replace(string(good), `"allowed":true`, `"allowed":false,"allowed":true`, 1),
		"case-alias":         strings.Replace(string(good), `"allowed":true`, `"Allowed":false,"allowed":true`, 1),
		"duplicate-scope":    strings.Replace(string(good), `"namespace":"ns"`, `"namespace":"wrong","namespace":"ns"`, 1),
		"duplicate-envelope": strings.Replace(string(good), `"kind":`, `"kind":"SubjectAccessReview","kind":`, 1),
		"oversized":          strings.Repeat(" ", MaxDiagnosticBytes) + string(good),
		"deep":               strings.Repeat("[", 18) + "null" + strings.Repeat("]", 18),
		"many-nodes":         "[" + strings.Repeat("0,", 1024) + "0]",
	}
	for name, raw := range malformed {
		t.Run(name, func(t *testing.T) {
			if err := decodePatchAccessReview([]byte(raw), target); err != ErrAccessReview {
				t.Fatal("malformed review accepted", err)
			}
		})
	}
}

func TestControllerPermissionFailureBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"denied", ErrPatchDenied, "patch_not_authorized"}, {"unverified", ErrAccessReview, "permission_check_unverified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, o, s := setupController(t)
			s.reviewErr = tc.err
			s.reads = [][]byte{deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")}
			logged := false
			out, err := c.Run(context.Background(), o.ID, o.OwnerEpoch, func(phase, code string) { logged = logged || phase == "preflight" && code == tc.reason })
			if !errors.Is(err, tc.err) || out.Business != Failed || out.DispatchFenced || out.ReasonCode != tc.reason || s.reviews != 1 || s.patches != 0 || !logged {
				t.Fatalf("operation=%+v err=%v reviews=%d patches=%d logged=%v", out, err, s.reviews, s.patches, logged)
			}
			assertAccessReviewCleanup(t, f, o, s.cleaned)
		})
	}
}

func assertAccessReviewCleanup(t *testing.T, f *fixture, o Operation, removed bool) {
	t.Helper()
	current, err := f.s.GetOperation(context.Background(), o.ID)
	if err != nil || current.Cleanup != CleanupVerified || !current.OwnerFenced || !removed {
		t.Fatalf("cleanup: operation=%+v err=%v removed=%v", current, err, removed)
	}
	var holds int
	if err = f.db.DB.QueryRow(`SELECT COUNT(*) FROM k8s_workload_holds WHERE operation_id=?`, o.ID).Scan(&holds); err != nil || holds != 0 {
		t.Fatalf("hold not released: %d %v", holds, err)
	}
}

func TestPrivateAccessReviewFixedRequestAndSuccessfulPatch(t *testing.T) {
	f, r, d, o := testRuntime(t)
	d.deployment = deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")
	d.patchResult = &target.SecretResult{Stdout: deploymentJSON(o.Intent.Image, o.ID, "uid-one", 2, 2, 2, 2, "")}
	d.afterReview = func() {
		live, err := f.s.GetOperation(context.Background(), o.ID)
		if err != nil || live.DispatchFenced || live.Business != NotDispatched {
			t.Fatal("review after fence", err)
		}
	}
	c := NewController(f.s, r)
	out, err := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if err != nil || !out.Successful() || d.reviewCount != 1 || d.patchCount != 1 {
		t.Fatalf("operation=%+v err=%v reviews=%d patches=%d", out, err, d.reviewCount, d.patchCount)
	}
	var request map[string]any
	if json.Unmarshal(d.reviewRequest, &request) != nil {
		t.Fatal("missing JSON review")
	}
	want, _ := json.Marshal(map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": map[string]any{"resourceAttributes": patchAccessAttributes(f.target)}})
	var expected map[string]any
	_ = json.Unmarshal(want, &expected)
	if !reflect.DeepEqual(request, expected) {
		t.Fatal("review not restricted to exact target")
	}
	for _, args := range d.calls {
		for i, arg := range args {
			if arg == "create" && i > 0 {
				if !reflect.DeepEqual(args[i:], []string{"create", "--raw=" + accessReviewPath, "--filename=-"}) {
					t.Fatal("unfixed raw POST argv", args[i:])
				}
			}
		}
	}
	assertAccessReviewCleanup(t, f, o, d.removed)
}

func TestPrivateAccessReviewFailuresNeverFenceOrPatch(t *testing.T) {
	for _, kind := range []string{"denied", "unavailable", "nonzero", "malformed", "wrong-target", "truncated", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			f, r, d, o := testRuntime(t)
			d.deployment = deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")
			want := ErrAccessReview
			reason := "permission_check_unverified"
			switch kind {
			case "denied":
				d.reviewResult = &target.SecretResult{Stdout: accessReviewJSON(f.target, map[string]any{"allowed": false, "denied": true})}
				want = ErrPatchDenied
				reason = "patch_not_authorized"
			case "unavailable":
				d.reviewErr = ErrTransportUnknown
			case "canceled":
				d.reviewErr = context.DeadlineExceeded
			case "nonzero":
				d.reviewResult = &target.SecretResult{ExitCode: 1, Stdout: accessReviewJSON(f.target, map[string]any{"allowed": true}), Stderr: []byte("Forbidden private diagnostic")}
			case "malformed":
				d.reviewResult = &target.SecretResult{Stdout: []byte(`{"status":{"allowed":true}}`)}
			case "wrong-target":
				wrong := f.target
				wrong.Namespace = "other"
				d.reviewResult = &target.SecretResult{Stdout: accessReviewJSON(wrong, map[string]any{"allowed": true})}
			case "truncated":
				d.reviewResult = &target.SecretResult{Truncated: true, Stdout: accessReviewJSON(f.target, map[string]any{"allowed": true})}
			}
			out, err := NewController(f.s, r).Run(context.Background(), o.ID, o.OwnerEpoch, nil)
			if !errors.Is(err, want) || out.Business != Failed || out.DispatchFenced || out.ReasonCode != reason || d.reviewCount != 1 || d.patchCount != 0 {
				t.Fatalf("operation=%+v err=%v reviews=%d patches=%d", out, err, d.reviewCount, d.patchCount)
			}
			assertAccessReviewCleanup(t, f, o, d.removed)
		})
	}
}

func TestPrivateAllowedReviewDoesNotProvePatchOutcome(t *testing.T) {
	for _, kind := range []string{"transport-lost", "rbac-revoked"} {
		t.Run(kind, func(t *testing.T) {
			f, r, d, o := testRuntime(t)
			d.deployment = deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")
			if kind == "transport-lost" {
				d.patchErr = ErrTransportUnknown
			} else {
				d.patchResult = &target.SecretResult{ExitCode: 1, Stderr: []byte("Forbidden: patch permission revoked after access review")}
			}
			out, err := NewController(f.s, r).Run(context.Background(), o.ID, o.OwnerEpoch, nil)
			if !errors.Is(err, ErrOutcomeUnknown) || out.Business != OutcomeUnknown || !out.DispatchFenced || !out.OwnerFenced || out.Cleanup != CleanupVerified || d.reviewCount != 1 || d.patchCount != 1 || !d.removed {
				t.Fatalf("operation=%+v err=%v reviews=%d patches=%d", out, err, d.reviewCount, d.patchCount)
			}
			if err = f.s.ReleaseHold(context.Background(), o.ID, o.OwnerEpoch); err != ErrHeld {
				t.Fatal("unknown hold released", err)
			}
		})
	}
}

func TestPrivateLocalRevocationAfterReviewStillPreventsDispatch(t *testing.T) {
	f, r, d, o := testRuntime(t)
	d.deployment = deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")
	d.afterReview = func() {
		if err := f.s.DeleteGrant(context.Background(), f.grant.ID, f.grant.Revision); err != nil {
			t.Fatal(err)
		}
	}
	out, err := NewController(f.s, r).Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if err == nil || out.Business != Failed || out.DispatchFenced || d.reviewCount != 1 || d.patchCount != 0 {
		t.Fatalf("operation=%+v err=%v reviews=%d patches=%d", out, err, d.reviewCount, d.patchCount)
	}
	assertAccessReviewCleanup(t, f, o, d.removed)
}

func TestPrivateSameHealthyImageSkipsAccessReview(t *testing.T) {
	f, r, d, o := testRuntime(t)
	d.deployment = deploymentJSON(o.Intent.Image, "", "uid-one", 2, 2, 2, 2, "")
	d.reviewErr = ErrTool
	out, err := NewController(f.s, r).Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if err != nil || out.Business != Unchanged || !out.Successful() || out.DispatchFenced || d.reviewCount != 0 || d.patchCount != 0 {
		t.Fatalf("operation=%+v err=%v reviews=%d patches=%d", out, err, d.reviewCount, d.patchCount)
	}
	assertAccessReviewCleanup(t, f, o, d.removed)
}

func TestPrivateWriterCannotBypassAccessReview(t *testing.T) {
	f, r, d, o := testRuntime(t)
	ctx := context.Background()
	w, err := r.OpenWriter(ctx, o.ID, o.OwnerEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(ctx)
	before := beforeReceipt()
	if _, err = f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, before.UID); err != nil {
		t.Fatal(err)
	}
	o, _ = f.s.GetOperation(ctx, o.ID)
	o, err = f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Patch(ctx, o, nil); err != ErrAccessReview || d.patchCount != 0 {
		t.Fatal("unreviewed session dispatched", err)
	}
	if err = w.CheckPatchAccess(ctx); err != ErrOwner || d.reviewCount != 0 {
		t.Fatal("review after dispatch fence", err)
	}
}

func TestPrivateLocalRevocationAfterFenceRemainsUnknown(t *testing.T) {
	f, r, d, o := testRuntime(t)
	d.deployment = deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")
	out, err := NewController(f.s, r).Run(context.Background(), o.ID, o.OwnerEpoch, func(phase, code string) {
		if phase == "update" && code == "image_update_dispatched" {
			if err := f.s.DeleteGrant(context.Background(), f.grant.ID, f.grant.Revision); err != nil {
				t.Fatal(err)
			}
		}
	})
	// The caller cannot infer that a fenced writer did not send PATCH from a
	// failed CLI/runtime call; the persisted state must remain conservative.
	if !errors.Is(err, ErrOutcomeUnknown) || out.Business != OutcomeUnknown || !out.DispatchFenced || !out.OwnerFenced || out.Cleanup != CleanupVerified || d.reviewCount != 1 || d.patchCount != 0 || !d.removed {
		t.Fatalf("operation=%+v err=%v reviews=%d patches=%d", out, err, d.reviewCount, d.patchCount)
	}
	if err = f.s.ReleaseHold(context.Background(), o.ID, o.OwnerEpoch); err != ErrHeld {
		t.Fatal("unknown hold released", err)
	}
}

func TestPrivateFailedRecheckInvalidatesEarlierPermission(t *testing.T) {
	f, r, d, o := testRuntime(t)
	ctx := context.Background()
	w, err := r.OpenWriter(ctx, o.ID, o.OwnerEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(ctx)
	if err = w.CheckPatchAccess(ctx); err != nil {
		t.Fatal(err)
	}
	d.reviewResult = &target.SecretResult{Stdout: accessReviewJSON(f.target, map[string]any{"allowed": false})}
	if err = w.CheckPatchAccess(ctx); err != ErrPatchDenied {
		t.Fatal("recheck allowed", err)
	}
	before := beforeReceipt()
	if _, err = f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, before.UID); err != nil {
		t.Fatal(err)
	}
	o, _ = f.s.GetOperation(ctx, o.ID)
	o, err = f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Patch(ctx, o, nil); err != ErrAccessReview || d.patchCount != 0 || d.reviewCount != 2 {
		t.Fatal("stale permission reused", err)
	}
}
