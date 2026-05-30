package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/pipeline"
)

// ---- 构建/部署配置 DTO(冻结契约;camelCase;secret 项绝无明文,仅 credentialId + maskedValue) ----

type buildVarDTO struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Secret       bool   `json:"secret"`
	Value        string `json:"value,omitempty"`
	CredentialID string `json:"credentialId,omitempty"`
	MaskedValue  string `json:"maskedValue,omitempty"`
}

type cacheDTO struct {
	Enabled bool     `json:"enabled"`
	Paths   []string `json:"paths"`
}

type toolchainDTO struct {
	Language string `json:"language"`
	Version  string `json:"version"`
}

type buildConfigDTO struct {
	Model          string        `json:"model"`
	DockerfilePath string        `json:"dockerfilePath"`
	Toolchain      toolchainDTO  `json:"toolchain"`
	ArtifactType   string        `json:"artifactType"`
	Vars           []buildVarDTO `json:"vars"`
	Cache          cacheDTO      `json:"cache"`
}

type imageRegistryDTO struct {
	Type             string `json:"type"`
	URL              string `json:"url"`
	CredentialID     string `json:"credentialId,omitempty"`
	MaskedCredential string `json:"maskedCredential,omitempty"`
}

type environmentDTO struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	TargetServerIDs []string         `json:"targetServerIds"`
	EnvVars         []buildVarDTO    `json:"envVars"`
	ImageRegistry   imageRegistryDTO `json:"imageRegistry"`
}

type settingsDTO struct {
	Build        buildConfigDTO   `json:"build"`
	Environments []environmentDTO `json:"environments"`
	UpdatedAt    string           `json:"updatedAt"`
}

// toBuildVarDTOs 把领域变量转 DTO(secret 项只暴露 credentialId + maskedValue,绝无明文)。
func toBuildVarDTOs(in []pipeline.BuildVar) []buildVarDTO {
	out := make([]buildVarDTO, 0, len(in))
	for _, v := range in {
		dto := buildVarDTO{ID: v.ID, Key: v.Key, Secret: v.Secret}
		if v.Secret {
			dto.CredentialID = v.CredentialID
			dto.MaskedValue = v.MaskedValue
		} else {
			dto.Value = v.Value
		}
		out = append(out, dto)
	}
	return out
}

// toSettingsDTO 把领域 Settings 转契约 DTO(切片保证非 nil;secret 仅掩码/引用)。
func toSettingsDTO(st *pipeline.Settings) settingsDTO {
	paths := st.Build.Cache.Paths
	if paths == nil {
		paths = []string{}
	}
	build := buildConfigDTO{
		Model:          st.Build.Model,
		DockerfilePath: st.Build.DockerfilePath,
		Toolchain:      toolchainDTO{Language: st.Build.Toolchain.Language, Version: st.Build.Toolchain.Version},
		ArtifactType:   st.Build.ArtifactType,
		Vars:           toBuildVarDTOs(st.Build.Vars),
		Cache:          cacheDTO{Enabled: st.Build.Cache.Enabled, Paths: paths},
	}

	envs := make([]environmentDTO, 0, len(st.Environments))
	for _, e := range st.Environments {
		ids := e.TargetServerIDs
		if ids == nil {
			ids = []string{}
		}
		envs = append(envs, environmentDTO{
			ID:              e.ID,
			Name:            e.Name,
			TargetServerIDs: ids,
			EnvVars:         toBuildVarDTOs(e.EnvVars),
			ImageRegistry: imageRegistryDTO{
				Type:             e.ImageRegistry.Type,
				URL:              e.ImageRegistry.URL,
				CredentialID:     e.ImageRegistry.CredentialID,
				MaskedCredential: e.ImageRegistry.MaskedCredential,
			},
		})
	}

	return settingsDTO{
		Build:        build,
		Environments: envs,
		UpdatedAt:    st.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writeSettingsError 把领域错误映射为契约错误码/状态码;绝不回显明文。
func writeSettingsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pipeline.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, pipeline.ErrSettingsVaultUnconfigured):
		writeError(w, http.StatusUnprocessableEntity, "vault_unconfigured", "保险库未配置 master key,无法校验或引用 secret 凭据")
	case errors.Is(err, pipeline.ErrCredentialNotFound):
		writeError(w, http.StatusUnprocessableEntity, "credential_not_found", "引用的保险库凭据不存在")
	case errors.Is(err, pipeline.ErrInvalidBuild):
		writeError(w, http.StatusUnprocessableEntity, "invalid_build", "构建模型必须为 dockerfile/toolchain,产物类型必须为 image/jar/dist")
	case errors.Is(err, pipeline.ErrInvalidVar):
		writeError(w, http.StatusUnprocessableEntity, "invalid_var", "变量键不能为空且同作用域内不可重复,secret 项须指定保险库凭据")
	case errors.Is(err, pipeline.ErrInvalidEnvironment):
		writeError(w, http.StatusUnprocessableEntity, "invalid_environment", "环境名不能为空,镜像仓库类型须为 harbor/acr/dockerhub/custom")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeGetPipelineSettingsHandler 返回 GET /api/projects/{id}/pipeline/settings handler。
// 首次访问无配置 → 惰性生成默认(模型 dockerfile/产物 image/空变量/空环境)并返回;secret 仅掩码。
func makeGetPipelineSettingsHandler(svc pipeline.SettingsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "构建/部署配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		st, err := svc.Get(r.Context(), id)
		if err != nil {
			writeSettingsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSettingsDTO(st))
	}
}

// makeSavePipelineSettingsHandler 返回 PUT /api/projects/{id}/pipeline/settings handler。
// 收 {build, environments};secret 项只传 {key,secret:true,credentialId}(不传明文/掩码)。
// 服务端规范化 → 校验 → 持久化(只存引用,绝无明文 secret)→ 回读返回(secret 回掩码)。
// 校验失败 → 422 定位到项;请求体限 256KB。
func makeSavePipelineSettingsHandler(svc pipeline.SettingsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "构建/部署配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<18) // 256KB

		var req struct {
			Build struct {
				Model          string `json:"model"`
				DockerfilePath string `json:"dockerfilePath"`
				Toolchain      struct {
					Language string `json:"language"`
					Version  string `json:"version"`
				} `json:"toolchain"`
				ArtifactType string   `json:"artifactType"`
				Vars         []reqVar `json:"vars"`
				Cache        struct {
					Enabled bool     `json:"enabled"`
					Paths   []string `json:"paths"`
				} `json:"cache"`
			} `json:"build"`
			Environments []struct {
				ID              string   `json:"id"`
				Name            string   `json:"name"`
				TargetServerIDs []string `json:"targetServerIds"`
				EnvVars         []reqVar `json:"envVars"`
				ImageRegistry   struct {
					Type         string `json:"type"`
					URL          string `json:"url"`
					CredentialID string `json:"credentialId"`
				} `json:"imageRegistry"`
			} `json:"environments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		build := pipeline.BuildConfig{
			Model:          req.Build.Model,
			DockerfilePath: req.Build.DockerfilePath,
			Toolchain: pipeline.Toolchain{
				Language: req.Build.Toolchain.Language,
				Version:  req.Build.Toolchain.Version,
			},
			ArtifactType: req.Build.ArtifactType,
			Vars:         toDomainVars(req.Build.Vars),
			Cache:        pipeline.Cache{Enabled: req.Build.Cache.Enabled, Paths: req.Build.Cache.Paths},
		}

		envs := make([]pipeline.Environment, 0, len(req.Environments))
		for _, e := range req.Environments {
			envs = append(envs, pipeline.Environment{
				ID:              e.ID,
				Name:            e.Name,
				TargetServerIDs: e.TargetServerIDs,
				EnvVars:         toDomainVars(e.EnvVars),
				ImageRegistry: pipeline.ImageRegistry{
					Type:         e.ImageRegistry.Type,
					URL:          e.ImageRegistry.URL,
					CredentialID: e.ImageRegistry.CredentialID,
				},
			})
		}

		st, err := svc.Save(r.Context(), id, pipeline.SettingsInput{Build: build, Environments: envs})
		if err != nil {
			writeSettingsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSettingsDTO(st))
	}
}

// reqVar 是请求里的一条变量(secret 项只传 credentialId;明文 value 仅 secret=false 时有意义)。
type reqVar struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Secret       bool   `json:"secret"`
	Value        string `json:"value"`
	CredentialID string `json:"credentialId"`
}

// toDomainVars 把请求变量转领域变量(secret 项的明文 value 在领域层会被丢弃,不入库)。
func toDomainVars(in []reqVar) []pipeline.BuildVar {
	out := make([]pipeline.BuildVar, 0, len(in))
	for _, v := range in {
		out = append(out, pipeline.BuildVar{
			ID:           v.ID,
			Key:          v.Key,
			Secret:       v.Secret,
			Value:        v.Value,
			CredentialID: v.CredentialID,
		})
	}
	return out
}
