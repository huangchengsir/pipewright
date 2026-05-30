// settings.go 是「构建/部署配置」的领域层(FR-5/FR-6/FR-7/部分 FR-9 · Story 2.4)。
//
// 每个项目持有一份独立于 2-2 流水线 spec 的构建/部署声明:构建配置(模型 A/B、
// 产物类型、构建变量〔明文 + 引用保险库的 secret〕、依赖缓存)与环境定义(名称 +
// 目标服务器引用占位 + 环境变量〔明文 + secret〕 + 镜像仓库绑定)。首次访问无配置时
// 惰性生成默认(构建模型 dockerfile / 产物 image / 空变量 / 空环境),并以
// INSERT ON CONFLICT(project_id) DO NOTHING + 回读权威行兜住并发首访竞态(仿
// pipeline/trigger 的 createDefault)。
//
// AC-SEC:secret 类型的构建变量 / 环境变量 / 镜像仓库凭据**只存 credentialId 引用**,
// DB / 响应 / 日志绝无明文 secret;非 secret 项才存明文 value。保存时对每个 secret
// 引用经 vault.Get 校验存在性(不存在 → ErrCredentialNotFound);保险库未配置且有
// secret 引用 → ErrVaultUnconfigured(不 panic,仿 trigger)。响应里的掩码由保险库
// 的 masked_value 即时回算,绝不回显明文。
//
// 本期目标服务器存在性校验留 Story 4-1(仿 trigger),仅校验 targetServerIds 字符串非空。
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
	"github.com/huangjiawei/devopstool/internal/vault"
)

// 构建模型枚举(model=A 自带 Dockerfile / B 平台工具链)。DB 存于 build_json;JSON camelCase。
const (
	// BuildModelDockerfile 表示「自带 Dockerfile」(模型 A)。
	BuildModelDockerfile = "dockerfile"
	// BuildModelToolchain 表示「平台按工具链构建」(模型 B)。
	BuildModelToolchain = "toolchain"
)

// 产物类型枚举。
const (
	// ArtifactImage 表示产物为容器镜像。
	ArtifactImage = "image"
	// ArtifactJAR 表示产物为 JAR 包。
	ArtifactJAR = "jar"
	// ArtifactDist 表示产物为静态资源 dist。
	ArtifactDist = "dist"
)

// 镜像仓库类型枚举。
const (
	RegistryHarbor    = "harbor"
	RegistryACR       = "acr"
	RegistryDockerhub = "dockerhub"
	RegistryCustom    = "custom"
)

// defaultDockerfilePath 是模型 A 的默认 Dockerfile 路径。
const defaultDockerfilePath = "Dockerfile"

// 领域错误。错误体不含任何明文 secret。
var (
	// ErrInvalidBuild 表示构建配置校验失败(model / artifactType 枚举非法等)。
	ErrInvalidBuild = errors.New("pipeline: invalid build config")
	// ErrInvalidVar 表示构建/环境变量校验失败(key 空或同作用域内重复)。
	ErrInvalidVar = errors.New("pipeline: invalid variable")
	// ErrInvalidEnvironment 表示环境定义校验失败(名称空 / 目标服务器 id 空 / 镜像仓库类型非法)。
	ErrInvalidEnvironment = errors.New("pipeline: invalid environment")
	// ErrCredentialNotFound 表示 secret 引用的 credentialId 不存在于保险库。
	ErrCredentialNotFound = errors.New("pipeline: referenced credential not found")
	// ErrSettingsVaultUnconfigured 表示保险库未配置但存在 secret 引用,无法校验/掩码。
	ErrSettingsVaultUnconfigured = errors.New("pipeline: vault unconfigured")
)

// Toolchain 是模型 B 的工具链选择(语言 + 版本)。
type Toolchain struct {
	Language string `json:"language"`
	Version  string `json:"version"`
}

// BuildVar 是一条构建/环境变量。Secret=false 时存明文 Value;Secret=true 时只存 CredentialID
// 引用(明文绝不入库),MaskedValue 仅在响应里由保险库即时回算、不持久化。
type BuildVar struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Secret       bool   `json:"secret"`
	Value        string `json:"value,omitempty"`
	CredentialID string `json:"credentialId,omitempty"`
	MaskedValue  string `json:"maskedValue,omitempty"`
}

// Cache 是依赖缓存声明(开关 + 缓存路径列表)。
type Cache struct {
	Enabled bool     `json:"enabled"`
	Paths   []string `json:"paths"`
}

// BuildConfig 是构建配置(模型 A/B、产物类型、构建变量、依赖缓存)。
type BuildConfig struct {
	Model          string     `json:"model"`
	DockerfilePath string     `json:"dockerfilePath"`
	Toolchain      Toolchain  `json:"toolchain"`
	ArtifactType   string     `json:"artifactType"`
	Vars           []BuildVar `json:"vars"`
	Cache          Cache      `json:"cache"`
}

// ImageRegistry 是外部镜像仓库绑定(FR-7 声明侧)。CredentialID 引用保险库凭据;
// MaskedCredential 仅响应里即时回算,不持久化。
type ImageRegistry struct {
	Type             string `json:"type"`
	URL              string `json:"url"`
	CredentialID     string `json:"credentialId,omitempty"`
	MaskedCredential string `json:"maskedCredential,omitempty"`
}

// Environment 是一个部署环境(名称 + 目标服务器引用占位 + 环境变量 + 镜像仓库)。
type Environment struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	TargetServerIDs []string      `json:"targetServerIds"`
	EnvVars         []BuildVar    `json:"envVars"`
	ImageRegistry   ImageRegistry `json:"imageRegistry"`
}

// Settings 是构建/部署配置领域模型(冻结 DTO 外层形状)。
type Settings struct {
	Build        BuildConfig
	Environments []Environment
	UpdatedAt    time.Time
}

// SettingsInput 是保存入参(secret 项只传 credentialId,不传明文/掩码)。
type SettingsInput struct {
	Build        BuildConfig
	Environments []Environment
}

// SettingsService 定义构建/部署配置领域对外接口。
type SettingsService interface {
	// Get 返回项目构建/部署配置。首次访问无配置时惰性生成默认并返回(secret 项回掩码)。
	// 项目不存在 → ErrProjectNotFound。
	Get(ctx context.Context, projectID string) (*Settings, error)
	// Save 规范化(补 id、trim)→ 校验(枚举 / key 非空不重复 / secret 引用存在性)→
	// 持久化(只存引用,绝无明文 secret)→ 回读返回(secret 项回掩码)。
	Save(ctx context.Context, projectID string, in SettingsInput) (*Settings, error)
}

// settingsService 是 store + vault 支撑的 SettingsService 实现。
type settingsService struct {
	db    *sql.DB
	vault vault.Vault
}

// NewSettingsService 构造 SettingsService。
//   - db:经参数化 SQL 触库。
//   - v:凭据保险库,经 Get 校验 secret 引用存在性、经 masked_value 回算掩码;
//     v 为 nil 或未配置 master key 时,涉及 secret 引用的保存返回 ErrSettingsVaultUnconfigured。
//
// 不在此做任何重活(无 init() 副作用,避免抬高空载内存)。
func NewSettingsService(db *sql.DB, v vault.Vault) SettingsService {
	return &settingsService{db: db, vault: v}
}

func (s *settingsService) Get(ctx context.Context, projectID string) (*Settings, error) {
	st, err := s.load(ctx, projectID)
	if err == nil {
		return st, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// 首次访问无配置:惰性生成默认。
	return s.createDefault(ctx, projectID)
}

func (s *settingsService) Save(ctx context.Context, projectID string, in SettingsInput) (*Settings, error) {
	// 确保配置已存在(惰性默认);Get 也完成项目存在性校验。
	if _, err := s.Get(ctx, projectID); err != nil {
		return nil, err
	}

	build, err := s.normalizeBuild(in.Build)
	if err != nil {
		return nil, err
	}
	envs, err := s.normalizeEnvironments(in.Environments)
	if err != nil {
		return nil, err
	}

	// 持久化前剥离掩码字段(掩码绝不入库;明文 secret 从不存在于此结构)。
	buildJSON, err := json.Marshal(toStoredBuild(build))
	if err != nil {
		return nil, fmt.Errorf("pipeline: marshal build: %w", err)
	}
	envsJSON, err := json.Marshal(toStoredEnvironments(envs))
	if err != nil {
		return nil, fmt.Errorf("pipeline: marshal environments: %w", err)
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`UPDATE pipeline_settings
		 SET build_json = ?, environments_json = ?, updated_at = ?
		 WHERE project_id = ?`,
		string(buildJSON), string(envsJSON), nowStr, projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("pipeline: update settings: %w", err)
	}
	return s.Get(ctx, projectID)
}

// load 读取已存在的配置行并回算掩码。无行 → sql.ErrNoRows(由 Get 转惰性默认)。
func (s *settingsService) load(ctx context.Context, projectID string) (*Settings, error) {
	var (
		buildJSON  string
		envsJSON   string
		updatedStr string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT build_json, environments_json, updated_at
		 FROM pipeline_settings WHERE project_id = ?`, projectID,
	).Scan(&buildJSON, &envsJSON, &updatedStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("pipeline: load settings: %w", err)
	}

	var build BuildConfig
	if strings.TrimSpace(buildJSON) != "" {
		if err := json.Unmarshal([]byte(buildJSON), &build); err != nil {
			return nil, fmt.Errorf("pipeline: parse build: %w", err)
		}
	}
	var envs []Environment
	if strings.TrimSpace(envsJSON) != "" {
		if err := json.Unmarshal([]byte(envsJSON), &envs); err != nil {
			return nil, fmt.Errorf("pipeline: parse environments: %w", err)
		}
	}

	updated, err := time.Parse(time.RFC3339, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("pipeline: parse updated_at: %w", err)
	}

	st := &Settings{Build: build, Environments: envs, UpdatedAt: updated}
	normalizeSettingsShape(st)
	// 回算掩码(读取库内引用对应保险库 masked_value;明文 secret 从不入库)。
	if err := s.applyMasks(ctx, st); err != nil {
		return nil, err
	}
	return st, nil
}

// createDefault 惰性生成默认构建/部署配置(模型 dockerfile / 产物 image / 空变量 / 空环境)。
// 项目不存在 → ErrProjectNotFound。并发首访 ON CONFLICT DO NOTHING + 回读权威行。
func (s *settingsService) createDefault(ctx context.Context, projectID string) (*Settings, error) {
	defaultBuild := BuildConfig{
		Model:          BuildModelDockerfile,
		DockerfilePath: defaultDockerfilePath,
		Toolchain:      Toolchain{},
		ArtifactType:   ArtifactImage,
		Vars:           []BuildVar{},
		Cache:          Cache{Enabled: false, Paths: []string{}},
	}
	buildJSON, err := json.Marshal(toStoredBuild(defaultBuild))
	if err != nil {
		return nil, fmt.Errorf("pipeline: marshal default build: %w", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO pipeline_settings (project_id, build_json, environments_json, created_at, updated_at)
		 VALUES (?, ?, '[]', ?, ?)
		 ON CONFLICT(project_id) DO NOTHING`,
		projectID, string(buildJSON), nowStr, nowStr,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("pipeline: insert default settings: %w", err)
	}

	// 不管本协程是否赢得插入,统一回读权威行(并发竞态下另一协程可能已写入)。
	st, lerr := s.load(ctx, projectID)
	if lerr != nil {
		// 极端情况下行被并发删除:退化为内存默认(避免 500)。
		if errors.Is(lerr, sql.ErrNoRows) {
			return &Settings{Build: defaultBuild, Environments: []Environment{}, UpdatedAt: now}, nil
		}
		return nil, lerr
	}
	return st, nil
}

// normalizeBuild 校验并规范化构建配置。
func (s *settingsService) normalizeBuild(in BuildConfig) (BuildConfig, error) {
	model := strings.TrimSpace(in.Model)
	if model == "" {
		model = BuildModelDockerfile
	}
	if model != BuildModelDockerfile && model != BuildModelToolchain {
		return BuildConfig{}, fmt.Errorf("%w: invalid model %q", ErrInvalidBuild, in.Model)
	}
	artifact := strings.TrimSpace(in.ArtifactType)
	if artifact == "" {
		artifact = ArtifactImage
	}
	if artifact != ArtifactImage && artifact != ArtifactJAR && artifact != ArtifactDist {
		return BuildConfig{}, fmt.Errorf("%w: invalid artifactType %q", ErrInvalidBuild, in.ArtifactType)
	}

	dockerfilePath := strings.TrimSpace(in.DockerfilePath)
	if model == BuildModelDockerfile && dockerfilePath == "" {
		dockerfilePath = defaultDockerfilePath
	}

	vars, err := s.normalizeVars(in.Vars)
	if err != nil {
		return BuildConfig{}, err
	}

	paths := make([]string, 0, len(in.Cache.Paths))
	for _, p := range in.Cache.Paths {
		p = strings.TrimSpace(p)
		if p != "" {
			paths = append(paths, p)
		}
	}

	return BuildConfig{
		Model:          model,
		DockerfilePath: dockerfilePath,
		Toolchain: Toolchain{
			Language: strings.TrimSpace(in.Toolchain.Language),
			Version:  strings.TrimSpace(in.Toolchain.Version),
		},
		ArtifactType: artifact,
		Vars:         vars,
		Cache:        Cache{Enabled: in.Cache.Enabled, Paths: paths},
	}, nil
}

// normalizeEnvironments 校验并规范化环境定义列表。
func (s *settingsService) normalizeEnvironments(in []Environment) ([]Environment, error) {
	out := make([]Environment, 0, len(in))
	for _, e := range in {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: environment name must not be empty", ErrInvalidEnvironment)
		}
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = uuid.NewString()
		}

		// 目标服务器引用:本期仅校验字符串非空(存在性留 4-1,仿 trigger)。
		ids := make([]string, 0, len(e.TargetServerIDs))
		for _, sid := range e.TargetServerIDs {
			sid = strings.TrimSpace(sid)
			if sid == "" {
				return nil, fmt.Errorf("%w: target server id must not be empty", ErrInvalidEnvironment)
			}
			ids = append(ids, sid)
		}

		envVars, err := s.normalizeVars(e.EnvVars)
		if err != nil {
			return nil, err
		}

		registry, err := s.normalizeRegistry(e.ImageRegistry)
		if err != nil {
			return nil, err
		}

		out = append(out, Environment{
			ID:              id,
			Name:            name,
			TargetServerIDs: ids,
			EnvVars:         envVars,
			ImageRegistry:   registry,
		})
	}
	return out, nil
}

// normalizeRegistry 校验并规范化镜像仓库绑定。空类型(未绑定)直接放行。
func (s *settingsService) normalizeRegistry(in ImageRegistry) (ImageRegistry, error) {
	regType := strings.TrimSpace(in.Type)
	url := strings.TrimSpace(in.URL)
	credID := strings.TrimSpace(in.CredentialID)

	// 完全未绑定:三字段皆空 → 视为空仓库,放行。
	if regType == "" && url == "" && credID == "" {
		return ImageRegistry{}, nil
	}
	if regType != RegistryHarbor && regType != RegistryACR && regType != RegistryDockerhub && regType != RegistryCustom {
		return ImageRegistry{}, fmt.Errorf("%w: invalid imageRegistry type %q", ErrInvalidEnvironment, in.Type)
	}
	// 镜像仓库凭据为 secret 引用:存在则经保险库校验存在性。
	if credID != "" {
		if err := s.ensureCredentialExists(credID); err != nil {
			return ImageRegistry{}, err
		}
	}
	return ImageRegistry{Type: regType, URL: url, CredentialID: credID}, nil
}

// normalizeVars 校验并规范化一组变量(构建变量或环境变量,同一作用域)。
// key 非空且作用域内不重复;secret 项只保留 credentialId 引用(明文绝不留存),
// 并经保险库校验该引用存在;非 secret 项保留明文 value。
func (s *settingsService) normalizeVars(in []BuildVar) ([]BuildVar, error) {
	out := make([]BuildVar, 0, len(in))
	seenKeys := make(map[string]struct{}, len(in))
	for _, v := range in {
		key := strings.TrimSpace(v.Key)
		if key == "" {
			return nil, fmt.Errorf("%w: variable key must not be empty", ErrInvalidVar)
		}
		if _, dup := seenKeys[key]; dup {
			return nil, fmt.Errorf("%w: duplicate key %q", ErrInvalidVar, key)
		}
		seenKeys[key] = struct{}{}

		id := strings.TrimSpace(v.ID)
		if id == "" {
			id = uuid.NewString()
		}

		nv := BuildVar{ID: id, Key: key, Secret: v.Secret}
		if v.Secret {
			credID := strings.TrimSpace(v.CredentialID)
			if credID == "" {
				return nil, fmt.Errorf("%w: secret variable %q requires a credentialId", ErrInvalidVar, key)
			}
			if err := s.ensureCredentialExists(credID); err != nil {
				return nil, err
			}
			nv.CredentialID = credID
			// 明文 secret 绝不留存:不读 v.Value。
		} else {
			nv.Value = v.Value
		}
		out = append(out, nv)
	}
	return out, nil
}

// ensureCredentialExists 经保险库校验 credentialId 存在性(不泄漏明文)。
// 保险库未配置 → ErrSettingsVaultUnconfigured;不存在 → ErrCredentialNotFound。
func (s *settingsService) ensureCredentialExists(credID string) error {
	if s.vault == nil {
		return ErrSettingsVaultUnconfigured
	}
	// 仅校验存在性(Exists 不解密、不刷新 last_used_at):保存配置只是「引用」凭据而非「使用」。
	// 用 Get 会无谓解密并把所有被引用 secret 标记为「最近使用」,语义失真且扩大解密面。
	ok, err := s.vault.Exists(credID)
	if err != nil {
		if errors.Is(err, vault.ErrVaultUnconfigured) {
			return ErrSettingsVaultUnconfigured
		}
		// 其它查询错误:不泄漏细节,按未配置态处理。
		return ErrSettingsVaultUnconfigured
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrCredentialNotFound, credID)
	}
	return nil
}

// applyMasks 为所有 secret 引用回算掩码(读 credentials.masked_value;明文 secret 从不入库)。
// 引用对应凭据已被删除时,掩码退化为通用点掩码(不报错,UI 仍能提示丢失)。
func (s *settingsService) applyMasks(ctx context.Context, st *Settings) error {
	for i := range st.Build.Vars {
		if st.Build.Vars[i].Secret {
			masked, err := s.maskedFor(ctx, st.Build.Vars[i].CredentialID)
			if err != nil {
				return err
			}
			st.Build.Vars[i].MaskedValue = masked
		}
	}
	for ei := range st.Environments {
		for vi := range st.Environments[ei].EnvVars {
			if st.Environments[ei].EnvVars[vi].Secret {
				masked, err := s.maskedFor(ctx, st.Environments[ei].EnvVars[vi].CredentialID)
				if err != nil {
					return err
				}
				st.Environments[ei].EnvVars[vi].MaskedValue = masked
			}
		}
		if st.Environments[ei].ImageRegistry.CredentialID != "" {
			masked, err := s.maskedFor(ctx, st.Environments[ei].ImageRegistry.CredentialID)
			if err != nil {
				return err
			}
			st.Environments[ei].ImageRegistry.MaskedCredential = masked
		}
	}
	return nil
}

// genericMask 是凭据缺失时的兜底掩码(绝不泄漏任何明文)。
const genericMask = "••••"

// maskedFor 读取 credentialId 对应保险库凭据的 masked_value(经参数化 SQL)。
// 凭据已不存在 → 返回通用点掩码(不阻断读取)。masked_value 由保险库写入时算好,绝非明文。
func (s *settingsService) maskedFor(ctx context.Context, credID string) (string, error) {
	if strings.TrimSpace(credID) == "" {
		return genericMask, nil
	}
	var masked string
	err := s.db.QueryRowContext(ctx,
		`SELECT masked_value FROM credentials WHERE id = ?`, credID,
	).Scan(&masked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return genericMask, nil
		}
		return "", fmt.Errorf("pipeline: load credential mask: %w", err)
	}
	if strings.TrimSpace(masked) == "" {
		return genericMask, nil
	}
	return masked, nil
}

// ---- 持久化形状(剥离掩码;明文 secret 从不存在于此结构) ----

type storedVar struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Secret       bool   `json:"secret"`
	Value        string `json:"value,omitempty"`
	CredentialID string `json:"credentialId,omitempty"`
}

type storedBuild struct {
	Model          string      `json:"model"`
	DockerfilePath string      `json:"dockerfilePath"`
	Toolchain      Toolchain   `json:"toolchain"`
	ArtifactType   string      `json:"artifactType"`
	Vars           []storedVar `json:"vars"`
	Cache          Cache       `json:"cache"`
}

type storedRegistry struct {
	Type         string `json:"type"`
	URL          string `json:"url"`
	CredentialID string `json:"credentialId,omitempty"`
}

type storedEnvironment struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	TargetServerIDs []string       `json:"targetServerIds"`
	EnvVars         []storedVar    `json:"envVars"`
	ImageRegistry   storedRegistry `json:"imageRegistry"`
}

func toStoredVars(in []BuildVar) []storedVar {
	out := make([]storedVar, 0, len(in))
	for _, v := range in {
		sv := storedVar{ID: v.ID, Key: v.Key, Secret: v.Secret}
		if v.Secret {
			sv.CredentialID = v.CredentialID // 仅引用,绝无明文
		} else {
			sv.Value = v.Value
		}
		out = append(out, sv)
	}
	return out
}

func toStoredBuild(b BuildConfig) storedBuild {
	paths := b.Cache.Paths
	if paths == nil {
		paths = []string{}
	}
	return storedBuild{
		Model:          b.Model,
		DockerfilePath: b.DockerfilePath,
		Toolchain:      b.Toolchain,
		ArtifactType:   b.ArtifactType,
		Vars:           toStoredVars(b.Vars),
		Cache:          Cache{Enabled: b.Cache.Enabled, Paths: paths},
	}
}

func toStoredEnvironments(in []Environment) []storedEnvironment {
	out := make([]storedEnvironment, 0, len(in))
	for _, e := range in {
		ids := e.TargetServerIDs
		if ids == nil {
			ids = []string{}
		}
		out = append(out, storedEnvironment{
			ID:              e.ID,
			Name:            e.Name,
			TargetServerIDs: ids,
			EnvVars:         toStoredVars(e.EnvVars),
			ImageRegistry: storedRegistry{
				Type:         e.ImageRegistry.Type,
				URL:          e.ImageRegistry.URL,
				CredentialID: e.ImageRegistry.CredentialID,
			},
		})
	}
	return out
}

// normalizeSettingsShape 保证从库回读的切片非 nil(JSON 输出 [] 而非 null)。
func normalizeSettingsShape(st *Settings) {
	if st.Build.Vars == nil {
		st.Build.Vars = []BuildVar{}
	}
	if st.Build.Cache.Paths == nil {
		st.Build.Cache.Paths = []string{}
	}
	if st.Environments == nil {
		st.Environments = []Environment{}
	}
	for i := range st.Environments {
		if st.Environments[i].TargetServerIDs == nil {
			st.Environments[i].TargetServerIDs = []string{}
		}
		if st.Environments[i].EnvVars == nil {
			st.Environments[i].EnvVars = []BuildVar{}
		}
	}
}
