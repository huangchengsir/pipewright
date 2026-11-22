package retention

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/productrelease"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestPruneKeepsOnlyUnsettledProductReleaseEvidence(t *testing.T) {
	ctx := t.Context()
	db := storetest.OpenDB(t)
	svc := NewService(db)
	project := seedProject(t, db)
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	runID := insertRun(t, db, project, "success", now.AddDate(0, 0, -60))
	other := insertRun(t, db, project, "success", now.AddDate(0, 0, -60))
	product, ref, batch := uuid.NewString(), uuid.NewString(), uuid.NewString()
	plan := productrelease.Plan{ProductID: product, Items: []productrelease.ItemPlan{{
		ApplicationID: project, CommitSHA: strings.Repeat("a", 40), Environment: "staging", TargetIDs: []string{"server"},
		ConfigDigest: strings.Repeat("b", 64), SnapshotRef: ref,
	}, {
		ApplicationID: uuid.NewString(), CommitSHA: strings.Repeat("c", 40), Environment: "staging", TargetIDs: []string{"server"},
		ConfigDigest: strings.Repeat("d", 64), SnapshotRef: uuid.NewString(),
	}}}
	raw, _ := json.Marshal(plan)
	sum := sha256.Sum256(raw)
	items, _ := json.Marshal([]productrelease.Item{{State: "dispatching", ApplicationID: project}, {State: "pending", ApplicationID: plan.Items[1].ApplicationID}})
	mustExec(t, db, `INSERT INTO product_release_batches(id,tenant_id,actor_id,product_id,request_id,fingerprint,revision,plan_json,items_json,created_at,updated_at) VALUES(?,?,?,?,?,?,1,?,?,?,?)`,
		batch, "__single__", "instance-owner", product, ref, hex.EncodeToString(sum[:]), string(raw), string(items), now.UnixMilli(), now.UnixMilli())
	// Simulate process loss before the acknowledgment recorded the item's run ID.
	mustExec(t, db, `INSERT INTO mcp_run_requests(scope,request_id,fingerprint,run_id) VALUES(?,?,?,?)`, "product-release:"+batch, "item:0", productrelease.ItemDigest(plan.Items[0]), runID)
	mustExec(t, db, `INSERT INTO mcp_run_requests(scope,request_id,fingerprint,run_id) VALUES(?,?,?,?)`, "product-release:"+batch, "item:1", productrelease.ItemDigest(plan.Items[0]), other)
	if err := svc.SetConfig(ctx, Config{Enabled: true, MaxAgeDays: 1}); err != nil {
		t.Fatal(err)
	}
	if count, err := svc.Prune(ctx, now); err != nil || count != 1 || runExists(t, db, other) || !runExists(t, db, runID) {
		t.Fatal("wrong batch position retained or unresolved run removed", count, err)
	}
	items, _ = json.Marshal([]productrelease.Item{{State: "succeeded", ApplicationID: project, RunID: runID}, {State: "pending", ApplicationID: plan.Items[1].ApplicationID}})
	mustExec(t, db, `UPDATE product_release_batches SET items_json=? WHERE id=?`, string(items), batch)
	if count, err := svc.Prune(ctx, now); err != nil || count != 0 || !runExists(t, db, runID) {
		t.Fatal("settled first run must survive while second item remains actionable", count, err)
	}
	items, _ = json.Marshal([]productrelease.Item{{State: "succeeded", ApplicationID: project, RunID: runID}, {State: "succeeded", ApplicationID: plan.Items[1].ApplicationID}})
	mustExec(t, db, `UPDATE product_release_batches SET items_json=?,revision=revision+1 WHERE id=?`, string(items), batch)
	if count, err := svc.Prune(ctx, now); err != nil || count != 1 || runExists(t, db, runID) {
		t.Fatal("settled run held indefinitely", count, err)
	}
}
