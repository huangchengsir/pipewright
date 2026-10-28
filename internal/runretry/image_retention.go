package runretry

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
	"time"
)

func RememberImage(ctx context.Context, image ImageRef) error {
	ex, ok := ExecutionFrom(ctx)
	if !ok {
		return ErrUnavailable
	}
	v, ok := ex.(interface {
		RememberImage(context.Context, ImageRef) error
	})
	if !ok {
		return ErrUnavailable
	}
	return v.RememberImage(ctx, image)
}
func (e *execution) RememberImage(ctx context.Context, image ImageRef) error {
	n, ok := NodeFrom(ctx)
	if !ok || !e.valid(n) || !strings.HasPrefix(image.Ref, "pipewright/") || image.ImageID == "" || image.RuntimeID == "" || image.ConfigSHA == "" {
		return ErrInvalid
	}
	// A separate tiny record survives node checkpoint quota failure. Arbitrary
	// artifact metadata/env never enters this proof's one-MiB reserved budget.
	proof := ImageRef{Ref: image.Ref, ImageID: image.ImageID, RuntimeID: image.RuntimeID, ConfigSHA: image.ConfigSHA, ProducerAttemptID: e.runID, ProducerNodeKey: n.Key}
	key := "image_gc:" + digest(proof.Ref)
	cipher, err := e.svc.seal("retained-image", e.l.root, key, proof)
	if err != nil {
		return ErrCheckpoint
	}
	warning := false
	err = configstate.WithWriteTxContext(ctx, e.svc.db, func(tx context.Context, q configstate.DBTX) error {
		if err := e.svc.lock(tx, q, e.l); err != nil {
			return err
		}
		var old []byte
		var state string
		err := q.QueryRowContext(tx, "SELECT ciphertext,state FROM run_retry_effects WHERE root_id=? AND effect_key=?", e.l.root, key).Scan(&old, &state)
		if err == nil {
			if state == "identity_evidence_unavailable" {
				warning = true
				return nil
			}
			var previous ImageRef
			if state != "retained_image" || e.svc.open(old, "retained-image", e.l.root, key, &previous) != nil || previous.Ref != proof.Ref || previous.RuntimeID != proof.RuntimeID || previous.ImageID != proof.ImageID || previous.ConfigSHA != proof.ConfigSHA {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var used int64
		if err := q.QueryRowContext(tx, "SELECT identity_bytes FROM run_retry_lineages WHERE root_id=?", e.l.root).Scan(&used); err != nil {
			return err
		}
		if used+int64(len(cipher)) > 1<<20 {
			warning = true
			now := time.Now().UnixMilli()
			_, err = q.ExecContext(tx, "INSERT INTO run_retry_effects(root_id,effect_key,attempt_id,state,created_at,updated_at) VALUES(?,?,?,'identity_evidence_unavailable',?,?)", e.l.root, key, e.runID, now, now)
			return err
		}
		now := time.Now().UnixMilli()
		if _, err = q.ExecContext(tx, "INSERT INTO run_retry_effects(root_id,effect_key,attempt_id,state,ciphertext,created_at,updated_at) VALUES(?,?,?,'retained_image',?,?,?)", e.l.root, key, e.runID, cipher, now, now); err != nil {
			return err
		}
		_, err = q.ExecContext(tx, "UPDATE run_retry_lineages SET identity_bytes=identity_bytes+? WHERE root_id=?", len(cipher), e.l.root)
		return err
	})
	if err != nil {
		return err
	}
	if warning {
		return ErrCheckpoint
	}
	return nil
}
