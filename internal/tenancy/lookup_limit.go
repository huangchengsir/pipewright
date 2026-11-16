package tenancy

import (
	"crypto/sha256"
	"sync"
	"time"
)

// LookupBudget is fixed-size and counts successes and misses alike. Hash bucket
// collisions can only narrow the budget, never allocate memory or expand it.
type LookupBudget struct {
	mu      sync.Mutex
	buckets [256]lookupWindow
}
type lookupWindow struct {
	start time.Time
	used  int
}

func (b *LookupBudget) Allow(key string, limit int) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	sum := sha256.Sum256([]byte(key))
	w := &b.buckets[int(sum[0])]
	if w.start.IsZero() || now.Sub(w.start) >= time.Minute {
		w.start = now
		w.used = 0
	}
	if w.used >= limit {
		return time.Minute - now.Sub(w.start)
	}
	w.used++
	return 0
}
