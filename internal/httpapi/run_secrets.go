package httpapi

import (
	"context"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// RunSecretSource 据一次运行解析它**真实用到的所有凭据**,把明文登记进一个 Masker,
// 供日志(3-6)/诊断(7-2)/反馈(7-5)/通知(5-3)出网或落库前脱敏(AC-SEC-04)。
//
// 这是「registerRunSecrets 只登记桩假 secret」红线债的真修(原先真实凭据未登记 → 3-3 真实
// 构建落地即明文泄漏)。凭据来源:① 项目主凭据(git token)② 流水线 settings 的 secret 构建
// 变量 + 环境变量 ③ 镜像仓库凭据。明文经 vault.Reveal 即取即用(不刷新 last_used_at,脱敏登记
// 不算「使用」),登记进 Masker 后仅用于子串替换;Masker 不持久化、不出网。
//
// 同时登记桩 runner 的合成假 secret(run.StubFailureSecret),保证 dev/测试的脱敏链路仍有效。
type RunSecretSource struct {
	runs     run.Service
	projects project.Service
	settings pipeline.SettingsService
	vault    vault.Vault
}

// NewRunSecretSource 构造解析器(任一依赖为 nil 时对应来源跳过,降级为只登记桩 secret)。
func NewRunSecretSource(runs run.Service, projects project.Service, settings pipeline.SettingsService, v vault.Vault) *RunSecretSource {
	return &RunSecretSource{runs: runs, projects: projects, settings: settings, vault: v}
}

// MaskerForRun 返回一个登记了该 run 全部真实凭据明文(+ 桩假 secret)的 Masker。
// 任何环节失败均降级(best-effort:能登记多少登记多少),绝不 panic、绝不阻断调用方。
func (s *RunSecretSource) MaskerForRun(ctx context.Context, runID string) *mask.Masker {
	m := mask.NewMasker()
	m.RegisterSecret(run.StubFailureSecret) // 桩 runner 合成日志的假 secret(dev/测试链路)
	if s == nil || s.runs == nil {
		return m
	}
	rn, err := s.runs.Get(ctx, runID)
	if err != nil || rn == nil || rn.ProjectID == "" {
		return m
	}
	s.registerProjectSecrets(ctx, m, rn.ProjectID)
	return m
}

// registerProjectSecrets 把某项目相关的所有 secret 凭据明文登记进 Masker(best-effort)。
func (s *RunSecretSource) registerProjectSecrets(ctx context.Context, m *mask.Masker, projectID string) {
	if s.vault == nil {
		return
	}
	reveal := func(credID string) {
		if credID == "" {
			return
		}
		// Reveal:解密取明文但不刷新 last_used_at(脱敏登记非「使用」)。失败静默跳过。
		if pt, err := s.vault.Reveal(credID); err == nil && pt != "" {
			m.RegisterSecret(pt)
		}
	}

	// ① 项目主凭据(git token 等)。
	if s.projects != nil {
		if proj, err := s.projects.Get(ctx, projectID); err == nil && proj != nil {
			reveal(proj.CredentialID)
		}
	}

	// ② 流水线 settings(2-4)的 secret 引用:构建变量 + 各环境变量 + 镜像仓库凭据。
	if s.settings != nil {
		if st, err := s.settings.Get(ctx, projectID); err == nil && st != nil {
			for _, v := range st.Build.Vars {
				if v.Secret {
					reveal(v.CredentialID)
				}
			}
			for _, e := range st.Environments {
				for _, v := range e.EnvVars {
					if v.Secret {
						reveal(v.CredentialID)
					}
				}
				reveal(e.ImageRegistry.CredentialID)
			}
			// ③ 自定义脚本步骤(Epic 8)的 secret env 引用。
			for _, step := range st.Steps {
				for _, v := range step.Env {
					if v.Secret {
						reveal(v.CredentialID)
					}
				}
			}
		}
	}
}
