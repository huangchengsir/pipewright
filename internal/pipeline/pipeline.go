// Package pipeline 是「流水线配置编辑器与编排画布」的领域层(UX-DR5 / 部分 FR-9 / Story 2.2)。
//
// 每个项目持有一份声明式流水线配置:结构化 spec(阶段 → 任务)+ 服务端渲染的只读 YAML
// 文本。首次访问无配置时惰性生成默认种子(源阶段引用项目仓库 + 空的 构建/部署/通知
// 三阶段),并以 INSERT ON CONFLICT(project_id) DO NOTHING + 回读权威行兜住并发首访竞态
// (仿 trigger.createDefault)。
//
// 保存(Save)做结构校验(阶段名非空、kind∈枚举、任务名非空、type 非空、stage/job id
// 全局不重复)→ 规范化(trim、补空 id)→ 渲染 YAML → 持久化(status 恒 draft)→ 回读。
// 完整引用存在性校验(凭据/服务器/镜像仓库)留 Story 2-6;本期 job.config 为自由 KV。
//
// 本期 spec 不存任何明文 secret(凭据走保险库引用,2-4 才落引用字段)。
// 领域包无 init() 副作用、无包级重对象,避免抬高空载内存。
package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	yaml "gopkg.in/yaml.v3"
)

// 阶段 kind 枚举(DB 存于 spec_json;JSON 字段 camelCase)。
const (
	// KindSource 表示流水线源阶段(引用项目仓库)。
	KindSource = "source"
	// KindBuild 表示构建阶段。
	KindBuild = "build"
	// KindDeploy 表示部署阶段。
	KindDeploy = "deploy"
	// KindNotify 表示通知阶段。
	KindNotify = "notify"
	// KindCustom 表示自定义阶段。
	KindCustom = "custom"
)

// statusDraft 是本期唯一发布状态(active/发布 = 后续 story)。
const statusDraft = "draft"

// 领域错误。错误体不含任何明文 secret。
var (
	// ErrProjectNotFound 表示引用的项目不存在。
	ErrProjectNotFound = errors.New("pipeline: project not found")
	// ErrInvalidStage 表示阶段校验失败(阶段名空 / kind 非枚举)。
	ErrInvalidStage = errors.New("pipeline: invalid stage")
	// ErrInvalidJob 表示任务校验失败(任务名空 / type 空)。
	ErrInvalidJob = errors.New("pipeline: invalid job")
	// ErrDuplicateID 表示 stage/job id 全局重复。
	ErrDuplicateID = errors.New("pipeline: duplicate id")
)

// Job 是一个任务(阶段下的最小编排单元)。Config 为自由 KV(本期任意结构;
// 2-4/2-6 才在内填强 schema)。Summary 为卡片副标题展示文本(可空)。
type Job struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Summary string         `json:"summary"`
	Config  map[string]any `json:"config"`
}

// Stage 是一个阶段(阶段列),含若干 Job。Kind 为 kind 枚举之一。
type Stage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Jobs []Job  `json:"jobs"`
}

// Spec 是流水线编排声明式配置(阶段集合)。
type Spec struct {
	Stages []Stage `json:"stages"`
}

// Config 是流水线配置领域模型(spec + 渲染 YAML + 状态 + 更新时间)。
type Config struct {
	Spec      Spec
	YAML      string
	Status    string
	UpdatedAt time.Time
}

// Service 定义流水线配置领域对外接口。
type Service interface {
	// Get 返回项目流水线配置。首次访问无配置时惰性生成默认种子并返回。
	// 项目不存在 → ErrProjectNotFound。
	Get(ctx context.Context, projectID string) (*Config, error)
	// Save 校验并规范化 spec → 渲染 YAML → 持久化(status=draft)→ 回读返回。
	// 校验失败 → ErrInvalidStage / ErrInvalidJob / ErrDuplicateID;项目不存在 → ErrProjectNotFound。
	Save(ctx context.Context, projectID string, spec Spec) (*Config, error)
}

// service 是 store 支撑的 Service 实现。
type service struct {
	db *sql.DB
}

// New 构造 Service(经参数化 SQL 触库)。不在此做任何重活
// (无 init() 副作用、无包级重对象,避免抬高空载内存)。
func New(db *sql.DB) Service {
	return &service{db: db}
}

func (s *service) Get(ctx context.Context, projectID string) (*Config, error) {
	cfg, err := s.load(ctx, projectID)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// 首次访问无配置:惰性生成默认种子。
	return s.createDefault(ctx, projectID)
}

func (s *service) Save(ctx context.Context, projectID string, spec Spec) (*Config, error) {
	// 确保配置已存在(惰性默认);Get 也完成项目存在性校验。
	if _, err := s.Get(ctx, projectID); err != nil {
		return nil, err
	}

	normalized, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}

	specJSON, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("pipeline: marshal spec: %w", err)
	}
	renderedYAML, err := renderYAML(normalized)
	if err != nil {
		return nil, err
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`UPDATE pipeline_configs
		 SET spec_json = ?, spec_yaml = ?, status = ?, updated_at = ?
		 WHERE project_id = ?`,
		string(specJSON), renderedYAML, statusDraft, nowStr, projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("pipeline: update config: %w", err)
	}
	return s.Get(ctx, projectID)
}

// load 读取已存在的配置行。无行 → sql.ErrNoRows(由 Get 转惰性默认)。
func (s *service) load(ctx context.Context, projectID string) (*Config, error) {
	var (
		specJSON   string
		specYAML   string
		status     string
		updatedStr string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT spec_json, spec_yaml, status, updated_at
		 FROM pipeline_configs WHERE project_id = ?`, projectID,
	).Scan(&specJSON, &specYAML, &status, &updatedStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("pipeline: load config: %w", err)
	}

	var spec Spec
	if strings.TrimSpace(specJSON) != "" {
		if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
			return nil, fmt.Errorf("pipeline: parse spec: %w", err)
		}
	}
	normalizeSpecShape(&spec)

	updated, err := time.Parse(time.RFC3339, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("pipeline: parse updated_at: %w", err)
	}
	return &Config{Spec: spec, YAML: specYAML, Status: status, UpdatedAt: updated}, nil
}

// createDefault 惰性生成项目默认流水线配置:源阶段(1 个 git_source 任务引用项目仓库)
// + 空的 构建/部署/通知 三阶段。项目不存在 → ErrProjectNotFound。
// 并发首访同项目时 ON CONFLICT DO NOTHING + 回读权威行,防双方各持不同种子。
func (s *service) createDefault(ctx context.Context, projectID string) (*Config, error) {
	summary, err := s.sourceSummary(ctx, projectID)
	if err != nil {
		return nil, err
	}

	spec := defaultSpec(summary)
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("pipeline: marshal default spec: %w", err)
	}
	renderedYAML, err := renderYAML(spec)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO pipeline_configs (project_id, spec_json, spec_yaml, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id) DO NOTHING`,
		projectID, string(specJSON), renderedYAML, statusDraft, nowStr, nowStr,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("pipeline: insert default config: %w", err)
	}

	// 不管本协程是否赢得插入,统一回读权威行(并发竞态下另一协程可能已写入)。
	return s.load(ctx, projectID)
}

// sourceSummary 读取项目仓库信息拼出源阶段任务摘要;项目不存在 → ErrProjectNotFound。
// 仓库信息读取失败时退化为通用摘要(不阻断惰性默认生成)。
func (s *service) sourceSummary(ctx context.Context, projectID string) (string, error) {
	var repoURL, defaultBranch string
	err := s.db.QueryRowContext(ctx,
		`SELECT repo_url, default_branch FROM projects WHERE id = ?`, projectID,
	).Scan(&repoURL, &defaultBranch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrProjectNotFound
		}
		return "", fmt.Errorf("pipeline: load project: %w", err)
	}
	branch := strings.TrimSpace(defaultBranch)
	if branch == "" {
		branch = "main"
	}
	repo := strings.TrimSpace(repoURL)
	if repo == "" {
		return branch + " · push/tag/PR", nil
	}
	return repo + " · " + branch, nil
}

// defaultSpec 构造默认种子:源阶段(git_source 引用项目仓库)+ 空构建/部署/通知三阶段。
func defaultSpec(sourceSummary string) Spec {
	return Spec{Stages: []Stage{
		{
			ID:   "stg_src",
			Name: "流水线源",
			Kind: KindSource,
			Jobs: []Job{{
				ID:      "job_src",
				Name:    "Gitee 源",
				Type:    "git_source",
				Summary: sourceSummary,
				Config:  map[string]any{},
			}},
		},
		{ID: "stg_build", Name: "构建", Kind: KindBuild, Jobs: []Job{}},
		{ID: "stg_deploy", Name: "部署", Kind: KindDeploy, Jobs: []Job{}},
		{ID: "stg_notify", Name: "通知", Kind: KindNotify, Jobs: []Job{}},
	}}
}

// normalizeSpec 校验并规范化 spec:trim 各字段、补空 id(uuid)、Config nil 补 {}、
// 校验阶段名非空 / kind 枚举 / 任务名非空 / type 非空 / stage+job id 全局不重复。
func normalizeSpec(in Spec) (Spec, error) {
	out := Spec{Stages: make([]Stage, 0, len(in.Stages))}
	seen := make(map[string]struct{})

	for _, st := range in.Stages {
		name := strings.TrimSpace(st.Name)
		if name == "" {
			return Spec{}, fmt.Errorf("%w: stage name must not be empty", ErrInvalidStage)
		}
		kind := strings.TrimSpace(st.Kind)
		if !isValidKind(kind) {
			return Spec{}, fmt.Errorf("%w: invalid kind %q", ErrInvalidStage, st.Kind)
		}
		stageID := strings.TrimSpace(st.ID)
		if stageID == "" {
			stageID = uuid.NewString()
		}
		if _, dup := seen[stageID]; dup {
			return Spec{}, fmt.Errorf("%w: stage id %q", ErrDuplicateID, stageID)
		}
		seen[stageID] = struct{}{}

		jobs := make([]Job, 0, len(st.Jobs))
		for _, jb := range st.Jobs {
			jobName := strings.TrimSpace(jb.Name)
			if jobName == "" {
				return Spec{}, fmt.Errorf("%w: job name must not be empty", ErrInvalidJob)
			}
			jobType := strings.TrimSpace(jb.Type)
			if jobType == "" {
				return Spec{}, fmt.Errorf("%w: job type must not be empty", ErrInvalidJob)
			}
			jobID := strings.TrimSpace(jb.ID)
			if jobID == "" {
				jobID = uuid.NewString()
			}
			if _, dup := seen[jobID]; dup {
				return Spec{}, fmt.Errorf("%w: job id %q", ErrDuplicateID, jobID)
			}
			seen[jobID] = struct{}{}

			cfg := jb.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			jobs = append(jobs, Job{
				ID:      jobID,
				Name:    jobName,
				Type:    jobType,
				Summary: strings.TrimSpace(jb.Summary),
				Config:  cfg,
			})
		}

		out.Stages = append(out.Stages, Stage{ID: stageID, Name: name, Kind: kind, Jobs: jobs})
	}
	return out, nil
}

// normalizeSpecShape 保证从库回读的 spec 切片/map 非 nil(JSON 输出为 [] / {} 而非 null)。
func normalizeSpecShape(spec *Spec) {
	if spec.Stages == nil {
		spec.Stages = []Stage{}
	}
	for i := range spec.Stages {
		if spec.Stages[i].Jobs == nil {
			spec.Stages[i].Jobs = []Job{}
		}
		for j := range spec.Stages[i].Jobs {
			if spec.Stages[i].Jobs[j].Config == nil {
				spec.Stages[i].Jobs[j].Config = map[string]any{}
			}
		}
	}
}

// isValidKind 判断 kind 是否为枚举之一。
func isValidKind(kind string) bool {
	switch kind {
	case KindSource, KindBuild, KindDeploy, KindNotify, KindCustom:
		return true
	default:
		return false
	}
}

// yamlSpec / yamlStage / yamlJob 是 YAML 渲染用的稳定形状(确定性字段顺序;
// 仅用于展示/导出,与持久化 JSON 解耦)。
type yamlSpec struct {
	Stages []yamlStage `yaml:"stages"`
}

type yamlStage struct {
	Name string    `yaml:"name"`
	Kind string    `yaml:"kind"`
	Jobs []yamlJob `yaml:"jobs"`
}

type yamlJob struct {
	Name    string         `yaml:"name"`
	Type    string         `yaml:"type"`
	Summary string         `yaml:"summary,omitempty"`
	Config  map[string]any `yaml:"config,omitempty"`
}

// renderYAML 把 spec 渲染为确定性 YAML 文本(供"查看源码"/导出)。
func renderYAML(spec Spec) (string, error) {
	ys := yamlSpec{Stages: make([]yamlStage, 0, len(spec.Stages))}
	for _, st := range spec.Stages {
		jobs := make([]yamlJob, 0, len(st.Jobs))
		for _, jb := range st.Jobs {
			var cfg map[string]any
			if len(jb.Config) > 0 {
				cfg = jb.Config
			}
			jobs = append(jobs, yamlJob{
				Name:    jb.Name,
				Type:    jb.Type,
				Summary: jb.Summary,
				Config:  cfg,
			})
		}
		ys.Stages = append(ys.Stages, yamlStage{Name: st.Name, Kind: st.Kind, Jobs: jobs})
	}
	out, err := yaml.Marshal(ys)
	if err != nil {
		return "", fmt.Errorf("pipeline: render yaml: %w", err)
	}
	return string(out), nil
}

// isForeignKeyErr 判断错误是否为外键约束失败(modernc sqlite 文本含 FOREIGN KEY)。
func isForeignKeyErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY")
}
