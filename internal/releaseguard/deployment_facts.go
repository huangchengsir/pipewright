package releaseguard

import (
	"context"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
)

// This is the result of the deployment's one-time health gate. It is distinct
// from the sampled Docker Health condition and contains no command, URL or log.
type deploymentHealthGateFact struct {
	ReceiptID  string    `json:"receiptId"`
	Sequence   int64     `json:"sequence"`
	Kind       string    `json:"kind"`
	At         time.Time `json:"at"`
	HealthGate string    `json:"healthGate"`
}

func (s *Service) deploymentFactsLocked(ctx context.Context, d *document, observed *TargetObservation, finished bool) error {
	// Only service-created observation modes have a deployment lineage. Pure
	// rule documents and legacy summaries are not new deployment observations.
	if d.Report.Plan.Mode != "attached" && d.Report.Plan.Mode != "from_now" {
		return nil
	}
	if observed.ContainerID == "" {
		if finished {
			condition(observed, "deployment_completion", "insufficient", "deployment_outcome_unavailable", "")
		}
		return nil
	}
	recorded, err := s.ledger.GetTarget(ctx, observed.ID)
	if err != nil || recorded.AttemptID != d.Report.AttemptID || recorded.WorkloadKey != observed.WorkloadKey || recorded.Source.ProjectID != d.Report.ProjectID || recorded.ServerID != observed.ServerID {
		condition(observed, "deployment_completion", "insufficient", "deployment_outcome_unavailable", "")
		return nil
	}
	if finished || d.Report.Plan.Mode == "from_now" {
		result, reason := "insufficient", "deployment_outcome_unavailable"
		if recorded.Outcome == deployattempt.Succeeded {
			result, reason = "passed", "deployment_completed"
		} else if !finished && (recorded.Outcome == deployattempt.Started || recorded.Outcome == deployattempt.ActivationStarted || recorded.Outcome == deployattempt.Activated) {
			result, reason = "pending", "deployment_completion_pending"
		}
		condition(observed, "deployment_completion", result, reason, "")
	}
	for _, existing := range observed.Conditions {
		if existing.Name == "deployment_health_gate" && existing.Result == "failed" {
			return nil
		}
	}
	for _, receipt := range recorded.Receipts {
		if receipt.HealthGate != "failed" || (receipt.Kind != deployattempt.DeployFailed && receipt.Kind != deployattempt.RolledBack) {
			continue
		}
		e := Evidence{ID: newEvidenceID(), TargetID: observed.ID, Kind: "deployment_health_gate", EventAt: receipt.At, CollectedAt: s.now().UTC(), Reason: "deployment_health_gate_failed", Summary: "recorded_deployment_health_gate_failure", Value: deploymentHealthGateFact{ReceiptID: receipt.ID, Sequence: receipt.Sequence, Kind: receipt.Kind, At: receipt.At, HealthGate: receipt.HealthGate}}
		condition(observed, "deployment_health_gate", "failed", "deployment_health_gate_failed", e.ID)
		aggregate(&d.Report, false)
		if err := s.save(ctx, d, "deployment_health_gate_failed", &e); err != nil {
			// Retain the known failure if evidence storage is unavailable. Do not
			// cite an evidence ID whose transaction did not commit.
			for i := range observed.Conditions {
				if observed.Conditions[i].Name == "deployment_health_gate" {
					observed.Conditions[i].EvidenceIDs = []string{}
				}
			}
			observed.Limitations = appendUnique(observed.Limitations, "deployment_evidence_unavailable")
			return err
		}
		return nil
	}
	return nil
}

// A failed evidence transaction must not erase an already-read immutable
// deployment failure when the caller reloads the last committed document.
func retainDeploymentHealthFailure(dst, src *document) {
	for _, source := range src.Report.Targets {
		for _, c := range source.Conditions {
			if c.Name != "deployment_health_gate" || c.Result != "failed" {
				continue
			}
			for i := range dst.Report.Targets {
				if dst.Report.Targets[i].ID == source.ID {
					condition(&dst.Report.Targets[i], c.Name, c.Result, c.Reason, "")
					dst.Report.Targets[i].Limitations = appendUnique(dst.Report.Targets[i].Limitations, "deployment_evidence_unavailable")
				}
			}
		}
	}
	aggregate(&dst.Report, false)
}
