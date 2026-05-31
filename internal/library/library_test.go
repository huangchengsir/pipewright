package library

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// testMasterKey 返回确定性测试用 master key。
func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 7)
	}
	return &k
}

// testDB 打开临时 SQLite(含全部迁移)。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB
}

// seedProject 插入一个最小凭据 + 项目,返回 project id(供应用模板/项目存在性)。
func seedProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'p', 'https://example.com/p.git', 'main', ?, ?, ?)`,
		projID, credID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// sampleSpec 返回一个最小合法 spec(恰一个源阶段 + 一个构建阶段)。
func sampleSpec() pipeline.Spec {
	return pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "src", Name: "源", Kind: pipeline.KindSource, Jobs: []pipeline.Job{
			{ID: "j1", Name: "git", Type: "git_source"},
		}},
		{ID: "build", Name: "构建", Kind: pipeline.KindBuild, Jobs: []pipeline.Job{
			{ID: "j2", Name: "compile", Type: "build"},
		}},
	}}
}

// ---- 模板 ----

func TestTemplateCreateApplyProducesValidPipeline(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	pipes := pipeline.New(db)
	svc := NewTemplateService(db, pipes)
	ctx := context.Background()

	tpl, err := svc.Create(ctx, TemplateInput{Name: "go-service", Description: "Go 服务模板", Spec: sampleSpec()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tpl.ID == "" || len(tpl.Spec.Stages) != 2 {
		t.Fatalf("unexpected template: %+v", tpl)
	}

	// 应用模板到项目:产出合法流水线(经 pipeline.Save 再校验/渲染)。
	cfg, err := svc.Apply(ctx, tpl.ID, projID)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(cfg.Spec.Stages) != 2 {
		t.Fatalf("applied pipeline stages = %d, want 2", len(cfg.Spec.Stages))
	}
	if strings.TrimSpace(cfg.YAML) == "" {
		t.Fatalf("applied pipeline should render YAML")
	}
	// 应用后,项目流水线读回与模板一致。
	got, err := pipes.Get(ctx, projID)
	if err != nil {
		t.Fatalf("pipeline Get after apply: %v", err)
	}
	if len(got.Spec.Stages) != 2 {
		t.Fatalf("project pipeline stages = %d, want 2", len(got.Spec.Stages))
	}
}

func TestTemplateCreateRejectsInvalidSpec(t *testing.T) {
	db := testDB(t)
	svc := NewTemplateService(db, pipeline.New(db))
	// 无源阶段 → pipeline.NormalizeSpec 拒绝 → ErrInvalidTemplate。
	_, err := svc.Create(context.Background(), TemplateInput{
		Name: "bad",
		Spec: pipeline.Spec{Stages: []pipeline.Stage{
			{Name: "构建", Kind: pipeline.KindBuild},
		}},
	})
	if err == nil {
		t.Fatalf("expected error for spec without source stage")
	}
	if want := ErrInvalidTemplate; !isWrapped(err, want) {
		t.Fatalf("err = %v, want wrapped %v", err, want)
	}
}

func TestTemplateCreateRejectsEmptyName(t *testing.T) {
	db := testDB(t)
	svc := NewTemplateService(db, pipeline.New(db))
	_, err := svc.Create(context.Background(), TemplateInput{Name: "  ", Spec: sampleSpec()})
	if !isWrapped(err, ErrInvalidTemplate) {
		t.Fatalf("err = %v, want ErrInvalidTemplate", err)
	}
}

func TestTemplateDuplicateName(t *testing.T) {
	db := testDB(t)
	svc := NewTemplateService(db, pipeline.New(db))
	ctx := context.Background()
	if _, err := svc.Create(ctx, TemplateInput{Name: "dup", Spec: sampleSpec()}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := svc.Create(ctx, TemplateInput{Name: "dup", Spec: sampleSpec()})
	if err != ErrTemplateNameTaken {
		t.Fatalf("err = %v, want ErrTemplateNameTaken", err)
	}
}

func TestTemplateGetDeleteNotFound(t *testing.T) {
	db := testDB(t)
	svc := NewTemplateService(db, pipeline.New(db))
	ctx := context.Background()
	if _, err := svc.Get(ctx, "missing"); err != ErrTemplateNotFound {
		t.Fatalf("Get err = %v, want ErrTemplateNotFound", err)
	}
	if err := svc.Delete(ctx, "missing"); err != ErrTemplateNotFound {
		t.Fatalf("Delete err = %v, want ErrTemplateNotFound", err)
	}
}

func TestTemplateListAndDelete(t *testing.T) {
	db := testDB(t)
	svc := NewTemplateService(db, pipeline.New(db))
	ctx := context.Background()
	a, _ := svc.Create(ctx, TemplateInput{Name: "a", Spec: sampleSpec()})
	svc.Create(ctx, TemplateInput{Name: "b", Spec: sampleSpec()})
	list, err := svc.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v (n=%d), err=%v", list, len(list), err)
	}
	// 升序按名称。
	if list[0].Name != "a" || list[1].Name != "b" {
		t.Fatalf("List order = %q,%q want a,b", list[0].Name, list[1].Name)
	}
	if err := svc.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list, _ := svc.List(ctx); len(list) != 1 {
		t.Fatalf("after delete len = %d, want 1", len(list))
	}
}

func TestTemplateApplyProjectNotFound(t *testing.T) {
	db := testDB(t)
	svc := NewTemplateService(db, pipeline.New(db))
	ctx := context.Background()
	tpl, _ := svc.Create(ctx, TemplateInput{Name: "t", Spec: sampleSpec()})
	if _, err := svc.Apply(ctx, tpl.ID, "no-such-project"); err != ErrProjectNotFound {
		t.Fatalf("Apply err = %v, want ErrProjectNotFound", err)
	}
}

// ---- 变量组 ----

func TestVarGroupNormalizationAndSecretRefs(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	cred, err := v.Create(vault.CreateInput{Name: "tok", Type: vault.TypeGitToken, Secret: "super-secret-plaintext"})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	svc := NewVarGroupService(db, v)
	ctx := context.Background()

	g, err := svc.Create(ctx, VarGroupInput{
		Name: "shared",
		Vars: []pipeline.BuildVar{
			{Key: "API_URL", Value: "https://api"},
			{Key: "TOKEN", Secret: true, CredentialID: cred.ID, Value: "leak-me"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 非 secret 保留明文;secret 只留引用 + 掩码,绝无明文。
	var apiVar, tokVar *pipeline.BuildVar
	for i := range g.Vars {
		switch g.Vars[i].Key {
		case "API_URL":
			apiVar = &g.Vars[i]
		case "TOKEN":
			tokVar = &g.Vars[i]
		}
	}
	if apiVar == nil || apiVar.Value != "https://api" {
		t.Fatalf("API_URL not preserved: %+v", apiVar)
	}
	if tokVar == nil {
		t.Fatalf("TOKEN missing")
	}
	if tokVar.Value != "" {
		t.Fatalf("secret var leaked plaintext value %q", tokVar.Value)
	}
	if tokVar.CredentialID != cred.ID {
		t.Fatalf("secret var credentialId = %q, want %q", tokVar.CredentialID, cred.ID)
	}
	if tokVar.MaskedValue == "" {
		t.Fatalf("secret var should carry masked value")
	}

	// 落库的 vars_json 绝不含明文 secret。
	var varsJSON string
	if err := db.QueryRowContext(ctx, `SELECT vars_json FROM variable_groups WHERE id = ?`, g.ID).Scan(&varsJSON); err != nil {
		t.Fatalf("read vars_json: %v", err)
	}
	if strings.Contains(varsJSON, "super-secret-plaintext") || strings.Contains(varsJSON, "leak-me") {
		t.Fatalf("plaintext secret leaked into DB: %s", varsJSON)
	}
}

func TestVarGroupRejectsDuplicateKeys(t *testing.T) {
	db := testDB(t)
	svc := NewVarGroupService(db, vault.New(db, testMasterKey()))
	_, err := svc.Create(context.Background(), VarGroupInput{
		Name: "dupkeys",
		Vars: []pipeline.BuildVar{
			{Key: "K", Value: "1"},
			{Key: "K", Value: "2"},
		},
	})
	if !isWrapped(err, ErrInvalidVar) {
		t.Fatalf("err = %v, want ErrInvalidVar", err)
	}
}

func TestVarGroupRejectsEmptyName(t *testing.T) {
	db := testDB(t)
	svc := NewVarGroupService(db, vault.New(db, testMasterKey()))
	_, err := svc.Create(context.Background(), VarGroupInput{Name: " "})
	if !isWrapped(err, ErrInvalidVarGroup) {
		t.Fatalf("err = %v, want ErrInvalidVarGroup", err)
	}
}

func TestVarGroupSecretRefMissingCredential(t *testing.T) {
	db := testDB(t)
	svc := NewVarGroupService(db, vault.New(db, testMasterKey()))
	_, err := svc.Create(context.Background(), VarGroupInput{
		Name: "g",
		Vars: []pipeline.BuildVar{{Key: "T", Secret: true, CredentialID: "does-not-exist"}},
	})
	if !isWrapped(err, ErrCredentialNotFound) {
		t.Fatalf("err = %v, want ErrCredentialNotFound", err)
	}
}

func TestVarGroupVaultUnconfigured(t *testing.T) {
	db := testDB(t)
	svc := NewVarGroupService(db, vault.New(db, nil)) // 未配置 master key
	_, err := svc.Create(context.Background(), VarGroupInput{
		Name: "g",
		Vars: []pipeline.BuildVar{{Key: "T", Secret: true, CredentialID: "x"}},
	})
	if !isWrapped(err, ErrVaultUnconfigured) {
		t.Fatalf("err = %v, want ErrVaultUnconfigured", err)
	}
}

func TestVarGroupUpdateAndDelete(t *testing.T) {
	db := testDB(t)
	svc := NewVarGroupService(db, vault.New(db, testMasterKey()))
	ctx := context.Background()
	g, err := svc.Create(ctx, VarGroupInput{Name: "g", Vars: []pipeline.BuildVar{{Key: "A", Value: "1"}}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	upd, err := svc.Update(ctx, g.ID, VarGroupInput{Name: "g2", Vars: []pipeline.BuildVar{{Key: "B", Value: "2"}}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Name != "g2" || len(upd.Vars) != 1 || upd.Vars[0].Key != "B" {
		t.Fatalf("unexpected updated group: %+v", upd)
	}
	if err := svc.Delete(ctx, g.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, g.ID); err != ErrVarGroupNotFound {
		t.Fatalf("Get after delete err = %v, want ErrVarGroupNotFound", err)
	}
}

func TestVarGroupUpdateNotFound(t *testing.T) {
	db := testDB(t)
	svc := NewVarGroupService(db, vault.New(db, testMasterKey()))
	_, err := svc.Update(context.Background(), "missing", VarGroupInput{Name: "x"})
	if err != ErrVarGroupNotFound {
		t.Fatalf("err = %v, want ErrVarGroupNotFound", err)
	}
}

func TestVarGroupDuplicateName(t *testing.T) {
	db := testDB(t)
	svc := NewVarGroupService(db, vault.New(db, testMasterKey()))
	ctx := context.Background()
	if _, err := svc.Create(ctx, VarGroupInput{Name: "dup"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := svc.Create(ctx, VarGroupInput{Name: "dup"}); err != ErrVarGroupNameTaken {
		t.Fatalf("err = %v, want ErrVarGroupNameTaken", err)
	}
}

// isWrapped 判定 err 链上是否含 target(errors.Is 包装链)。
func isWrapped(err, target error) bool {
	return errors.Is(err, target)
}
