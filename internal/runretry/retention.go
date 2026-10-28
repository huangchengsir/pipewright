package runretry

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"time"
)

func (s *Service) gcLock(ctx context.Context, q configstate.DBTX, root, project string) error {
	if err := configstate.LockEpochs(ctx, q, s.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
		return err
	}
	var exists int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id=?", project).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		if _, err := configstate.LockProject(ctx, q, s.db, project); err != nil {
			return err
		}
	}
	query := "SELECT revision FROM run_retry_lineages WHERE root_id=?"
	if store.DialectOf(s.db) == store.MySQL {
		query += " FOR UPDATE"
	}
	var revision int64
	return q.QueryRowContext(ctx, query, root).Scan(&revision)
}

func (s *Service) gcClear(ctx context.Context, q configstate.DBTX, root string) (bool, error) {
	var holds int
	err := q.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM pipeline_runs p JOIN run_retry_attempts a ON a.run_id=p.id WHERE a.root_id=?)+
 (SELECT COUNT(*) FROM project_activity p JOIN run_retry_attempts a ON a.run_id=p.run_id WHERE a.root_id=? AND p.released_at IS NULL)+
 (SELECT COUNT(*) FROM run_retry_nodes n JOIN run_retry_attempts a ON a.run_id=n.attempt_id WHERE a.root_id=? AND n.status IN ('running','outcome_unknown'))+
 (SELECT COUNT(*) FROM run_retry_dispatch d JOIN run_retry_attempts a ON a.run_id=d.attempt_id WHERE a.root_id=? AND d.state IN ('pending','dispatching','accepted','running','outcome_unknown'))+
 (SELECT COUNT(*) FROM run_retry_refs r JOIN run_retry_attempts a ON a.run_id=r.producer_attempt_id WHERE a.root_id=? AND r.released_at IS NULL)+
 (SELECT COUNT(*) FROM run_retry_resources WHERE root_id=? AND state<>'drained')+
 (SELECT COUNT(*) FROM run_retry_external_targets WHERE root_id=? AND state IN ('running','outcome_unknown'))+
 (SELECT COUNT(*) FROM run_retry_lineages WHERE root_id=? AND active_attempt_id IS NOT NULL)`, root, root, root, root, root, root, root, root).Scan(&holds)
	return holds == 0, err
}

type gcImage struct {
	key, state string
	image      ImageRef
}

// PruneOrphans collects only lineages with no retained runs or unverified work.
// The durable claim serializes new pins; physical removal is outside SQL.
func (s *Service) PruneOrphans(ctx context.Context, now time.Time, release func(context.Context, ImageRef) error) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.root_id,l.project_id FROM run_retry_lineages l WHERE NOT EXISTS(SELECT 1 FROM run_retry_attempts a JOIN pipeline_runs p ON p.id=a.run_id WHERE a.root_id=l.root_id) ORDER BY l.updated_at,l.root_id LIMIT 16`)
	if err != nil {
		return 0, err
	}
	type candidate struct{ root, project string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.root, &c.project); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	deleted := 0
	evidenceBlocked := false
	for _, c := range candidates {
		var images []gcImage
		claimed := false
		err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
			if err := s.gcLock(tx, q, c.root, c.project); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
			// Rotate unresolved candidates without interpreting age as exit proof.
			if _, err := q.ExecContext(tx, "UPDATE run_retry_lineages SET updated_at=? WHERE root_id=?", now.UnixMilli(), c.root); err != nil {
				return err
			}
			clear, err := s.gcClear(tx, q, c.root)
			if err != nil || !clear {
				return err
			}
			rows, err := q.QueryContext(tx, "SELECT effect_key,state,ciphertext FROM run_retry_effects WHERE root_id=? AND effect_key LIKE 'image_gc:%' ORDER BY effect_key", c.root)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var item gcImage
				var cipher []byte
				if err = rows.Scan(&item.key, &item.state, &cipher); err != nil {
					return err
				}
				if item.state == "gc_failed" || item.state == "gc_claimed" {
					return nil
				}
				if item.state == "gc_done" {
					continue
				}
				if item.state != "retained_image" || s.open(cipher, "retained-image", c.root, item.key, &item.image) != nil {
					// Preserve unreadable ownership proof and commit scan rotation.
					// No physical work is claimed. Report partial unavailability
					// after the rest of this bounded batch can make progress.
					evidenceBlocked = true
					return nil
				}
				images = append(images, item)
			}
			if err = rows.Err(); err != nil {
				return err
			}
			rows.Close()
			for _, item := range images {
				if _, err = q.ExecContext(tx, "UPDATE run_retry_effects SET state='gc_claimed',updated_at=? WHERE root_id=? AND effect_key=? AND state='retained_image'", now.UnixMilli(), c.root, item.key); err != nil {
					return err
				}
			}
			claimed = true
			return nil
		})
		if err != nil {
			return deleted, err
		}
		if !claimed {
			continue
		}
		allDone := true
		for _, item := range images {
			state := "gc_failed"
			if release != nil {
				probe, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := release(probe, item.image)
				cancel()
				if err == nil {
					state = "gc_done"
				}
			}
			if state != "gc_done" {
				allDone = false
			}
			if _, err = s.db.ExecContext(ctx, "UPDATE run_retry_effects SET state=?,updated_at=? WHERE root_id=? AND effect_key=? AND state='gc_claimed'", state, time.Now().UnixMilli(), c.root, item.key); err != nil {
				return deleted, err
			}
		}
		if !allDone {
			continue
		}
		collected := false
		err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
			if err := s.gcLock(tx, q, c.root, c.project); err != nil {
				return err
			}
			clear, err := s.gcClear(tx, q, c.root)
			if err != nil || !clear {
				return err
			}
			var unresolved int
			if err = q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_effects WHERE root_id=? AND effect_key LIKE 'image_gc:%' AND state<>'gc_done'", c.root).Scan(&unresolved); err != nil || unresolved > 0 {
				return err
			}
			for _, table := range []string{"run_retry_reader_exits", "run_retry_nodes", "run_retry_dispatch"} {
				if _, err = q.ExecContext(tx, "DELETE FROM "+table+" WHERE attempt_id IN (SELECT run_id FROM run_retry_attempts WHERE root_id=?)", c.root); err != nil {
					return err
				}
			}
			if _, err = q.ExecContext(tx, "DELETE FROM run_retry_refs WHERE owner_attempt_id IN (SELECT run_id FROM run_retry_attempts WHERE root_id=?) OR producer_attempt_id IN (SELECT run_id FROM run_retry_attempts WHERE root_id=?)", c.root, c.root); err != nil {
				return err
			}
			for _, table := range []string{"run_retry_resources", "run_retry_external_targets", "run_retry_effects", "run_retry_requests", "run_retry_sources", "run_retry_manifests", "run_retry_attempts"} {
				if _, err = q.ExecContext(tx, "DELETE FROM "+table+" WHERE root_id=?", c.root); err != nil {
					return err
				}
			}
			result, err := q.ExecContext(tx, "DELETE FROM run_retry_lineages WHERE root_id=?", c.root)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			collected = n == 1
			return err
		})
		if err != nil {
			return deleted, err
		}
		if collected {
			deleted++
		}
	}
	if evidenceBlocked {
		return deleted, ErrUnavailable
	}
	return deleted, nil
}
