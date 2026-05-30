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
