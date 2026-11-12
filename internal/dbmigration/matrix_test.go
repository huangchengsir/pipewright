package dbmigration

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// Each remote invocation may select one exact pair. Missing DSNs retain the
// storetest skip contract; the acceptance runner rejects skips for selected pairs.
func TestAllDatabaseMigrationDirections(t *testing.T) {
	sourceFilter := os.Getenv("PIPEWRIGHT_TEST_MIGRATION_SOURCE")
	targetFilter := os.Getenv("PIPEWRIGHT_TEST_MIGRATION_TARGET")
	engines := []store.Dialect{store.SQLite, store.MySQL, store.PostgreSQL, store.DM, store.SQLServer}
	selected := 0
	for _, source := range engines {
		for _, target := range engines {
			if source == target || sourceFilter != "" && sourceFilter != source.String() || targetFilter != "" && targetFilter != target.String() {
				continue
			}
			selected++
			t.Run(source.String()+"_to_"+target.String(), func(t *testing.T) {
				deepMigration(t, storetest.OpenForDialect(t, source), storetest.OpenEmptyForDialect(t, target))
			})
		}
	}
	if selected == 0 {
		t.Fatal("migration filter selected no supported distinct engine pair")
	}
}
func TestDeepFixtureSQLiteCopy(t *testing.T) { deepMigration(t, database(t, true), database(t, false)) }

func deepMigration(t *testing.T, source, dest *store.Store) {
	t.Helper()
	ctx := context.Background()
	key := [32]byte{0x32, 0x51, 0x19}
	const stamp = "2026-11-11T03:04:05Z"
	sourceVault := vault.New(source.DB, &key)
	seal := func(plain []byte) []byte {
		t.Helper()
		cipher, e := sourceVault.SealSecret(plain)
		if e != nil {
			t.Fatal(e)
		}
		return cipher
	}
	credentialPlain := []byte("private fixture token 中文😀\x00\xff")
	credentialCipher := seal(credentialPlain)
	insert := func(name string, fields map[string]any) {
		t.Helper()
		var table Table
		for _, candidate := range Manifest() {
			if candidate.Name == name {
				table = candidate
				break
			}
		}
		if table.Name == "" {
			t.Fatal("unknown fixture table", name)
		}
		var columns, marks []string
		var args []any
		for _, col := range table.Columns {
			if value, ok := fields[col.Name]; ok {
				columns = append(columns, store.Identifier(source.Dialect, col.Name))
				mark := "?"
				if col.Kind == Binary {
					mark = store.BinaryPlaceholder(source.Dialect)
				}
				marks = append(marks, mark)
				args = append(args, value)
			}
		}
		if len(columns) != len(fields) {
			t.Fatal("unknown fixture column", name)
		}
		if _, e := source.DB.Exec("INSERT INTO "+store.Identifier(source.Dialect, name)+" ("+strings.Join(columns, ",")+") VALUES ("+strings.Join(marks, ",")+")", args...); e != nil {
			t.Fatal("fixture insert", name, e)
		}
	}
	insert("credentials", map[string]any{"id": "credential", "name": "凭据😀  ", "type": "git_token", "scope": "", "ciphertext": credentialCipher, "masked_value": "***", "created_at": stamp, "updated_at": stamp, "username": "用户😀  "})
	insert("projects", map[string]any{"id": "project", "name": "迁移项目😀  ", "repo_url": "https://invalid.example/repo", "default_branch": "main", "credential_id": "credential", "created_at": stamp, "updated_at": stamp})
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "build", Name: "构建😀", Kind: "build", Jobs: []pipeline.Job{{ID: "compile", Name: "编译", Type: "script"}, {ID: "package", Name: "打包", Type: "script", Needs: []string{"compile"}}}}}}
	specJSON, e := json.Marshal(spec)
	if e != nil {
		t.Fatal(e)
	}
	insert("pipeline_configs", map[string]any{"project_id": "project", "spec_json": string(specJSON), "spec_yaml": "# preserved YAML\n", "status": "ready", "created_at": stamp, "updated_at": stamp, "saved_at": nil})
	insert("pipeline_runs", map[string]any{"id": "run", "project_id": "project", "status": "failed", "trigger_type": "manual", "trigger_branch": "main", "created_at": stamp, "started_at": stamp, "finished_at": stamp, "params_json": `{"empty":"","unicode":"😀"}`})
	insert("run_steps", map[string]any{"id": "step", "run_id": "run", "name": "构建😀  ", "status": "failed", "ordinal": 0, "started_at": stamp, "finished_at": nil})
	insert("run_logs", map[string]any{"id": "log", "run_id": "run", "seq": int64(math.MaxInt64), "ts": stamp, "stream": "stdout", "step_ordinal": int64(math.MinInt64), "text": "日志😀 Éé 空格  "})
	insert("run_logs", map[string]any{"id": "empty-log", "run_id": "run", "seq": int64(0), "ts": stamp, "stream": "stderr", "step_ordinal": int64(-1), "text": ""})
	paths := RuntimePaths{DataDir: t.TempDir()}
	paths.Artifacts = filepath.Join(paths.DataDir, "artifacts")
	paths.Repositories = filepath.Join(paths.DataDir, "repos")
	paths.Cache = filepath.Join(paths.DataDir, "cache")
	artifacts, e := artifactstore.New(paths.Artifacts)
	if e != nil {
		t.Fatal(e)
	}
	artifactBytes := []byte{0, 255, 128, 42, 10}
	artifactKey, size, e := artifacts.Put(bytes.NewReader(artifactBytes))
	if e != nil {
		t.Fatal(e)
	}
	insert("run_artifacts", map[string]any{"id": "artifact", "run_id": "run", "type": "archive", "name": "文件😀  ", "reference": artifactKey, "size_bytes": size, "metadata_json": `{"stored":true}`, "created_at": stamp})
	// Create authentic encrypted retry lineage/attempt/node snapshots through the
	// domain service, then settle the historical attempt before freezing it.
	retry := runretry.New(source.DB, sourceVault)
	snapshot := runretry.Snapshot{SchemaVersion: runretry.SchemaVersion, ProjectID: "project", Spec: spec, RequestedRef: "main", CommitSHA: strings.Repeat("a", 40), Trigger: runretry.Trigger{Type: "manual", Branch: "main", Actor: "admin"}}
	if e = retry.SaveManifest(ctx, "run", snapshot); e != nil {
		t.Fatal("retry fixture", e)
	}
	if e = retry.Complete(ctx, "run", "failed"); e != nil {
		t.Fatal(e)
	}
	// Binary collations for these ledger keys must retain case-distinct identities.
	for _, id := range []string{"DraftCase", "draftcase"} {
		insert("mcp_config_drafts", map[string]any{"id": id, "owner_token_id": "owner", "project_id": "project", "request_id": id, "input_digest": strings.Repeat("a", 64), "base_revision": int64(1), "policy_version": "v1", "validator_version": "v1", "authz_revision": int64(math.MaxInt64), "operations_json": "[]", "patch_digest": strings.Repeat("b", 64), "created_at": int64(0), "expires_at": int64(math.MaxInt64), "applied_operation_id": nil})
	}
	insert("oauth_apps", map[string]any{"provider": "github", "client_id": "fixture-client", "client_secret_ciphertext": seal([]byte("fixture-client-secret")), "base_url": "", "enabled": 0, "created_at": stamp, "updated_at": stamp})
	insert("oauth_apps", map[string]any{"provider": "gitee", "client_id": "public-client", "client_secret_ciphertext": nil, "base_url": "", "enabled": 0, "created_at": stamp, "updated_at": stamp})
	insert("oauth_credentials", map[string]any{"credential_id": "credential", "provider": "github", "issuer": "https://github.com", "client_id": "fixture-client", "redirect_uri": "https://invalid.example/callback", "app_revision": "fixture", "account_id": "123", "login": "fixture-user", "refresh_ciphertext": seal([]byte("fixture-refresh")), "expires_at": nil, "refresh_expires_at": int64(math.MaxInt64), "refresh_after": int64(0), "revision": int64(1), "state": "ready", "claim_id": "", "claim_owner": "", "updated_at": int64(0)})
	admin := auth.NewService(source.DB, nil)
	if e = admin.Bootstrap("admin", "fixture-admin-password"); e != nil {
		t.Fatal(e)
	}
	session, e := admin.Login("admin", "fixture-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	insert("audit_log", map[string]any{"id": "audit", "timestamp": stamp, "actor": "admin", "action": "fixture", "target_type": "project", "target_id": "project", "detail_json": `{"unicode":"😀","empty":""}`, "ip": nil, "created_at": stamp})
	if e = VerifySecrets(ctx, source, &key); e != nil {
		t.Fatal("source secret verification", e)
	}
	if e = VerifyLocalResources(ctx, source, &key, paths); e != nil {
		t.Fatal("source artifact verification", e)
	}
	target, e := InitializeTarget(ctx, dest, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	report, e := CopySnapshot(ctx, source, target)
	if e != nil {
		t.Fatal(e)
	}
	verified, e := VerifySnapshot(ctx, source, target)
	if e != nil || reportDigest(report) != reportDigest(verified) {
		t.Fatal("copy verification", e)
	}
	if e = VerifySecrets(ctx, dest, &key); e != nil {
		t.Fatal("target secret verification", e)
	}
	if e = VerifyLocalResources(ctx, dest, &key, paths); e != nil {
		t.Fatal("target artifact verification", e)
	}
	if e = auth.NewService(dest.DB, nil).VerifyRecoveryPassword(ctx, "admin", "fixture-admin-password"); e != nil {
		t.Fatal("administrator authentication after copy", e)
	}
	if _, e = runretry.New(dest.DB, vault.New(dest.DB, &key)).Context(ctx, "run"); e != nil {
		t.Fatal("retry context after copy", e)
	}
	var cipher []byte
	var username string
	if e = dest.DB.QueryRow("SELECT ciphertext,username FROM credentials WHERE id=?", "credential").Scan(&cipher, &username); e != nil || !bytes.Equal(cipher, credentialCipher) || username != "用户😀  " {
		t.Fatal("credential bytes/trailing spaces", e)
	}
	plain, e := vault.New(dest.DB, &key).OpenSecret(cipher)
	if e != nil || !bytes.Equal(plain, credentialPlain) {
		t.Fatal("credential decrypt after copy", e)
	}
	clear(plain)
	var seq, ordinal int64
	var text string
	if e = dest.DB.QueryRow("SELECT seq,step_ordinal,text FROM run_logs WHERE id=?", "log").Scan(&seq, &ordinal, &text); e != nil || seq != math.MaxInt64 || ordinal != math.MinInt64 || text != "日志😀 Éé 空格  " {
		t.Fatal("64-bit/non-BMP/trailing text", e)
	}
	var count int
	if e = dest.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE token=?", session.Token).Scan(&count); e != nil || count != 1 {
		t.Fatal("session preservation", e)
	}
	if e = dest.DB.QueryRow("SELECT COUNT(*) FROM mcp_config_drafts").Scan(&count); e != nil || count != 2 {
		t.Fatal("case-distinct ledger keys", e)
	}
	if _, e = dest.DB.Exec("UPDATE audit_log SET actor='changed' WHERE id='audit'"); e == nil {
		t.Fatal("audit became mutable")
	}
	populated := 0
	for _, proof := range report.Tables {
		if proof.Rows > 0 {
			populated++
		}
	}
	t.Logf("verified %d business tables, %d populated: deep run/log/artifact/retry/config/session/OAuth fixtures; actual master-key decrypt and CAS file hashes; no runtime process restart claimed", len(report.Tables), populated)
}
