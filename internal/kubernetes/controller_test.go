package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"testing"
	"time"
)

type controlledSession struct {
	t                    *testing.T
	f                    *fixture
	operation            Operation
	reads                [][]byte
	accepted             []byte
	patchErr, errorClose error
	reviews              int
	reviewErr            error
	patches              int
	patch                []byte
	cleaned              bool
}

func (s *controlledSession) GetDeployment(context.Context) ([]byte, error) {
	if len(s.reads) == 0 {
		return nil, errors.New("read_failed")
	}
	r := s.reads[0]
	if len(s.reads) > 1 {
		s.reads = s.reads[1:]
	}
	return append([]byte(nil), r...), nil
}
func (s *controlledSession) CheckPatchAccess(ctx context.Context) error {
	s.reviews++
	live, err := s.f.s.GetOperation(ctx, s.operation.ID)
	if err != nil || live.DispatchFenced || live.Business != NotDispatched {
		s.t.Fatal("permission review followed dispatch fence", err)
	}
	return s.reviewErr
}
func (s *controlledSession) Patch(ctx context.Context, o Operation, p []byte) ([]byte, error) {
	s.patches++
	s.patch = append([]byte(nil), p...)
	live, e := s.f.s.GetOperation(ctx, o.ID)
	if e != nil || !live.DispatchFenced || live.Business != OutcomeUnknown {
		s.t.Fatal("PATCH preceded durable fence", e)
	}
	return append([]byte(nil), s.accepted...), s.patchErr
}
func (s *controlledSession) Close(ctx context.Context) (RuntimeLease, error) {
	s.cleaned = true
	if s.errorClose != nil {
		return RuntimeLease{}, s.errorClose
	}
	cleanupRuntime(s.t, s.f, s.operation)
	return s.f.s.GetRuntimeLease(ctx, s.operation.ID)
}
func deploymentJSON(image, marker, uid string, desired, total, updated, available int, extra string) []byte {
	v := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"uid": uid, "resourceVersion": "rv-not-an-integer/001", "generation": 3, "annotations": map[string]string{OperationAnnotation: marker}}, "spec": map[string]any{"replicas": desired, "template": map[string]any{"metadata": map[string]any{"labels": map[string]string{"app": "backend"}}, "spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": image}}}}}, "status": map[string]any{"observedGeneration": 3, "replicas": total, "updatedReplicas": updated, "availableReplicas": available}}
	if extra != "" {
		v["privateIgnoredField"] = extra
	}
	b, _ := json.Marshal(v)
	return b
}
func setupController(t *testing.T) (*Controller, *fixture, Operation, *controlledSession) {
	t.Helper()
	f := newFixture(t, storetest.Open(t))
	o := f.operation(t, "run-1")
	var e error
	o, e = f.s.ApproveOperation(context.Background(), o.ID, o.OwnerEpoch, o.Revision, o.IntentHash)
	if e != nil {
		t.Fatal(e)
	}
	sess := &controlledSession{t: t, f: f, operation: o}
	c := NewController(f.s, nil)
	c.poll = time.Millisecond
	c.writer = func(context.Context, string, string) (deploymentWriter, error) { f.runtime(t, o); return sess, nil }
	return c, f, o, sess
}
func TestControllerSingleCASAndHPAReadiness(t *testing.T) {
	c, _, o, s := setupController(t)
	s.reads = [][]byte{deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "private"), deploymentJSON(o.Intent.Image, o.ID, "uid-one", 3, 3, 3, 3, "")}
	s.accepted = deploymentJSON(o.Intent.Image, o.ID, "uid-one", 2, 3, 2, 1, "")
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if e != nil || !out.Successful() || !out.OwnerFenced || s.patches != 1 || s.reviews != 1 || !s.cleaned {
		t.Fatalf("out=%+v err=%v patches=%d", out, e, s.patches)
	}
	var patch []struct {
		Op, Path string
		Value    any
	}
	if json.Unmarshal(s.patch, &patch) != nil || len(patch) != 6 || patch[0].Path != "/metadata/uid" || patch[1].Value != "rv-not-an-integer/001" || patch[2].Path != "/spec/template/spec/containers/0/name" || patch[4].Op != "replace" {
		t.Fatal("unsafe patch", string(s.patch))
	}
	if _, e = c.Run(context.Background(), o.ID, o.OwnerEpoch, nil); !errors.Is(e, ErrOwner) || s.patches != 1 {
		t.Fatal("duplicate dispatched", e)
	}
}
func TestControllerSameHealthyImageIsUnchanged(t *testing.T) {
	c, _, o, s := setupController(t)
	s.reads = [][]byte{deploymentJSON(o.Intent.Image, "other-operation", "uid-one", 2, 2, 2, 2, "")}
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if e != nil || out.Business != Unchanged || !out.Successful() || s.patches != 0 || s.reviews != 0 {
		t.Fatalf("out=%+v err=%v", out, e)
	}
}
func TestControllerLostPatchStaysUnknownAndHolds(t *testing.T) {
	c, f, o, s := setupController(t)
	s.reads = [][]byte{deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")}
	s.patchErr = errors.New("transport_lost")
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if !errors.Is(e, ErrOutcomeUnknown) || out.Business != OutcomeUnknown || !out.OwnerFenced || out.Cleanup != CleanupVerified || s.patches != 1 {
		t.Fatalf("out=%+v err=%v", out, e)
	}
	if f.s.ReleaseHold(context.Background(), o.ID, o.OwnerEpoch) != ErrHeld {
		t.Fatal("unknown hold released")
	}
}
func TestControllerReadyCleanupBlockedRemainsBusinessReady(t *testing.T) {
	c, _, o, s := setupController(t)
	s.reads = [][]byte{deploymentJSON(o.Intent.Image, "", "uid-one", 2, 2, 2, 2, "")}
	s.errorClose = ErrRuntimeUnverified
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if e == nil || out.Business != Unchanged || out.Successful() || !out.OwnerFenced {
		t.Fatalf("out=%+v err=%v", out, e)
	}
}
func TestControllerForeignTemplateCannotComplete(t *testing.T) {
	c, _, o, s := setupController(t)
	s.reads = [][]byte{deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, ""), deploymentJSON(o.Intent.Image, "foreign-operation", "uid-one", 2, 2, 2, 2, "")}
	s.accepted = deploymentJSON(o.Intent.Image, o.ID, "uid-one", 2, 3, 2, 1, "")
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if !errors.Is(e, ErrSuperseded) || out.Business != Superseded || out.Successful() {
		t.Fatalf("out=%+v err=%v", out, e)
	}
}
func TestPrivateDeploymentParserRejectsInvalidFacts(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"kind":"Pod"}`), []byte(`null`), []byte(`{} {}`)} {
		if _, e := decodeDeployment(raw, "app"); e == nil {
			t.Fatal("malformed accepted")
		}
	}
	raw := deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "private-secret")
	v, e := decodeDeployment(raw, "app")
	if e != nil {
		t.Fatal(e)
	}
	safe, _ := json.Marshal(v.Receipt)
	if string(safe) == string(raw) {
		t.Fatal("raw object returned")
	}
}

type readonlySession struct {
	raw      []byte
	closeErr error
}

func (s *readonlySession) GetDeployment(context.Context) ([]byte, error) {
	return append([]byte(nil), s.raw...), nil
}
func (s *readonlySession) Close(context.Context) (RuntimeLease, error) {
	return RuntimeLease{}, s.closeErr
}
func TestReconciliationOldImageDoesNotDisproveLostPatch(t *testing.T) {
	c, _, o, s := setupController(t)
	old := deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")
	s.reads = [][]byte{old}
	s.patchErr = errors.New("lost")
	out, _ := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	c.observer = func(context.Context, string, Target, string, string) (deploymentReader, error) {
		return &readonlySession{raw: old}, nil
	}
	out, e := c.Reconcile(context.Background(), o.ID, out.Revision)
	if e != nil || out.Business != OutcomeUnknown {
		t.Fatalf("out=%+v err=%v", out, e)
	}
}
func TestObserverCleanupFailureBlocksReuse(t *testing.T) {
	c, _, o, s := setupController(t)
	raw := deploymentJSON(o.Intent.Image, "", "uid-one", 2, 2, 2, 2, "")
	s.reads = [][]byte{raw}
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if e != nil {
		t.Fatal(e)
	}
	c.observer = func(context.Context, string, Target, string, string) (deploymentReader, error) {
		return &readonlySession{raw: raw, closeErr: ErrRuntimeUnverified}, nil
	}
	if _, e = c.VerifyReuse(context.Background(), o.ID, out.IntentHash); !errors.Is(e, ErrRuntimeUnverified) {
		t.Fatal("observer cleanup ignored", e)
	}
}

func TestControllerAcceptedDeadlineRemainsKnownFailure(t *testing.T) {
	c, _, o, s := setupController(t)
	s.reads = [][]byte{deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")}
	var doc map[string]any
	_ = json.Unmarshal(deploymentJSON(o.Intent.Image, o.ID, "uid-one", 2, 2, 1, 1, ""), &doc)
	doc["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}
	s.accepted, _ = json.Marshal(doc)
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if !errors.Is(e, ErrRollout) || out.Business != Failed || !out.OwnerFenced || out.Cleanup != CleanupVerified {
		t.Fatalf("out=%+v err=%v", out, e)
	}
}
func TestReconciledReadyReleasesOnlyVerifiedHold(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "blocked"}[blocked], func(t *testing.T) {
			c, f, o, s := setupController(t)
			s.reads = [][]byte{deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, "")}
			s.patchErr = errors.New("lost")
			if blocked {
				s.errorClose = ErrRuntimeUnverified
			}
			out, _ := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
			c.observer = func(context.Context, string, Target, string, string) (deploymentReader, error) {
				return &readonlySession{raw: deploymentJSON(o.Intent.Image, o.ID, "uid-one", 2, 2, 2, 2, "")}, nil
			}
			out, e := c.Reconcile(context.Background(), o.ID, out.Revision)
			if e != nil || out.Business != Ready {
				t.Fatal(out, e)
			}
			next := f.operation(t, "run-2")
			_, e = f.s.AcquireHold(context.Background(), next.ID, next.OwnerEpoch, "uid-one")
			if blocked && !errors.Is(e, ErrHeld) || !blocked && e != nil {
				t.Fatal("incorrect hold release", e)
			}
		})
	}
}

func TestControllerOldGenerationDeadlineContinuesToReady(t *testing.T) {
	c, _, o, s := setupController(t)
	s.reads = [][]byte{deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 2, 2, 2, 2, ""), deploymentJSON(o.Intent.Image, o.ID, "uid-one", 2, 2, 2, 2, "")}
	var doc map[string]any
	_ = json.Unmarshal(deploymentJSON(o.Intent.Image, o.ID, "uid-one", 2, 2, 1, 1, ""), &doc)
	status := doc["status"].(map[string]any)
	status["observedGeneration"] = 2
	status["conditions"] = []any{map[string]any{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}
	s.accepted, _ = json.Marshal(doc)
	out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if e != nil || !out.Successful() || s.patches != 1 {
		t.Fatalf("old generation condition terminated new rollout: state=%s err=%v", out.Business, e)
	}
}

// Real runtime open failures must not leave a typed nil inside an interface:
// the controller always performs owner fencing in its deferred finalization.
func TestControllerRuntimeOpenFailureFinalizesWithoutSession(t *testing.T) {
	_, f, o, _ := setupController(t)
	openErr := errors.New("test_transport_unavailable")
	calls := 0
	runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) {
		calls++
		return nil, openErr
	}, nil)
	c := NewController(f.s, runtime)
	out, err := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
	if !errors.Is(err, openErr) || out.Business != Failed || !out.OwnerFenced || out.DispatchFenced || out.Successful() || calls != 1 {
		t.Fatalf("open failure was not safely finalized: business=%s fenced=%v dispatched=%v calls=%d err=%v", out.Business, out.OwnerFenced, out.DispatchFenced, calls, err)
	}
	reader, err := c.observer(context.Background(), "reconcile", o.Intent.Authorization.Target, o.ID, "observer-test")
	if !errors.Is(err, openErr) || reader != nil || calls != 2 {
		t.Fatalf("failed observer exposed a session: reader=%T calls=%d err=%v", reader, calls, err)
	}
}

func TestDeploymentEventTimeSurvivesLaterObservation(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "patch", true: "unchanged"}[unchanged], func(t *testing.T) {
			c, f, o, s := setupController(t)
			at := time.Date(2026, 11, 3, 10, 0, 0, 123000000, time.UTC)
			f.s.now = func() time.Time { return at }
			old := deploymentJSON("registry.invalid/app/backend:old", "", "uid-one", 1, 1, 1, 1, "")
			ready := deploymentJSON(o.Intent.Image, o.ID, "uid-one", 1, 1, 1, 1, "")
			if unchanged {
				old = ready
			}
			s.reads = [][]byte{old}
			s.accepted = ready
			out, e := c.Run(context.Background(), o.ID, o.OwnerEpoch, nil)
			if e != nil || out.Receipt.DeploymentAt != at.UnixMilli() {
				t.Fatal(out, e)
			}
			f.s.now = func() time.Time { return at.Add(time.Hour) }
			c.observer = func(context.Context, string, Target, string, string) (deploymentReader, error) {
				return &readonlySession{raw: ready}, nil
			}
			out, e = c.Reconcile(context.Background(), o.ID, out.Revision)
			if e != nil || out.Receipt.DeploymentAt != at.UnixMilli() || out.UpdatedAt != at.Add(time.Hour).UnixMilli() {
				t.Fatal(out, e)
			}
		})
	}
}
