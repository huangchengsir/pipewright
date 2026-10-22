package deploy

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// ImageObserver is an optional notification boundary. Implementations must
// recognize explicit observation consent from ctx; ordinary deployments grant
// no extra reads. Before must honor its five-second context. After must return
// promptly. Neither callback owns deployment, rollback, or retry capabilities.
type ImageObserver interface {
	DeploymentStarted(context.Context, string)
	BeforeImageActivation(context.Context, string, string)
	AfterImageActivation(context.Context, string, string, string)
}

func WithAttemptLedger(w deployattempt.Writer) Option { return func(s *service) { s.attempts = w } }
func WithImageObserver(o ImageObserver) Option        { return func(s *service) { s.imageObserver = o } }

type attemptScopeKey struct{}
type sourceStageKey struct{}

// WithSourceStage carries the real deploy stage ID from the caller; no stage
// is inferred from logs or artifact names.
func WithSourceStage(ctx context.Context, stageID string) context.Context {
	return context.WithValue(ctx, sourceStageKey{}, stageID)
}

func (s *service) beginAttempt(ctx context.Context, runID, initiator string) (context.Context, error) {
	if s.attempts == nil {
		return ctx, nil
	}
	stage, _ := ctx.Value(sourceStageKey{}).(string)
	resolved := ""
	if s.runs != nil {
		if rn, err := s.runs.Get(ctx, runID); err == nil && !rn.ResolvedCommitConflict {
			resolved = rn.ResolvedCommitSHA
		}
	}
	a, err := s.attempts.Begin(ctx, deployattempt.BeginInput{RunID: runID, StageID: stage, Initiator: initiator, ResolvedFullSHA: resolved})
	if err != nil {
		return ctx, err
	}
	ctx = context.WithValue(ctx, attemptScopeKey{}, a)
	s.notifyImage(func() { s.imageObserver.DeploymentStarted(ctx, a.ID) })
	return ctx, nil
}

func (s *service) notifyImage(fn func()) {
	if s.imageObserver == nil {
		return
	}
	defer func() {
		if recover() != nil {
			log.Printf("[deploy] image observer interrupted")
		}
	}()
	fn()
}

type imageReceipt struct {
	target              *deployattempt.Target
	actual              deployattempt.ActualImage
	previous            string
	rollbackContainerID string
	gate                string
	unknown             bool
}

func expectedImage(a run.Artifact, ref string) deployattempt.ExpectedImage {
	e := deployattempt.ExpectedImage{Reference: ref, DigestKind: deployattempt.DigestUnavailable}
	repo, _ := a.Metadata["digestRepository"].(string)
	d, _ := a.Metadata["digest"].(string)
	if repo != "" && deploymentImageDigest.MatchString(d) {
		e.Repository = repo
		e.Digest = d
		e.DigestKind = deployattempt.DigestRepositoryUnknown
		// Only explicit builder metadata may establish a media type. The current
		// publish path does not prove index/manifest/config relationships.
		if k, _ := a.Metadata["digestKind"].(string); k == deployattempt.DigestManifest || k == deployattempt.DigestIndex {
			e.DigestKind = k
		}
	} else if deploymentImageDigest.MatchString(ref) {
		e.Digest = ref
		e.DigestKind = deployattempt.DigestConfig
	}
	e.Platform.OS, _ = a.Metadata["os"].(string)
	e.Platform.Architecture, _ = a.Metadata["architecture"].(string)
	e.Platform.Variant, _ = a.Metadata["variant"].(string)
	return e
}

// ObservationImageTarget resolves the same name and immutable image reference
// as deployment, using local metadata only. It grants no inspection or execute
// capability and does not mutate the artifact or configuration map.
func ObservationImageTarget(a run.Artifact, cfg map[string]string) (string, deployattempt.ExpectedImage, error) {
	if a.Type != run.ArtifactImage {
		return "", deployattempt.ExpectedImage{}, errors.New("deploy: image artifact required")
	}
	ref, err := imageDeploymentReference(a)
	if err != nil {
		return "", deployattempt.ExpectedImage{}, err
	}
	if strings.TrimSpace(ref) == "" {
		return "", deployattempt.ExpectedImage{}, errors.New("deploy: image reference unavailable")
	}
	return imageContainerName(a, cfg), expectedImage(a, ref), nil
}

func (s *service) startImageReceipt(ctx context.Context, srv *target.Server, a run.Artifact, st *imageState) error {
	if s.attempts == nil {
		return nil
	}
	attempt, _ := ctx.Value(attemptScopeKey{}).(*deployattempt.Attempt)
	if attempt == nil {
		var err error
		ctx, err = s.beginAttempt(ctx, a.RunID, "image")
		if err != nil {
			return err
		}
		attempt, _ = ctx.Value(attemptScopeKey{}).(*deployattempt.Attempt)
	}
	fingerprint := ""
	if ex, ok := s.targets.(interface {
		Capture(context.Context, string) (target.ConnectionSnapshot, error)
	}); ok {
		// Capture reads local configuration/ciphertext version only; it performs
		// no SSH. Missing capture evidence never becomes a verified identity.
		if snap, err := ex.Capture(ctx, srv.ID); err == nil {
			fingerprint = snap.Fingerprint()
		}
	}
	t, err := s.attempts.StartTarget(ctx, attempt.ID, deployattempt.TargetInput{ArtifactID: a.ID, ArtifactName: a.Name, ServerID: srv.ID, ServerName: srv.Name, ContainerName: st.name, ConnectionFingerprint: fingerprint, ExpectedImage: expectedImage(a, st.ref)})
	if err != nil {
		return err
	}
	st.receipt = &imageReceipt{target: t, actual: deployattempt.ActualImage{RepoDigests: []string{}}}
	return nil
}

func (s *service) imageEvent(ctx context.Context, st imageState, kind, gate, reason string) bool {
	if st.receipt == nil {
		return true
	}
	r := st.receipt
	if r.unknown && kind != deployattempt.OutcomeUnknown {
		return false
	}
	if kind == deployattempt.OutcomeUnknown {
		r.unknown = true
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.attempts.AppendReceipt(cleanup, r.target.ID, deployattempt.Receipt{Kind: kind, ActualImage: r.actual, PreviousImageReference: r.previous, RollbackContainerID: r.rollbackContainerID, HealthGate: gate, Reason: reason})
	if err == nil {
		return true
	}
	r.unknown = true
	log.Printf("[deploy] deployment receipt unavailable for target %s", r.target.ID)
	// A durable activation_started entry already signals an uncertain outcome
	// if this best-effort unknown write also fails. Never replay remote work.
	if kind != deployattempt.OutcomeUnknown {
		_, _ = s.attempts.AppendReceipt(cleanup, r.target.ID, deployattempt.Receipt{Kind: deployattempt.OutcomeUnknown, ActualImage: r.actual, Reason: "receipt_persistence_failed", HealthGate: gate})
	}
	return false
}

func (s *service) beforeImage(ctx context.Context, st imageState) {
	if st.receipt == nil {
		return
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.notifyImage(func() { s.imageObserver.BeforeImageActivation(c, st.receipt.target.AttemptID, st.receipt.target.ID) })
}

func (s *service) afterImage(ctx context.Context, st imageState) {
	if st.receipt == nil || st.receipt.unknown || !deployattempt.ValidContainerID(st.receipt.actual.ContainerID) {
		return
	}
	s.notifyImage(func() {
		s.imageObserver.AfterImageActivation(ctx, st.receipt.target.AttemptID, st.receipt.target.ID, st.receipt.actual.ContainerID)
	})
}

func cleanPreviousImage(ref string) string {
	ref = strings.TrimSpace(ref)
	if len(ref) > 1024 || strings.ContainsAny(ref, "\x00\r\n\t ") {
		return ""
	}
	return ref
}
