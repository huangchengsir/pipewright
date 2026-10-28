package deploy

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
	"sync"
)

type recoveryReceiptsKey struct{}
type recoveryReceipts struct {
	mu         sync.Mutex
	retry      *runretry.Service
	run, node  string
	operations map[string]*runretry.TargetOperation
	proofs     map[string]runretry.TargetProof
	unknown    map[string]bool
}

func recordRecoveryImage(ctx context.Context, t *deployattempt.Target, receipt *deployattempt.Receipt, actual deployattempt.ActualImage, kind string) {
	scope, ok := ctx.Value(recoveryReceiptsKey{}).(*recoveryReceipts)
	if !ok {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	op := scope.operations[t.ServerID]
	if op == nil {
		return
	}
	p, exists := scope.proofs[t.ServerID]
	if !exists {
		p = op.Proof
	}
	p.DeploymentTargetID = t.ID
	p.ContainerName = t.ContainerName
	if receipt != nil {
		p.DeploymentReceiptID = receipt.ID
	}
	p.ReceiptKind = kind
	if receipt != nil && (receipt.ID == "" || receipt.TargetID != t.ID || receipt.Kind != kind) {
		scope.unknown[t.ServerID] = true
	}
	if actual.ContainerID != "" {
		p.ContainerID = actual.ContainerID
	}
	if actual.ConfigID != "" {
		p.ImageID = actual.ConfigID
	}
	p.ContainerCreatedAt = actual.ContainerCreatedAt
	p.ContainerStartedAt = actual.ContainerStartedAt
	if kind == deployattempt.OutcomeUnknown {
		scope.unknown[t.ServerID] = true
		p.ExitKnown = false
	}
	if kind == deployattempt.Succeeded && !scope.unknown[t.ServerID] {
		p.ExitKnown = true
		p.ExitCode = 0
	}
	if err := scope.retry.ProgressTarget(ctx, scope.run, scope.node, op.ID, p); err != nil {
		scope.unknown[t.ServerID] = true
	}
	scope.proofs[t.ServerID] = p
}

// VerifyTargetOperation only inspects an existing completed receipt/workload.
// Arbitrary command delivery without a persisted known exit stays unknown.
func (s *service) VerifyTargetOperation(ctx context.Context, op runretry.TargetOperation) (string, runretry.TargetProof, error) {
	p := op.Proof
	srv, err := s.targets.Get(ctx, op.ServerID)
	if err != nil {
		return "", p, err
	}
	if connectionBinding(srv) != p.ConnectionFingerprint || !p.ExitKnown {
		return "", p, runretry.ErrUnavailable
	}
	local := *s
	if capture, ok := s.targets.(interface {
		Capture(context.Context, string) (target.ConnectionSnapshot, error)
	}); ok {
		snap, err := capture.Capture(ctx, op.ServerID)
		if err != nil {
			return "", p, err
		}
		local.targets = &recoveryTarget{Service: s.targets, traces: map[string]*targetTrace{}, snapshots: map[string]target.ConnectionSnapshot{op.ServerID: snap}}
	}
	if p.Kind == "image" || p.Kind == "local_image" {
		if p.ReceiptKind != deployattempt.Succeeded || p.DeploymentTargetID == "" || p.DeploymentReceiptID == "" || p.RuntimeID == "" {
			return "", p, runretry.ErrUnknown
		}
		runtime, err := local.targetRuntime(ctx, op.ServerID)
		if err != nil || runtime != p.RuntimeID {
			return "", p, runretry.ErrUnavailable
		}
		c, err := local.inspectTargetContainer(ctx, op.ServerID, p.ContainerName)
		if err != nil || c.ID != p.ContainerID {
			return "", p, runretry.ErrUnavailable
		}
		// A full CID on the previously recorded daemon is immutable. These missing
		// attributes may be observed from that exact existing object, never its name alone.
		if p.ImageID == "" {
			p.ImageID = c.Image
		}
		if p.ContainerCreatedAt == "" {
			p.ContainerCreatedAt = c.Created
		}
		if p.ContainerStartedAt == "" {
			p.ContainerStartedAt = c.Started
		}
	}
	if p.ExitCode != 0 {
		return "failed", p, nil
	}
	if err = local.verifyTargetReceipt(ctx, p); err != nil {
		return "", p, err
	}
	if err = local.verifySuccessfulTarget(ctx, op.ServerID, p); err != nil {
		return "", p, err
	}
	return "success", p, nil
}

func RetryTargetVerifier(s Service) runretry.TargetVerifier {
	v, ok := s.(*service)
	if !ok {
		return nil
	}
	return v.VerifyTargetOperation
}

func (s *service) verifyTargetReceipt(ctx context.Context, p runretry.TargetProof) error {
	if p.Kind != "image" && p.Kind != "local_image" {
		return nil
	}
	if p.DeploymentTargetID == "" || p.DeploymentReceiptID == "" || p.ReceiptKind != deployattempt.Succeeded {
		return runretry.ErrUnavailable
	}
	reader, ok := s.attempts.(interface {
		GetTarget(context.Context, string) (*deployattempt.Target, error)
	})
	if !ok {
		return runretry.ErrUnavailable
	}
	t, err := reader.GetTarget(ctx, p.DeploymentTargetID)
	if err != nil {
		return runretry.ErrUnavailable
	}
	if t.Source.RunID != p.AttemptID || t.ServerID != p.ServerID || t.ArtifactID != p.ArtifactID || t.ContainerName != p.ContainerName || t.Outcome != deployattempt.Succeeded {
		return runretry.ErrConflict
	}
	for _, r := range t.Receipts {
		if r.ID == p.DeploymentReceiptID {
			if r.Kind != deployattempt.Succeeded || r.TargetID != t.ID || r.ActualImage.ContainerID != p.ContainerID || r.ActualImage.ConfigID != "" && r.ActualImage.ConfigID != p.ImageID {
				return runretry.ErrConflict
			}
			return nil
		}
	}
	return runretry.ErrUnavailable
}
