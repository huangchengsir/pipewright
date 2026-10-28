package runretry

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReviewUnstartedSkipNeverClaimsCleanupOrOverwritesEffects(t *testing.T) {
	for _, mode := range []string{"pending", "begun", "effectful", "begin-race", "budget"} {
		t.Run(mode, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				f := newFixture(t, st)
				ctx := WithExecution(context.Background(), f.ex)
				node, ok := f.ex.ControlNode("stage", "cleanup")
				if !ok {
					t.Fatal("missing cleanup")
				}
				if mode == "begun" {
					if err := f.ex.Begin(ctx, node); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "effectful" {
					if _, err := f.db.Exec("INSERT INTO run_retry_resources(id,root_id,attempt_id,node_key,state,ciphertext,updated_at) VALUES('effect','root','root',?,'allocated',X'00',0)", node.Key); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "budget" {
					if _, err := f.db.Exec("UPDATE run_retry_lineages SET checkpoint_bytes=? WHERE root_id='root'", 31<<20); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if mode == "begin-race" {
					start := make(chan struct{})
					begun := make(chan error, 1)
					skipped := make(chan error, 1)
					go func() { <-start; begun <- f.ex.Begin(ctx, node) }()
					go func() { <-start; skipped <- RecordUnstartedSkip(ctx, node, "condition_skipped") }()
					close(start)
					first, second := <-begun, <-skipped
					if (first == nil) == (second == nil) {
						t.Fatal("CAS admitted both/neither owners", first, second)
					}
					err = second
				} else {
					err = RecordUnstartedSkip(ctx, node, "condition_skipped")
				}
				var status, owner string
				if e := f.db.QueryRow("SELECT status,owner_epoch FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", node.Key).Scan(&status, &owner); e != nil {
					t.Fatal(e)
				}
				switch mode {
				case "budget":
					if !errors.Is(err, ErrCheckpoint) || status != "skipped" || owner != "" {
						t.Fatal("checkpoint warning rolled back the known skip", status, owner, err)
					}
				case "pending":
					if err != nil || status != "skipped" || owner != "" {
						t.Fatal(status, owner, err)
					}
					if drained, e := f.s.ResourcesDrained(ctx, "root"); e != nil || !drained {
						t.Fatal(drained, e)
					}
				case "begun":
					if !errors.Is(err, ErrConflict) || status != "running" || owner == "" {
						t.Fatal(status, owner, err)
					}
				case "effectful":
					if !errors.Is(err, ErrUnknown) || status != "pending" || owner != "" {
						t.Fatal(status, owner, err)
					}
				case "begin-race":
					if err == nil {
						if status != "skipped" || owner != "" {
							t.Fatal(status, owner)
						}
					} else if status != "running" || owner == "" {
						t.Fatal(status, owner, err)
					}
				}
			})
		})
	}
}
