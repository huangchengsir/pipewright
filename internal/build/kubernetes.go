package build

import (
	"context"
	"errors"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"strconv"
)

type kubernetesPreparedKey struct{}
type kubernetesPrepared struct {
	operation kubernetes.Operation
	node      runretry.NodeRef
	adopted   bool
}

func kubernetesStage(stage pipeline.Stage) bool {
	for _, j := range stage.Jobs {
		if j.Type == pipeline.KubernetesJobType {
			return true
		}
	}
	return false
}
func KubernetesBindingKey(stage, job string) string { return stage + "/" + job }

// FreezeKubernetesPolicy runs during initial capture before graph compilation.
// A config author cannot disable server-owned approval by setting Gate=false.
func FreezeKubernetesPolicy(ctx context.Context, s *kubernetes.Service, snap *runretry.Snapshot) error {
	if err := pipeline.ValidateKubernetesStages(snap.Spec); err != nil {
		return err
	}
	snap.Kubernetes = map[string]kubernetes.Authorization{}
	for i, stage := range snap.Spec.Stages {
		for _, j := range stage.Jobs {
			if j.Type != pipeline.KubernetesJobType {
				continue
			}
			if s == nil {
				return runretry.ErrUnavailable
			}
			cfg, _, e := pipeline.ParseKubernetesConfig(j.Config)
			if e != nil {
				return e
			}
			a, e := s.AuthorizeConfigured(ctx, kubernetes.Target{ProjectID: snap.ProjectID, Environment: snap.Trigger.Environment, ConnectionID: cfg.ConnectionID, CredentialID: cfg.CredentialID, Context: cfg.Context, Namespace: cfg.Namespace, Deployment: cfg.Deployment, Container: cfg.Container, Executor: kubernetes.Executor{Kind: cfg.Executor.Kind, ServerID: cfg.Executor.ServerID}})
			if e != nil {
				return e
			}
			snap.Kubernetes[KubernetesBindingKey(stage.ID, j.ID)] = a
			snap.Spec.Stages[i].Gate = stage.Gate || a.RequireApproval
		}
	}
	return nil
}
func sourceDeclarationIndex(a run.Artifact) (int, bool) {
	v, ok := a.Metadata["declarationIndex"]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), n == float64(int(n))
	case string:
		i, e := strconv.Atoi(n)
		return i, e == nil
	}
	return 0, false
}

// exactKubernetesImage proves the selected checkpoint still names a real stored
// artifact from that producer. Neither a tag nor the first matching artifact is
// sufficient; the writer sees repository@sha256 and a full checked-out commit.
func (b *Builder) exactKubernetesImage(ctx context.Context, ex runretry.Execution, n runretry.NodeRef, src pipeline.KubernetesArtifactSource) (kubernetes.ImageSource, string, error) {
	in, e := ex.Inputs(ctx, n)
	if e != nil || len(in.Images) != 1 || len(in.Files) > 0 || b.artifactLister == nil {
		return kubernetes.ImageSource{}, "", runretry.ErrUnavailable
	}
	var image runretry.ImageRef
	for _, v := range in.Images {
		image = v
	}
	if image.ArtifactID == "" || image.ProducerAttemptID == "" || image.ProducerNodeKey == "" {
		return kubernetes.ImageSource{}, "", runretry.ErrUnavailable
	}
	artifacts, e := b.artifactLister(ctx, image.ProducerAttemptID)
	if e != nil {
		return kubernetes.ImageSource{}, "", e
	}
	matches := 0
	var ref string
	for _, a := range artifacts {
		if a.ID != image.ArtifactID {
			continue
		}
		index, valid := sourceDeclarationIndex(a)
		pushed, _ := a.Metadata["pushed"].(bool)
		if a.Type != run.ArtifactImage || a.RunID != image.ProducerAttemptID || metadataString(a, "logicalNodeKey") != image.ProducerNodeKey || metadataString(a, "sourceStageId") != src.StageID || metadataString(a, "sourceJobId") != src.JobID || !pushed || !valid || index != *src.DeclarationIndex {
			return kubernetes.ImageSource{}, "", runretry.ErrUnavailable
		}
		ref = metadataString(a, "digestRepository") + "@" + metadataString(a, "digest")
		if !kubernetes.ValidImageReference(ref) {
			return kubernetes.ImageSource{}, "", runretry.ErrUnavailable
		}
		if metadataString(run.Artifact{Metadata: image.Metadata}, "digestRepository")+"@"+metadataString(run.Artifact{Metadata: image.Metadata}, "digest") != ref {
			return kubernetes.ImageSource{}, "", runretry.ErrUnavailable
		}
		matches++
	}
	if matches != 1 {
		return kubernetes.ImageSource{}, "", runretry.ErrUnavailable
	}
	source := kubernetes.ImageSource{RunID: image.ProducerAttemptID, StageID: src.StageID, JobID: src.JobID, DeclarationIndex: *src.DeclarationIndex, ArtifactID: image.ArtifactID, Commit: ex.Snapshot().CommitSHA}
	if len(source.Commit) != 40 && len(source.Commit) != 64 {
		return source, "", runretry.ErrUnavailable
	}
	return source, ref, nil
}

// PrepareKubernetesStage is injected before the ordinary approval coordinator.
// No credential is opened and no cluster request is sent while approving.
func (b *Builder) PrepareKubernetesStage(ctx context.Context, r *run.Run, stage pipeline.Stage) (dagrun.StagePreparation, error) {
	var out dagrun.StagePreparation
	if err := b.verifyInheritedKubernetes(ctx, stage); err != nil {
		return out, err
	}
	if !kubernetesStage(stage) {
		return out, nil
	}
	if len(stage.Jobs) != 1 || b.kubernetes == nil || b.kubernetesController == nil || b.kubernetesOwner == "" {
		return out, runretry.ErrUnavailable
	}
	j := stage.Jobs[0]
	cfg, src, e := pipeline.ParseKubernetesConfig(j.Config)
	if e != nil {
		return out, e
	}
	ex, ok := runretry.ExecutionFrom(ctx)
	if !ok {
		return out, runretry.ErrUnavailable
	}
	n, ok := ex.Node(stage.ID, j.ID)
	if !ok {
		return out, runretry.ErrUnavailable
	}
	if ex.Decision(n).Action != "execute" {
		return out, nil
	}
	a, ok := ex.Snapshot().Kubernetes[KubernetesBindingKey(stage.ID, j.ID)]
	if !ok || a.Target.ConnectionID != cfg.ConnectionID || a.Target.CredentialID != cfg.CredentialID || a.Target.Namespace != cfg.Namespace || a.Target.Deployment != cfg.Deployment || a.Target.Container != cfg.Container || a.Target.Context != cfg.Context || a.Target.Executor.Kind != cfg.Executor.Kind || a.Target.Executor.ServerID != cfg.Executor.ServerID {
		return out, runretry.ErrUnavailable
	}
	current, e := b.kubernetes.Authorize(ctx, a.Target)
	if e != nil {
		return out, e
	}
	if !SameKubernetesTrust(a, current) || current.RequireApproval && !stage.Gate {
		return out, runretry.ErrUnavailable
	}
	if !runretry.IsRecovery(ex) && current.Fingerprint != a.Fingerprint {
		return out, kubernetes.ErrConflict
	}
	a = current
	source, image, e := b.exactKubernetesImage(ctx, ex, n, src)
	if e != nil {
		return out, e
	}
	attempt := int(r.AttemptNumber)
	if attempt < 1 {
		attempt = 1
	}
	if runretry.IsRecovery(ex) {
		previousID, previousErr := runretry.PreviousKubernetes(ctx, n)
		if previousErr == nil {
			prior, e := b.kubernetes.GetOperation(ctx, previousID)
			if e != nil {
				return out, e
			}
			if !SameKubernetesTrust(prior.Intent.Authorization, a) || prior.Intent.Image != image || prior.Intent.Source != source || prior.Intent.TimeoutSeconds != cfg.TimeoutSeconds {
				return out, runretry.ErrUnavailable
			}
			if !prior.OwnerFenced || prior.Cleanup != kubernetes.CleanupVerified || prior.Retired {
				return out, runretry.ErrBlocked
			}
			if prior.Successful() {
				prior, e = b.kubernetesController.VerifyReuse(ctx, prior.ID, prior.IntentHash)
				if e != nil {
					return out, e
				}
				out.Context = context.WithValue(ctx, kubernetesPreparedKey{}, kubernetesPrepared{operation: prior, node: n, adopted: true})
				out.RequireGate = a.RequireApproval
				out.Approve = func(ctx context.Context) error { return b.kubernetes.ValidateAuthorization(ctx, a) }
				return out, nil
			}
			if prior.Business != kubernetes.Failed {
				return out, runretry.ErrUnknown
			}
		} else if !errors.Is(previousErr, runretry.ErrUnavailable) {
			return out, previousErr
		}
	}
	op, e := b.kubernetes.CreateOperation(ctx, kubernetes.OperationInput{RunID: r.ID, NodeID: n.Key, Attempt: attempt, OwnerEpoch: b.kubernetesOwner, Intent: kubernetes.Intent{Authorization: a, Image: image, Source: source, TimeoutSeconds: cfg.TimeoutSeconds}})
	if e != nil {
		return out, e
	}
	out.Context = context.WithValue(ctx, kubernetesPreparedKey{}, kubernetesPrepared{operation: op, node: n})
	out.RequireGate = a.RequireApproval
	out.Approve = func(ctx context.Context) error {
		current, e := b.kubernetes.GetOperation(ctx, op.ID)
		if e != nil {
			return e
		}
		_, e = b.kubernetes.ApproveOperation(ctx, op.ID, op.OwnerEpoch, current.Revision, op.IntentHash)
		return e
	}
	out.Close = func(ctx context.Context) error {
		current, e := b.kubernetes.GetOperation(ctx, op.ID)
		if e != nil || current.OwnerFenced {
			return e
		}
		if current.Business != kubernetes.NotDispatched {
			return kubernetes.ErrOwner
		}
		current, e = b.kubernetes.FailBeforeDispatch(ctx, op.ID, op.OwnerEpoch, current.Revision, "stage_not_dispatched")
		if e != nil {
			return e
		}
		_, e = b.kubernetes.FenceOwner(ctx, op.ID, op.OwnerEpoch, current.Revision)
		return e
	}
	return out, nil
}
func (b *Builder) runKubernetesJob(ctx context.Context, r *run.Run, j pipeline.Job, rep dagrun.StageReporter) error {
	p, ok := ctx.Value(kubernetesPreparedKey{}).(kubernetesPrepared)
	if !ok || (!p.adopted && p.operation.RunID != r.ID) || p.node.ExecutionJobID != j.ID || b.kubernetesController == nil {
		return runretry.ErrUnavailable
	}
	var o kubernetes.Operation
	var e error
	if p.adopted {
		o, e = b.kubernetesController.VerifyReuse(ctx, p.operation.ID, p.operation.IntentHash)
	} else {
		o, e = b.kubernetesController.Run(ctx, p.operation.ID, p.operation.OwnerEpoch, func(phase, code string) {
			_ = rep.Log(ctx, streamStdout, fmt.Sprintf("kubernetes %s: %s", phase, code))
		})
	}
	if recorder, ok := rep.(interface{ recordKubernetes(kubernetes.Operation) }); ok {
		recorder.recordKubernetes(o)
	}
	if e != nil {
		_ = rep.Log(context.WithoutCancel(ctx), streamStderr, "kubernetes deployment did not complete; inspect operation status")
		return e
	}
	if !o.Successful() {
		return errors.New("kubernetes_deployment_unverified")
	}
	return nil
}
func (r *recoveryReporter) recordKubernetes(o kubernetes.Operation) {
	if o.ID == "" {
		r.invalid = true
		r.external = "none"
		return
	}
	r.kubernetes = &runretry.KubernetesReceipt{OperationID: o.ID, SourceAttemptID: o.RunID, IntentHash: o.IntentHash, Revision: o.Revision, Business: o.Business, Cleanup: o.Cleanup}
	r.deploymentNotDispatched = false
	switch o.Business {
	case kubernetes.OutcomeUnknown, kubernetes.Submitted:
		r.external = "unknown"
	case kubernetes.Ready, kubernetes.Unchanged, kubernetes.Failed, kubernetes.Superseded:
		r.external = "verified"
	default:
		r.external = "none"
	}
	if o.Cleanup != kubernetes.CleanupVerified {
		r.invalid = true
		r.reason = "kubernetes_cleanup_unverified"
	}
}

// FinalizeKubernetesRun fences any preparation left before approval/dispatch.
// Runtime cleanup never runs here without an already fenced old owner.
func FinalizeKubernetesRun(ctx context.Context, s *kubernetes.Service, runID string) error {
	if s == nil {
		return nil
	}
	ops, e := s.ListOperations(ctx, runID)
	if e != nil {
		return e
	}
	var result error
	for _, o := range ops {
		if o.OwnerFenced {
			continue
		}
		if o.Business == kubernetes.NotDispatched {
			o, e = s.FailBeforeDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, "run_settled_before_dispatch")
			if e != nil {
				result = errors.Join(result, e)
				continue
			}
		}
		_, e = s.FenceOwner(ctx, o.ID, o.OwnerEpoch, o.Revision)
		result = errors.Join(result, e)
	}
	return result
}

func SameKubernetesTrust(a, b kubernetes.Authorization) bool {
	return a.Target == b.Target && a.ClusterKey == b.ClusterKey && a.ProfileFingerprint == b.ProfileFingerprint
}

func (b *Builder) verifyInheritedKubernetes(ctx context.Context, stage pipeline.Stage) error {
	ex, ok := runretry.ExecutionFrom(ctx)
	if !ok || !runretry.IsRecovery(ex) {
		return nil
	}
	snap := ex.Snapshot()
	if snap.Compiled == nil {
		return runretry.ErrUnavailable
	}
	ancestors := map[string]bool{}
	queue := append([]string(nil), snap.Compiled.Dependencies[stage.ID]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if ancestors[id] {
			continue
		}
		ancestors[id] = true
		queue = append(queue, snap.Compiled.Dependencies[id]...)
	}
	for _, st := range snap.Compiled.Stages {
		if !ancestors[st.ID] {
			continue
		}
		for _, j := range st.Jobs {
			if j.Type != pipeline.KubernetesJobType {
				continue
			}
			n, ok := ex.Node(st.ID, j.ID)
			if !ok {
				return runretry.ErrUnavailable
			}
			if ex.Decision(n).Action != "reuse" {
				continue
			}
			o, e := runretry.NodeOutcome(ctx, n)
			if e != nil || o.Kubernetes == nil || b.kubernetesController == nil {
				return runretry.ErrUnavailable
			}
			if _, e = b.kubernetesController.VerifyReuse(ctx, o.Kubernetes.OperationID, o.Kubernetes.IntentHash); e != nil {
				return e
			}
		}
	}
	return nil
}
