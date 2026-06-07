package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
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

// 列 compose 项目 **不用** `docker compose ls`(那是 v2 插件专属;装 v1 docker-compose 或
// 无 v2 插件的主机会扫不到)。改为按容器的 compose 标签 `com.docker.compose.project` 聚合 ——
// v1/v2 通吃,且不依赖 compose CLI。`|||` 作分隔(项目名/路径不含此串)。
// 用 {{.Status}}(老 docker 也有;{{.State}} 是 20.10+ 才有,103 的旧 docker 会报模板错)。
// running 与否由 deriveState(status) 判定(复用 server_containers.go)。
var cmdComposeLabels = []string{
	"docker", "ps", "-a", "--format",
	`{{.Label "com.docker.compose.project"}}|||{{.Label "com.docker.compose.project.config_files"}}|||{{.Status}}`,
}

// stackDTO 是单个 compose 项目的展示 DTO(冻结契约)。
type stackDTO struct {
	Name        string `json:"name"`
	Status      string `json:"status"`      // 如 "running(2)" / "1/2 运行"
	ConfigFiles string `json:"configFiles"` // compose 文件路径(标签里有才填;v1 旧版常为空)
}

type serverStacksDTO struct {
	ServerID    string     `json:"serverId"`
	Reachable   bool       `json:"reachable"`
	Runtime     string     `json:"runtime"`
	Error       string     `json:"error"`
	Stacks      []stackDTO `json:"stacks"`
	CollectedAt string     `json:"collectedAt"`
}

// parseStacks 据 `docker ps -a` 的 compose 标签聚合出项目(按 project 分组,空 project = 裸
// docker run 容器,跳过)。status 给「running(R)」或「R/T 运行」。
func parseStacks(out string) []stackDTO {
	type acc struct {
		running, total int
		cfg            string
	}
	m := map[string]*acc{}
	order := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 3)
		if len(parts) < 3 {
			continue
		}
		proj := strings.TrimSpace(parts[0])
		if proj == "" {
			continue // 非 compose 容器
		}
		a := m[proj]
		if a == nil {
			a = &acc{}
			m[proj] = a
			order = append(order, proj)
		}
		a.total++
		if deriveState(strings.TrimSpace(parts[2])) == "running" {
			a.running++
		}
		if a.cfg == "" {
			if cfg := strings.TrimSpace(parts[1]); cfg != "" {
				a.cfg = cfg
			}
		}
	}
	list := []stackDTO{}
	for _, proj := range order {
		a := m[proj]
		status := fmt.Sprintf("running(%d)", a.running)
		if a.running < a.total {
			status = fmt.Sprintf("%d/%d 运行", a.running, a.total)
		}
		list = append(list, stackDTO{Name: proj, Status: status, ConfigFiles: a.cfg})
	}
	return list
}

func collectServerStacks(ctx context.Context, svc target.Service, id string) (serverStacksDTO, error) {
	out := serverStacksDTO{ServerID: id, Stacks: []stackDTO{}, CollectedAt: time.Now().UTC().Format(time.RFC3339)}
	cctx, cancel := context.WithTimeout(ctx, stacksCmdTimeout)
	defer cancel()

	res, err := svc.Exec(cctx, id, cmdComposeLabels)
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

var reContainerHexID = regexp.MustCompile(`^[a-f0-9]{6,64}$`)

// detectComposeBin 探测可用的 compose CLI:v2 `docker compose` 优先,回退 v1 `docker-compose`。
// 返回基础命令(如 ["docker","compose"] / ["docker-compose"]);都没有返回 nil。
func detectComposeBin(ctx context.Context, svc target.Service, id string) []string {
	if res, err := svc.Exec(ctx, id, []string{"docker", "compose", "version"}); err == nil && res.ExitCode == 0 {
		return []string{"docker", "compose"}
	}
	if res, err := svc.Exec(ctx, id, []string{"docker-compose", "version"}); err == nil && res.ExitCode == 0 {
		return []string{"docker-compose"}
	}
	return nil
}

// projectContainerIDs 取某 compose 项目(按标签)的所有容器 ID(校验为纯十六进制,防注入)。
func projectContainerIDs(ctx context.Context, svc target.Service, id, project string) ([]string, error) {
	res, err := svc.Exec(ctx, id, []string{"docker", "ps", "-aq", "--filter", "label=com.docker.compose.project=" + project})
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, ln := range strings.Fields(res.Stdout) {
		if reContainerHexID.MatchString(ln) {
			ids = append(ids, ln)
		}
	}
	return ids, nil
}

// makeStackActionHandler 返回 POST /api/servers/{id}/stacks/action(认证 + CSRF;写)。
// 版本无关:start/stop/restart/down 按项目标签取容器 ID,用**纯 docker** 操作(不依赖 compose
// CLI,v1/v2 都行);update 需 compose 文件 + compose CLI(v2 docker compose / v1 docker-compose)。
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
			writeError(w, http.StatusBadRequest, "invalid_stack", "非法动作(start/stop/restart/down/update)")
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		out := stackDTOResult{ServerID: id, Name: req.Name, Action: req.Action}
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor: auditActor, Action: audit.ActionStackOp, TargetType: audit.TargetServer, TargetID: id,
				Detail: map[string]any{"name": req.Name, "action": req.Action, "ok": ok}, IP: clientIP(r),
			})
		}
		timeout := stacksCmdTimeout
		if req.Action == "update" {
			timeout = stacksUpTimeout
		}
		cctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		// 构造命令:
		var cmd []string
		if req.Action == "update" {
			bin := detectComposeBin(cctx, svc, id)
			if bin == nil {
				out.OK = false
				out.Error = "该主机未检测到 docker compose / docker-compose,无法更新(可改用重启)"
				auditOp(false)
				writeJSON(w, http.StatusOK, out)
				return
			}
			fileArgs, ferr := composeFileArgs(req.ConfigFile)
			if ferr != nil {
				writeError(w, http.StatusBadRequest, "invalid_stack", ferr.Error())
				return
			}
			cmd = append(append([]string{}, bin...), "-p", req.Name)
			cmd = append(cmd, fileArgs...)
			cmd = append(cmd, "up", "-d", "--pull", "always")
		} else {
			// start/stop/restart/down:按标签取容器 ID,用纯 docker 操作(版本无关)。
			ids, ierr := projectContainerIDs(cctx, svc, id, req.Name)
			if ierr != nil {
				if errors.Is(ierr, target.ErrNotFound) {
					writeServerError(w, ierr)
					return
				}
				out.OK = false
				out.Error = humanServiceError(ierr)
				auditOp(false)
				writeJSON(w, http.StatusOK, out)
				return
			}
			if len(ids) == 0 {
				out.OK = false
				out.Error = "未找到该项目的容器(可能已删除)"
				auditOp(false)
				writeJSON(w, http.StatusOK, out)
				return
			}
			if req.Action == "down" {
				cmd = append([]string{"docker", "rm", "-f"}, ids...)
			} else { // start / stop / restart
				cmd = append([]string{"docker", req.Action}, ids...)
			}
		}

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
				msg = "stack " + req.Action + " 以非零状态退出"
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
		// 3) up -d(用探测到的 compose CLI:v2 docker compose / v1 docker-compose)。
		bin := detectComposeBin(cctx, svc, id)
		if bin == nil {
			out.Error = "该主机未检测到 docker compose / docker-compose,无法部署"
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		upCmd := append(append([]string{}, bin...), "-p", req.Name, "-f", composePath, "up", "-d")
		res, err := svc.Exec(cctx, id, upCmd)
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
