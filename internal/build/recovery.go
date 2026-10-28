package build

// Recovery uses the existing adapters. It adds verified inputs and durable
// boundaries around them; it does not introduce another Docker/SSH executor.
import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/dag"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

type recoveryNodeContext struct {
	node   runretry.NodeRef
	inputs runretry.Inputs
	shared string
	strict bool
}
type recoveryNodeContextKey struct{}

func checkpointWarning(ctx context.Context, rep dagrun.StageReporter, err error) error {
	if errors.Is(err, runretry.ErrCheckpoint) {
		_ = rep.Log(ctx, streamStderr, "恢复检查点未保存；本次业务结果保留，该结果暂不可复用")
		return nil
	}
	return err
}
func beginControl(ctx context.Context, ex runretry.Execution, stage, key string) (runretry.NodeRef, error) {
	n, ok := ex.ControlNode(stage, key)
	if !ok {
		return n, runretry.ErrInvalid
	}
	if ex.Decision(n).Action != "control" {
		return n, runretry.ErrBlocked
	}
	return n, ex.Begin(ctx, n)
}
func finishControl(ctx context.Context, ex runretry.Execution, n runretry.NodeRef, status, external string, rep dagrun.StageReporter) error {
	return checkpointWarning(ctx, rep, ex.Control(context.WithoutCancel(ctx), n, runretry.Outcome{Status: status, ExecutionMode: "real", ExternalOutcome: external, Complete: status == run.StepSuccess, Reusable: status == run.StepSuccess}))
}

func (b *Builder) runRecoveryStage(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter, reports TestReportSink, ex runretry.Execution) (stageErr error) {
	init, err := beginControl(ctx, ex, stage.ID, "init")
	if err != nil {
		return err
	}
	proj, settings, err := b.resolve(ctx, r)
	if err != nil {
		_ = finishControl(ctx, ex, init, run.StepFailed, "none", rep)
		return err
	}
	if err = finishControl(ctx, ex, init, run.StepSuccess, "none", rep); err != nil {
		return err
	}
	images := &stageImageArtifacts{}
	rep = &imageStageReporter{StageReporter: rep, images: images, stage: stage}
	var serviceNetwork, shared string
	// A selected workspace/service rebuild group runs in dependency order in a
	// fresh shared workspace. Successful producers outside that explicit group
	// remain inherited. The plan has already shown the extra business executions.
	rebuild := false
	for _, jb := range stage.Jobs {
		if n, ok := ex.Node(stage.ID, jb.ID); ok {
			d := ex.Decision(n)
			if d.Action == "execute" && d.Status == "pending" && runretry.IsRecovery(ex) {
				rebuild = rebuild || len(stage.Services) > 0
			}
		}
	}
	if runretry.IsRecovery(ex) {
		if flags, ok := ex.(interface {
			Rebuilding(context.Context, string) (bool, error)
		}); ok {
			group, e := flags.Rebuilding(ctx, stage.ID)
			if e != nil {
				return e
			}
			rebuild = rebuild || group
		}
	}
	if rebuild || len(stage.Post) > 0 {
		shared, _, _, err = b.cloneJobWorkspace(ctx, r, proj, rep)
		if err != nil {
			return err
		}
	}
	cleanupNode, cleanupErr := beginControl(ctx, ex, stage.ID, "cleanup")
	if cleanupErr != nil {
		if shared != "" {
			_ = os.RemoveAll(shared)
		}
		return cleanupErr
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		cleanErr := b.stopRecoveryServices(cleanupCtx, stage, serviceNetwork)
		if remote, ok := b.driver.(interface{ CleanupRecovery(context.Context) error }); ok {
			cleanErr = errors.Join(cleanErr, remote.CleanupRecovery(cleanupCtx))
		}
		if shared != "" {
			cleanErr = errors.Join(cleanErr, os.RemoveAll(shared))
		}
		status, external := run.StepSuccess, "verified"
		if cleanErr != nil {
			status, external = "outcome_unknown", "unknown"
		}
		cerr := finishControl(cleanupCtx, ex, cleanupNode, status, external, rep)
		barrier, berr := beginControl(cleanupCtx, ex, stage.ID, "barrier")
		if berr == nil {
			s := run.StepSuccess
			if stageErr != nil {
				s = run.StepFailed
			}
			if cleanErr != nil {
				s = "outcome_unknown"
			}
			berr = finishControl(cleanupCtx, ex, barrier, s, external, rep)
		}
		if stageErr == nil && cerr != nil {
			stageErr = cerr
		}
		if stageErr == nil && berr != nil {
			stageErr = berr
		}
		// Unknown cleanup is a recovery boundary, not a fabricated business failure.
		if cleanErr != nil {
			_ = rep.Log(cleanupCtx, streamStderr, "服务或工作区清理尚未核验；新的恢复尝试保持阻止")
		}
	}()
	if len(stage.Services) > 0 {
		n, e := beginControl(ctx, ex, stage.ID, "services")
		if e != nil {
			return e
		}
		serviceCtx, contextErr := runretry.WithNode(ctx, n)
		if contextErr != nil {
			return contextErr
		}
		network, ok := b.startStageServices(serviceCtx, r, stage, rep)
		serviceNetwork = network
		if !ok {
			_ = finishControl(ctx, ex, n, run.StepFailed, "unknown", rep)
			return ErrBuildFailed
		}
		ready, ok := b.driver.(interface {
			ReadyService(context.Context, string) error
		})
		if !ok {
			_ = finishControl(ctx, ex, n, run.StepFailed, "unknown", rep)
			return runretry.ErrUnavailable
		}
		for _, sv := range stage.Services {
			if e = ready.ReadyService(ctx, network+"-"+sv.Name); e != nil {
				_ = finishControl(ctx, ex, n, run.StepFailed, "none", rep)
				return ErrBuildFailed
			}
		}
		if e = finishControl(ctx, ex, n, run.StepSuccess, "verified", rep); e != nil {
			return e
		}
	}
	var nodes []dag.Node
	byID := map[string]pipeline.Job{}
	initial := dag.Result{}
	dependencies, err := ex.Snapshot().JobDependencies(stage.ID)
	if err != nil {
		return err
	}
	for _, jb := range stage.Jobs {
		needs, ok := dependencies[jb.ID]
		if !ok {
			return runretry.ErrUnavailable
		}
		nodes = append(nodes, dag.Node{ID: jb.ID, Needs: needs})
		byID[jb.ID] = jb
		n, ok := ex.Node(stage.ID, jb.ID)
		if !ok {
			return runretry.ErrInvalid
		}
		d := ex.Decision(n)
		if d.Action != "execute" {
			s := dag.StatusSkipped
			if d.Status == run.StepSuccess && d.Action == "reuse" {
				s = dag.StatusSuccess
			}
			if d.Status == run.StepFailed || d.Action == "blocked" {
				s = dag.StatusFailed
			}
			initial[jb.ID] = dag.NodeResult{Status: s}
		}
	}
	graph, err := dag.New(nodes)
	if err != nil {
		return err
	}
	var envMu sync.Mutex
	envOut := map[string][]pipeline.BuildVar{}
	var sharedMu sync.Mutex
	result := graph.Schedule(ctx, func(ctx context.Context, id string) error {
		if rebuild {
			sharedMu.Lock()
			defer sharedMu.Unlock()
		}
		jb := byID[id]
		n, _ := ex.Node(stage.ID, id)
		if e := ex.Begin(ctx, n); e != nil {
			return e
		}
		_ = rep.JobRunning(ctx, id)
		failPreparation := func(e error) error {
			_ = checkpointWarning(context.WithoutCancel(ctx), rep, ex.Finish(context.WithoutCancel(ctx), n, runretry.Outcome{Status: run.StepFailed, ExecutionMode: "real", ExternalOutcome: "none", ReasonCode: "input_unavailable", DeploymentNotDispatched: isDeployJob(jb.Type)}))
			_ = rep.JobDone(context.WithoutCancel(ctx), id, run.StepFailed)
			return e
		}
		in, e := ex.Inputs(ctx, n)
		if e != nil && (runretry.IsRecovery(ex) || cfgString(jb.Config, "artifactSource") != "" || jb.Config["__matrixArtifactSourceAmbiguous"] == true) {
			return failPreparation(e)
		}
		if e != nil {
			_ = rep.Log(ctx, streamStderr, "当前运行恢复资料不足；继续使用本次已生成的显式产物")
		}
		if e == nil {
			for _, value := range in.Env {
				mask.RegisterContextSecret(ctx, value)
			}
			if e = b.validateRecoveryImages(ctx, in.Images); e != nil {
				return failPreparation(e)
			}
			b.hydrateRecoveryImages(images, in.Images)
		}
		nodeShared := ""
		inputVerified := e == nil
		if rebuild || len(stage.Jobs) == 1 && len(stage.Post) > 0 {
			// Preserve the legacy single-job/Post workspace within this attempt.
			// An inherited producer still requires declared inputs or an explicit rebuild.
			nodeShared = shared
		}
		nodeCtx := context.WithValue(ctx, recoveryNodeContextKey{}, recoveryNodeContext{node: n, inputs: in, shared: nodeShared, strict: e == nil})
		nodeCtx, e = runretry.WithNode(nodeCtx, n)
		if e != nil {
			return failPreparation(e)
		}
		recorder := &recoveryReporter{StageReporter: rep.JobReporter(id), b: b, node: n, outputs: runretry.Inputs{Images: map[string]runretry.ImageRef{}}, mode: run.ExecutionReal}
		nodeCtx = context.WithValue(nodeCtx, recoveryReporterContextKey{}, recorder)
		sink := &reporterSink{rep: recorder}
		copyBuilder := *b
		if inputVerified || runretry.IsRecovery(ex) {
			copyBuilder.artifactLister = func(_ context.Context, _ string) ([]run.Artifact, error) { return artifactsFromInputs(in), nil }
		}
		var upstream []pipeline.BuildVar
		if runretry.IsRecovery(ex) {
			upstream = envVars(in.Env)
		} else {
			envMu.Lock()
			for _, dep := range jb.Needs {
				upstream = append(upstream, envOut[dep]...)
			}
			envMu.Unlock()
		}
		var outputs []pipeline.BuildVar
		external := "none"
		businessErr := func() error {
			if _, remote := b.driver.(*recoveryRemoteDriver); remote && !isScriptJob(jb.Type) && jb.Type != "git_source" {
				recorder.RecordExecution(run.ExecutionStub)
				return nil
			}
			switch {
			case isScriptJob(jb.Type):
				var e error
				outputs, e = copyBuilder.runScriptJobIsolated(nodeCtx, sink, recorder, r, jb, stage, proj, settings, serviceNetwork, upstream, reports)
				return e
			case isBuildImageJob(jb.Type):
				return copyBuilder.runBuildImageJobIsolated(nodeCtx, sink, recorder, r, jb, stage, proj, settings, false)
			case isDeployJob(jb.Type):
				e := copyBuilder.runDeployJobWithEnvironment(nodeCtx, recorder, jb, r.ID, r.Trigger.Params, settings, r.Trigger.ResolvedEnvironment, stage)
				if e == nil {
					external = "verified"
				}
				return e
			case jb.Type == "push_image":
				e := copyBuilder.runPushImageJob(nodeCtx, r, stage, jb, recorder, proj, settings, images)
				if e == nil {
					external = "verified"
				}
				return e
			case jb.Type == "notify":
				// Delivery errors stay best effort, but are never evidence of successful
				// delivery. The notifier reports the precise receipt through the recorder.
				copyBuilder.runNotifyJob(nodeCtx, recorder, jb, r)
				external = recorder.external
				return nil
			case jb.Type == "git_source":
				ws, _, cleanup, e := copyBuilder.cloneJobWorkspace(nodeCtx, r, proj, recorder)
				_ = ws
				if e == nil {
					cleanup()
				}
				return e
			default:
				recorder.RecordExecution(run.ExecutionStub)
				return nil
			}
		}()
		if recorder.external != "" {
			external = recorder.external
		}
		status := run.StepSuccess
		if businessErr != nil {
			status = run.StepFailed
		}
		recorder.outputs.Env = map[string]string{}
		for _, v := range outputs {
			mask.RegisterContextSecret(nodeCtx, v.Value)
			recorder.outputs.Env[v.Key] = v.Value
		}
		envMu.Lock()
		envOut[id] = outputs
		envMu.Unlock()
		outcome := runretry.Outcome{Status: status, ExecutionMode: recorder.mode, ExternalOutcome: external, Outputs: recorder.outputs, Targets: recorder.targets, Complete: businessErr == nil && !recorder.invalid, Reusable: businessErr == nil && !recorder.invalid, WorkspaceIncomplete: recorder.workspaceIncomplete, ReasonCode: recorder.reason, DeploymentNotDispatched: recorder.deploymentNotDispatched}
		runretry.ApplyOutputPolicy(jb, &outcome, time.Now())
		finishErr := checkpointWarning(context.WithoutCancel(nodeCtx), recorder, ex.Finish(context.WithoutCancel(nodeCtx), n, outcome))
		_ = rep.JobDone(context.WithoutCancel(nodeCtx), id, status)
		if businessErr != nil {
			return businessErr
		}
		return finishErr
	}, dag.Options{MaxConcurrency: defaultJobDAGConcurrency, Initial: initial})
	for _, jb := range stage.Jobs {
		if _, inherited := initial[jb.ID]; inherited {
			continue
		}
		nr := result[jb.ID]
		if nr.Status == dag.StatusSkipped || nr.Status == dag.StatusCanceled {
			n, _ := ex.Node(stage.ID, jb.ID)
			if e := ex.Begin(context.WithoutCancel(ctx), n); e == nil {
				_ = checkpointWarning(ctx, rep, ex.Finish(context.WithoutCancel(ctx), n, runretry.Outcome{Status: run.StepSkipped, ExecutionMode: "real", ExternalOutcome: "none"}))
			}
			_ = rep.JobDone(context.WithoutCancel(ctx), jb.ID, run.StepSkipped)
		}
	}
	jobsDone, e := beginControl(context.WithoutCancel(ctx), ex, stage.ID, "jobs_done")
	if e != nil {
		return e
	}
	failed := result.Counts()[dag.StatusFailed] > 0 || result.Counts()[dag.StatusCanceled] > 0
	s := run.StepSuccess
	if failed {
		s = run.StepFailed
	}
	if e = finishControl(ctx, ex, jobsDone, s, "none", rep); e != nil {
		return e
	}
	b.runRecoveryPost(context.WithoutCancel(ctx), r, stage, shared, failed, rep, ex)
	if failed {
		return ErrBuildFailed
	}
	return nil
}

type recoveryReporter struct {
	dagrun.StageReporter
	b                            *Builder
	node                         runretry.NodeRef
	outputs                      runretry.Inputs
	mode, external               string
	invalid, workspaceIncomplete bool
	targets                      []runretry.TargetReceipt
	reason                       string
	deploymentNotDispatched      bool
}

func (r *recoveryReporter) noDeploymentDispatch() {
	r.deploymentNotDispatched = true
	r.external = "none"
	r.reason = "input_unavailable"
}

func recordDeploymentNotDispatched(rep any) {
	if r, ok := rep.(interface{ noDeploymentDispatch() }); ok {
		r.noDeploymentDispatch()
	}
}

func (r *recoveryReporter) recordTargets(targets []runretry.TargetReceipt) {
	r.targets = append([]runretry.TargetReceipt(nil), targets...)
}

func (r *recoveryReporter) RecordExecution(mode string) {
	if mode == run.ExecutionStub {
		r.mode = mode
	}
	run.RecordExecution(r.StageReporter, mode)
}
func (r *recoveryReporter) recordWorkspace(incomplete bool) {
	r.workspaceIncomplete = r.workspaceIncomplete || incomplete
}
func (r *recoveryReporter) delivery(outcome string) { r.external = outcome }
func recordExternalOutcome(rep any, outcome string) {
	if r, ok := rep.(interface{ delivery(string) }); ok {
		r.delivery(outcome)
	}
}
func (r *recoveryReporter) EmitArtifact(ctx context.Context, a run.Artifact) error {
	if a.Metadata == nil {
		a.Metadata = map[string]any{}
	}
	a.Metadata["logicalNodeKey"] = r.node.Key
	if a.Type == run.ArtifactImage {
		identity, err := r.b.checkpointImage(ctx, a.Reference)
		if err != nil {
			r.invalid = true
		} else {
			identity.Name = a.Name
			identity.Metadata = a.Metadata
			r.outputs.Images[r.node.Key] = identity
			retained := identity
			if local := metadataString(a, "localReference"); local != "" {
				retained.Ref = local
			}
			if strings.HasPrefix(retained.Ref, "pipewright/") {
				if err := runretry.RememberImage(ctx, retained); err != nil {
					_ = r.Log(ctx, streamStderr, "identity_evidence_unavailable：镜像清理身份未能保存，需人工核对")
					r.invalid = true
					r.reason = "identity_evidence_unavailable"
				}
			}
		}
	} else {
		stored, _ := a.Metadata["stored"].(bool)
		wp := metadataString(a, "workspacePath")
		kind := metadataString(a, "format")
		if !stored || wp == "" {
			r.invalid = true
		} else {
			r.outputs.Files = append(r.outputs.Files, runretry.FileRef{Key: a.Reference, SHA256: a.Reference, Size: a.SizeBytes, Path: filepath.ToSlash(wp), Kind: kind, Name: a.Name, ArtifactType: a.Type, Metadata: a.Metadata})
		}
	}
	return r.StageReporter.EmitArtifact(ctx, a)
}
func envVars(m map[string]string) []pipeline.BuildVar {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]pipeline.BuildVar, 0, len(keys))
	for _, k := range keys {
		out = append(out, pipeline.BuildVar{Key: k, Value: m[k]})
	}
	return out
}
func artifactsFromInputs(in runretry.Inputs) []run.Artifact {
	var out []run.Artifact
	for _, f := range in.Files {
		metadata := map[string]any{}
		for k, v := range f.Metadata {
			metadata[k] = v
		}
		metadata["stored"], metadata["workspacePath"], metadata["format"] = true, f.Path, f.Kind
		out = append(out, run.Artifact{Type: f.ArtifactType, Name: f.Name, Reference: f.Key, SizeBytes: f.Size, Metadata: metadata})
	}
	for _, k := range runretry.SortedKeys(in.Images) {
		v := in.Images[k]
		metadata := map[string]any{}
		for key, value := range v.Metadata {
			metadata[key] = value
		}
		metadata["localReference"] = v.Ref
		if v.Digest != "" {
			metadata["digest"] = v.Digest
		}
		out = append(out, run.Artifact{Type: run.ArtifactImage, Name: v.Name, Reference: v.Ref, Metadata: metadata})
	}
	return out
}
func (b *Builder) hydrateRecoveryImages(images *stageImageArtifacts, inputs map[string]runretry.ImageRef) {
	images.mu.Lock()
	defer images.mu.Unlock()
	for _, a := range artifactsFromInputs(runretry.Inputs{Images: inputs}) {
		images.arts = append(images.arts, a)
	}
}
func (b *Builder) stopRecoveryServices(ctx context.Context, stage pipeline.Stage, network string) error {
	if network == "" {
		return nil
	}
	runner, ok := b.driver.(ServiceRunner)
	if !ok {
		return runretry.ErrUnknown
	}
	var errs []error
	for _, sv := range stage.Services {
		code, err := runner.StopService(ctx, network+"-"+sv.Name, nil)
		if err != nil || code != 0 {
			errs = append(errs, runretry.ErrUnknown)
		}
	}
	code, err := runner.RemoveNetwork(ctx, network, nil)
	if err != nil || code != 0 {
		errs = append(errs, runretry.ErrUnknown)
	}
	return errors.Join(errs...)
}
func (b *Builder) runRecoveryPost(ctx context.Context, r *run.Run, stage pipeline.Stage, ws string, failed bool, rep dagrun.StageReporter, ex runretry.Execution) {
	ctx, cancel := context.WithTimeout(ctx, postTotalTimeout)
	defer cancel()
	for i, ps := range stage.Post {
		n, e := beginControl(ctx, ex, stage.ID, fmt.Sprintf("post:%d", i))
		if e != nil {
			continue
		}
		postCtx, e := runretry.WithNode(ctx, n)
		postCtx = context.WithValue(postCtx, recoveryNodeContextKey{}, recoveryNodeContext{node: n, shared: ws})
		if e != nil {
			_ = finishControl(ctx, ex, n, run.StepFailed, "none", rep)
			continue
		}
		status := run.StepSkipped
		if pipeline.PostConditionMatches(ps.Condition, failed) {
			status = run.StepSuccess
			if ws == "" {
				status = run.StepFailed
			} else {
				var files []runretry.FileRef
				for _, job := range stage.Jobs {
					node, ok := ex.Node(stage.ID, job.ID)
					if !ok {
						continue
					}
					o, e := runretry.NodeOutcome(ctx, node)
					if e == nil && o.Status == run.StepSuccess && o.Complete && o.ExecutionMode == "real" {
						files = append(files, o.Outputs.Files...)
					}
				}
				if err := b.restoreRecoveryFiles(ctx, ws, files); err != nil {
					_ = finishControl(ctx, ex, n, run.StepFailed, "none", rep)
					continue
				}
				step := pipeline.PipelineStep{Type: pipeline.StepTypeScript, Image: ps.Image, Commands: ps.Commands, WorkDir: ps.WorkDir, Env: runParamsAsEnv(r.Trigger.Params)}
				if err := b.scriptEvidenceBuilder(&reporterSink{rep: rep}).runScriptStep(postCtx, &reporterSink{rep: rep}, 0, step, ws); err != nil {
					status = run.StepFailed
					_ = rep.Log(ctx, streamStderr, "阶段后置步骤未成功；保留业务阶段结果")
				}
			}
		}
		_ = finishControl(ctx, ex, n, status, "none", rep)
	}
}

// Recovery-workspace input selection is bound to the logical consumer; this
// hook intentionally never restores every artifact from the parent run.
func (b *Builder) restoreNodeInputs(ctx context.Context, ws string) error {
	v, ok := ctx.Value(recoveryNodeContextKey{}).(recoveryNodeContext)
	if !ok {
		return nil
	}
	return b.restoreRecoveryFiles(ctx, ws, v.inputs.Files)
}
func recoveryShared(ctx context.Context) string {
	v, _ := ctx.Value(recoveryNodeContextKey{}).(recoveryNodeContext)
	return v.shared
}
