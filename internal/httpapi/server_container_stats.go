package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 容器实时资源 stats(Portainer 式):一次性采样 `docker stats --no-stream`。经 SSH 跑 docker,
// 目标机零侵入。
//
// AC-SEC-02:命令是纯静态只读 array、不接受任何用户输入,不拼 shell。GET 只读 → 过 auth、
// 豁免 CSRF;某台不可达/无 docker → reachable:false / runtime:"",绝不 500。

// statsCmdTimeout:`docker stats --no-stream` 需先采两帧算 CPU 差值,可能比纯列表慢一点,给 20s。
const statsCmdTimeout = 20 * time.Second

// cmdContainerStats 是固定的只读采样命令(无用户输入)。
var cmdContainerStats = []string{"docker", "stats", "--no-stream", "--format", "{{json .}}"}

// containerStatDTO 是单个容器的实时资源样本(冻结契约)。
type containerStatDTO struct {
	Name     string `json:"name"`     // 容器名(docker stats 的 Name 字段)
	CpuPerc  string `json:"cpuPerc"`  // CPU 百分比,如 "0.15%"
	MemUsage string `json:"memUsage"` // 内存用量/上限,如 "12.5MiB / 1.94GiB"
	MemPerc  string `json:"memPerc"`  // 内存百分比,如 "0.63%"
	NetIO    string `json:"netIO"`    // 网络 IO,如 "1.2kB / 0B"
	BlockIO  string `json:"blockIO"`  // 块设备 IO,如 "0B / 0B"
}

// serverContainerStatsDTO 是单台服务器容器 stats 响应体(冻结契约)。
type serverContainerStatsDTO struct {
	ServerID    string             `json:"serverId"`
	Reachable   bool               `json:"reachable"`
	Runtime     string             `json:"runtime"`
	Error       string             `json:"error"`
	Stats       []containerStatDTO `json:"stats"`
	CollectedAt string             `json:"collectedAt"`
}

// dockerStatsLine 对应 `docker stats --format '{{json .}}'` 的逐行 JSON。
type dockerStatsLine struct {
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
}

// parseContainerStats 逐行解析 docker stats 的 JSON 输出;脏行(空/非 JSON)跳过。
func parseContainerStats(out string) []containerStatDTO {
	list := []containerStatDTO{}
	if len(out) > imagesOutMax {
		out = out[:imagesOutMax]
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p dockerStatsLine
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue
		}
		list = append(list, containerStatDTO{
			Name:     p.Name,
			CpuPerc:  p.CPUPerc,
			MemUsage: p.MemUsage,
			MemPerc:  p.MemPerc,
			NetIO:    p.NetIO,
			BlockIO:  p.BlockIO,
		})
	}
	return list
}

func collectServerContainerStats(ctx context.Context, svc target.Service, id string) (serverContainerStatsDTO, error) {
	out := serverContainerStatsDTO{ServerID: id, Stats: []containerStatDTO{}, CollectedAt: time.Now().UTC().Format(time.RFC3339)}
	cctx, cancel := context.WithTimeout(ctx, statsCmdTimeout)
	defer cancel()

	res, err := svc.Exec(cctx, id, cmdContainerStats)
	if err != nil {
		out.Reachable = false
		out.Error = humanContainersError(err)
		if isLocateError(err) {
			return out, err
		}
		return out, nil
	}
	out.Reachable = true
	if res.ExitCode != 0 {
		out.Error = "未检测到容器运行时(docker 未安装或当前用户无权限)"
		return out, nil
	}
	out.Runtime = "docker"
	out.Stats = parseContainerStats(res.Stdout)
	return out, nil
}

// makeServerContainerStatsHandler 返回 GET /api/servers/{id}/containers/stats(认证,只读)。
func makeServerContainerStatsHandler(svc target.Service) http.HandlerFunc {
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
		out, locErr := collectServerContainerStats(r.Context(), svc, id)
		if locErr != nil {
			writeServerError(w, locErr)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
