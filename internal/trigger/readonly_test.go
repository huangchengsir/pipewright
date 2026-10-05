package trigger

import (
	"context"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestResolveExistingEnvNoDefaultsAndFirstMatch(t *testing.T) {
	storetest.SkipIfMySQL(t)
	db := storetest.OpenDB(t)
	id := seedProject(t, db)
	ctx := context.Background()
	if env, _, err := ResolveExistingEnv(ctx, db, id, "main"); env != "" || err != nil {
		t.Fatalf("missing: %s %v", env, err)
	}
	const stamp = "2026-10-05T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO pipeline_triggers (project_id,webhook_token,webhook_secret_ciphertext,branch_mappings_json,created_at,updated_at) VALUES (?, 'token', X'00', '[{"branchPattern":"main","environment":"first","targetServerIds":["server"]},{"branchPattern":"*","environment":"second"}]', ?, ?)`, id, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	env, servers, err := ResolveExistingEnv(ctx, db, id, "main")
	if err != nil || env != "first" || len(servers) != 1 || servers[0] != "server" {
		t.Fatalf("first match: %s %v %v", env, servers, err)
	}
	if _, err := db.Exec(`PRAGMA query_only=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE pipeline_triggers SET branch_mappings_json='{' WHERE project_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveExistingEnv(ctx, db, id, "main"); err == nil {
		t.Fatal("corrupt mapping hidden")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveExistingEnv(ctx, db, id, "main"); err == nil {
		t.Fatal("database failure hidden")
	}
}
