package maintenance

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReviewCompletedMaintenanceReleaseRetriesBeforeFence(t *testing.T) {
	storetest.SkipIfMySQL(t)
	st := storetest.Open(t)
	ctx := context.Background()
	done, e := Enter(ctx, st.DB, "returned_operation")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec("CREATE TRIGGER fail_completed_release BEFORE DELETE ON selfupdate_leases BEGIN SELECT RAISE(ABORT,'synthetic cleanup SQL failure'); END"); e != nil {
		t.Fatal(e)
	}
	done()
	var n int
	if e = st.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_leases").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if e = Fence(ctx, st.DB, "install"); !errors.Is(e, ErrBlocked) {
		t.Fatal("failed accounting was treated as cleared", e)
	}
	if _, e = st.DB.Exec("DROP TRIGGER fail_completed_release"); e != nil {
		t.Fatal(e)
	}
	if e = Fence(ctx, st.DB, "install"); e != nil {
		t.Fatalf("returned local operation remained permanently blocked after DB recovery: %v", e)
	}
}
func TestReviewMaintenanceRetryDoesNotClearReplacedOrForeignReference(t *testing.T) {
	storetest.SkipIfMySQL(t)
	st := storetest.Open(t)
	ctx := context.Background()
	done, e := Enter(ctx, st.DB, "original_local_scope")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec("CREATE TRIGGER fail_release_identity BEFORE DELETE ON selfupdate_leases BEGIN SELECT RAISE(ABORT,'synthetic cleanup SQL failure'); END"); e != nil {
		t.Fatal(e)
	}
	done()
	if _, e = st.DB.Exec("UPDATE selfupdate_leases SET kind='foreign_reference',created_at=0"); e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec("DROP TRIGGER fail_release_identity"); e != nil {
		t.Fatal(e)
	}
	if e = Fence(ctx, st.DB, "install"); !errors.Is(e, ErrBlocked) {
		t.Fatal("new/foreign row deleted using old completion evidence", e)
	}
	var n int
	if e = st.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_leases WHERE kind='foreign_reference'").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}

func TestReviewForeignOAuthUseBlocksFenceWithoutLocalCompletion(t *testing.T) {
	st := storetest.Open(t)
	if _, e := st.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('oauth-fence-c','test','git_token','',X'00','test','old','old')"); e != nil {
		t.Fatal(e)
	}
	if _, e := st.DB.Exec("INSERT INTO oauth_credential_uses(id,credential_id,generation,owner,created_at) VALUES('oauth-fence-use','oauth-fence-c',1,'unknown-other-process',0)"); e != nil {
		t.Fatal(e)
	}
	e := Fence(context.Background(), st.DB, "install")
	var blocked *BlockedError
	if !errors.As(e, &blocked) || len(blocked.Blockers) != 1 || blocked.Blockers[0].Kind != "admitted_work" || blocked.Blockers[0].Count != 1 {
		t.Fatal("unknown persisted OAuth use did not hold installation", e)
	}
	var n int
	if e = st.DB.QueryRow("SELECT COUNT(*) FROM oauth_credential_uses WHERE owner='unknown-other-process'").Scan(&n); e != nil || n != 1 {
		t.Fatal("absence of local evidence cleared a foreign reference", n, e)
	}
}
