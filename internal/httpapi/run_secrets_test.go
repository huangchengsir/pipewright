package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// TestRunSecretSourceMasksRealCredential 验证**红线修**:MaskerForRun 登记的是该 run 真实用到的
// 凭据明文(从 vault 解密的项目主凭据),而非仅桩假 secret。这是「registerRunSecrets 只登记
// StubFailureSecret」债的真修——3-3 真实构建落地后,真实日志/诊断/反馈/通知里的真凭据也 [MASKED]。
func TestRunSecretSourceMasksRealCredential(t *testing.T) {
	st := testStoreAuth(t)
	v := vault.New(st.DB, testMasterKey())
	const realSecret = "ghp_REALtoken_should_be_masked_9z"
	cred, err := v.Create(vault.CreateInput{Name: "git", Type: vault.TypeGitToken, Secret: realSecret})
	if err != nil {
		t.Fatalf("create cred: %v", err)
	}

	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	proj, err := psvc.Create(context.Background(), project.CreateInput{
		Name: "acme", RepoURL: "https://gitee.com/acme/shop.git", CredentialID: cred.ID, DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	rsvc := run.New(st.DB)
	rn, err := rsvc.Create(context.Background(), proj.ID, run.Trigger{Type: "manual", Branch: "main", Actor: "admin"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	src := NewRunSecretSource(rsvc, psvc, pipeline.NewSettingsService(st.DB, v), v)
	m := src.MaskerForRun(context.Background(), rn.ID)

	// 真实凭据明文出现在(模拟的)失败日志里 → 必须被脱敏。
	scrubbed := m.Scrub("fatal: Authentication failed: token=" + realSecret)
	if strings.Contains(scrubbed, realSecret) {
		t.Fatalf("真实凭据未脱敏(红线债未修): %q", scrubbed)
	}
	if !strings.Contains(scrubbed, "[MASKED]") {
		t.Fatalf("脱敏后应含 [MASKED]: %q", scrubbed)
	}

	// 桩 secret 仍登记(向后兼容,既有测试不破)。
	if strings.Contains(m.Scrub(run.StubFailureSecret), run.StubFailureSecret) {
		t.Fatal("桩 secret 应仍被脱敏")
	}

	// secretSrc 为 nil 时降级:只脱敏桩 secret,不脱敏真实凭据(证明真修来自 source 的解析)。
	mNil := maskerFor(context.Background(), nil, rn.ID)
	if !strings.Contains(mNil.Scrub(run.StubFailureSecret), "[MASKED]") {
		t.Fatal("nil 降级仍应脱敏桩 secret")
	}
	if !strings.Contains(mNil.Scrub(realSecret), realSecret) {
		t.Fatal("nil 降级不应脱敏真实凭据(对照:证明真修来自 source)")
	}
}

func TestRunSecretSourceMasksSelectedEnvironmentVariable(t *testing.T) {
	st := testStoreAuth(t)
	v := vault.New(st.DB, testMasterKey())
	const secret = "environment_secret_must_not_appear_in_logs"
	cred, err := v.Create(vault.CreateInput{Name: "environment", Type: vault.TypeGitToken, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gitCred, err := v.Create(vault.CreateInput{Name: "source", Type: vault.TypeGitToken, Secret: "unrelated_source_credential"})
	if err != nil {
		t.Fatal(err)
	}
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	proj, err := psvc.Create(ctx, project.CreateInput{Name: "env-mask", RepoURL: "https://gitee.com/acme/env.git", CredentialID: gitCred.ID, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	settings := pipeline.NewSettingsService(st.DB, v)
	_, err = settings.Save(ctx, proj.ID, pipeline.SettingsInput{
		Build:        pipeline.DefaultSettings().Build,
		Environments: []pipeline.Environment{{Name: "prod", EnvVars: []pipeline.BuildVar{{Key: "TOKEN", Secret: true, CredentialID: cred.ID}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The unrelated project credential cannot mask this environment-only secret.
	rsvc := run.New(st.DB)
	rn, err := rsvc.Create(ctx, proj.ID, run.Trigger{Type: "manual", Branch: "main", Actor: "admin", ResolvedEnvironment: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	m := NewRunSecretSource(rsvc, psvc, settings, v).MaskerForRun(ctx, rn.ID)
	masked := m.Scrub("TOKEN=" + secret)
	if strings.Contains(masked, secret) || !strings.Contains(masked, "[MASKED]") {
		t.Fatal("environment variable was not masked")
	}
	if _, err = rsvc.AppendLog(ctx, rn.ID, "stdout", 0, masked); err != nil {
		t.Fatal(err)
	}
	logs, err := rsvc.GetLogs(ctx, rn.ID, 0)
	if err != nil || len(logs) != 1 || strings.Contains(logs[0].Text, secret) {
		t.Fatal("persisted environment log was not masked")
	}
}
