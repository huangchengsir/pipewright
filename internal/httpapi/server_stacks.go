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

// Compose / Stacks 管理(Portainer 式)。经 SSH 跑 `docker compose`(v2 插件),目标机零侵入。
//
// AC-SEC-02:列表只读;项目名经严格白名单(reDockerTgt:首字符非 `-`、无 shell 元字符、无 `/`
// 不能路径穿越),命令 array 化不拼 shell。部署的 compose 内容经 Upload 以**文件**落到受管目录
// (非 shell 注入面),再 `docker compose -f <path> up -d`。写操作过 CSRF + 审计(detail 仅
// 项目名 + 动作,不含 compose 内容)。

const (
	stacksCmdTimeout = 20 * time.Second
	stacksUpTimeout  = 8 * time.Minute // up 可能拉镜像 + 构建,给充裕超时
	// stacksBaseDir 是受管 compose 目录基址。部署时在其下按项目名建子目录存 docker-compose.yml,
	// 使 `compose ls` 可见、后续可重新 up。需运行 SSH 用户对该目录可写(root 默认可)。
	stacksBaseDir   = "/opt/pipewright/stacks"
	composeFileName = "docker-compose.yml"
	composeMaxBytes = 512 << 10 // 512 KiB compose 上限
)

var cmdComposeLs = []string{"docker", "compose", "ls", "--all", "--format", "json"}

// stackDTO 是单个 compose 项目的展示 DTO(冻结契约)。
type stackDTO struct {
	Name        string `json:"name"`
	Status      string `json:"status"`      // 如 "running(2)" / "exited(1)"
	ConfigFiles string `json:"configFiles"` // compose 文件路径
}

type serverStacksDTO struct {
	ServerID    string     `json:"serverId"`
	Reachable   bool       `json:"reachable"`
	Runtime     string     `json:"runtime"`
	Error       string     `json:"error"`
	Stacks      []stackDTO `json:"stacks"`
	CollectedAt string     `json:"collectedAt"`
}

// composeLsLine 映射 `docker compose ls --format json` 的元素(JSON 数组)。
type composeLsLine struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

func parseStacks(out string) []stackDTO {
	list := []stackDTO{}
	out = strings.TrimSpace(out)
	if out == "" {
		return list
	}
	var arr []composeLsLine
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		// 个别 docker 版本逐行 JSON;回退逐行解析。
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var p composeLsLine
			if json.Unmarshal([]byte(line), &p) == nil && p.Name != "" {
				list = append(list, stackDTO{Name: p.Name, Status: p.Status, ConfigFiles: p.ConfigFiles})
			}
		}
		return list
	}
	for _, p := range arr {
		list = append(list, stackDTO{Name: p.Name, Status: p.Status, ConfigFiles: p.ConfigFiles})
	}
	return list
}

func collectServerStacks(ctx context.Context, svc target.Service, id string) (serverStacksDTO, error) {
	out := serverStacksDTO{ServerID: id, Stacks: []stackDTO{}, CollectedAt: time.Now().UTC().Format(time.RFC3339)}
	cctx, cancel := context.WithTimeout(ctx, stacksCmdTimeout)
	defer cancel()

	res, err := svc.Exec(cctx, id, cmdComposeLs)
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
		// docker 未装 / 无 compose 插件 / 无权限。
		out.Error = "未检测到 docker compose(需 docker 与 compose v2 插件,且当前用户有权限)"
		return out, nil
	}
	out.Runtime = "docker"
	out.Stacks = parseStacks(res.Stdout)
	return out, nil
}

// makeServerStacksHandler 返回 GET /api/servers/{id}/stacks(认证,只读)。
func makeServerStacksHandler(svc target.Service) http.HandlerFunc {
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
		out, locErr := collectServerStacks(r.Context(), svc, id)
		if locErr != nil {
			writeServerError(w, locErr)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// stackActionRequest 是 update/down/start/stop/restart 请求体。
type stackActionRequest struct {
	Name       string `json:"name"`
	Action     string `json:"action"`               // start|stop|restart|down|update
	ConfigFile string `json:"configFile,omitempty"` // action=update 必填:compose ls 给的配置文件路径
}

type stackDTOResult struct {
	ServerID string `json:"serverId"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	Error    string `json:"error"`
}

var stackActions = map[string]bool{"start": true, "stop": true, "restart": true, "down": true, "update": true}

// composeFileArgs 把 compose ls 给的 ConfigFile(可能逗号分隔多文件)拆成 `-f <path>` 参数对。
// 每个路径须以 `/` 开头(绝对路径,杜绝被当 flag 注入)、无控制字符;经 array 传参不拼 shell。
func composeFileArgs(configFile string) ([]string, error) {
	var args []string
	for _, p := range strings.Split(configFile, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > 512 || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\n\r\x00") {
			return nil, errors.New("非法 compose 文件路径")
		}
		args = append(args, "-f", p)
	}
	if len(args) == 0 {
		return nil, errors.New("缺少 compose 文件路径(该 Stack 无可用配置,无法更新)")
	}
	return args, nil
}

// makeStackActionHandler 返回 POST /api/servers/{id}/stacks/action(认证 + CSRF;写)。
// 对已有项目按 project name(标签)操作,无需 compose 文件。
func makeStackActionHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req stackActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if !reDockerTgt.MatchString(req.Name) || len(req.Name) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_stack", "项目名非法")
			return
		}
		if !stackActions[req.Action] {
			writeError(w, http.StatusBadRequest, "invalid_stack", "非法动作(start/stop/restart/down)")
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		// 构造命令 + 超时:
		//   - update:`docker compose -p <name> -f <cfg>... up -d --pull always`(拉新镜像 + 重建,
		//     即「升级」);需 compose ls 给的配置文件路径,给 up 的长超时。
		//   - 其余:`docker compose -p <name> <action>`(down/start/stop/restart 按项目标签操作)。
		var cmd []string
		timeout := stacksCmdTimeout
		if req.Action == "update" {
			fileArgs, err := composeFileArgs(req.ConfigFile)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_stack", err.Error())
				return
			}
			cmd = append([]string{"docker", "compose", "-p", req.Name}, fileArgs...)
			cmd = append(cmd, "up", "-d", "--pull", "always")
			timeout = stacksUpTimeout
		} else {
			cmd = []string{"docker", "compose", "-p", req.Name, req.Action}
		}
		out := stackDTOResult{ServerID: id, Name: req.Name, Action: req.Action}
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor: auditActor, Action: audit.ActionStackOp, TargetType: audit.TargetServer, TargetID: id,
				Detail: map[string]any{"name": req.Name, "action": req.Action, "ok": ok}, IP: clientIP(r),
			})
		}
		cctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		res, err := svc.Exec(cctx, id, cmd)
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
				msg = "docker compose " + req.Action + " 以非零状态退出"
			}
			out.OK = false
			out.Error = truncateLog(msg, 1024)
		} else {
			out.OK = true
			out.Output = truncateLog(strings.TrimSpace(res.Stdout), 2048)
		}
		auditOp(out.OK)
		writeJSON(w, http.StatusOK, out)
	}
}

// stackDeployRequest 是部署(贴 compose yaml → up -d)请求体。
type stackDeployRequest struct {
	Name    string `json:"name"`
	Compose string `json:"compose"` // 完整 docker-compose.yml 文本
}

// makeStackDeployHandler 返回 POST /api/servers/{id}/stacks/deploy(认证 + CSRF;写)。
// mkdir 受管目录 → Upload compose 文件 → `docker compose -p <name> -f <path> up -d`。
func makeStackDeployHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req stackDeployRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if !reDockerTgt.MatchString(req.Name) || len(req.Name) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_stack", "项目名非法(仅字母数字与 . _ -,不以 - 开头)")
			return
		}
		compose := strings.TrimSpace(req.Compose)
		if compose == "" {
			writeError(w, http.StatusBadRequest, "invalid_stack", "compose 内容不能为空")
			return
		}
		if len(compose) > composeMaxBytes {
			writeError(w, http.StatusBadRequest, "invalid_stack", "compose 内容过大")
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		// 受管目录:基址 + 项目名(name 正则已排除 `/` 与前导 `.`,无路径穿越)。
		dir := stacksBaseDir + "/" + req.Name
		composePath := dir + "/" + composeFileName
		out := stackDTOResult{ServerID: id, Name: req.Name, Action: "deploy"}
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor: auditActor, Action: audit.ActionStackOp, TargetType: audit.TargetServer, TargetID: id,
				Detail: map[string]any{"name": req.Name, "action": "deploy", "ok": ok}, IP: clientIP(r),
			})
		}

		cctx, cancel := context.WithTimeout(r.Context(), stacksUpTimeout)
		defer cancel()

		// 1) 建目录。
		if res, err := svc.Exec(cctx, id, []string{"mkdir", "-p", dir}); err != nil {
			out.Error = humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		} else if res.ExitCode != 0 {
			out.Error = "创建受管目录失败:" + truncateLog(strings.TrimSpace(res.Stderr), 256)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		// 2) 写 compose 文件(字节流,非 shell)。
		if err := svc.Upload(cctx, id, strings.NewReader(compose), composePath); err != nil {
			out.Error = "写入 compose 文件失败:" + humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		// 3) up -d。
		res, err := svc.Exec(cctx, id, []string{"docker", "compose", "-p", req.Name, "-f", composePath, "up", "-d"})
		if err != nil {
			out.Error = humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = "docker compose up 以非零状态退出"
			}
			out.OK = false
			out.Error = truncateLog(msg, 1024)
		} else {
			out.OK = true
			out.Output = truncateLog(strings.TrimSpace(res.Stdout)+"\n"+strings.TrimSpace(res.Stderr), 2048)
		}
		auditOp(out.OK)
		writeJSON(w, http.StatusOK, out)
	}
}
