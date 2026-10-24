package deployattempt

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

var fullID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var fullSHA = regexp.MustCompile(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`)
var imagePlatformPart = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

func ValidContainerID(id string) bool { return fullID.MatchString(id) }

// ValidLocalExpected distinguishes a newly captured pre-activation local
// image identity from legacy config IDs and repository/manifest identities.
// A container inspection must never be used to populate this expectation.
func ValidLocalExpected(e ExpectedImage) bool {
	return e.IdentitySource == IdentityScriptPreActivation && e.DigestKind == DigestConfig &&
		digest.MatchString(e.Digest) && e.Reference == e.Digest && e.Repository == "" &&
		imagePlatformPart.MatchString(e.Platform.OS) && imagePlatformPart.MatchString(e.Platform.Architecture) &&
		(e.Platform.Variant == "" || imagePlatformPart.MatchString(e.Platform.Variant))
}

// WorkloadKey isolates projects, explicit environment (including empty), server
// records, and declared container roles across changing runtime generations.
func WorkloadKey(projectID, environment, serverID, role string) string {
	b, _ := json.Marshal([]string{projectID, environment, serverID, role})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func safe(s string, n int) bool { return len(s) <= n && !strings.ContainsAny(s, "\x00\r\n") }

func (s *Store) Begin(ctx context.Context, in BeginInput) (*Attempt, error) {
	if s == nil || s.db == nil || in.RunID == "" || !safe(in.RunID, 64) || !safe(in.StageID, 255) || !safe(in.Initiator, 32) || (in.ResolvedFullSHA != "" && !fullSHA.MatchString(in.ResolvedFullSHA)) {
		return nil, ErrInvalid
	}
	a := &Attempt{ID: uuid.NewString(), CreatedAt: time.Now().UTC(), Targets: []Target{}}
	a.Source = Source{RunID: in.RunID, StageID: in.StageID, Initiator: in.Initiator, ResolvedFullSHA: in.ResolvedFullSHA}
	// Source project/environment are authoritative local run facts, never values
	// supplied by a browser or inferred from a server/container name.
	err := s.db.QueryRowContext(ctx, `SELECT r.project_id,r.resolved_environment,COALESCE(v.revision,0) FROM pipeline_runs r LEFT JOIN project_config_versions v ON v.project_id=r.project_id WHERE r.id=?`, in.RunID).Scan(&a.Source.ProjectID, &a.Source.Environment, &a.Source.ConfigRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("deployattempt: source: %w", err)
	}
	body, _ := json.Marshal(a.Source)
	_, err = s.db.ExecContext(ctx, `INSERT INTO deployment_attempts(id,run_id,project_id,created_at,source_json) VALUES(?,?,?,?,?)`, a.ID, a.Source.RunID, a.Source.ProjectID, a.CreatedAt.UnixMilli(), string(body))
	if err != nil {
		return nil, fmt.Errorf("deployattempt: begin: %w", err)
	}
	return a, nil
}

func validateExpected(e ExpectedImage) bool {
	if !safe(e.Reference, 1024) || !safe(e.Repository, 512) || !safe(e.Platform.OS, 32) || !safe(e.Platform.Architecture, 32) || !safe(e.Platform.Variant, 32) {
		return false
	}
	if e.IdentitySource != "" {
		return ValidLocalExpected(e)
	}
	switch e.DigestKind {
	case DigestUnavailable:
		return e.Digest == ""
	case DigestManifest, DigestIndex, DigestRepositoryUnknown:
		return e.Repository != "" && digest.MatchString(e.Digest)
	case DigestConfig:
		return digest.MatchString(e.Digest)
	default:
		return false
	}
}

func (s *Store) StartTarget(ctx context.Context, attemptID string, in TargetInput) (*Target, error) {
	if in.ArtifactID == "" || in.ServerID == "" || in.ContainerName == "" || !safe(in.ArtifactID, 64) || !safe(in.ServerID, 64) || !safe(in.ArtifactName, 255) || !safe(in.ServerName, 255) || !safe(in.ContainerName, 255) || (in.ConnectionFingerprint != "" && !fullID.MatchString(in.ConnectionFingerprint)) || !validateExpected(in.ExpectedImage) {
		return nil, ErrInvalid
	}
	a, err := s.attempt(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	t := &Target{ID: uuid.NewString(), AttemptID: attemptID, Source: a.Source, TargetInput: in, WorkloadKey: WorkloadKey(a.Source.ProjectID, a.Source.Environment, in.ServerID, in.ContainerName), IdentityTrust: "host_identity_unverified", CreatedAt: time.Now().UTC(), Outcome: Started, Receipts: []Receipt{}}
	body, _ := json.Marshal(t)
	_, err = s.db.ExecContext(ctx, `INSERT INTO deployment_attempt_targets(id,attempt_id,workload_key,created_at,sequence,outcome,target_json) VALUES(?,?,?,?,0,?,?)`, t.ID, t.AttemptID, t.WorkloadKey, t.CreatedAt.UnixMilli(), Started, string(body))
	if err != nil {
		return nil, fmt.Errorf("deployattempt: target: %w", err)
	}
	return t, nil
}

func validReceipt(r Receipt) bool {
	switch r.Kind {
	case ActivationStarted, Activated, Succeeded, DeployFailed, RolledBack, OutcomeUnknown, Aborted, IdentityObserved:
	default:
		return false
	}
	if r.ActualImage.ContainerID != "" && !ValidContainerID(r.ActualImage.ContainerID) {
		return false
	}
	if (r.Kind == Activated || r.Kind == Succeeded || r.Kind == IdentityObserved) && !ValidContainerID(r.ActualImage.ContainerID) {
		return false
	}
	if r.RollbackContainerID != "" && !ValidContainerID(r.RollbackContainerID) {
		return false
	}
	if r.ActualImage.ConfigID != "" && !digest.MatchString(r.ActualImage.ConfigID) {
		return false
	}
	if len(r.ActualImage.RepoDigests) > 32 || !safe(r.PreviousImageReference, 1024) || !safe(r.Reason, 128) {
		return false
	}
	for _, ref := range r.ActualImage.RepoDigests {
		repo, d, ok := strings.Cut(ref, "@")
		if !ok || repo == "" || !safe(ref, 1024) || !digest.MatchString(d) {
			return false
		}
	}
	if r.ActualImage.ManifestMappingVerified && (r.ActualImage.ConfigID == "" || len(r.ActualImage.RepoDigests) == 0) {
		return false
	}
	switch r.HealthGate {
	case "", "not_run", "passed", "failed":
	default:
		return false
	}
	return safe(r.ActualImage.ContainerCreatedAt, 64) && safe(r.ActualImage.ContainerStartedAt, 64) && safe(r.ActualImage.Platform.OS, 32) && safe(r.ActualImage.Platform.Architecture, 32) && safe(r.ActualImage.Platform.Variant, 32)
}

func allowed(from, to string) bool {
	if to == IdentityObserved {
		return from == Activated || from == Succeeded || from == RolledBack
	}
	switch from {
	case Started:
		return to == ActivationStarted || to == DeployFailed || to == Aborted || to == OutcomeUnknown
	case ActivationStarted:
		return to == Activated || to == DeployFailed || to == RolledBack || to == OutcomeUnknown
	case Activated:
		return to == Succeeded || to == DeployFailed || to == RolledBack || to == OutcomeUnknown
	case Succeeded:
		return to == RolledBack || to == DeployFailed || to == OutcomeUnknown
	default:
		return false
	}
}

// AppendReceipt never overwrites a prior receipt. A CAS orders concurrent
// writers. On ambiguous persistence errors callers must not replay remote work.
func (s *Store) AppendReceipt(ctx context.Context, targetID string, r Receipt) (*Receipt, error) {
	if !validReceipt(r) {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var seq int64
	var outcome string
	err = tx.QueryRowContext(ctx, `SELECT sequence,outcome FROM deployment_attempt_targets WHERE id=?`, targetID).Scan(&seq, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !allowed(outcome, r.Kind) {
		return nil, ErrConflict
	}
	nextOutcome := r.Kind
	if r.Kind == IdentityObserved {
		nextOutcome = outcome
	}
	res, err := tx.ExecContext(ctx, `UPDATE deployment_attempt_targets SET sequence=?,outcome=? WHERE id=? AND sequence=?`, seq+1, nextOutcome, targetID, seq)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, ErrConflict
	}
	r.ID = uuid.NewString()
	r.TargetID = targetID
	r.Sequence = seq + 1
	r.At = time.Now().UTC()
	if r.ActualImage.RepoDigests == nil {
		r.ActualImage.RepoDigests = []string{}
	}
	body, _ := json.Marshal(r)
	if len(body) > 48<<10 {
		return nil, ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO deployment_attempt_receipts(id,target_id,sequence,kind,created_at,receipt_json) VALUES(?,?,?,?,?,?)`, r.ID, targetID, r.Sequence, r.Kind, r.At.UnixMilli(), string(body))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) attempt(ctx context.Context, id string) (*Attempt, error) {
	a := &Attempt{Targets: []Target{}}
	var at int64
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT id,created_at,source_json FROM deployment_attempts WHERE id=?`, id).Scan(&a.ID, &at, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(body), &a.Source) != nil {
		return nil, ErrInvalid
	}
	a.CreatedAt = time.UnixMilli(at).UTC()
	return a, nil
}

func (s *Store) Get(ctx context.Context, id string) (*Attempt, error) {
	a, err := s.attempt(ctx, id)
	if err != nil {
		return nil, err
	}
	a.Targets, err = s.ListTargets(ctx, id)
	return a, err
}

func (s *Store) GetTarget(ctx context.Context, id string) (*Target, error) {
	var body, outcome string
	err := s.db.QueryRowContext(ctx, `SELECT target_json,outcome FROM deployment_attempt_targets WHERE id=?`, id).Scan(&body, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t := &Target{}
	if json.Unmarshal([]byte(body), t) != nil {
		return nil, ErrInvalid
	}
	t.Outcome = outcome
	t.Receipts = []Receipt{}
	rows, err := s.db.QueryContext(ctx, `SELECT receipt_json FROM deployment_attempt_receipts WHERE target_id=? ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		var r Receipt
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(b), &r) != nil {
			return nil, ErrInvalid
		}
		if r.ActualImage.RepoDigests == nil {
			r.ActualImage.RepoDigests = []string{}
		}
		t.Receipts = append(t.Receipts, r)
	}
	return t, rows.Err()
}

func (s *Store) targetQuery(ctx context.Context, q string, args ...any) ([]Target, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []Target{}
	for _, id := range ids {
		t, e := s.GetTarget(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, *t)
	}
	return out, nil
}

func (s *Store) ListTargets(ctx context.Context, id string) ([]Target, error) {
	return s.targetQuery(ctx, `SELECT id FROM deployment_attempt_targets WHERE attempt_id=? ORDER BY created_at,id`, id)
}
func (s *Store) ListByWorkload(ctx context.Context, key string, limit int) ([]Target, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.targetQuery(ctx, `SELECT id FROM deployment_attempt_targets WHERE workload_key=? ORDER BY created_at DESC,id DESC LIMIT ?`, key, limit)
}
func (s *Store) ListByRun(ctx context.Context, runID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM deployment_attempts WHERE run_id=? ORDER BY created_at DESC,id DESC LIMIT 100`, runID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []Attempt{}
	for _, id := range ids {
		a, e := s.Get(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, *a)
	}
	return out, nil
}

// RecoverInterrupted is startup-only; it records incomplete execution without
// reissuing any SSH, Docker, inspection, or deployment command.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	targets, err := s.targetQuery(ctx, `SELECT id FROM deployment_attempt_targets WHERE outcome IN ('started','activation_started','activated') ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	for _, t := range targets {
		_, err = s.AppendReceipt(ctx, t.ID, Receipt{Kind: OutcomeUnknown, Reason: "service_restarted", HealthGate: "not_run"})
		if err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}
