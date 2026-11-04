package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var ErrRollout = errors.New("kubernetes_workload_not_ready")
var ErrSuperseded = errors.New("kubernetes_target_superseded")
var ErrOutcomeUnknown = errors.New("kubernetes_outcome_unknown")

const OperationAnnotation = "pipewright.io/deployment-operation"

type deploymentReader interface {
	GetDeployment(context.Context) ([]byte, error)
	Close(context.Context) (RuntimeLease, error)
}
type deploymentWriter interface {
	deploymentReader
	CheckPatchAccess(context.Context) error
	Patch(context.Context, Operation, []byte) ([]byte, error)
}

// Controller owns the fixed deployment protocol. All raw objects remain inside
// this package; logs expose stable phase/reason codes only.
type Controller struct {
	store    *Service
	runtime  *Runtime
	writer   func(context.Context, string, string) (deploymentWriter, error)
	observer func(context.Context, string, Target, string, string) (deploymentReader, error)
	poll     time.Duration
	owner    string
}

func NewController(s *Service, r *Runtime, processOwner ...string) *Controller {
	c := &Controller{store: s, runtime: r, poll: 2 * time.Second}
	c.owner = "process_" + uuid.NewString()
	if len(processOwner) == 1 && validID(processOwner[0]) {
		c.owner = processOwner[0]
	}
	c.writer = func(ctx context.Context, id, owner string) (deploymentWriter, error) {
		session, err := r.OpenWriter(ctx, id, owner)
		if err != nil {
			return nil, err
		}
		return session, nil
	}
	c.observer = func(ctx context.Context, purpose string, t Target, id, owner string) (deploymentReader, error) {
		session, err := r.OpenObserver(ctx, purpose, t, id, owner)
		if err != nil {
			return nil, err
		}
		return session, nil
	}
	return c
}

type PhaseLog func(phase, code string)

func phase(log PhaseLog, p, c string) {
	if log != nil {
		log(p, c)
	}
}

type deploymentView struct {
	Receipt
	index       int
	annotations bool
	template    map[string]any
}

func decodeDeployment(raw []byte, container string) (deploymentView, error) {
	var out deploymentView
	if len(raw) == 0 || len(raw) > MaxPrivateResponseBytes {
		return out, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var doc map[string]any
	if d.Decode(&doc) != nil || d.Decode(new(any)) != io.EOF {
		return out, ErrInvalid
	}
	obj := func(v any) map[string]any { x, _ := v.(map[string]any); return x }
	str := func(v any) string { x, _ := v.(string); return x }
	num := func(v any) (int64, bool) {
		if v == nil {
			return 0, true
		}
		n, ok := v.(json.Number)
		if !ok {
			return 0, false
		}
		i, e := n.Int64()
		return i, e == nil && i >= 0
	}
	if str(doc["kind"]) != "Deployment" || str(doc["apiVersion"]) != "apps/v1" {
		return out, ErrInvalid
	}
	meta, spec, status := obj(doc["metadata"]), obj(doc["spec"]), obj(doc["status"])
	out.UID = str(meta["uid"])
	out.ResourceVersion = str(meta["resourceVersion"])
	var ok bool
	if out.Generation, ok = num(meta["generation"]); !ok {
		return out, ErrInvalid
	}
	if out.ObservedGeneration, ok = num(status["observedGeneration"]); !ok {
		return out, ErrInvalid
	}
	if out.DesiredReplicas, ok = num(spec["replicas"]); !ok {
		return out, ErrInvalid
	}
	if spec["replicas"] == nil {
		out.DesiredReplicas = 1
	}
	if out.TotalReplicas, ok = num(status["replicas"]); !ok {
		return out, ErrInvalid
	}
	if out.UpdatedReplicas, ok = num(status["updatedReplicas"]); !ok {
		return out, ErrInvalid
	}
	if out.AvailableReplicas, ok = num(status["availableReplicas"]); !ok {
		return out, ErrInvalid
	}
	if spec["paused"] != nil {
		var ok bool
		out.Paused, ok = spec["paused"].(bool)
		if !ok {
			return out, ErrInvalid
		}
	}
	ann := obj(meta["annotations"])
	out.annotations = ann != nil
	out.Marker = str(ann[OperationAnnotation])
	out.Revision = str(ann["deployment.kubernetes.io/revision"])
	out.template = obj(spec["template"])
	if out.template == nil {
		return out, ErrInvalid
	}
	containers, _ := obj(out.template["spec"])["containers"].([]any)
	found := 0
	for i, item := range containers {
		c := obj(item)
		if str(c["name"]) == container {
			found++
			out.index = i
			out.Image = str(c["image"])
		}
	}
	if found != 1 {
		return out, ErrInvalid
	}
	out.TemplateHash = fingerprint(out.template)
	conditions, _ := status["conditions"].([]any)
	for _, item := range conditions {
		c := obj(item)
		if str(c["type"]) == "Progressing" && str(c["status"]) == "False" && str(c["reason"]) == "ProgressDeadlineExceeded" {
			out.Reason = "progress_deadline_exceeded"
		}
	}
	if !validReceipt(out.Receipt) {
		return deploymentView{}, ErrInvalid
	}
	return out, nil
}
func readDeployment(ctx context.Context, r deploymentReader, container string) (deploymentView, error) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, e := r.GetDeployment(call)
	defer clear(raw)
	if e != nil {
		return deploymentView{}, e
	}
	return decodeDeployment(raw, container)
}
func fixedPatch(o Operation, v deploymentView) ([]byte, Receipt, error) {
	type change struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	prefix := "/spec/template/spec/containers/" + strconv.Itoa(v.index)
	changes := []change{{"test", "/metadata/uid", v.UID}, {"test", "/metadata/resourceVersion", v.ResourceVersion}, {"test", prefix + "/name", o.Intent.Authorization.Target.Container}, {"test", prefix + "/image", v.Image}, {"replace", prefix + "/image", o.Intent.Image}}
	if v.annotations {
		changes = append(changes, change{"add", "/metadata/annotations/pipewright.io~1deployment-operation", o.ID})
	} else {
		changes = append(changes, change{"add", "/metadata/annotations", map[string]string{OperationAnnotation: o.ID}})
	}
	// This temporary object never leaves the private parser/controller boundary.
	containers := v.template["spec"].(map[string]any)["containers"].([]any)
	containers[v.index].(map[string]any)["image"] = o.Intent.Image
	before := v.Receipt
	before.ExpectedTemplateHash = fingerprint(v.template)
	b, e := json.Marshal(changes)
	return b, before, e
}
func (c *Controller) record(ctx context.Context, id, owner string, state BusinessState, r Receipt) (Operation, error) {
	o, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return o, e
	}
	return c.store.RecordObservation(ctx, id, owner, o.Revision, state, r)
}
func sameObservation(base Receipt, now Receipt, marker string) bool {
	return base.UID == now.UID && base.TemplateHash == now.TemplateHash && base.Image == now.Image && now.Marker == marker
}
func (c *Controller) Run(ctx context.Context, id, owner string, log PhaseLog) (out Operation, runErr error) {
	o, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return out, e
	}
	if o.OwnerEpoch != owner || o.OwnerFenced || o.Retired || o.Business != NotDispatched || o.DispatchFenced {
		return o, ErrOwner
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(o.Intent.TimeoutSeconds)*time.Second)
	defer cancel()
	var w deploymentWriter
	failureReason := "pre_dispatch_failed"
	defer func() {
		clean, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		phase(log, "cleanup", "cleanup_started")
		if w != nil {
			if _, e := w.Close(clean); e != nil {
				runErr = errors.Join(runErr, e)
			}
		}
		fresh, e := c.store.GetOperation(clean, id)
		if e != nil {
			runErr = errors.Join(runErr, e)
			return
		}
		if runErr != nil && fresh.Business == NotDispatched {
			fresh, e = c.store.FailBeforeDispatch(clean, id, owner, fresh.Revision, failureReason)
			if e != nil {
				runErr = errors.Join(runErr, e)
			}
		}
		if !fresh.OwnerFenced {
			fresh, e = c.store.FenceOwner(clean, id, owner, fresh.Revision)
			if e != nil {
				runErr = errors.Join(runErr, e)
			}
		}
		if fresh.Cleanup == CleanupVerified && fresh.Business != OutcomeUnknown && fresh.Business != Submitted {
			if e = c.store.ReleaseHold(clean, id, owner); e != nil {
				runErr = errors.Join(runErr, e)
			}
		}
		out = fresh
		if !out.Successful() && runErr == nil {
			runErr = ErrRollout
		}
		if out.Cleanup == CleanupVerified {
			phase(log, "cleanup", "cleanup_verified")
		} else {
			phase(log, "cleanup", "cleanup_unverified")
		}
	}()
	if e = c.store.ValidateAuthorization(ctx, o.Intent.Authorization); e != nil {
		return o, e
	}
	phase(log, "preflight", "preflight_started")
	w, e = c.writer(work, id, owner)
	if e != nil {
		return o, e
	}
	v, e := readDeployment(work, w, o.Intent.Authorization.Target.Container)
	if e != nil {
		return o, e
	}
	if v.Paused || v.DesiredReplicas == 0 {
		return o, ErrRollout
	}
	if _, e = c.store.AcquireHold(work, id, owner, v.UID); e != nil {
		return o, e
	}
	base := v.Receipt
	unchanged := v.Image == o.Intent.Image
	if !unchanged {
		check, checked := context.WithTimeout(work, 10*time.Second)
		e = w.CheckPatchAccess(check)
		checked()
		if e != nil {
			switch {
			case errors.Is(e, ErrPatchDenied):
				failureReason = "patch_not_authorized"
			case errors.Is(e, ErrAccessReview):
				failureReason = "permission_check_unverified"
			}
			phase(log, "preflight", failureReason)
			return o, e
		}
		patch, before, e := fixedPatch(o, v)
		if e != nil {
			return o, e
		}
		defer clear(patch)
		fresh, e := c.store.GetOperation(work, id)
		if e != nil {
			return o, e
		}
		prepared, e := c.store.PrepareDispatch(work, id, owner, fresh.Revision, before)
		if e != nil {
			return o, e
		}
		phase(log, "update", "image_update_dispatched")
		call, done := context.WithTimeout(work, 10*time.Second)
		raw, patchErr := w.Patch(call, prepared, patch)
		done()
		defer clear(raw)
		if patchErr != nil {
			return prepared, ErrOutcomeUnknown
		}
		accepted, e := decodeDeployment(raw, o.Intent.Authorization.Target.Container)
		clear(raw)
		if e != nil {
			return prepared, ErrOutcomeUnknown
		}
		if accepted.UID != v.UID || accepted.Marker != o.ID || accepted.Image != o.Intent.Image {
			_, _ = c.record(context.WithoutCancel(ctx), id, owner, Superseded, accepted.Receipt)
			return prepared, ErrSuperseded
		}
		if _, e = c.record(work, id, owner, Submitted, accepted.Receipt); e != nil {
			return prepared, e
		}
		base = accepted.Receipt
		v = accepted
	}
	phase(log, "rollout", "rollout_observing")
	for {
		if !sameObservation(base, v.Receipt, base.Marker) {
			_, _ = c.record(context.WithoutCancel(ctx), id, owner, Superseded, v.Receipt)
			return o, ErrSuperseded
		}
		if receiptReady(v.Receipt) {
			state := Ready
			if unchanged {
				state = Unchanged
			}
			out, e = c.record(work, id, owner, state, v.Receipt)
			if e == nil {
				phase(log, "rollout", "rollout_ready")
			}
			return out, e
		}
		if v.Reason == "progress_deadline_exceeded" && v.ObservedGeneration >= v.Generation {
			if !unchanged {
				_, _ = c.record(context.WithoutCancel(ctx), id, owner, Failed, v.Receipt)
			}
			return o, ErrRollout
		}
		timer := time.NewTimer(c.poll)
		select {
		case <-work.Done():
			timer.Stop()
			if !unchanged {
				_, _ = c.record(context.WithoutCancel(ctx), id, owner, Failed, v.Receipt)
			}
			return o, ErrRollout
		case <-timer.C:
		}
		v, e = readDeployment(work, w, o.Intent.Authorization.Target.Container)
		if e != nil {
			return o, ErrOutcomeUnknown
		}
	}
}

// Inspect obtains a fresh, bounded observation using a read-only session. Closing
// that observer never clears a blocked writer lease or changes original history.
func (c *Controller) Inspect(ctx context.Context, id, purpose string) (receipt Receipt, err error) {
	o, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return receipt, e
	}
	if purpose != "reconcile" && purpose != "reuse" {
		return receipt, ErrInvalid
	}
	r, e := c.observer(ctx, purpose, o.Intent.Authorization.Target, id, c.owner)
	if e != nil {
		return receipt, e
	}
	defer func() {
		clean, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		_, closeErr := r.Close(clean)
		err = errors.Join(err, closeErr)
	}()
	v, e := readDeployment(ctx, r, o.Intent.Authorization.Target.Container)
	return v.Receipt, e
}
func (c *Controller) Probe(ctx context.Context, t Target) (receipt Receipt, err error) {
	r, e := c.observer(ctx, "probe", t, "", c.owner)
	if e != nil {
		return receipt, e
	}
	defer func() {
		clean, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		_, closeErr := r.Close(clean)
		err = errors.Join(err, closeErr)
	}()
	v, e := readDeployment(ctx, r, t.Container)
	return v.Receipt, e
}
func (c *Controller) Reconcile(ctx context.Context, id string, expected int64) (Operation, error) {
	o, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return o, e
	}
	if o.Revision != expected {
		return o, ErrConflict
	}
	if !o.OwnerFenced {
		return o, ErrOwner
	}
	r, e := c.Inspect(ctx, id, "reconcile")
	if e != nil {
		return o, e
	}
	return c.store.ApplyReconciliation(ctx, id, expected, r)
}

// ApplyReconciliation records a server-observed receipt after the caller's final
// session/CAS check. HTTP must never construct receipt facts from request JSON.
func (s *Service) ApplyReconciliation(ctx context.Context, id string, expected int64, r Receipt) (Operation, error) {
	o, e := s.GetOperation(ctx, id)
	if e != nil {
		return o, e
	}
	if o.Revision != expected {
		return o, ErrConflict
	}
	if !o.OwnerFenced {
		return o, ErrOwner
	}
	state := OutcomeUnknown
	if !o.DispatchFenced && o.Business == Failed {
		// Read-only cluster facts cannot erase proof that this writer never dispatched.
		state = Failed
	} else if o.Receipt != nil {
		expectedHash := o.Receipt.TemplateHash
		if o.Business == OutcomeUnknown {
			expectedHash = o.Receipt.ExpectedTemplateHash
		}
		if r.UID != o.Receipt.UID || (o.Business != OutcomeUnknown && r.Marker != o.ID && o.DispatchFenced) {
			state = Superseded
		} else if r.Image == o.Intent.Image && r.TemplateHash == expectedHash {
			if !o.DispatchFenced && o.Business == Unchanged {
				state = Unchanged
			} else if r.Marker == o.ID {
				state = Submitted
				if receiptReady(r) {
					state = Ready
				}
			}
		}
	}
	// A single old RV/image does not disprove an in-flight update. Keep unknown.
	out, e := s.RecordReconciliation(ctx, id, expected, state, r)
	if e != nil {
		return out, e
	}
	if out.Cleanup == CleanupVerified && out.Business != OutcomeUnknown && out.Business != Submitted {
		if e = s.ReleaseHold(ctx, id, out.OwnerEpoch); e != nil {
			return out, e
		}
	}
	return out, nil
}
func (c *Controller) VerifyReuse(ctx context.Context, id, intentHash string) (Operation, error) {
	o, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return o, e
	}
	if !o.Successful() || o.IntentHash != intentHash || o.Receipt == nil {
		return o, ErrDenied
	}
	r, e := c.Inspect(ctx, id, "reuse")
	if e != nil {
		return o, e
	}
	if !receiptReady(r) || !sameObservation(*o.Receipt, r, o.Receipt.Marker) {
		return o, ErrSuperseded
	}
	fresh, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return fresh, e
	}
	if fresh.Revision != o.Revision || !fresh.Successful() {
		return fresh, ErrConflict
	}
	return fresh, nil
}

// RecoverSettled only fences writers whose owning run is already terminal. It
// never takes over a running worker, publishes again, or treats age as proof.
func (c *Controller) RecoverSettled(ctx context.Context, departedOwners []string) error {
	owners := map[string]bool{}
	for _, owner := range departedOwners {
		if validID(owner) {
			owners[owner] = true
		}
	}
	if len(owners) == 0 {
		return nil
	}
	ops, e := c.store.PendingRecovery(ctx)
	if e != nil {
		return e
	}
	var result error
	for _, o := range ops {
		if !owners[o.OwnerEpoch] {
			continue
		}
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		var state string
		e = c.store.db.QueryRowContext(ctx, "SELECT status FROM pipeline_runs WHERE id=?", o.RunID).Scan(&state)
		if e != nil {
			result = errors.Join(result, ErrNotFound)
			continue
		}
		if state != "success" && state != "failed" && state != "canceled" {
			continue
		}
		if !o.OwnerFenced {
			if o.Business == NotDispatched {
				o, e = c.store.FailBeforeDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, "worker_interrupted")
				if e != nil {
					result = errors.Join(result, e)
					continue
				}
			}
			o, e = c.store.FenceOwner(ctx, o.ID, o.OwnerEpoch, o.Revision)
			if e != nil {
				result = errors.Join(result, e)
				continue
			}
		}
		if o.Cleanup != CleanupVerified {
			settled, settleErr := c.store.settleNeverStarted(ctx, o.ID, o.OwnerEpoch)
			if settleErr != nil {
				result = errors.Join(result, settleErr)
				continue
			}
			if settled {
				o.Cleanup = CleanupVerified
			}
		}
		if o.Cleanup != CleanupVerified {
			_, e = c.runtime.Cleanup(ctx, o.ID)
			if e != nil {
				result = errors.Join(result, e)
				continue
			}
		}
		o, e = c.store.GetOperation(ctx, o.ID)
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		if o.Cleanup == CleanupVerified && o.Business != Submitted && o.Business != OutcomeUnknown {
			result = errors.Join(result, c.store.ReleaseHold(ctx, o.ID, o.OwnerEpoch))
		}
	}
	leases, e := c.store.PendingRuntimeCleanup(ctx)
	if e != nil {
		return errors.Join(result, e)
	}
	for _, lease := range leases {
		if lease.Purpose == "writer" || !owners[lease.OwnerEpoch] {
			continue
		}
		_, e = c.runtime.Cleanup(ctx, lease.OperationID)
		result = errors.Join(result, e)
	}
	return result
}

// PrepareRetire reads fresh cluster facts outside any authorization/database
// transaction. The HTTP layer must reauthenticate and CAS Retire afterwards.
func (c *Controller) PrepareRetire(ctx context.Context, id string, expected int64) (Receipt, error) {
	o, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return Receipt{}, e
	}
	if o.Revision != expected {
		return Receipt{}, ErrConflict
	}
	if !o.OwnerFenced || o.Cleanup != CleanupVerified || o.Business != OutcomeUnknown || o.Retired {
		return Receipt{}, ErrDenied
	}
	r, e := c.Inspect(ctx, id, "reconcile")
	if e != nil {
		return Receipt{}, e
	}
	fresh, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return Receipt{}, e
	}
	if fresh.Revision != expected {
		return Receipt{}, ErrConflict
	}
	return r, nil
}
