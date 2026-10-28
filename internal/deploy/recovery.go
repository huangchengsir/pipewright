package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
	"io"
	"path"
	"strings"
	"sync"
	"time"
)

func WithRetryLedger(s *runretry.Service) Option { return func(d *service) { d.retry = s } }

type retryBypassKey struct{}

// ErrNotDispatched is emitted only by the recovery adapter before its first
// durable target reservation. It does not classify errors from external work.
var ErrNotDispatched = errors.New("deploy: no target operation dispatched")

func privateHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func connectionBinding(s *target.Server) string {
	return privateHash(struct {
		ID, Host, User, Credential string
		Port                       int
	}{s.ID, s.Host, s.User, s.CredentialID, s.Port})
}

type targetTrace struct {
	unknown, known, notStarted bool
	cid                        string
	exitCode                   int
}
type recoveryTarget struct {
	target.Service
	mu        sync.Mutex
	traces    map[string]*targetTrace
	snapshots map[string]target.ConnectionSnapshot
}

func (t *recoveryTarget) record(id string, cmd []string, out *target.ExecResult, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.traces[id]
	if r == nil {
		r = &targetTrace{}
		t.traces[id] = r
	}
	if err != nil {
		if target.IsNotStarted(err) {
			r.notStarted = true
		} else {
			r.unknown = true
		}
		return
	}
	if out == nil {
		r.unknown = true
		return
	}
	r.known = true
	r.exitCode = out.ExitCode
	if out.ExitCode == 0 && len(cmd) > 1 && cmd[0] == "docker" && cmd[1] == "run" && deployattemptValidID(strings.TrimSpace(out.Stdout)) {
		r.cid = strings.TrimSpace(out.Stdout)
	}
}
func deployattemptValidID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func (t *recoveryTarget) Exec(ctx context.Context, id string, cmd []string) (*target.ExecResult, error) {
	var out *target.ExecResult
	var err error
	if pinned, ok := t.Service.(target.PinnedExecutor); ok {
		snap, exists := t.snapshots[id]
		if !exists {
			return nil, target.NotStarted(target.ErrTargetChanged)
		}
		var r *target.LimitedResult
		r, err = pinned.ExecPinned(ctx, snap, cmd, target.ExecutionLimits{Bytes: 64 << 10, Lines: 1000})
		if r != nil {
			out = &target.ExecResult{Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode}
			if r.Truncated {
				err = target.OutcomeUnknown(target.ErrLimitedUnavailable)
			}
		}
	} else {
		out, err = t.Service.Exec(ctx, id, cmd)
	}
	t.record(id, cmd, out, err)
	return out, err
}
func (t *recoveryTarget) Upload(ctx context.Context, id string, r io.Reader, p string) error {
	var err error
	if pinned, ok := t.Service.(target.PinnedExecutor); ok {
		err = pinned.UploadPinned(ctx, t.snapshots[id], r, p)
	} else {
		err = t.Service.Upload(ctx, id, r, p)
	}
	result := &target.ExecResult{}
	t.record(id, nil, result, err)
	return err
}
func (t *recoveryTarget) Capture(ctx context.Context, id string) (target.ConnectionSnapshot, error) {
	if s, ok := t.snapshots[id]; ok {
		return s, nil
	}
	return target.ConnectionSnapshot{}, target.NotStarted(target.ErrLimitedUnavailable)
}
func (t *recoveryTarget) ExecLimited(ctx context.Context, s target.ConnectionSnapshot, cmd []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
	return t.ExecPinned(ctx, s, cmd, l)
}
func (t *recoveryTarget) ExecPinned(ctx context.Context, s target.ConnectionSnapshot, cmd []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
	out, e := t.Exec(ctx, s.Server.ID, cmd)
	if out == nil {
		return nil, e
	}
	return &target.LimitedResult{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode}, e
}
func (t *recoveryTarget) UploadPinned(ctx context.Context, s target.ConnectionSnapshot, r io.Reader, p string) error {
	return t.Upload(ctx, s.Server.ID, r, p)
}

func (s *service) deployRecoveryStage(ctx context.Context, runID string, ids []string, cfg map[string]string, strategy string) (results []TargetResult, resultErr error) {
	reservationStarted := false
	defer func() {
		if resultErr != nil && !reservationStarted {
			resultErr = errors.Join(ErrNotDispatched, resultErr)
		}
	}()
	ctx, done, admissionErr := s.beginActivity(ctx, runID)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer done()
	n, _ := runretry.NodeFrom(ctx)
	ex, ok := runretry.ExecutionFrom(ctx)
	if !ok {
		return nil, runretry.ErrUnavailable
	}
	if len(ids) == 0 {
		return nil, ErrNoServers
	}
	seen := map[string]bool{}
	servers := map[string]*target.Server{}
	tracked := &recoveryTarget{Service: s.targets, traces: map[string]*targetTrace{}, snapshots: map[string]target.ConnectionSnapshot{}}
	for _, id := range ids {
		if seen[id] {
			return nil, runretry.ErrInvalid
		}
		seen[id] = true
		srv, e := s.targets.Get(ctx, id)
		if e != nil {
			return nil, e
		}
		servers[id] = srv
		if capture, ok := s.targets.(interface {
			Capture(context.Context, string) (target.ConnectionSnapshot, error)
		}); ok {
			snap, e := capture.Capture(ctx, id)
			if e != nil {
				return nil, e
			}
			tracked.snapshots[id] = snap
		}
	}
	artifact := run.Artifact{Type: "command", ID: "command"}
	if strings.TrimSpace(cfg["artifactType"]) != "command" {
		arts, e := s.runs.ListArtifacts(ctx, runID)
		if e != nil {
			return nil, e
		}
		a, e := selectStageArtifact(arts, strings.TrimSpace(cfg["artifactType"]), strings.TrimSpace(cfg["artifactSource"]))
		if e != nil {
			return nil, e
		}
		if a == nil {
			return nil, ErrArtifactNotFound
		}
		artifact = *a
	}
	configHash := privateHash(struct {
		Config                        map[string]string
		Strategy, ID, Type, Reference string
		Metadata                      map[string]any
	}{cfg, NormalizeStrategy(strategy), artifact.ID, artifact.Type, artifact.Reference, artifact.Metadata})
	previous := map[string]runretry.TargetReceipt{}
	reexecuteCompleted := false
	if runretry.IsRecovery(ex) {
		o, _, err := runretry.PreviousOutcome(ctx, n)
		if err != nil {
			return nil, runretry.ErrUnavailable
		}
		reexecuteCompleted = o.Status == "success"
		for _, r := range o.Targets {
			if previous[r.ServerID].ServerID != "" {
				return nil, runretry.ErrConflict
			}
			previous[r.ServerID] = r
		}
	}
	inherited := map[string]TargetResult{}
	local := *s
	local.targets = tracked
	var selected []string
	proofs := map[string]runretry.TargetProof{}
	for _, id := range ids {
		proof := runretry.TargetProof{Kind: artifact.Type, ConnectionFingerprint: connectionBinding(servers[id]), ConfigHash: configHash, ArtifactID: artifact.ID, ArtifactReference: artifact.Reference}
		if LocalDockerEnabled(cfg) {
			proof.Kind = "local_image"
		}
		if proof.Kind == "image" || proof.Kind == "local_image" {
			runtime, err := local.targetRuntime(ctx, id)
			if err != nil {
				return nil, err
			}
			proof.RuntimeID = runtime
		}
		old := previous[id]
		if old.ServerID != "" {
			if old.OperationID == "" || old.SourceAttemptID == "" {
				return nil, runretry.ErrUnavailable
			}
			op, e := s.retry.TargetOperation(ctx, runID, n.Key, old.OperationID)
			if e != nil {
				return nil, e
			}
			if op.ServerID != id || op.AttemptID != old.SourceAttemptID {
				return nil, runretry.ErrConflict
			}
			if op.State == "running" || op.State == "outcome_unknown" {
				return nil, runretry.ErrUnknown
			}
			if op.Proof.ConnectionFingerprint != proof.ConnectionFingerprint {
				return nil, runretry.ErrConflict
			}
			sameInput := op.Proof.ConfigHash == proof.ConfigHash && op.Proof.ArtifactID == proof.ArtifactID && op.Proof.ArtifactReference == proof.ArtifactReference
			if old.Status == run.TargetSuccess {
				if op.State != "success" || !op.Proof.ExitKnown || op.Proof.ExitCode != 0 {
					return nil, runretry.ErrUnavailable
				}
				if e = local.verifyTargetReceipt(ctx, op.Proof); e != nil {
					return nil, e
				}
				if e = local.verifySuccessfulTarget(ctx, id, op.Proof); e != nil {
					return nil, e
				}
				if !reexecuteCompleted && sameInput {
					inherited[id] = TargetResult{ServerID: id, ServerName: servers[id].Name, Status: run.TargetSuccess, OperationID: op.ID, SourceAttemptID: op.AttemptID, Message: "已核验并保留原成功目标"}
					continue
				}
			}
		}
		selected = append(selected, id)
		proofs[id] = proof
	}
	operations := map[string]*runretry.TargetOperation{}
	for _, id := range selected {
		// An error from reservation itself may have lost a commit acknowledgement.
		// Never convert that uncertainty into a no-dispatch proof.
		reservationStarted = true
		op, e := s.retry.BeginTarget(ctx, runID, n, id, proofs[id])
		if e != nil {
			return nil, e
		}
		operations[id] = op
	}
	var fresh []TargetResult
	var businessErr error
	receipts := &recoveryReceipts{retry: s.retry, run: runID, node: n.Key, operations: operations, proofs: map[string]runretry.TargetProof{}, unknown: map[string]bool{}}
	if len(selected) > 0 {
		workCtx := context.WithValue(ctx, recoveryReceiptsKey{}, receipts)
		fresh, businessErr = local.DeployForStage(context.WithValue(workCtx, retryBypassKey{}, true), runID, selected, cfg, strategy)
	}
	by := map[string]TargetResult{}
	for _, r := range fresh {
		by[r.ServerID] = r
	}
	for _, id := range selected {
		r, found := by[id]
		if !found {
			r = TargetResult{ServerID: id, ServerName: servers[id].Name, Status: run.TargetFailed, Message: "部署结果未核实", OutcomeUnknown: true}
		}
		tracked.mu.Lock()
		trace := tracked.traces[id]
		if trace == nil {
			trace = &targetTrace{notStarted: true}
		}
		copyTrace := *trace
		tracked.mu.Unlock()
		proof := operations[id].Proof
		receipts.mu.Lock()
		if p, ok := receipts.proofs[id]; ok {
			proof = p
		}
		receiptUnknown := receipts.unknown[id]
		receipts.mu.Unlock()
		if receiptUnknown {
			copyTrace.unknown = true
		}
		proof.ExitKnown = !copyTrace.unknown
		proof.NotStarted = copyTrace.notStarted && !copyTrace.known
		proof.ExitCode = copyTrace.exitCode
		state := "failed"
		if r.Status == run.TargetSuccess {
			state = "success"
			if (proof.Kind == "image" || proof.Kind == "local_image") && (proof.ReceiptKind != "succeeded" || proof.DeploymentTargetID == "" || proof.DeploymentReceiptID == "") {
				r.OutcomeUnknown = true
			}
			var err error
			proof, err = local.captureTargetProof(ctx, id, artifact, cfg, proof, copyTrace)
			if err != nil {
				r.OutcomeUnknown = true
			}
			if !r.OutcomeUnknown && (proof.Kind == "image" || proof.Kind == "local_image") {
				if err = s.retry.ProgressTarget(context.WithoutCancel(ctx), runID, n.Key, operations[id].ID, proof); err != nil {
					r.OutcomeUnknown = true
				}
			}
		}
		if copyTrace.unknown || r.OutcomeUnknown {
			state = "outcome_unknown"
			proof.ExitKnown = false
			r.OutcomeUnknown = true
			businessErr = errors.Join(businessErr, runretry.ErrUnknown)
		}
		finCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := s.retry.FinishTarget(finCtx, runID, n.Key, operations[id].ID, state, proof)
		cancel()
		if err != nil {
			r.OutcomeUnknown = true
			businessErr = errors.Join(businessErr, runretry.ErrUnknown)
		}
		r.OperationID = operations[id].ID
		r.SourceAttemptID = runID
		if r.OutcomeUnknown {
			r.Message = "目标操作结果尚未核实，禁止自动重放"
		}
		by[id] = r
	}
	results = make([]TargetResult, 0, len(ids))
	dts := make([]run.DeployTarget, 0, len(ids))
	for _, id := range ids {
		r, ok := inherited[id]
		if !ok {
			r = by[id]
		}
		results = append(results, r)
		if _, old := inherited[id]; !old {
			dts = append(dts, run.DeployTarget{RunID: runID, ServerID: id, ServerName: r.ServerName, Status: r.Status, Message: r.Message, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt})
		}
	}
	// Child persistence never rewrites parent rows, source timestamps or receipts.
	if len(dts) > 0 {
		if err := s.runs.SaveDeployTargets(ctx, runID, dts); err != nil {
			return results, err
		}
	}
	return results, businessErr
}

const retryContainerProjection = `{"id":{{json .Id}},"image":{{json .Image}},"created":{{json .Created}},"started":{{json .State.StartedAt}},"running":{{json .State.Running}}}`

type retryContainer struct {
	ID, Image, Created, Started string
	Running                     bool
}

func (s *service) inspectTargetContainer(ctx context.Context, id, name string) (retryContainer, error) {
	var c retryContainer
	r, e := s.targets.Exec(ctx, id, []string{"docker", "container", "inspect", "--format", retryContainerProjection, name})
	if e != nil || r == nil || r.ExitCode != 0 || !decodeLocalProjection(r.Stdout, &c) || !deployattemptValidID(c.ID) || !deploymentImageDigest.MatchString(c.Image) || !c.Running {
		return c, runretry.ErrUnavailable
	}
	return c, nil
}
func (s *service) targetRuntime(ctx context.Context, id string) (string, error) {
	r, e := s.targets.Exec(ctx, id, []string{"docker", "info", "--format", "{{.ID}}"})
	if e != nil || r == nil || r.ExitCode != 0 || len(r.Stdout) > 4096 || strings.TrimSpace(r.Stdout) == "" {
		return "", runretry.ErrUnavailable
	}
	return privateHash(strings.TrimSpace(r.Stdout)), nil
}
func (s *service) captureTargetProof(ctx context.Context, id string, a run.Artifact, cfg map[string]string, p runretry.TargetProof, t targetTrace) (runretry.TargetProof, error) {
	if p.Kind == "command" {
		return p, nil
	}
	if p.Kind == "image" || p.Kind == "local_image" {
		if !deployattemptValidID(t.cid) {
			return p, runretry.ErrUnavailable
		}
		name := imageContainerName(a, cfg)
		if p.Kind == "local_image" {
			name = cfg["containerName"]
		}
		c, e := s.inspectTargetContainer(ctx, id, name)
		if e != nil || c.ID != t.cid {
			return p, runretry.ErrUnavailable
		}
		runtime, e := s.targetRuntime(ctx, id)
		if e != nil {
			return p, e
		}
		if p.RuntimeID != "" && p.RuntimeID != runtime {
			return p, runretry.ErrConflict
		}
		p.ContainerName = name
		p.ContainerID = c.ID
		p.ImageID = c.Image
		p.ContainerCreatedAt = c.Created
		p.ContainerStartedAt = c.Started
		p.RuntimeID = runtime
		return p, nil
	}
	p.CurrentPath = path.Join(releaseBase(a, cfg), "current")
	p.ReleasePath = s.readCurrentRelease(ctx, id, p.CurrentPath)
	if p.ReleasePath == "" || !strings.HasPrefix(p.ReleasePath, path.Join(releaseBase(a, cfg), "releases")+"/") {
		return p, runretry.ErrUnavailable
	}
	return p, nil
}
func (s *service) verifySuccessfulTarget(ctx context.Context, id string, p runretry.TargetProof) error {
	if !p.ExitKnown || p.ExitCode != 0 {
		return runretry.ErrUnavailable
	}
	if p.Kind == "command" {
		return nil
	}
	if p.Kind == "image" || p.Kind == "local_image" {
		runtime, e := s.targetRuntime(ctx, id)
		if e != nil || runtime != p.RuntimeID {
			return runretry.ErrUnavailable
		}
		c, e := s.inspectTargetContainer(ctx, id, p.ContainerName)
		if e != nil || c.ID != p.ContainerID || c.Image != p.ImageID || c.Created != p.ContainerCreatedAt || c.Started != p.ContainerStartedAt {
			return runretry.ErrUnavailable
		}
		return nil
	}
	if p.CurrentPath == "" || p.ReleasePath == "" || s.readCurrentRelease(ctx, id, p.CurrentPath) != p.ReleasePath {
		return runretry.ErrUnavailable
	}
	return nil
}
