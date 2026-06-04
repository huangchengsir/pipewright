package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/library"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/trigger"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// ---- AI 生成 / 应用 DTO(冻结契约;camelCase;绝无明文密钥) ----

type aiAnalysisDTO struct {
	Cloned          bool     `json:"cloned"`
	Language        string   `json:"language"`
	LanguageVersion string   `json:"languageVersion"`
	BuildTool       string   `json:"buildTool"`
	HasDockerfile   bool     `json:"hasDockerfile"`
	ArtifactHint    string   `json:"artifactHint"`
	Signals         []string `json:"signals"`
}

type aiProposalJobDTO struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Summary string         `json:"summary"`
	Config  map[string]any `json:"config,omitempty"` // LLM 填好的可执行配置(前端原样回传 apply)
	Needs   []string       `json:"needs,omitempty"`  // 同阶段依赖的 job name(串行);空=并行
}

type aiProposalStageDTO struct {
	ID   string             `json:"id"`
	Name string             `json:"name"`
	Kind string             `json:"kind"`
	Jobs []aiProposalJobDTO `json:"jobs"`
}

type aiToolchainDTO struct {
	Language string `json:"language"`
	Version  string `json:"version"`
}

type aiProposalBuildDTO struct {
	Model          string         `json:"model"`
	Toolchain      aiToolchainDTO `json:"toolchain"`
	ArtifactType   string         `json:"artifactType"`
	DockerfilePath string         `json:"dockerfilePath"`
}

type aiBranchMappingDTO struct {
	ID            string `json:"id"`
	BranchPattern string `json:"branchPattern"`
	Environment   string `json:"environment"`
}

type aiProposalDTO struct {
	Stages         []aiProposalStageDTO `json:"stages"`
	Build          aiProposalBuildDTO   `json:"build"`
	BranchMappings []aiBranchMappingDTO `json:"branchMappings"`
	Rationale      string               `json:"rationale"`
}

// aiGenerateDTO 是 generate 端点响应(冻结契约:available/reason/analysis/proposal)。
type aiGenerateDTO struct {
	Available bool           `json:"available"`
	Reason    string         `json:"reason"`
	Analysis  aiAnalysisDTO  `json:"analysis"`
	Proposal  *aiProposalDTO `json:"proposal"`
}

// aiApplyDTO 是 apply 端点响应(冻结契约:applied{spec,settings,triggers})。
type aiApplyDTO struct {
	Applied struct {
		Spec     bool `json:"spec"`
		Settings bool `json:"settings"`
		Triggers bool `json:"triggers"`
	} `json:"applied"`
}

func toAIAnalysisDTO(a ai.RepoAnalysis) aiAnalysisDTO {
	signals := a.Signals
	if signals == nil {
		signals = []string{}
	}
	return aiAnalysisDTO{
		Cloned:          a.Cloned,
		Language:        a.Language,
		LanguageVersion: a.LanguageVersion,
		BuildTool:       a.BuildTool,
		HasDockerfile:   a.HasDockerfile,
		ArtifactHint:    a.ArtifactHint,
		Signals:         signals,
	}
}

func toAIProposalDTO(p *ai.Proposal) *aiProposalDTO {
	if p == nil {
		return nil
	}
	stages := make([]aiProposalStageDTO, 0, len(p.Stages))
	for _, st := range p.Stages {
		jobs := make([]aiProposalJobDTO, 0, len(st.Jobs))
		for _, jb := range st.Jobs {
			jobs = append(jobs, aiProposalJobDTO{ID: jb.ID, Name: jb.Name, Type: jb.Type, Summary: jb.Summary, Config: jb.Config, Needs: jb.Needs})
		}
		stages = append(stages, aiProposalStageDTO{ID: st.ID, Name: st.Name, Kind: st.Kind, Jobs: jobs})
	}
	mappings := make([]aiBranchMappingDTO, 0, len(p.BranchMappings))
	for _, m := range p.BranchMappings {
		mappings = append(mappings, aiBranchMappingDTO{ID: m.ID, BranchPattern: m.BranchPattern, Environment: m.Environment})
	}
	return &aiProposalDTO{
		Stages: stages,
		Build: aiProposalBuildDTO{
			Model:          p.Build.Model,
			Toolchain:      aiToolchainDTO{Language: p.Build.Toolchain.Language, Version: p.Build.Toolchain.Version},
			ArtifactType:   p.Build.ArtifactType,
			DockerfilePath: p.Build.DockerfilePath,
		},
		BranchMappings: mappings,
		Rationale:      p.Rationale,
	}
}

// aiGenerateDeps 聚合 generate/apply 两 handler 所需服务(复用已注入服务,无新顶层依赖)。
type aiGenerateDeps struct {
	analyzer    ai.RepoAnalyzer
	aiSvc       ai.Service
	projects    project.Service
	pipes       pipeline.Service
	settings    pipeline.SettingsService
	triggers    trigger.Service
	vault       vault.Vault
	customNodes library.CustomNodeService // 复用库自定义节点(动态拼入 AI 节点目录;可 nil)
}

// reasonAINotConfigured 是 AI 未配置时的友好引导(不阻断手动建项目;HTTP 200,available=false)。
const reasonAINotConfigured = "AI 提供商未配置,可在 设置·AI 提供商 配置;或手动编辑流水线"

// makeAIGenerateHandler 返回 POST /api/projects/{id}/pipeline/ai-generate handler。
//
// 编排:取项目(repoUrl + credentialId)→ 取 token(vault.Get,进程内取用即弃)→
// RepoAnalyzer.Analyze(克隆失败 → cloned=false 降级)→ ai.Service.Generate(未配 →
// available=false;LLM 失败 → available=true + reason 人读错误 + 空 proposal)。
// **两路降级均 HTTP 200,绝不 500、绝无明文密钥。** 项目不存在 → 404。
func makeAIGenerateHandler(d aiGenerateDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.aiSvc == nil || d.projects == nil || d.analyzer == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "AI 生成所需服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			NLSupplement string `json:"nlSupplement"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
				return
			}
		}

		proj, err := d.projects.Get(ctx, id)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		// 取仓库克隆 token(进程内取用即弃);取不到不致命 → 以空 token 尝试(多走降级)。
		token := ""
		if d.vault != nil && strings.TrimSpace(proj.CredentialID) != "" {
			if t, terr := d.vault.Get(proj.CredentialID); terr == nil {
				token = t
			}
		}

		analysis := d.analyzer.Analyze(ctx, proj.RepoURL, token)
		token = "" // 明文用完即弃
		_ = token

		dto := aiGenerateDTO{
			Available: true,
			Analysis:  toAIAnalysisDTO(analysis),
		}

		// 动态节点目录:内置工具清单 + 复用库自定义节点(运行时拼入,新增即生效),作为
		// LLM 可组合的「可用节点」全集,避免只产粗粒度三段式。
		catalog := ai.BuiltinNodeCatalog()
		if d.customNodes != nil {
			if nodes, lerr := d.customNodes.List(ctx); lerr == nil {
				for _, cn := range nodes {
					desc := strings.TrimSpace(cn.Summary)
					if desc == "" {
						desc = strings.TrimSpace(cn.Description)
					}
					nt := strings.TrimSpace(cn.NodeType)
					if nt == "" {
						nt = "templated"
					}
					catalog = append(catalog, ai.NodeKind{
						Type: nt, Label: cn.Name, Category: "custom", Description: desc, Custom: true,
					})
				}
			}
		}

		proposal, gerr := d.aiSvc.Generate(ctx, ai.GenerateInput{
			Analysis: analysis,
			NL:       req.NLSupplement,
			Catalog:  catalog,
		})
		switch {
		case gerr == nil:
			dto.Proposal = toAIProposalDTO(proposal)
		case errors.Is(gerr, ai.ErrAINotConfigured):
			// AI 未配/未启用:友好降级,不阻断。
			dto.Available = false
			dto.Reason = reasonAINotConfigured
		default:
			// LLM 调用/解析失败:available=true 但带人读错误(绝无密钥),proposal 空。
			dto.Available = true
			dto.Reason = "AI 生成失败:" + humanizeGenerateErr(gerr)
		}

		writeJSON(w, http.StatusOK, dto)
	}
}

// humanizeGenerateErr 把生成错误转人读消息(绝无明文密钥;ErrGenerateFailed 已是人读)。
func humanizeGenerateErr(err error) string {
	switch {
	case errors.Is(err, ai.ErrVaultUnconfigured):
		return "保险库未配置,无法读取 API 密钥"
	case errors.Is(err, ai.ErrGenerateFailed):
		// ErrGenerateFailed 的包裹文本本身即人读(且构造时已确保不含密钥)。
		msg := err.Error()
		if i := strings.Index(msg, ": "); i >= 0 {
			return strings.TrimSpace(msg[i+2:])
		}
		return "调用模型失败,请稍后重试"
	default:
		return "调用模型失败,请稍后重试"
	}
}

// makeAIApplyHandler 返回 POST /api/projects/{id}/pipeline/ai-apply handler。
//
// 仅写被勾选项:selections.stageIds → 合入 pipeline spec(始终保留/补源阶段,满足不变式)
// 经 pipeline.Save;selections.build=true → SettingsService.Save(secret 引用留空,不编造
// credentialId);selections.branchMappingIds → trigger.Save(合并去重既有)。
// 各服务校验仍生效:校验失败透传 422;项目不存在 404。
func makeAIApplyHandler(d aiGenerateDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.pipes == nil || d.settings == nil || d.triggers == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "AI 应用所需服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		r.Body = http.MaxBytesReader(w, r.Body, 1<<18) // 256KB
		var req struct {
			Proposal   aiProposalReq `json:"proposal"`
			Selections struct {
				StageIDs         []string `json:"stageIds"`
				Build            bool     `json:"build"`
				BranchMappingIDs []string `json:"branchMappingIds"`
			} `json:"selections"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		var resp aiApplyDTO

		// 1) stageIds → 合入 pipeline spec(始终保留/补源阶段)。
		if len(req.Selections.StageIDs) > 0 {
			if err := applySelectedStages(ctx, d.pipes, id, req.Proposal, req.Selections.StageIDs); err != nil {
				writeAIApplyError(w, err)
				return
			}
			resp.Applied.Spec = true
		}

		// 2) build=true → 写 pipeline_settings(secret 引用留空)。
		if req.Selections.Build {
			if err := applySelectedBuild(ctx, d.settings, id, req.Proposal.Build); err != nil {
				writeAIApplyError(w, err)
				return
			}
			resp.Applied.Settings = true
		}

		// 3) branchMappingIds → 合入 trigger 分支映射(合并去重)。
		if len(req.Selections.BranchMappingIDs) > 0 {
			if err := applySelectedMappings(ctx, d.triggers, id, req.Proposal.BranchMappings, req.Selections.BranchMappingIDs); err != nil {
				writeAIApplyError(w, err)
				return
			}
			resp.Applied.Triggers = true
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// aiProposalReq 是 apply 请求体内的 proposal(与响应 DTO 同形;由前端原样回传)。
type aiProposalReq struct {
	Stages []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		Jobs []struct {
			ID      string         `json:"id"`
			Name    string         `json:"name"`
			Type    string         `json:"type"`
			Summary string         `json:"summary"`
			Config  map[string]any `json:"config"`
			Needs   []string       `json:"needs"`
		} `json:"jobs"`
	} `json:"stages"`
	Build struct {
		Model     string `json:"model"`
		Toolchain struct {
			Language string `json:"language"`
			Version  string `json:"version"`
		} `json:"toolchain"`
		ArtifactType   string `json:"artifactType"`
		DockerfilePath string `json:"dockerfilePath"`
	} `json:"build"`
	BranchMappings []struct {
		ID            string `json:"id"`
		BranchPattern string `json:"branchPattern"`
		Environment   string `json:"environment"`
	} `json:"branchMappings"`
}

// isSingletonStageKind 报告该 kind 是否为「标准单例阶段」(每条流水线至多一个)。
// 源阶段单独处理(恒由既有保留);build/deploy/notify 为单例,custom 可多个。
func isSingletonStageKind(kind string) bool {
	switch kind {
	case pipeline.KindBuild, pipeline.KindDeploy, pipeline.KindNotify:
		return true
	default:
		return false
	}
}

// applySelectedStages 把勾选的提案阶段合入既有 spec:标准单例 kind(build/deploy/notify)
// 就地替换既有同 kind 阶段(避免重复),custom 阶段追加,始终保留既有源阶段(满足 2-2 不变式)。
// 经 pipeline.Save(校验仍生效)。
func applySelectedStages(ctx context.Context, svc pipeline.Service, projectID string, prop aiProposalReq, stageIDs []string) error {
	cur, err := svc.Get(ctx, projectID)
	if err != nil {
		return err
	}

	want := make(map[string]struct{}, len(stageIDs))
	for _, sid := range stageIDs {
		want[sid] = struct{}{}
	}

	// 把第 i 个提案阶段转为 pipeline.Stage。为每个 job 分配稳定 id(服务端保留已给 id),
	// 并把 LLM 按「依赖 job 的 name」写的 needs 映射为对应 job id —— 实现节点级串行依赖
	// (如 build_image needs build_backend),否则同阶段全并行、镜像拿不到 jar 而失败。
	toStage := func(i int) pipeline.Stage {
		st := prop.Stages[i]
		// 先给每个 job 定 id,并建 name/原 id → 分配 id 的映射(供 needs 解析)。
		ids := make([]string, len(st.Jobs))
		refToID := make(map[string]string, len(st.Jobs)*2)
		for k, jb := range st.Jobs {
			aid := fmt.Sprintf("aijob_%d_%d", i, k)
			ids[k] = aid
			if n := strings.TrimSpace(jb.Name); n != "" {
				refToID[n] = aid
			}
			if id := strings.TrimSpace(jb.ID); id != "" {
				refToID[id] = aid
			}
		}
		jobs := make([]pipeline.Job, 0, len(st.Jobs))
		for k, jb := range st.Jobs {
			cfg := jb.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			needs := make([]string, 0, len(jb.Needs))
			for _, ref := range jb.Needs {
				if id, ok := refToID[strings.TrimSpace(ref)]; ok && id != ids[k] {
					needs = append(needs, id)
				}
			}
			jobs = append(jobs, pipeline.Job{ID: ids[k], Name: jb.Name, Type: jb.Type, Summary: jb.Summary, Config: cfg, Needs: needs})
		}
		return pipeline.Stage{ID: "", Name: st.Name, Kind: st.Kind, Jobs: jobs}
	}

	// 被勾选的非源提案阶段索引(保序);源阶段恒由既有保留以满足不变式,不重复合入。
	chosen := make([]int, 0, len(prop.Stages))
	for i, st := range prop.Stages {
		if _, ok := want[st.ID]; ok && st.Kind != pipeline.KindSource {
			chosen = append(chosen, i)
		}
	}
	// 标准单例 kind(build/deploy/notify)被提案替换:就地替换既有同 kind,避免追加出重复阶段。
	singleton := map[string]int{}
	for _, i := range chosen {
		if isSingletonStageKind(prop.Stages[i].Kind) {
			singleton[prop.Stages[i].Kind] = i // 多个同 kind 取最后一个
		}
	}

	merged := make([]pipeline.Stage, 0, len(cur.Spec.Stages)+len(chosen))
	placed := map[string]bool{}
	for _, st := range cur.Spec.Stages {
		if idx, ok := singleton[st.Kind]; ok {
			if !placed[st.Kind] { // 就地用提案版本替换;既有同单例 kind 的多余重复行一并去重
				merged = append(merged, toStage(idx))
				placed[st.Kind] = true
			}
			continue
		}
		merged = append(merged, st) // 源/通知/custom 等未被替换的既有阶段保留
	}
	// 追加既有中不存在的提案阶段:新单例 kind(就地未放下)+ 所有 custom 阶段(可多个),保序。
	for _, i := range chosen {
		st := prop.Stages[i]
		if isSingletonStageKind(st.Kind) {
			if !placed[st.Kind] {
				merged = append(merged, toStage(i))
				placed[st.Kind] = true
			}
			continue
		}
		merged = append(merged, toStage(i)) // custom 总是追加
	}

	_, err = svc.Save(ctx, projectID, pipeline.Spec{Stages: merged})
	return err
}

// applySelectedBuild 把提案 build 写入 pipeline_settings(secret 引用留空,不编造 credentialId)。
// 保留既有环境定义(仅覆盖 build 部分)。
func applySelectedBuild(ctx context.Context, svc pipeline.SettingsService, projectID string, b struct {
	Model     string `json:"model"`
	Toolchain struct {
		Language string `json:"language"`
		Version  string `json:"version"`
	} `json:"toolchain"`
	ArtifactType   string `json:"artifactType"`
	DockerfilePath string `json:"dockerfilePath"`
}) error {
	cur, err := svc.Get(ctx, projectID)
	if err != nil {
		return err
	}

	build := pipeline.BuildConfig{
		Model:          b.Model,
		DockerfilePath: b.DockerfilePath,
		Toolchain:      pipeline.Toolchain{Language: b.Toolchain.Language, Version: b.Toolchain.Version},
		ArtifactType:   b.ArtifactType,
		Vars:           []pipeline.BuildVar{}, // AI 不编造 secret 变量/凭据
		Cache:          pipeline.Cache{Enabled: false, Paths: []string{}},
	}

	_, err = svc.Save(ctx, projectID, pipeline.SettingsInput{
		Build:        build,
		Environments: cur.Environments, // 保留既有环境
	})
	return err
}

// applySelectedMappings 把勾选的提案分支映射合入既有 trigger 分支映射(去重:同 pattern+env 不重复)。
func applySelectedMappings(ctx context.Context, svc trigger.Service, projectID string, mappings []struct {
	ID            string `json:"id"`
	BranchPattern string `json:"branchPattern"`
	Environment   string `json:"environment"`
}, mappingIDs []string) error {
	cur, err := svc.Get(ctx, projectID)
	if err != nil {
		return err
	}

	want := make(map[string]struct{}, len(mappingIDs))
	for _, mid := range mappingIDs {
		want[mid] = struct{}{}
	}

	// 去重键:branchPattern + "\x00" + environment(已存的不重复合入)。
	seen := make(map[string]struct{}, len(cur.BranchMappings))
	merged := make([]trigger.BranchMapping, 0, len(cur.BranchMappings)+len(mappings))
	for _, m := range cur.BranchMappings {
		seen[m.BranchPattern+"\x00"+m.Environment] = struct{}{}
		merged = append(merged, m)
	}

	for _, m := range mappings {
		if _, ok := want[m.ID]; !ok {
			continue
		}
		key := strings.TrimSpace(m.BranchPattern) + "\x00" + strings.TrimSpace(m.Environment)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, trigger.BranchMapping{
			ID:              "", // 服务端补新 id
			BranchPattern:   m.BranchPattern,
			Environment:     m.Environment,
			TargetServerIDs: []string{},
		})
	}

	_, err = svc.Save(ctx, projectID, trigger.SaveInput{
		Events:          cur.Events,
		BranchMappings:  merged,
		UnmatchedPolicy: cur.UnmatchedPolicy,
	})
	return err
}

// writeAIApplyError 把 apply 过程中各服务的领域错误映射为契约错误码(404/422/503/500)。
func writeAIApplyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pipeline.ErrProjectNotFound),
		errors.Is(err, trigger.ErrProjectNotFound),
		errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, pipeline.ErrInvalidStage):
		writeError(w, http.StatusUnprocessableEntity, "invalid_stage", "阶段名不能为空且 kind 必须为 source/build/deploy/notify/custom 之一")
	case errors.Is(err, pipeline.ErrInvalidJob):
		writeError(w, http.StatusUnprocessableEntity, "invalid_job", "任务名与类型不能为空")
	case errors.Is(err, pipeline.ErrDuplicateID):
		writeError(w, http.StatusUnprocessableEntity, "duplicate_id", "阶段或任务 id 重复")
	case errors.Is(err, pipeline.ErrInvalidBuild):
		writeError(w, http.StatusUnprocessableEntity, "invalid_build", "构建配置非法")
	case errors.Is(err, trigger.ErrInvalidBranchPattern):
		writeError(w, http.StatusUnprocessableEntity, "invalid_branch_pattern", "分支模式非法")
	case errors.Is(err, trigger.ErrInvalidPolicy):
		writeError(w, http.StatusUnprocessableEntity, "invalid_policy", "未匹配策略非法")
	case errors.Is(err, pipeline.ErrSettingsVaultUnconfigured),
		errors.Is(err, trigger.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}
