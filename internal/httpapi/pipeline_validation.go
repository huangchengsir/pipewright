package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/pipeline"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/trigger"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// ---- 校验结果 DTO(冻结契约;camelCase;外层 {ready, issues:[{severity,code,scope,field,message}]} 定死) ----

type issueDTO struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Scope    string `json:"scope"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}

type validationDTO struct {
	Ready  bool       `json:"ready"`
	Issues []issueDTO `json:"issues"`
}

// toValidationDTO 把领域 Issue 列表转契约 DTO(切片保证非 nil → JSON 输出 [])。
func toValidationDTO(issues []pipeline.Issue) validationDTO {
	out := make([]issueDTO, 0, len(issues))
	for _, is := range issues {
		out = append(out, issueDTO{
			Severity: is.Severity,
			Code:     is.Code,
			Scope:    is.Scope,
			Field:    is.Field,
			Message:  is.Message,
		})
	}
	return validationDTO{Ready: pipeline.Ready(issues), Issues: out}
}

// makeGetPipelineValidationHandler 返回 GET /api/projects/{id}/pipeline/validation handler(只读 + 认证)。
// 聚合 spec(2-2)+ settings(2-4)+ trigger(2-3)+ 项目凭据,对每个 secret credentialId 经
// vault.Exists 预判存在性(纯函数不触 DB)→ 组装 ValidationInput → pipeline.Validate → DTO。
// 项目不存在 → 404 project_not_found;任一服务未初始化 → 503。
func makeGetPipelineValidationHandler(
	pl pipeline.Service,
	ps pipeline.SettingsService,
	tr trigger.Service,
	pr project.Service,
	v vault.Vault,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pl == nil || ps == nil || tr == nil || pr == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "校验所需服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		// 流水线 spec(惰性默认;也完成项目存在性校验)。
		cfg, err := pl.Get(ctx, id)
		if err != nil {
			writeValidationError(w, err)
			return
		}
		// 构建/部署配置。
		st, err := ps.Get(ctx, id)
		if err != nil {
			if isVaultUnconfiguredErr(err) {
				writeJSON(w, http.StatusOK, toValidationDTO(degradedVaultIssues()))
				return
			}
			writeValidationError(w, err)
			return
		}
		// 触发配置(密钥仅掩码;此处只读 events/分支映射)。
		tcfg, err := tr.Get(ctx, id)
		if err != nil {
			if isVaultUnconfiguredErr(err) {
				writeJSON(w, http.StatusOK, toValidationDTO(degradedVaultIssues()))
				return
			}
			writeValidationError(w, err)
			return
		}
		// 项目主凭据存在性。
		proj, err := pr.Get(ctx, id)
		if err != nil {
			writeValidationError(w, err)
			return
		}

		in := buildValidationInput(cfg, st, tcfg, proj, v)
		writeJSON(w, http.StatusOK, toValidationDTO(pipeline.Validate(in)))
	}
}

// buildValidationInput 把四服务读出的领域模型 + vault 存在性预判组装为中性 ValidationInput。
// secret 引用经 vault.Exists 预判存在性塞入(纯函数据此判 credential_missing,不触 DB)。
func buildValidationInput(
	cfg *pipeline.Config,
	st *pipeline.Settings,
	tcfg *trigger.Config,
	proj *project.Project,
	v vault.Vault,
) pipeline.ValidationInput {
	in := pipeline.ValidationInput{
		Stages: cfg.Spec.Stages,
		Build:  st.Build,
	}

	// 构建变量 secret 引用存在性。
	in.BuildVars = toValidationVars(st.Build.Vars, v)

	// 环境(含环境变量 + 镜像仓库)secret 引用存在性。
	envs := make([]pipeline.ValidationEnvironment, 0, len(st.Environments))
	for _, e := range st.Environments {
		reg := pipeline.ValidationRegistry{
			Type:         e.ImageRegistry.Type,
			URL:          e.ImageRegistry.URL,
			CredentialID: e.ImageRegistry.CredentialID,
		}
		if e.ImageRegistry.CredentialID != "" {
			reg.CredentialExists = credentialExists(v, e.ImageRegistry.CredentialID)
		}
		envs = append(envs, pipeline.ValidationEnvironment{
			Name:          e.Name,
			EnvVars:       toValidationVars(e.EnvVars, v),
			ImageRegistry: reg,
		})
	}
	in.Environments = envs

	// 分支映射 + 事件。
	mappings := make([]pipeline.ValidationBranchMapping, 0, len(tcfg.BranchMappings))
	for _, m := range tcfg.BranchMappings {
		mappings = append(mappings, pipeline.ValidationBranchMapping{
			BranchPattern:   m.BranchPattern,
			Environment:     m.Environment,
			TargetServerIDs: m.TargetServerIDs,
		})
	}
	in.BranchMappings = mappings
	in.Events = pipeline.ValidationEvents{
		Push:        tcfg.Events.Push,
		Tag:         tcfg.Events.Tag,
		PullRequest: tcfg.Events.PullRequest,
	}

	// 项目主凭据存在性(项目创建已要求凭据,此为兜底)。
	in.ProjectCredentialOK = proj.CredentialID != "" && credentialExists(v, proj.CredentialID)

	// 保险库未配置(master key 缺失)是环境级状态:纯函数据此抑制 credential_missing 误报、
	// 改发 vault_unconfigured warning,而非把「保险库没起来」呈现为「所有凭据悬挂」。
	in.VaultUnconfigured = vaultUnconfigured(v)

	return in
}

// isVaultUnconfiguredErr 判断聚合读取错误是否为「保险库未配置 master key」类(settings/trigger/vault)。
// 保险库未起来是环境级状态,不应让 validation 落 500;短路为降级提示。
func isVaultUnconfiguredErr(err error) bool {
	return errors.Is(err, vault.ErrVaultUnconfigured) ||
		errors.Is(err, trigger.ErrVaultUnconfigured) ||
		errors.Is(err, pipeline.ErrSettingsVaultUnconfigured)
}

// degradedVaultIssues 返回保险库未配置时的降级校验结果(单条 warning,不报一堆 credential_missing)。
func degradedVaultIssues() []pipeline.Issue {
	return []pipeline.Issue{{
		Severity: pipeline.SeverityWarning, Code: "vault_unconfigured", Scope: pipeline.ScopeEnvs,
		Field: "", Message: "保险库未配置 master key,暂无法校验流水线配置;请先在 设置·凭据保险库 配置",
	}}
}

// vaultUnconfigured 报告保险库是否未配置 master key(nil 或 Exists 返回 ErrVaultUnconfigured)。
func vaultUnconfigured(v vault.Vault) bool {
	if v == nil {
		return true
	}
	_, err := v.Exists("__vault_probe__")
	return errors.Is(err, vault.ErrVaultUnconfigured)
}

// toValidationVars 把领域变量转中性视图;secret 项经 vault.Exists 预判引用存在性。
func toValidationVars(in []pipeline.BuildVar, v vault.Vault) []pipeline.ValidationVar {
	out := make([]pipeline.ValidationVar, 0, len(in))
	for _, bv := range in {
		nv := pipeline.ValidationVar{Key: bv.Key, Secret: bv.Secret, CredentialID: bv.CredentialID}
		if bv.Secret && bv.CredentialID != "" {
			nv.CredentialExists = credentialExists(v, bv.CredentialID)
		}
		out = append(out, nv)
	}
	return out
}

// credentialExists 经保险库校验 credentialId 存在性(不解密、不刷新 last_used_at)。
// vault 为 nil 或未配置或查询出错 → 视为不存在(校验侧保守:悬挂引用产 error)。
func credentialExists(v vault.Vault, id string) bool {
	if v == nil || id == "" {
		return false
	}
	ok, err := v.Exists(id)
	if err != nil {
		return false
	}
	return ok
}

// writeValidationError 把聚合读取过程中的领域错误映射为契约错误码/状态码。
// 各服务的项目不存在错误统一映射为 404 project_not_found。
func writeValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pipeline.ErrProjectNotFound),
		errors.Is(err, trigger.ErrProjectNotFound),
		errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}
