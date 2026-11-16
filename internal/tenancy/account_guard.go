package tenancy

import (
	"github.com/huangchengsir/pipewright/internal/auth"
	"sync"
	"time"
)

// Counts belong to complete immutable account IDs, never shared hash buckets.
// Idle entries expire after one hour; live/recent entries are never evicted to
// make room for attacker-selected keys. Saturation fails closed for new keys.
type accountGuards struct {
	mu      sync.Mutex
	entries map[string]*accountGuardEntry
}
type accountGuardEntry struct {
	manager *auth.LockoutManager
	last    time.Time
	refs    int
}

func (g *accountGuards) acquire(key string) (*auth.LockoutManager, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if g.entries == nil {
		g.entries = map[string]*accountGuardEntry{}
	}
	e := g.entries[key]
	if e == nil {
		if len(g.entries) >= 4096 {
			for k, v := range g.entries {
				if v.refs == 0 && now.Sub(v.last) > time.Hour {
					delete(g.entries, k)
				}
			}
		}
		if len(g.entries) >= 4096 {
			return nil, func() {}, &auth.ErrLockedOut{RetryAfter: time.Minute}
		}
		e = &accountGuardEntry{manager: auth.NewLockoutManager(nil)}
		g.entries[key] = e
	}
	e.last = now
	e.refs++
	return e.manager, func() { g.mu.Lock(); e.refs--; e.last = time.Now(); g.mu.Unlock() }, nil
}
