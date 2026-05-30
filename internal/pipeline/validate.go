// validate.go 是「流水线配置合法性校验」的纯函数层(FR-9 / Story 2.6)。
//
// 单 tab 内的局部校验由 2-2(spec)/2-4(settings)/2-3(triggers)各自的保存路径
// 兜住;本期补的是**跨 tab 整体就绪度**校验:聚合 spec + settings + triggers + 项目
// 凭据存在性,产出结构化 Issue 列表(error/warning/info,定位到 tab + 字段)。
//
// 核心跨 tab 校验:分支映射的 environment 必须匹配「环境与凭据」(settings.Environments)
// 中已定义的环境名(trim 后精确匹配、大小写敏感)——单 tab 内无法做。
//
// Validate 是纯函数:无 init 副作用、无 DB、无包级重对象;DB 读(spec/settings/triggers/
// vault.Exists)全在 handler 层完成后塞进 ValidationInput。易 -race 单测。
package pipeline

import "strings"

// severity 枚举(冻结契约;error 阻塞 ready,warning/info 仅提示)。
const (
	// SeverityError 表示阻塞就绪的错误级 issue。
	SeverityError = "error"
	// SeverityWarning 表示不影响就绪的告警级 issue。
	SeverityWarning = "warning"
	// SeverityInfo 表示信息级 issue。
	SeverityInfo = "info"
)

// scope 枚举(冻结契约;前端据此定位 tab)。
const (
	// ScopeCanvas 对应 2-2 编排画布 tab。
	ScopeCanvas = "canvas"
	// ScopeVars 对应 2-4 构建配置(变量)tab。
	ScopeVars = "vars"
	// ScopeEnvs 对应 2-4 环境与凭据 tab。
	ScopeEnvs = "envs"
	// ScopeTriggers 对应 2-3 触发设置 tab。
	ScopeTriggers = "triggers"
)

// Issue 是一条校验结果(冻结契约;DTO 外层形状定死)。Field 为定位路径(可空);
// Code 为稳定枚举(前端据此本地化/定位);Message 为中文人读。
type Issue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Scope    string `json:"scope"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}

// ValidationVar 是参与校验的一条变量(构建变量或环境变量)的中性视图。
// CredentialExists 由 handler 层预先经 vault.Exists 判定后塞入(纯函数不触 DB)。
type ValidationVar struct {
	Key              string
	Secret           bool
	CredentialID     string
	CredentialExists bool
}

// ValidationRegistry 是参与校验的镜像仓库绑定的中性视图。
// CredentialExists 仅在 CredentialID 非空时有意义,由 handler 层预判。
type ValidationRegistry struct {
	Type             string
	URL              string
	CredentialID     string
	CredentialExists bool
}

// ValidationEnvironment 是参与校验的一个环境的中性视图(name + 变量 + 镜像仓库)。
type ValidationEnvironment struct {
	Name          string
	EnvVars       []ValidationVar
	ImageRegistry ValidationRegistry
}

// ValidationBranchMapping 是参与校验的一条分支映射的中性视图。
type ValidationBranchMapping struct {
	BranchPattern   string
	Environment     string
	TargetServerIDs []string
}

// ValidationEvents 是触发事件开关的中性视图。
type ValidationEvents struct {
	Push        bool
	Tag         bool
	PullRequest bool
}

// ValidationInput 是 Validate 的全部输入(中性值类型,与 DB/HTTP 解耦)。
// secret 引用的悬挂判定经各 *Var/*Registry 的 CredentialExists 标记(handler 层
// vault.Exists 预判后塞入),纯函数据此判 credential_missing 而不触 DB。
type ValidationInput struct {
	// Stages 是 2-2 spec 的阶段集合(复用本包 Stage)。
	Stages []Stage
	// Build 是 2-4 构建配置(model/toolchain/dockerfilePath;Vars 单独经 BuildVars 传 CredentialExists)。
	Build BuildConfig
	// BuildVars 是构建变量的中性视图(带 secret 引用存在性标记)。
	BuildVars []ValidationVar
	// Environments 是 2-4 环境定义的中性视图(name + envVars + imageRegistry)。
	Environments []ValidationEnvironment
	// BranchMappings 是 2-3 分支映射的中性视图。
	BranchMappings []ValidationBranchMapping
	// Events 是 2-3 触发事件开关。
	Events ValidationEvents
	// ProjectCredentialOK 表示项目主凭据存在且可读(handler 层预判)。
	ProjectCredentialOK bool
	// VaultUnconfigured 表示保险库未配置 master key(环境级状态)。为 true 时抑制
	// credential_missing/project_credential_missing(无法校验存在性),改发一条 vault_unconfigured warning。
	VaultUnconfigured bool
}

// Validate 执行跨 tab 整体就绪度校验,返回 Issue 列表(顺序:canvas→vars→envs→triggers→project)。
// 纯函数:不触 DB、无副作用。
func Validate(in ValidationInput) []Issue {
	issues := make([]Issue, 0)
	// 保险库未配置(master key 缺失)是**环境级**状态,不应被呈现为「所有凭据都悬挂」的配置级错误。
	// 此时无法校验任何 secret 引用存在性 → 抑制 credential_missing/project_credential_missing
	// (把存在性视为已知,避免逐条误报),改发一条 vault_unconfigured warning。
	if in.VaultUnconfigured {
		in.ProjectCredentialOK = true
		in.BuildVars = markCredentialExists(in.BuildVars)
		envs := make([]ValidationEnvironment, len(in.Environments))
		copy(envs, in.Environments)
		for i := range envs {
			envs[i].EnvVars = markCredentialExists(envs[i].EnvVars)
			envs[i].ImageRegistry.CredentialExists = true
		}
		in.Environments = envs
		issues = append(issues, Issue{
			Severity: SeverityWarning, Code: "vault_unconfigured", Scope: ScopeEnvs,
			Field: "", Message: "保险库未配置 master key,暂无法校验 secret 凭据引用;请先在 设置·凭据保险库 配置",
		})
	}
	issues = append(issues, validateCanvas(in)...)
	issues = append(issues, validateVars(in)...)
	issues = append(issues, validateEnvs(in)...)
	issues = append(issues, validateTriggers(in)...)
	issues = append(issues, validateProject(in)...)
	return issues
}

// markCredentialExists 返回把每个变量 CredentialExists 置 true 的副本(保险库未配置时抑制悬挂误报)。
func markCredentialExists(vars []ValidationVar) []ValidationVar {
	out := make([]ValidationVar, len(vars))
	copy(out, vars)
	for i := range out {
		out[i].CredentialExists = true
	}
	return out
}

// Ready 报告 issues 中是否无 error 级 issue(warning/info 不影响就绪)。
func Ready(issues []Issue) bool {
	for _, is := range issues {
		if is.Severity == SeverityError {
			return false
		}
	}
	return true
}

// validateCanvas 校验 2-2 spec:源阶段存在性 + build/deploy 阶段存在性 + deploy 需环境定义。
func validateCanvas(in ValidationInput) []Issue {
	var issues []Issue

	hasSource, hasBuild, hasDeploy := false, false, false
	for _, st := range in.Stages {
		switch strings.TrimSpace(st.Kind) {
		case KindSource:
			hasSource = true
		case KindBuild:
			if len(st.Jobs) > 0 {
				hasBuild = true
			}
		case KindDeploy:
			if len(st.Jobs) > 0 {
				hasDeploy = true
			}
		}
	}

	// 源阶段缺失(2-2 不变式已基本兜住,此为聚合复述)。
	if !hasSource {
		issues = append(issues, Issue{
			Severity: SeverityError, Code: "source_stage_missing", Scope: ScopeCanvas,
			Field: "stages", Message: "流水线缺少源阶段,无法引用项目仓库",
		})
	}

	// 无 build 且无 deploy 阶段任务:触发后无实际产出。
	if !hasBuild && !hasDeploy {
		issues = append(issues, Issue{
			Severity: SeverityWarning, Code: "no_build_or_deploy", Scope: ScopeCanvas,
			Field: "stages", Message: "流水线无构建或部署任务,触发后无实际产出",
		})
	}

	// 有 deploy 阶段任务但 settings 无环境定义:部署无落点。
	if hasDeploy && len(in.Environments) == 0 {
		issues = append(issues, Issue{
			Severity: SeverityWarning, Code: "deploy_without_environment", Scope: ScopeCanvas,
			Field: "stages", Message: "存在部署任务但未在「环境与凭据」中定义任何环境",
		})
	}

	return issues
}

// validateVars 校验 2-4 构建配置:工具链完整性 + Dockerfile 路径 + 构建变量 secret 引用悬挂。
func validateVars(in ValidationInput) []Issue {
	var issues []Issue

	model := strings.TrimSpace(in.Build.Model)
	if model == BuildModelToolchain {
		// model=toolchain 但 language/version 空 → error。
		if strings.TrimSpace(in.Build.Toolchain.Language) == "" || strings.TrimSpace(in.Build.Toolchain.Version) == "" {
			issues = append(issues, Issue{
				Severity: SeverityError, Code: "toolchain_incomplete", Scope: ScopeVars,
				Field: "build.toolchain", Message: "构建模型为「平台工具链」时必须指定语言与版本",
			})
		}
	}
	if model == BuildModelDockerfile {
		// model=dockerfile 但 dockerfilePath 空 → warning(退化为 Dockerfile)。
		if strings.TrimSpace(in.Build.DockerfilePath) == "" {
			issues = append(issues, Issue{
				Severity: SeverityWarning, Code: "dockerfile_path_default", Scope: ScopeVars,
				Field: "build.dockerfilePath", Message: "未指定 Dockerfile 路径,将退化为默认 Dockerfile",
			})
		}
	}

	// secret 构建变量 credentialId 悬挂(凭据已删)→ error。
	for i, v := range in.BuildVars {
		if issue, ok := secretVarIssue(v, ScopeVars, "build.vars["+itoa(i)+"]"); ok {
			issues = append(issues, issue)
		}
	}

	return issues
}

// validateEnvs 校验 2-4 环境定义:镜像仓库 url/凭据完整性 + 环境变量 secret 引用悬挂。
func validateEnvs(in ValidationInput) []Issue {
	var issues []Issue

	for ei, env := range in.Environments {
		prefix := "environments[" + itoa(ei) + "]"

		reg := env.ImageRegistry
		regType := strings.TrimSpace(reg.Type)
		if regType != "" {
			// 镜像仓库 type 非空但 url 空 → error。
			if strings.TrimSpace(reg.URL) == "" {
				issues = append(issues, Issue{
					Severity: SeverityError, Code: "registry_url_missing", Scope: ScopeEnvs,
					Field: prefix + ".imageRegistry.url", Message: "环境「" + strings.TrimSpace(env.Name) + "」的镜像仓库已选类型但未填仓库地址",
				})
			}
			// 镜像仓库绑定但无 credentialId → warning。
			if strings.TrimSpace(reg.CredentialID) == "" {
				issues = append(issues, Issue{
					Severity: SeverityWarning, Code: "registry_credential_missing", Scope: ScopeEnvs,
					Field: prefix + ".imageRegistry.credentialId", Message: "环境「" + strings.TrimSpace(env.Name) + "」的镜像仓库未绑定推送凭据",
				})
			} else if !reg.CredentialExists {
				// 镜像仓库凭据引用悬挂 → error。
				issues = append(issues, Issue{
					Severity: SeverityError, Code: "credential_missing", Scope: ScopeEnvs,
					Field: prefix + ".imageRegistry.credentialId", Message: "环境「" + strings.TrimSpace(env.Name) + "」的镜像仓库凭据已不存在",
				})
			}
		}

		// 环境的 secret envVar credentialId 悬挂 → error。
		for vi, v := range env.EnvVars {
			if issue, ok := secretVarIssue(v, ScopeEnvs, prefix+".envVars["+itoa(vi)+"]"); ok {
				issues = append(issues, issue)
			}
		}
	}

	return issues
}

// validateTriggers 校验 2-3 触发设置:分支映射 environment 跨 tab 匹配(核心)+
// 手动触发提示 + 目标服务器留 4-1。
func validateTriggers(in ValidationInput) []Issue {
	var issues []Issue

	// 已定义环境名集合(trim 后精确匹配、大小写敏感)。
	defined := make(map[string]struct{}, len(in.Environments))
	for _, env := range in.Environments {
		name := strings.TrimSpace(env.Name)
		if name != "" {
			defined[name] = struct{}{}
		}
	}

	for i, m := range in.BranchMappings {
		prefix := "branchMappings[" + itoa(i) + "]"

		// 核心跨 tab 校验:分支映射 environment 非空但未在 settings.environments 中定义 → error。
		envName := strings.TrimSpace(m.Environment)
		if envName != "" {
			if _, ok := defined[envName]; !ok {
				issues = append(issues, Issue{
					Severity: SeverityError, Code: "environment_undefined", Scope: ScopeTriggers,
					Field:   prefix + ".environment",
					Message: "分支映射「" + strings.TrimSpace(m.BranchPattern) + "」引用的环境「" + envName + "」未在「环境与凭据」中定义",
				})
			}
		}

		// targetServerIds 非空 → warning（留 4-1）。
		if len(m.TargetServerIDs) > 0 {
			issues = append(issues, Issue{
				Severity: SeverityWarning, Code: "target_servers_pending", Scope: ScopeTriggers,
				Field:   prefix + ".targetServerIds",
				Message: "目标服务器登记将在 Story 4-1 提供;当前映射的服务器引用暂不校验",
			})
		}
	}

	// 所有触发事件关闭且无分支映射 → info（仅手动触发）。
	if !in.Events.Push && !in.Events.Tag && !in.Events.PullRequest && len(in.BranchMappings) == 0 {
		issues = append(issues, Issue{
			Severity: SeverityInfo, Code: "manual_only", Scope: ScopeTriggers,
			Field: "events", Message: "未开启任何自动触发事件,流水线仅能手动触发",
		})
	}

	return issues
}

// validateProject 校验项目主凭据存在性(项目创建已要求凭据,此为兜底)。
func validateProject(in ValidationInput) []Issue {
	if !in.ProjectCredentialOK {
		return []Issue{{
			Severity: SeverityError, Code: "project_credential_missing", Scope: ScopeCanvas,
			Field: "credentialId", Message: "项目仓库凭据不存在或不可读,无法拉取源码",
		}}
	}
	return nil
}

// secretVarIssue 判定一条 secret 变量的引用是否悬挂;非 secret 或引用存在则不产 issue。
func secretVarIssue(v ValidationVar, scope, field string) (Issue, bool) {
	if !v.Secret {
		return Issue{}, false
	}
	// secret 变量缺 credentialId 或引用已不存在 → credential_missing(error)。
	if strings.TrimSpace(v.CredentialID) == "" || !v.CredentialExists {
		return Issue{
			Severity: SeverityError, Code: "credential_missing", Scope: scope,
			Field: field, Message: "变量「" + strings.TrimSpace(v.Key) + "」引用的保险库凭据已不存在",
		}, true
	}
	return Issue{}, false
}

// itoa 是无依赖的小整数转字符串(用于 field 定位路径下标;避免引 strconv 仅为此)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
