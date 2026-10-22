package configstate

import (
	"context"
	"database/sql"
	"errors"
)

var ErrPrecondition = errors.New("configstate: revision precondition required")

type MutationResult struct {
	Revision   int64
	Successful int
	Err        error
}
type mutationKey struct{}
type mutationContext struct {
	expected *int64
	require  bool
	result   *MutationResult
}

// WithMutation carries an HTTP precondition through the domain boundary. It
// never opens a transaction around handlers or their external preflight work.
func WithMutation(ctx context.Context, expected *int64, require bool, result *MutationResult) context.Context {
	return context.WithValue(ctx, mutationKey{}, &mutationContext{expected, require, result})
}

func CheckExpected(ctx context.Context, rev int64) error {
	m, _ := ctx.Value(mutationKey{}).(*mutationContext)
	if m == nil {
		return nil
	}
	if m.require && m.expected == nil {
		return ErrPrecondition
	}
	if m.expected != nil && *m.expected != rev {
		return ErrConflict
	}
	return nil
}
func RecordMutation(ctx context.Context, revision int64, err error) {
	m, _ := ctx.Value(mutationKey{}).(*mutationContext)
	if m == nil || m.result == nil {
		return
	}
	m.result.Err = err
	if err == nil {
		m.result.Revision = revision
		m.result.Successful++
		m.expected = &m.result.Revision
	}
}

func MutateProject(ctx context.Context, db *sql.DB, id string, fn func(context.Context, DBTX) error) error {
	m, _ := ctx.Value(mutationKey{}).(*mutationContext)
	var next int64
	err := WithWriteTxContext(ctx, db, func(txctx context.Context, q DBTX) error {
		if err := LockEpochs(txctx, q, db, EpochPolicy, EpochSecrets, EpochExecutor); err != nil {
			return err
		}
		rev, err := LockProject(txctx, q, db, id)
		if err != nil {
			return err
		}
		if m != nil {
			if m.require && m.expected == nil {
				return ErrPrecondition
			}
			if m.expected != nil && *m.expected != rev {
				return ErrConflict
			}
		}
		if err := CheckIdle(txctx, q, id); err != nil {
			return err
		}
		if err := fn(txctx, q); err != nil {
			return err
		}
		next, err = BumpRevision(txctx, q, id, rev)
		return err
	})
	if m != nil && m.result != nil {
		m.result.Err = err
		if err == nil {
			m.result.Revision = next
			m.result.Successful++
			m.expected = &m.result.Revision
		}
	}
	return err
}
