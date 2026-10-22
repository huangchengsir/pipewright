package deploy

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type deployActivityKey struct{}
type deployActivity struct {
	mu                   sync.Mutex
	readers              int
	svc                  *service
	id, projectID, runID string
}

// Admission protects current configuration reads without holding SQL locks
// across SSH, health probes, archive IO, or asynchronous diagnosis. Remote
// leftovers are not inferred from these reader holds and no TTL releases them.
func (s *service) beginActivity(ctx context.Context, runID string) (context.Context, func(), error) {
	if s.batchDB == nil {
		return ctx, func() {}, nil
	}
	if a, ok := ctx.Value(deployActivityKey{}).(*deployActivity); ok && a.svc == s && a.runID == runID {
		if done, ok := a.retain(); ok {
			return ctx, done, nil
		}
	}
	a := &deployActivity{readers: 1, svc: s, id: uuid.NewString(), runID: runID}
	// Register before SQL admission: an ambiguous COMMIT must still leave local
	// proof that no deployment/configuration reader started after this attempt.
	if err := configstate.StartActivityReaders(a.id, s.activityOwner); err != nil {
		return ctx, nil, err
	}
	err := configstate.WithWriteTxContext(ctx, s.batchDB, func(txctx context.Context, q configstate.DBTX) error {
		if err := configstate.LockEpochs(txctx, q, s.batchDB, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
			return err
		}
		if err := q.QueryRowContext(txctx, `SELECT project_id FROM pipeline_runs WHERE id=?`, runID).Scan(&a.projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrRunNotFound
			}
			return err
		}
		_, err := configstate.AcquireActivityTx(txctx, q, s.batchDB, configstate.Activity{ID: a.id, ProjectID: a.projectID, RunID: runID, OwnerEpoch: s.activityOwner, Kind: "deploy", Phase: "executing"})
		return err
	})
	if err != nil {
		a.admissionFailed()
		return ctx, nil, err
	}
	return context.WithValue(ctx, deployActivityKey{}, a), a.done, nil
}

func (a *deployActivity) admissionFailed() {
	a.mu.Lock()
	a.readers = 0
	a.mu.Unlock()
	configstate.RecordActivityReadersExited(a.id, a.svc.activityOwner)
	// A plain nonlocking SELECT could race an unresolved server-side COMMIT.
	// Reacquire the admission epochs before treating absence as authoritative;
	// a lock timeout/unknown result conservatively retains the exit evidence.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	knownGone := false
	err := configstate.WithWriteTxContext(ctx, a.svc.batchDB, func(txctx context.Context, q configstate.DBTX) error {
		if err := configstate.LockEpochs(txctx, q, a.svc.batchDB, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
			return err
		}
		var owner string
		var released sql.NullInt64
		err := q.QueryRowContext(txctx, `SELECT owner_epoch,released_at FROM project_activity WHERE activity_id=?`, a.id).Scan(&owner, &released)
		if errors.Is(err, sql.ErrNoRows) {
			knownGone = true
			return nil
		}
		if err != nil {
			return err
		}
		knownGone = owner == a.svc.activityOwner && released.Valid
		return nil
	})
	if err == nil && knownGone {
		configstate.ForgetActivityReaders(a.id, a.svc.activityOwner)
	}
}

func (a *deployActivity) retain() (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.readers <= 0 {
		return nil, false
	}
	a.readers++
	return a.done, true
}

func (a *deployActivity) done() {
	a.mu.Lock()
	a.readers--
	last := a.readers == 0
	a.mu.Unlock()
	if !last {
		return
	}
	configstate.RecordActivityReadersExited(a.id, a.svc.activityOwner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := configstate.WithWriteTxContext(ctx, a.svc.batchDB, func(txctx context.Context, q configstate.DBTX) error {
		if _, err := configstate.LockProject(txctx, q, a.svc.batchDB, a.projectID); err != nil {
			return err
		}
		return configstate.ReleaseActivityTx(txctx, q, a.id, a.svc.activityOwner)
	})
	if err != nil {
		log.Printf("[deploy] activity %s for run %s retained after reader exit: %v", a.id, a.runID, err)
	} else {
		configstate.ForgetActivityReaders(a.id, a.svc.activityOwner)
	}
}

func retainDiagnosticActivity(ctx context.Context) func() {
	if a, ok := ctx.Value(deployActivityKey{}).(*deployActivity); ok {
		if done, ok := a.retain(); ok {
			return done
		}
	}
	return func() {}
}
