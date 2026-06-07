package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 新增容器(Portainer 式 docker run)。POST /api/servers/{id}/containers。
//
// AC-SEC-02 要害:所有参数严格白名单校验,命令一律 array 化
// (`docker run -d [--name][--restart][-p][-e][-v] <image> [cmd...]`),经 target.Exec
// 各参数 shell 转义后执行,**绝不拼 shell 字符串**。任一参数非法 → 400 invalid_create_spec,
// 命令不构造、不执行。SSH/命令失败 → 200 + ok:false + 人读 error(绝无密钥),不 500。
// 成功(命令成功投递)后写 append-only 审计(detail 脱敏:仅 image/name,不含 env 值)。

var (
	// 镜像引用:registry/repo:tag@digest 形态。首字符强制字母数字(禁 `-` 防 flag 注入),
	// 其余为字母数字 + `._/:@-`(无任何 shell 元字符)。
	reImageRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)
	// 端口映射:[ip:][hostPort:]containerPort[/tcp|/udp]。
	rePortSpec = regexp.MustCompile(`^(\d{1,3}(\.\d{1,3}){3}:)?(\d{1,5}:)?\d{1,5}(/(tcp|udp))?$`)
	// 环境变量名:首字符字母/下划线,其余字母数字下划线。
	reEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// 卷路径段(host 路径 / 命名卷 / 容器内路径):字母数字 + `._/@-`,无 shell 元字符。
	reVolumeSeg = regexp.MustCompile(`^[\w/][\w./@-]*$`)
)

// restartPolicies 是允许的重启策略(docker 枚举)。
var restartPolicies = map[string]bool{"no": true, "always": true, "unless-stopped": true, "on-failure": true}

// createContainerRequest 是 POST /api/servers/{id}/containers 请求体(冻结契约)。
type createContainerRequest struct {
	Image   string   `json:"image"`             // 必填,如 nginx:latest
	Name    string   `json:"name,omitempty"`    // 可选,容器名
	Ports   []string `json:"ports,omitempty"`   // "8080:80" / "127.0.0.1:8080:80" / "80"
	Env     []string `json:"env,omitempty"`     // "KEY=VALUE"
	Volumes []string `json:"volumes,omitempty"` // "/host:/ctr" / "/host:/ctr:ro" / "vol:/ctr"
	Restart string   `json:"restart,omitempty"` // no|always|unless-stopped|on-failure
	Command string   `json:"command,omitempty"` // 可选,按空白拆成参数(不经 shell)
}

// createContainerDTO 是响应体(冻结契约)。
type createContainerDTO struct {
	ServerID    string `json:"serverId"`
	OK          bool   `json:"ok"`
	ContainerID string `json:"containerId"` // 成功时为新容器完整 ID
	Error       string `json:"error"`       // ok=false 时人读(绝无密钥)
}

// validateCreateSpec 校验创建参数,合法返回 nil。AC-SEC-02:绝不放过可被当 flag / shell 解释的输入。
func validateCreateSpec(req *createContainerRequest) error {
	img := strings.TrimSpace(req.Image)
	if img == "" {
		return errors.New("镜像不能为空")
	}
	if len(img) > 256 || !reImageRef.MatchString(img) {
		return errors.New("非法镜像引用")
	}
	req.Image = img

	if req.Name != "" {
		if len(req.Name) > 128 || !reDockerTgt.MatchString(req.Name) {
			return errors.New("非法容器名(仅字母数字与 . _ -,且不以 - 开头)")
		}
	}
	if req.Restart != "" && !restartPolicies[req.Restart] {
		return errors.New("非法重启策略(no/always/unless-stopped/on-failure)")
	}
	if len(req.Ports) > 64 || len(req.Env) > 128 || len(req.Volumes) > 64 {
		return errors.New("端口/环境变量/卷数量过多")
	}
	for _, p := range req.Ports {
		if err := validatePortSpec(p); err != nil {
			return err
		}
	}
	for _, e := range req.Env {
		if err := validateEnvSpec(e); err != nil {
			return err
		}
	}
	for _, v := range req.Volumes {
		if err := validateVolumeSpec(v); err != nil {
			return err
		}
	}
	if len(req.Command) > 1024 {
		return errors.New("command 过长")
	}
	for _, a := range strings.Fields(req.Command) {
		// 命令参数同样禁止以 `-` 开头被当作 docker run 自身的 flag(image 之后才是容器命令,
		// docker 已按位置区分,但稳妥起见仍禁 shell 元字符)。
		if strings.ContainsAny(a, "`$;|&<>(){}\n\r") {
			return errors.New("command 含非法字符")
		}
	}
	return nil
}

func validatePortSpec(p string) error {
	if !rePortSpec.MatchString(p) {
		return fmt.Errorf("非法端口映射:%s", truncateLog(p, 64))
	}
	// 进一步校验每个端口号 ≤ 65535。
	hostPart := p
	if i := strings.IndexByte(hostPart, '/'); i >= 0 {
		hostPart = hostPart[:i]
	}
	for _, seg := range strings.Split(hostPart, ":") {
		if seg == "" || strings.Contains(seg, ".") {
			continue // 跳过空段与 IP 段
		}
		if n, err := strconv.Atoi(seg); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("端口号越界:%s", truncateLog(seg, 16))
		}
	}
	return nil
}

func validateEnvSpec(e string) error {
	i := strings.IndexByte(e, '=')
	if i <= 0 {
		return errors.New("环境变量须为 KEY=VALUE 形式")
	}
	key, val := e[:i], e[i+1:]
	if !reEnvKey.MatchString(key) {
		return fmt.Errorf("非法环境变量名:%s", truncateLog(key, 64))
	}
	if strings.ContainsAny(val, "\n\r\x00") {
		return errors.New("环境变量值不能含换行或空字符")
	}
	if len(val) > 4096 {
		return errors.New("环境变量值过长")
	}
	return nil
}

func validateVolumeSpec(v string) error {
	parts := strings.Split(v, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("非法卷挂载:%s", truncateLog(v, 64))
	}
	if len(parts) == 3 {
		if parts[2] != "ro" && parts[2] != "rw" {
			return errors.New("卷模式仅支持 ro/rw")
		}
	}
	for _, seg := range parts[:2] {
		if seg == "" || !reVolumeSeg.MatchString(seg) {
			return fmt.Errorf("非法卷路径段:%s", truncateLog(seg, 64))
		}
	}
	return nil
}

// buildDockerRunCmd 构造 `docker run -d ...` 命令 array(不拼 shell)。调用前须先过 validateCreateSpec。
func buildDockerRunCmd(req *createContainerRequest) []string {
	cmd := []string{"docker", "run", "-d"}
	if req.Name != "" {
		cmd = append(cmd, "--name", req.Name)
	}
	if req.Restart != "" {
		cmd = append(cmd, "--restart", req.Restart)
	}
	for _, p := range req.Ports {
		cmd = append(cmd, "-p", p)
	}
	for _, e := range req.Env {
		cmd = append(cmd, "-e", e)
	}
	for _, v := range req.Volumes {
		cmd = append(cmd, "-v", v)
	}
	cmd = append(cmd, req.Image)
	cmd = append(cmd, strings.Fields(req.Command)...)
	return cmd
}

// makeCreateContainerHandler 返回 POST /api/servers/{id}/containers handler(认证 + CSRF;写操作)。
func makeCreateContainerHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req createContainerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if err := validateCreateSpec(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_create_spec", "创建参数非法:"+err.Error())
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		out := createContainerDTO{ServerID: id}
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor:      auditActor,
				Action:     audit.ActionContainerCreate,
				TargetType: audit.TargetServer,
				TargetID:   id,
				// detail 仅白名单结构化字段:image/name/ok(不含 env 值等敏感内容)。
				Detail: map[string]any{"image": req.Image, "name": req.Name, "ok": ok},
				IP:     clientIP(r),
			})
		}

		res, err := svc.Exec(r.Context(), id, buildDockerRunCmd(&req))
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
				msg = "docker run 以非零状态退出"
			}
			out.OK = false
			out.Error = truncateLog(msg, 1024)
		} else {
			out.OK = true
			out.ContainerID = strings.TrimSpace(res.Stdout)
		}
		auditOp(out.OK)
		writeJSON(w, http.StatusOK, out)
	}
}
