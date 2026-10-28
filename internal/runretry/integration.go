package runretry

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/configstate"
)

// RestorePreparationFailure is permitted only before any node Begin. Restoring
// previous effective results preserves their true producer and timings.
func (s *Service) RestorePreparationFailure(ctx context.Context, id string) error {
	return s.restoreBeforeExecution(ctx, id, "preparation_failed")
}

// RestoreBeforeRunner requires the trusted worker's proof that Runner.Run was
// never entered, plus empty external ledgers. Owner absence alone is not proof.
func (s *Service) RestoreBeforeRunner(ctx context.Context, id, reason string) error {
	if reason != "cancelled_before_start" && reason != "preparation_failed" {
		return ErrInvalid
	}
	return s.restoreBeforeExecution(ctx, id, reason)
}
func (s *Service) RestoreQueuedCancellation(ctx context.Context, id string) error {
	var status string
	var started sql.NullString
	if err := s.q(ctx).QueryRowContext(ctx, "SELECT status,started_at FROM pipeline_runs WHERE id=?", id).Scan(&status, &started); err != nil {
		return err
	}
	if status != "failed" || started.Valid {
		return ErrBlocked
	}
	return s.restoreBeforeExecution(ctx, id, "cancelled_before_start")
}
func (s *Service) restoreBeforeExecution(ctx context.Context, id, reason string) error {
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		var root, project, parent string
		err := q.QueryRowContext(tx, "SELECT a.root_id,l.project_id,a.parent_id FROM run_retry_attempts a JOIN run_retry_lineages l ON l.root_id=a.root_id WHERE a.run_id=?", id).Scan(&root, &project, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = s.lock(tx, q, lineage{root: root, project: project}); err != nil {
			return err
		}
		var begun int
		if err = q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND owner_epoch<>''", id).Scan(&begun); err != nil {
			return err
		}
		if begun != 0 {
			return ErrUnknown
		}
		var external int
		if err = q.QueryRowContext(tx, "SELECT (SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=?)+(SELECT COUNT(*) FROM run_retry_resources WHERE attempt_id=?)", id, id).Scan(&external); err != nil {
			return err
		}
		if external != 0 {
			return ErrUnknown
		}
		var dispatch string
		err = q.QueryRowContext(tx, "SELECT state FROM run_retry_dispatch WHERE attempt_id=?", id).Scan(&dispatch)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if dispatch == "outcome_unknown" || dispatch == "dispatching" {
			return ErrUnknown
		}
		if parent == "" {
			_, err = q.ExecContext(tx, "UPDATE run_retry_nodes SET status='failed',reason_code=?,resources_drained=1 WHERE attempt_id=? AND status='pending' AND origin='executed'", reason, id)
			return err
		}
		previous, err := s.records(tx, parent)
		if err != nil {
			return err
		}
		for key, record := range previous {
			origin := "blocked"
			if record.status == "success" {
				origin = "reuse"
			} else if record.status == "skipped" {
				origin = "skip"
			}
			if _, err = q.ExecContext(tx, "UPDATE run_retry_nodes SET status=?,origin=?,source_attempt_id=?,source_node_key=?,reason_code=?,resources_drained=1 WHERE attempt_id=? AND node_key=? AND status='pending'", record.status, origin, record.source, record.sourceKey, reason, id, key); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(tx, "UPDATE run_retry_requests SET reason_code=? WHERE attempt_id=? AND reason_code IN ('','preparation_failed')", reason, id)
		return err
	})
}

// ResourcesDrained covers owned resource controls as well as local reader exit.
func (s *Service) ResourcesDrained(ctx context.Context, id string) (bool, error) {
	captured, err := s.HasManifest(ctx, id)
	if err != nil || !captured {
		return !captured, err
	}
	var pending int
	if err = s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=? AND state IN ('running','outcome_unknown')", id).Scan(&pending); err != nil || pending > 0 {
		return false, err
	}
	if err = s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_resources WHERE attempt_id=? AND state<>'drained'", id).Scan(&pending); err != nil || pending > 0 {
		return false, err
	}
	var started int
	if err = s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND owner_epoch<>''", id).Scan(&started); err != nil {
		return false, err
	}
	if started == 0 {
		return true, nil
	}
	l, err := s.load(ctx, id)
	if err != nil {
		return false, err
	}
	records, err := s.records(ctx, id)
	if err != nil {
		return false, err
	}
	for _, n := range l.graph.Nodes {
		if n.ControlKey != "cleanup" {
			continue
		}
		record := records[n.Key]
		if record.owner == "" {
			continue
		}
		if !record.drained {
			return false, nil
		}
	}
	err = s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=? AND state IN ('running','outcome_unknown')", id).Scan(&pending)
	return pending == 0, err
}

// ArtifactSource deliberately excludes env values and private checkpoint data.
type ArtifactSource struct {
	RunID, NodeKey string
	References     []string
}

func (s *Service) ArtifactSources(ctx context.Context, id string) ([]ArtifactSource, error) {
	captured, err := s.HasManifest(ctx, id)
	if err != nil || !captured {
		return nil, err
	}
	l, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	records, err := s.records(ctx, id)
	if err != nil {
		return nil, err
	}
	var sources []ArtifactSource
	for _, n := range l.graph.Nodes {
		record := records[n.Key]
		if n.JobID == "" || record.origin != "reuse" || record.status != "success" {
			continue
		}
		o, err := s.outcome(ctx, l, n, record)
		if err != nil {
			return nil, ErrUnavailable
		}
		if !o.Complete || !o.Reusable || o.Status != "success" || (o.ValidUntil != nil && !time.Now().Before(*o.ValidUntil)) {
			return nil, ErrUnavailable
		}
		source := ArtifactSource{RunID: record.source, NodeKey: record.sourceKey}
		for _, file := range o.Outputs.Files {
			source.References = append(source.References, file.Key)
		}
		for _, image := range o.Outputs.Images {
			source.References = append(source.References, image.Ref)
		}
		if len(source.References) > 0 {
			sources = append(sources, source)
		}
	}
	return sources, nil
}

// ClaimEffect reserves an event at most once; this is not a delivery receipt.
func (s *Service) ClaimEffect(ctx context.Context, id, key string) (bool, error) {
	if strings.TrimSpace(key) == "" || len(key) > 191 {
		return false, ErrInvalid
	}
	l, err := s.load(ctx, id)
	if errors.Is(err, ErrUnavailable) {
		captured, check := s.HasManifest(ctx, id)
		if check != nil {
			return false, check
		}
		if captured {
			return false, ErrUnavailable
		}
		var exists int
		if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM pipeline_runs WHERE id=?", id).Scan(&exists); err != nil {
			return false, err
		}
		if exists == 0 {
			return false, nil
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	claimed := false
	err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		var existing int
		if err := q.QueryRowContext(tx, "SELECT COUNT(*) FROM pipeline_runs WHERE id=?", id).Scan(&existing); err != nil {
			return err
		}
		if existing == 0 {
			return nil
		}
		var state string
		err := q.QueryRowContext(tx, "SELECT state FROM run_retry_effects WHERE root_id=? AND effect_key=?", l.root, key).Scan(&state)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := time.Now().UnixMilli()
		_, err = q.ExecContext(tx, "INSERT INTO run_retry_effects(root_id,effect_key,attempt_id,state,created_at,updated_at) VALUES(?,?,?,'reserved',?,?)", l.root, key, id, now, now)
		claimed = err == nil
		return err
	})
	return claimed, err
}
func (s *Service) FinishEffect(ctx context.Context, id, key string) error {
	var root string
	err := s.q(ctx).QueryRowContext(ctx, "SELECT root_id FROM run_retry_attempts WHERE run_id=?", id).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.q(ctx).ExecContext(ctx, "UPDATE run_retry_effects SET state='attempted',updated_at=? WHERE root_id=? AND effect_key=? AND attempt_id=? AND state='reserved'", time.Now().UnixMilli(), root, key, id)
	return err
}
