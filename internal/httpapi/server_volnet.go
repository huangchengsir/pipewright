package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 数据卷 + 网络管理(Portainer 式):列表 / 创建 / 删除。经 SSH 跑 docker,目标机零侵入。
// AC-SEC-02:名称严格白名单(reDockerTgt:首字符非 `-`、无 shell 元字符),命令 array 化。
// 列表只读;创建/删除写,过 CSRF + 审计。

const volnetCmdTimeout = 15 * time.Second

// ─── 数据卷 ───────────────────────────────────────────────────────────────────

type volumeDTO struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
}
type serverVolumesDTO struct {
	ServerID    string      `json:"serverId"`
	Reachable   bool        `json:"reachable"`
	Runtime     string      `json:"runtime"`
	Error       string      `json:"error"`
	Volumes     []volumeDTO `json:"volumes"`
	CollectedAt string      `json:"collectedAt"`
}
type dockerVolumeLine struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

func parseVolumes(out string) []volumeDTO {
	list := []volumeDTO{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p dockerVolumeLine
		if json.Unmarshal([]byte(line), &p) == nil && p.Name != "" {
			list = append(list, volumeDTO{Name: p.Name, Driver: p.Driver})
		}
	}
	return list
}

func makeServerVolumesHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}
		out := serverVolumesDTO{ServerID: id, Volumes: []volumeDTO{}, CollectedAt: time.Now().UTC().Format(time.RFC3339)}
		cctx, cancel := context.WithTimeout(r.Context(), volnetCmdTimeout)
		defer cancel()
		res, err := svc.Exec(cctx, id, []string{"docker", "volume", "ls", "--format", "{{json .}}"})
		if err != nil {
			out.Reachable = false
			out.Error = humanContainersError(err)
			if isLocateError(err) {
				writeServerError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		out.Reachable = true
		if res.ExitCode != 0 {
			out.Error = "未检测到容器运行时(docker 未安装或当前用户无权限)"
			writeJSON(w, http.StatusOK, out)
			return
		}
		out.Runtime = "docker"
		out.Volumes = parseVolumes(res.Stdout)
		writeJSON(w, http.StatusOK, out)
	}
}

// ─── 网络 ─────────────────────────────────────────────────────────────────────

type networkDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}
type serverNetworksDTO struct {
	ServerID    string       `json:"serverId"`
	Reachable   bool         `json:"reachable"`
	Runtime     string       `json:"runtime"`
	Error       string       `json:"error"`
	Networks    []networkDTO `json:"networks"`
	CollectedAt string       `json:"collectedAt"`
}
type dockerNetworkLine struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Scope  string `json:"Scope"`
}

func parseNetworks(out string) []networkDTO {
	list := []networkDTO{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p dockerNetworkLine
		if json.Unmarshal([]byte(line), &p) == nil && p.Name != "" {
			list = append(list, networkDTO{ID: p.ID, Name: p.Name, Driver: p.Driver, Scope: p.Scope})
		}
	}
	return list
}

func makeServerNetworksHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}
		out := serverNetworksDTO{ServerID: id, Networks: []networkDTO{}, CollectedAt: time.Now().UTC().Format(time.RFC3339)}
		cctx, cancel := context.WithTimeout(r.Context(), volnetCmdTimeout)
		defer cancel()
		res, err := svc.Exec(cctx, id, []string{"docker", "network", "ls", "--format", "{{json .}}"})
		if err != nil {
			out.Reachable = false
			out.Error = humanContainersError(err)
			if isLocateError(err) {
				writeServerError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		out.Reachable = true
		if res.ExitCode != 0 {
			out.Error = "未检测到容器运行时(docker 未安装或当前用户无权限)"
			writeJSON(w, http.StatusOK, out)
			return
		}
		out.Runtime = "docker"
		out.Networks = parseNetworks(res.Stdout)
		writeJSON(w, http.StatusOK, out)
	}
}

// ─── 写操作(创建/删除卷与网络)共用骨架 ────────────────────────────────────────

type volnetActionRequest struct {
	Name string `json:"name"`
}
type volnetActionDTO struct {
	ServerID string `json:"serverId"`
	Action   string `json:"action"`
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
}

// volnetWriteHandler 抽出 volume/network 的 create/rm 共用骨架。
func volnetWriteHandler(svc target.Service, aud audit.Recorder, auditAction, label string, build func(name string) []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req volnetActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if !reDockerTgt.MatchString(req.Name) || len(req.Name) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_name", "名称非法(仅字母数字与 . _ -,不以 - 开头)")
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}
		out := volnetActionDTO{ServerID: id, Action: label, Name: req.Name}
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor: auditActor, Action: auditAction, TargetType: audit.TargetServer, TargetID: id,
				Detail: map[string]any{"action": label, "name": req.Name, "ok": ok}, IP: clientIP(r),
			})
		}
		cctx, cancel := context.WithTimeout(r.Context(), volnetCmdTimeout)
		defer cancel()
		res, err := svc.Exec(cctx, id, build(req.Name))
		if err != nil {
			if errors.Is(err, target.ErrNotFound) {
				writeServerError(w, err)
				return
			}
			if errors.Is(err, target.ErrCredentialNotFound) || errors.Is(err, target.ErrVaultUnconfigured) {
				auditOp(false)
				writeServerError(w, err)
				return
			}
			out.OK = false
			out.Error = humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = "命令以非零状态退出"
			}
			out.OK = false
			out.Error = truncateLog(msg, 1024)
		} else {
			out.OK = true
		}
		auditOp(out.OK)
		writeJSON(w, http.StatusOK, out)
	}
}

func makeVolumeCreateHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return volnetWriteHandler(svc, aud, audit.ActionVolumeOp, "create", func(name string) []string {
		return []string{"docker", "volume", "create", name}
	})
}
func makeVolumeRemoveHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return volnetWriteHandler(svc, aud, audit.ActionVolumeOp, "rm", func(name string) []string {
		return []string{"docker", "volume", "rm", name}
	})
}
func makeNetworkCreateHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return volnetWriteHandler(svc, aud, audit.ActionNetworkOp, "create", func(name string) []string {
		return []string{"docker", "network", "create", name}
	})
}
func makeNetworkRemoveHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return volnetWriteHandler(svc, aud, audit.ActionNetworkOp, "rm", func(name string) []string {
		return []string{"docker", "network", "rm", name}
	})
}
