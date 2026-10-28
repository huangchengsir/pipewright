package main

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"log"
	"time"
)

// A reserved hook means at-most-once attempted work, never confirmed delivery.
// Root-scoped success hooks cannot be replayed by a linked recovery attempt.
func retryTerminalOnce(ledger *runretry.Service, key string, perAttempt, successOnly bool, hook func(context.Context, string, string)) func(context.Context, string, string) {
	return func(ctx context.Context, id, status string) {
		if hook == nil || successOnly && status != run.StatusSuccess {
			return
		}
		if ledger == nil {
			hook(ctx, id, status)
			return
		}
		eventKey := key
		if perAttempt {
			eventKey += ":" + id
		}
		bounded, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		claimed, err := ledger.ClaimEffect(bounded, id, eventKey)
		if err != nil {
			log.Printf("[runretry] run %s: terminal event reservation unavailable", id)
			return
		}
		if !claimed {
			return
		}
		defer func() {
			finish, stop := context.WithTimeout(context.WithoutCancel(bounded), 3*time.Second)
			defer stop()
			if ledger.FinishEffect(finish, id, eventKey) != nil {
				log.Printf("[runretry] run %s: terminal event attempt evidence unavailable", id)
			}
		}()
		hook(bounded, id, status)
	}
}
