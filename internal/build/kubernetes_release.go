package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

type kubernetesReleasePreparedKey struct{}
type kubernetesReleasePrepared struct {
	record  kubernetes.ReleaseRecord
	node    runretry.NodeRef
	adopted bool
}

func WithKubernetesReleases(c *kubernetes.ReleaseController) BuilderOption {
	return func(b *Builder) { b.kubernetesReleases = c }
}
func (b *Builder) releaseSourceFiles(ctx context.Context, r *run.Run, source *pipeline.KubernetesReleaseSource, ex runretry.Execution, n runretry.NodeRef, rep dagrun.StageReporter) ([]kubernetes.ReleaseInputFile, error) {
	if source == nil {
		return nil, kubernetes.ErrManifest
	}
	switch source.Kind {
	case "inline":
		return kubernetes.InlineReleaseFile(source.Content)
	case "git":
		if len(ex.Snapshot().CommitSHA) != 40 {
			return nil, runretry.ErrUnavailable
		}
		project, _, e := b.resolve(ctx, r)
		if e != nil {
			return nil, e
		}
		workspace, _, cleanup, e := b.cloneJobWorkspace(ctx, r, project, rep)
		if e != nil {
			return nil, e
		}
		defer cleanup()
		return kubernetes.ReadReleaseGitFiles(workspace, ex.Snapshot().CommitSHA, source.Path)
	case "artifact":
		if source.Artifact == nil || b.artStore == nil {
			return nil, runretry.ErrUnavailable
		}
		inputs, e := ex.Inputs(ctx, n)
		if e != nil {
			return nil, e
		}
		producer, ok := ex.Node(source.Artifact.StageID, source.Artifact.JobID)
		if !ok {
			return nil, runretry.ErrUnavailable
		}
		var candidates []runretry.FileRef
		for _, file := range inputs.Files {
			index, valid := sourceDeclarationIndex(run.Artifact{Metadata: file.Metadata})
			if valid && index == *source.Artifact.DeclarationIndex && file.ProducerNodeKey == producer.Key {
				candidates = append(candidates, file)
			}
		}
		if len(candidates) != 1 {
			return nil, runretry.ErrUnavailable
		}
		file := candidates[0]
		if file.Size < 0 || file.Size > kubernetes.MaxReleaseInputBytes || file.Key != file.SHA256 {
			return nil, runretry.ErrUnavailable
		}
		reader, e := b.artStore.Open(file.Key)
		if e != nil {
			return nil, e
		}
		data, e := io.ReadAll(io.LimitReader(reader, kubernetes.MaxReleaseInputBytes+1))
		reader.Close()
		digest := sha256.Sum256(data)
		if e != nil || hex.EncodeToString(digest[:]) != file.SHA256 || len(data) != int(file.Size) || len(data) > kubernetes.MaxReleaseInputBytes {
			return nil, runretry.ErrUnavailable
		}
		if file.Kind == "tar.gz" {
			return kubernetes.ReadReleaseArchive(data, source.Path)
		}
		if source.Path != "" && source.Path != file.Path {
			return nil, kubernetes.ErrManifest
		}
		return []kubernetes.ReleaseInputFile{{Path: file.Path, Content: data, Digest: file.SHA256}}, nil
	}
	return nil, kubernetes.ErrManifest
}
func releaseVariableTyped(text, kind string) (any, error) {
	if kind == "string" {
		return text, nil
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, kubernetes.ErrVariable
	}
	return value, nil
}
func (b *Builder) releaseVariables(ctx context.Context, r *run.Run, cfg pipeline.KubernetesReleaseConfig, ex runretry.Execution, n runretry.NodeRef) (map[string]kubernetes.ReleaseVariableValue, error) {
	_, settings, e := b.resolve(ctx, r)
	if e != nil {
		return nil, e
	}
	inputs, e := ex.Inputs(ctx, n)
	if e != nil {
		return nil, e
	}
	environment := map[string]pipeline.BuildVar{}
	for _, v := range projectBuildVars(settings, r.Trigger.ResolvedEnvironment) {
		environment[v.Key] = v
	}
	out := map[string]kubernetes.ReleaseVariableValue{}
	for _, v := range cfg.Variables {
		value := v.Value
		secret := false
		text := ""
		found := false
		switch v.Source {
		case "fixed":
			found = true
		case "parameter":
			text, found = ex.Snapshot().Trigger.Params[v.Ref]
		case "output":
			text, found = inputs.Env[v.Ref]
		case "environment":
			variable, ok := environment[v.Ref]
			if ok {
				if variable.Secret {
					if b.vault == nil || kubernetes.IsProtectedCredentialID(variable.CredentialID) {
						return nil, kubernetes.ErrVariable
					}
					text, e = b.vault.Reveal(variable.CredentialID)
					secret = true
					if e != nil {
						return nil, kubernetes.ErrVariable
					}
				} else {
					text = variable.Value
				}
				found = true
			}
		case "secret":
			if b.vault == nil || kubernetes.IsProtectedCredentialID(v.Ref) {
				return nil, kubernetes.ErrVariable
			}
			text, e = b.vault.Reveal(v.Ref)
			secret = true
			found = e == nil
		}
		if !found {
			return nil, kubernetes.ErrVariable
		}
		if v.Source != "fixed" {
			value, e = releaseVariableTyped(text, v.Type)
			if e != nil {
				return nil, e
			}
		}
		if secret {
			mask.RegisterContextSecret(ctx, text)
		}
		out[v.Name] = kubernetes.ReleaseVariableValue{Type: v.Type, Value: value, Secret: secret}
	}
	return out, nil
}
func (b *Builder) prepareKubernetesRelease(ctx context.Context, r *run.Run, stage pipeline.Stage) (out dagrun.StagePreparation, resultErr error) {
	phase := "configuration"
	defer func() { resultErr = kubernetes.WithReleasePreparationPhase(resultErr, phase) }()
	if b.kubernetes == nil || b.kubernetesReleases == nil || b.kubernetesOwner == "" || len(stage.Jobs) != 1 {
		return out, runretry.ErrUnavailable
	}
	job := stage.Jobs[0]
	phase = "snapshot"
	ex, ok := runretry.ExecutionFrom(ctx)
	if !ok {
		return out, runretry.ErrUnavailable
	}
	node, ok := ex.Node(stage.ID, job.ID)
	if !ok {
		return out, runretry.ErrUnavailable
	}
	if ex.Decision(node).Action != "execute" {
		return out, nil
	}
	policy, ok := ex.Snapshot().KubernetesReleasePolicies[KubernetesBindingKey(stage.ID, job.ID)]
	if !ok {
		return out, runretry.ErrUnavailable
	}
	phase = "authorization"
	if e := b.kubernetes.ValidateReleaseAuthorization(ctx, policy); e != nil {
		return out, e
	}
	phase = "configuration"
	full, e := b.kubernetes.ResolveReleaseConfig(ctx, r.ProjectID, job.ID, job.Config)
	if e != nil {
		return out, e
	}
	cfg, e := pipeline.ParseKubernetesReleaseConfig(full)
	if e != nil {
		return out, e
	}
	phase = "release_record"
	record, e := b.kubernetes.ReleaseForNode(ctx, r.ID, node.Key)
	if rollbackID, rollback := ex.Snapshot().KubernetesRollbacks[node.Key]; rollback {
		if e != nil || record.ID != rollbackID || record.Plan.RollbackOf == "" || !record.Plan.ForceApproval {
			return out, runretry.ErrUnavailable
		}
	}
	if errors.Is(e, kubernetes.ErrNotFound) {
		frozenPrevious := ""
		if runretry.IsRecovery(ex) {
			previous, priorErr := runretry.PreviousKubernetesRelease(ctx, node)
			if priorErr == nil {
				prior, readErr := b.kubernetes.GetRelease(ctx, previous)
				if readErr != nil {
					return out, readErr
				}
				if prior.Successful() {
					prior, readErr = b.kubernetesReleases.VerifyReuse(ctx, prior.ID, prior.PlanID)
					if readErr != nil {
						return out, readErr
					}
					out.Context = context.WithValue(ctx, kubernetesReleasePreparedKey{}, kubernetesReleasePrepared{record: prior, node: node, adopted: true})
					out.RequireGate = policy.RequireApproval
					out.Approve = func(ctx context.Context) error {
						return b.kubernetes.ValidateReleaseAuthorization(ctx, prior.Plan.Authorization)
					}
					return out, nil
				}
				if !prior.OwnerFenced || prior.Cleanup != kubernetes.CleanupVerified || prior.State == "outcome_unknown" {
					return out, runretry.ErrBlocked
				}
				frozenPrevious = prior.ID
			} else if !errors.Is(priorErr, runretry.ErrUnavailable) {
				return out, priorErr
			}
		}
		if frozenPrevious != "" {
			record, e = b.kubernetesReleases.PrepareRetry(ctx, r.ID, node.Key, b.kubernetesOwner, frozenPrevious, policy)
			if e != nil {
				return out, e
			}
		} else {
			data, _ := json.Marshal(cfg)
			phase = "variables"
			variables, e := b.releaseVariables(ctx, r, cfg, ex, node)
			if e != nil {
				return out, e
			}
			switch cfg.Mode {
			case "helm":
				phase = "chart"
				chart, e := b.releaseChartBytes(ctx, r, cfg.Source, ex, node, nilReleaseReporter{})
				if e != nil {
					return out, e
				}
				defer clear(chart)
				values := []byte("{}")
				if cfg.Helm.Values != nil {
					phase = "values"
					files, e := b.releaseSourceFiles(ctx, r, cfg.Helm.Values, ex, node, nilReleaseReporter{})
					if e != nil || len(files) != 1 {
						return out, kubernetes.ErrManifest
					}
					values = files[0].Content
				}
				values, e = kubernetes.RenderReleaseValues(values, variables)
				if e != nil {
					return out, e
				}
				defer clear(values)
				phase = "helm"
				record, e = b.kubernetesReleases.PrepareHelm(ctx, r.ID, node.Key, b.kubernetesOwner, policy, cfg.Namespace, cfg.Helm.Release, chart, values, cfg.Helm.Hooks == "approved-jobs", data, cfg.TimeoutSeconds, *cfg.ObserveSeconds)
				if e != nil {
					return out, e
				}
			case "progressive":
				phase = "images"
				updates, e := b.releaseImageUpdates(ctx, cfg, ex, node)
				if e != nil {
					return out, e
				}
				p := cfg.Progressive
				phase = "progressive"
				record, e = b.kubernetesReleases.PrepareProgressive(ctx, r.ID, node.Key, b.kubernetesOwner, policy, kubernetes.ReleaseProgressiveInput{Namespace: cfg.Namespace, Stable: cfg.Workload.Name, Candidate: p.Candidate, Service: p.Service, TrackKey: p.TrackKey, TrackValue: p.TrackValue, Batches: p.Batches, Images: updates}, data, cfg.TimeoutSeconds, *cfg.ObserveSeconds)
				if e != nil {
					return out, e
				}
			case "image":
				phase = "images"
				updates, e := b.releaseImageUpdates(ctx, cfg, ex, node)
				if e != nil {
					return out, e
				}
				phase = "image"
				record, e = b.kubernetesReleases.PrepareImage(ctx, r.ID, node.Key, b.kubernetesOwner, policy, cfg.Workload.Kind, cfg.Namespace, cfg.Workload.Name, updates, data, cfg.TimeoutSeconds, *cfg.ObserveSeconds)
				if e != nil {
					return out, e
				}
			case "manifest":
				phase = "source"
				files, e := b.releaseSourceFiles(ctx, r, cfg.Source, ex, node, nilReleaseReporter{})
				if e != nil {
					return out, e
				}
				phase = "manifest"
				record, e = b.kubernetesReleases.PrepareManifest(ctx, r.ID, node.Key, b.kubernetesOwner, policy, files, variables, data, cfg.TimeoutSeconds, *cfg.ObserveSeconds)
				if e != nil {
					return out, e
				}
			default:
				return out, runretry.ErrUnavailable
			}
		}
	} else if e != nil {
		return out, e
	}
	if record.State == "paused" {
		out.Context = context.WithValue(ctx, kubernetesReleasePreparedKey{}, kubernetesReleasePrepared{record: record, node: node})
		return out, nil
	}
	if record.OwnerEpoch != b.kubernetesOwner || record.OwnerFenced {
		return out, runretry.ErrBlocked
	}
	out.Context = context.WithValue(ctx, kubernetesReleasePreparedKey{}, kubernetesReleasePrepared{record: record, node: node})
	out.RequireGate = record.Plan.Authorization.RequireApproval || record.Plan.ForceApproval
	out.Approve = func(ctx context.Context) error {
		fresh, e := b.kubernetes.GetRelease(ctx, record.ID)
		if e != nil {
			return e
		}
		if fresh.State == "running" && fresh.ApprovedID == fresh.PlanID {
			return b.kubernetes.ValidateReleaseAuthorization(ctx, fresh.Plan.Authorization)
		}
		_, e = b.kubernetes.ApproveRelease(ctx, record.ID, b.kubernetesOwner, fresh.Revision, fresh.PlanID)
		return e
	}
	out.Close = func(ctx context.Context) error {
		fresh, e := b.kubernetes.GetRelease(ctx, record.ID)
		if e != nil {
			return e
		}
		if fresh.State == "prepared" || fresh.State == "awaiting_approval" {
			_, e = b.kubernetes.SetReleaseTerminal(ctx, fresh.ID, b.kubernetesOwner, fresh.Revision, "failed", "stage_not_dispatched")
		}
		return e
	}
	return out, nil
}
func (b *Builder) runKubernetesReleaseJob(ctx context.Context, rep dagrun.StageReporter) error {
	prepared, ok := ctx.Value(kubernetesReleasePreparedKey{}).(kubernetesReleasePrepared)
	if !ok || b.kubernetesReleases == nil {
		return runretry.ErrUnavailable
	}
	var record kubernetes.ReleaseRecord
	var e error
	if prepared.record.State == "paused" {
		return run.ErrSuspended
	}
	if prepared.adopted {
		record, e = b.kubernetesReleases.VerifyReuse(ctx, prepared.record.ID, prepared.record.PlanID)
	} else {
		record, e = b.kubernetesReleases.Run(ctx, prepared.record.ID, b.kubernetesOwner, func(phase, code string) { _ = rep.Log(ctx, streamStdout, "kubernetes "+phase+": "+code) })
	}
	if recorder, ok := rep.(interface {
		recordKubernetesRelease(kubernetes.ReleaseRecord)
	}); ok {
		recorder.recordKubernetesRelease(record)
	}
	if errors.Is(e, kubernetes.ErrReleasePaused) {
		return run.ErrSuspended
	}
	if e != nil {
		return e
	}
	if !record.Successful() {
		return runretry.ErrUnavailable
	}
	return nil
}
func (r *recoveryReporter) recordKubernetesRelease(record kubernetes.ReleaseRecord) {
	if record.ID == "" {
		r.invalid = true
		r.external = "none"
		return
	}
	r.kubernetesRelease = &runretry.KubernetesReleaseReceipt{ReleaseID: record.ID, SourceAttemptID: record.RunID, NodeKey: record.NodeKey, PlanID: record.PlanID, Revision: record.Revision, State: record.State, Cleanup: record.Cleanup}
	switch record.State {
	case "outcome_unknown":
		r.external = "unknown"
	case "ready", "failed", "partial_failed", "rolled_back":
		r.external = "verified"
	default:
		r.external = "none"
	}
	if record.Cleanup != kubernetes.CleanupVerified {
		r.invalid = true
		r.reason = "kubernetes_cleanup_unverified"
	}
}

type nilReleaseReporter struct{}

func (nilReleaseReporter) Log(context.Context, string, string) error        { return nil }
func (nilReleaseReporter) EmitArtifact(context.Context, run.Artifact) error { return nil }
func (nilReleaseReporter) JobRunning(context.Context, string) error         { return nil }
func (nilReleaseReporter) JobDone(context.Context, string, string) error    { return nil }
func (n nilReleaseReporter) JobReporter(string) dagrun.StageReporter        { return n }
