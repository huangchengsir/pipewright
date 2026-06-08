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

// ─── Stack 详情(查看某项目里的服务/容器 + compose 文件) ──────────────────────
// 列表只给项目名 + 计数;详情让用户「点进去」看这个 compose 项目里到底有哪些服务/容器,
// 以及(能定位到时)compose 文件原文。服务/容器靠项目标签聚合(v1/v2、新旧 docker 通吃,
// 不依赖 compose CLI);compose 原文从 `config_files` 标签指向的路径 cat 出来(老 v1 常无此
// 标签 → 只读展示服务、提示无法在线编辑)。

const stackComposeReadMax = 256 << 10 // compose 文件读取上限 256 KiB

// stackServiceDTO 是 stack 内单个服务/容器的展示 DTO(冻结契约)。
type stackServiceDTO struct {
	Service string `json:"service"` // compose 服务名(com.docker.compose.service 标签;可能为空)
	Name    string `json:"name"`    // 容器名
	Image   string `json:"image"`   // 镜像
	State   string `json:"state"`   // running/exited/... (deriveState)
	Status  string `json:"status"`  // 人读状态
	Ports   string `json:"ports"`   // 端口映射原文
}

// stackDetailDTO 是单个 compose 项目的详情响应体(冻结契约)。
type stackDetailDTO struct {
	ServerID      string            `json:"serverId"`
	Name          string            `json:"name"`
	Reachable     bool              `json:"reachable"`
	Runtime       string            `json:"runtime"`
	Error         string            `json:"error"`
	Services      []stackServiceDTO `json:"services"`
	Running       int               `json:"running"`
	Total         int               `json:"total"`
	ConfigFiles   string            `json:"configFiles"`   // config_files 标签原文(逗号分隔;v1 旧版常空)
	Compose       string            `json:"compose"`       // compose 文件内容(可定位且可读才填)
	ComposeSource string            `json:"composeSource"` // "file"(读到原文) | "none"(无法定位)
	Editable      bool              `json:"editable"`      // 单一绝对路径 → 可在线编辑保存重部署
	CollectedAt   string            `json:"collectedAt"`
}

// cmdStackServices 按项目标签列该 stack 的所有容器(逐字段 Tab 模板,新旧 docker 通吃)。
func cmdStackServices(project string) []string {
	return []string{
		"docker", "ps", "-a", "--no-trunc",
		"--filter", "label=com.docker.compose.project=" + project,
		"--format", `{{.Label "com.docker.compose.service"}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}`,
	}
}

// cmdStackConfigFiles 取该项目容器的 config_files 标签(逐行,取首个非空)。
func cmdStackConfigFiles(project string) []string {
	return []string{
		"docker", "ps", "-a",
		"--filter", "label=com.docker.compose.project=" + project,
		"--format", `{{.Label "com.docker.compose.project.config_files"}}`,
	}
}

func parseStackServices(out string) []stackServiceDTO {
	list := []stackServiceDTO{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		field := func(i int) string {
			if i < len(f) {
				return strings.TrimSpace(f[i])
			}
			return ""
		}
		name := field(1)
		if name == "" {
			continue
		}
		status := field(3)
		list = append(list, stackServiceDTO{
			Service: field(0),
			Name:    strings.TrimPrefix(name, "/"),
			Image:   field(2),
			State:   deriveState(status),
			Status:  status,
			Ports:   field(4),
		})
	}
	return list
}

// primaryComposePath 从 config_files 标签取「单一绝对路径」用于读取/写回。多文件或相对路径
// 返回 ok=false(无法安全地在线编辑)。路径经绝对 + 无控制字符校验,经 array 传参不拼 shell。
func primaryComposePath(configFiles string) (string, bool) {
	var paths []string
	for _, p := range strings.Split(configFiles, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) != 1 {
		return "", false
	}
	p := paths[0]
	if len(p) > 512 || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\n\r\x00") {
		return "", false
	}
	return p, true
}

func collectStackDetail(ctx context.Context, svc target.Service, id, project string) (stackDetailDTO, error) {
	out := stackDetailDTO{ServerID: id, Name: project, Services: []stackServiceDTO{}, ComposeSource: "none", CollectedAt: time.Now().UTC().Format(time.RFC3339)}
	cctx, cancel := context.WithTimeout(ctx, stacksCmdTimeout)
	defer cancel()

	res, err := svc.Exec(cctx, id, cmdStackServices(project))
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
	out.Services = parseStackServices(res.Stdout)
	out.Total = len(out.Services)
	for _, s := range out.Services {
		if s.State == "running" {
			out.Running++
		}
	}

	// config_files 标签 → 取首个非空。
	if cfgRes, cfgErr := svc.Exec(cctx, id, cmdStackConfigFiles(project)); cfgErr == nil && cfgRes.ExitCode == 0 {
		for _, ln := range strings.Split(cfgRes.Stdout, "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				out.ConfigFiles = ln
				break
			}
		}
	}
	// 三级解析 compose 文件(标签 → 受管目录 → 探测);读到 = 可编辑。
	if cpath, body, ok := resolveComposeFile(cctx, svc, id, project, out.ConfigFiles); ok {
		out.Compose = body
		out.ConfigFiles = cpath
		out.ComposeSource = "file"
		out.Editable = true
	}
	return out, nil
}

// resolveComposeFile 三级解析某 stack 的 compose 文件并读出原文,返回(路径, 内容, 命中):
//
//	① config_files 标签的单一绝对路径(新版 compose 有;老 v1 恒空)
//	② Pipewright 受管目录 /opt/pipewright/stacks/<project>/docker-compose.yml(自部署的)
//	③ 有界扫描常见根,按「父目录名规范化 == 项目名」匹配(存量外部、文件还在的)
//
// 每级都要求文件**可读**才算命中(标签可能指向已删文件)。详情展示与「更新」共用此函数,
// 确保「详情能编辑」⇔「更新能跑」一致。project 经调用方 reDockerTgt 校验,无路径穿越。
func resolveComposeFile(ctx context.Context, svc target.Service, id, project, labelCfg string) (string, string, bool) {
	if p, ok := primaryComposePath(labelCfg); ok {
		if body, readOK := catComposeFile(ctx, svc, id, p); readOK {
			return p, body, true
		}
	}
	managed := stacksBaseDir + "/" + project + "/" + composeFileName
	if body, readOK := catComposeFile(ctx, svc, id, managed); readOK {
		return managed, body, true
	}
	if dpath, ok := discoverComposeFile(ctx, svc, id, project); ok {
		if body, readOK := catComposeFile(ctx, svc, id, dpath); readOK {
			return dpath, body, true
		}
	}
	return "", "", false
}

// stackDiscoverRoots 是有界扫描的常见 compose 部署根(只扫这些 + maxdepth,绝不全盘 find)。
var stackDiscoverRoots = []string{"/root", "/home", "/opt", "/srv", "/data", "/app", "/usr/local"}

// cmdDiscoverComposeFiles 是定值 find 命令(array 化,无 shell、无用户输入):列常见根下的
// compose 文件路径。某些根不存在会让 find 非零退出 + stderr 噪声,但 stdout 仍含已找到的文件
// → 调用方忽略 ExitCode,只解析 stdout。
var cmdDiscoverComposeFiles = append(append([]string{"find"}, stackDiscoverRoots...),
	"-maxdepth", "5", "(",
	"-name", "docker-compose.yml", "-o", "-name", "docker-compose.yaml",
	"-o", "-name", "compose.yml", "-o", "-name", "compose.yaml", ")")

// composeProjectFromPath 按 docker-compose v1 规则从 compose 文件路径反推项目名:取父目录
// basename,小写,仅保留 [a-z0-9](compose 把其余字符剔除,如 "docker-compose" → "dockercompose")。
func composeProjectFromPath(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 { // 去文件名 → 父目录
		p = p[:i]
	}
	if i := strings.LastIndex(p, "/"); i >= 0 { // 父目录 basename
		p = p[i+1:]
	}
	var sb strings.Builder
	for _, r := range strings.ToLower(p) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// discoverComposeFile 有界扫描常见根,返回「父目录名规范化 == project」的首个 compose 文件路径。
func discoverComposeFile(ctx context.Context, svc target.Service, id, project string) (string, bool) {
	if project == "" {
		return "", false
	}
	res, err := svc.Exec(ctx, id, cmdDiscoverComposeFiles)
	if err != nil { // 传输层失败才放弃;find 非零退出(根不存在)仍可能有 stdout
		return "", false
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		p := strings.TrimSpace(line)
		if p == "" || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\n\r\x00") {
			continue
		}
		if composeProjectFromPath(p) == project {
			return p, true
		}
	}
	return "", false
}

// catComposeFile 读取远端 compose 文件(array 化 cat,非 shell)。文件不存在/为空 → readOK=false。
func catComposeFile(ctx context.Context, svc target.Service, id, path string) (string, bool) {
	res, err := svc.Exec(ctx, id, []string{"cat", path})
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	if strings.TrimSpace(res.Stdout) == "" {
		return "", false
	}
	body := res.Stdout
	if len(body) > stackComposeReadMax {
		body = body[:stackComposeReadMax]
	}
	return body, true
}

// makeServerStackDetailHandler 返回 GET /api/servers/{id}/stacks/{name}(认证,只读)。
func makeServerStackDetailHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")
		if !reDockerTgt.MatchString(name) || len(name) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_stack", "项目名非法")
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}
		out, locErr := collectStackDetail(r.Context(), svc, id, name)
		if locErr != nil {
			writeServerError(w, locErr)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// stackSaveRequest 是「在线编辑 compose 并保存重部署」请求体。
type stackSaveRequest struct {
	Name       string `json:"name"`
	Compose    string `json:"compose"`
	ConfigFile string `json:"configFile"` // 详情给的单一绝对路径(写回原位)
}

// makeStackSaveHandler 返回 POST /api/servers/{id}/stacks/save(认证 + CSRF;写)。
// 把编辑后的 compose 写回原始路径 → `docker compose -p <name> -f <path> up -d`(就地升级)。
// 仅对详情判定 editable(单一绝对路径)的 stack 可用;detail 内容不入审计(仅项目名 + 动作)。
func makeStackSaveHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req stackSaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if !reDockerTgt.MatchString(req.Name) || len(req.Name) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_stack", "项目名非法")
			return
		}
		path, ok := primaryComposePath(req.ConfigFile)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_stack", "该 Stack 无单一可写 compose 路径,无法在线保存")
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

		out := stackDTOResult{ServerID: id, Name: req.Name, Action: "save"}
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor: auditActor, Action: audit.ActionStackOp, TargetType: audit.TargetServer, TargetID: id,
				Detail: map[string]any{"name": req.Name, "action": "save", "ok": ok}, IP: clientIP(r),
			})
		}

		cctx, cancel := context.WithTimeout(r.Context(), stacksUpTimeout)
		defer cancel()

		bin := detectComposeBin(cctx, svc, id)
		if bin == nil {
			out.Error = "该主机未检测到 docker compose / docker-compose,无法保存重部署"
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		// 写回原始路径(字节流,非 shell)。
		if err := svc.Upload(cctx, id, strings.NewReader(compose), path); err != nil {
			out.Error = "写入 compose 文件失败:" + humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}
		upCmd := append(append([]string{}, bin...), "-p", req.Name, "-f", path, "up", "-d")
		res, err := svc.Exec(cctx, id, upCmd)
		if err != nil {
			if errors.Is(err, target.ErrNotFound) || errors.Is(err, target.ErrCredentialNotFound) || errors.Is(err, target.ErrVaultUnconfigured) {
				auditOp(false)
				writeServerError(w, err)
				return
			}
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
			// compose 文件路径:优先用前端从列表带来的 config_files;为空/非法(老 compose v1
			// 标签恒空)时,走与详情一致的三级解析(受管目录 → 探测)兜底,使「详情能编辑」⇔
			// 「更新能跑」一致。都解析不到才报清晰错误。
			fileArgs, ferr := composeFileArgs(req.ConfigFile)
			if ferr != nil {
				if cpath, _, ok := resolveComposeFile(cctx, svc, id, req.Name, req.ConfigFile); ok {
					fileArgs = []string{"-f", cpath}
				} else {
					out.OK = false
					out.Error = "未找到该 Stack 的 compose 文件(外部部署且文件已移除/无法定位),无法更新(可改用重启)"
					auditOp(false)
					writeJSON(w, http.StatusOK, out)
					return
				}
			}
			// 「更新」= 拉新镜像 + 重建有变化的服务。**不用** `up -d --pull always`:`--pull` 标志
			// 是 compose v2 才有的,docker-compose v1(如 1.18)的 up 不认会报 usage 退出。改成版本
			// 通吃的两步:先独立 `pull`(v1/v2 都有),再 `up -d`。pull best-effort(可能离线/部分
			// 镜像本地构建无法拉),失败不阻断后续 up(仍按本地镜像重建有变化的服务)。
			base := append(append([]string{}, bin...), "-p", req.Name)
			base = append(base, fileArgs...)
			pullCmd := append(append([]string{}, base...), "pull")
			if pullRes, pullErr := svc.Exec(cctx, id, pullCmd); pullErr == nil && pullRes.ExitCode != 0 {
				out.Output = "⚠ 拉取镜像未全部成功(可能离线或镜像为本地构建),已按现有镜像继续重建。\n"
			}
			cmd = append(append([]string{}, base...), "up", "-d")
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
			// out.Output 可能已含 update 的 pull 警告前缀,追加 up 的输出而非覆盖。
			out.Output = truncateLog(strings.TrimSpace(out.Output+"\n"+strings.TrimSpace(res.Stdout)), 2048)
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
