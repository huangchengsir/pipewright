// Package completedrefs retains only this process's proof that a scoped
// operation has returned. It retries SQL bookkeeping, never business operations.
package completedrefs

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

type identity struct {
	db                   *sql.DB
	namespace, id, owner string
}
type completion struct {
	mu     sync.Mutex
	remove func(context.Context) error
}
type registry struct {
	mu      sync.Mutex
	pending map[identity]*completion
}

var current registry

// Complete records immutable exact-owner completion before attempting removal.
// Failed attempts retain that evidence; a fresh process intentionally has none.
func Complete(ctx context.Context, db *sql.DB, namespace, id, owner string, remove func(context.Context) error) error {
	return current.complete(ctx, identity{db, namespace, id, owner}, remove)
}
func (r *registry) complete(ctx context.Context, key identity, remove func(context.Context) error) error {
	if key.db == nil || key.namespace == "" || key.id == "" || key.owner == "" || remove == nil {
		return errors.New("completedrefs: invalid completion identity")
	}
	r.mu.Lock()
	if r.pending == nil {
		r.pending = make(map[identity]*completion)
	}
	entry := r.pending[key]
	if entry == nil {
		entry = &completion{remove: remove}
		r.pending[key] = entry
	}
	r.mu.Unlock()
	return r.retry(ctx, key, entry)
}
func (r *registry) retry(ctx context.Context, key identity, entry *completion) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	r.mu.Lock()
	stillPending := r.pending[key] == entry
	r.mu.Unlock()
	if !stillPending {
		return nil
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e := entry.remove(attempt); e != nil {
		return e
	}
	r.mu.Lock()
	if r.pending[key] == entry {
		delete(r.pending, key)
	}
	r.mu.Unlock()
	return nil
}

// Flush is called before opening an outer write transaction. Calling raw DB
// cleanup inside an existing transaction could deadlock a single-connection DB.
func Flush(ctx context.Context, db *sql.DB) { current.flush(ctx, db) }
func (r *registry) flush(ctx context.Context, db *sql.DB) {
	budget, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = budget
	type item struct {
		key   identity
		entry *completion
	}
	r.mu.Lock()
	items := make([]item, 0)
	for key, entry := range r.pending {
		if key.db == db {
			items = append(items, item{key, entry})
		}
	}
	r.mu.Unlock()
	for _, work := range items {
		if ctx.Err() != nil {
			return
		}
		_ = r.retry(ctx, work.key, work.entry)
	}
}
