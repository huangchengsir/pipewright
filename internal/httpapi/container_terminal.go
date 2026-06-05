package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Story 6.4(FR-18):容器内交互终端(WS ↔ SSH → `docker exec -it`)。
//
// 经界面进入目标机上某容器,执行交互式命令。架构约定:**WebSocket 仅**用于交互式容器终端
// (其余实时流走 SSE)。本端点是平台唯一的 WS 升级点。
//
// 安全(FR-18 / AC-SEC-02):
//   - 鉴权:WS 升级是 GET,经 /api 组的 requireAuth 校验会话 cookie(未登录 → 401,升级失败)。
//     CSRF 对 GET 豁免,改以**同源(Origin)校验**防跨站 WS 劫持(见 originPatterns)。
//   - 命令 array 化:`docker exec -it <containerID> <shell>`,containerID 经严格白名单
//     (首字符 [\w] 防 flag 注入、无 shell 元字符),shell 经枚举白名单;经 target.ExecInteractive
//     各参数 shell 转义后执行,绝不拼 shell 字符串。
//   - 凭据经 vault 即用即弃,绝不入 WS 帧/日志。
//   - 审计:开终端是高危操作,握手成功(SSH PTY 建立)后写 append-only 审计(谁/哪台/哪容器/何时)。

const (
	// wsReadLimit 限制单条 WS 消息大小(防超大输入帧;终端输入本就细碎)。
	wsReadLimit = 1 << 20 // 1 MiB
	// ptyReadChunk 是从 SSH PTY 读出转发到 WS 的缓冲块大小。
	ptyReadChunk = 32 * 1024
	// termShellDefault 是未指定 shell 时的默认登录 shell。
	termShellDefault = "/bin/sh"
)

// reContainerID 校验 docker 容器名/ID:首字符强制 [\w](禁 `-` 开头防 flag 注入),
// 其余为字母数字下划线 + 点/-(无任何 shell 元字符)。与 6-2/6-3 风格一致。
var reContainerID = regexp.MustCompile(`^[\w][\w.-]*$`)

// allowedShells 是允许进入容器的交互 shell 白名单(绝对路径;防任意命令注入到 exec 参数)。
var allowedShells = map[string]struct{}{
	"/bin/sh":       {},
	"/bin/bash":     {},
	"/bin/ash":      {},
	"/bin/zsh":      {},
	"/usr/bin/sh":   {},
	"/usr/bin/bash": {},
	"/usr/bin/zsh":  {},
	"sh":            {},
	"bash":          {},
}

// validateContainerTarget 校验容器 ID 与 shell,合法返回归一后的 shell。
func validateContainerTarget(containerID, shell string) (string, error) {
	if containerID == "" {
		return "", errors.New("容器 ID 不能为空")
	}
	if len(containerID) > 128 {
		return "", errors.New("容器 ID 过长")
	}
	if !reContainerID.MatchString(containerID) {
		return "", errors.New("非法容器 ID(仅允许字母数字与 . _ -,且不得以 - 开头)")
	}
	if shell == "" {
		shell = termShellDefault
	}
	if _, ok := allowedShells[shell]; !ok {
		return "", errors.New("非法 shell(不在允许白名单内)")
	}
	return shell, nil
}

// buildContainerExecCmd 构造 `docker exec -it <containerID> <shell>` 命令 array(不拼 shell)。
// 调用前须先过 validateContainerTarget。
func buildContainerExecCmd(containerID, shell string) []string {
	return []string{"docker", "exec", "-it", containerID, shell}
}

// makeContainerTerminalHandler 返回 GET /api/servers/{id}/containers/{containerId}/terminal handler。
// **唯一的 WS 升级点**。已由 /api 组套了 requireAuth(未登录 → 401,不会升级)。本 handler 再做
// 同源(Origin)校验,然后 WS↔SSH(PTY)双向泵:WS 文本帧 → stdin;PTY 输出 → WS 二进制帧;
// resize 控制帧(JSON {type:"resize",cols,rows})→ WindowChange。
func makeContainerTerminalHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		containerID := chi.URLParam(r, "containerId")
		shell, err := validateContainerTarget(containerID, r.URL.Query().Get("shell"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_container_target", "容器或 shell 非法:"+err.Error())
			return
		}

		// 先确认服务器存在(在升级为 WS 之前给出标准 HTTP 状态码)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		// WS 升级:同源校验(防跨站 WS 劫持)。OriginPatterns 仅放行 Host 自身;
		// 非同源 → Accept 返回错误,直接 403,绝不开 SSH。
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: originPatterns(r),
		})
		if err != nil {
			// Accept 已写过响应(含 403 同源拒绝);此处仅返回。
			return
		}
		conn.SetReadLimit(wsReadLimit)

		// 开 PTY 交互会话(docker exec -it <id> <shell>)。失败 → 以 WS close 帧人读告知,不泄密。
		cmd := buildContainerExecCmd(containerID, shell)
		// PTY 会话生命周期独立于单次 HTTP 请求 ctx(r.Context() 在 hijack 后不可靠),
		// 用自管理 ctx,WS 关闭时 cancel 收尾。
		sessCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sess, err := svc.ExecInteractive(sessCtx, id, cmd)
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, truncateWSReason(humanTerminalError(err)))
			return
		}
		defer func() { _ = sess.Close() }()

		// 握手成功(SSH PTY 已建立)= 高危操作落地 → 写审计(谁/哪台/哪容器/何时)。
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionContainerTerminal,
			TargetType: audit.TargetServer,
			TargetID:   id,
			Detail:     map[string]any{"containerId": containerID, "shell": shell},
			IP:         clientIP(r),
		})

		pumpTerminal(sessCtx, cancel, conn, sess)
	}
}

// validateHostShell 校验主机 shell(白名单);空则默认登录 shell。供主机终端(无容器)使用。
func validateHostShell(shell string) (string, error) {
	if shell == "" {
		shell = termShellDefault
	}
	if _, ok := allowedShells[shell]; !ok {
		return "", errors.New("非法 shell(不在允许白名单内)")
	}
	return shell, nil
}

// makeServerTerminalHandler 返回 GET /api/servers/{id}/terminal handler —— **主机 shell** 终端
// (SSH 直接起交互 shell,不进容器)。这是「连服务器终端」的默认目标;容器终端是可选的更窄目标。
//
// 安全姿态同容器终端:鉴权由 /api 组 requireAuth 把守;WS 同源校验防跨站劫持;shell 经白名单;
// cmd array 化(仅 [shell],无拼接);凭据 vault 即用即弃;握手成功写审计(server_terminal)。
// 注意:服务器注册的 SSH 凭据本就具宿主机权限(容器模式的 docker exec 亦在宿主跑),主机 shell
// 不扩大信任边界,只是把既有权限诚实暴露。
func makeServerTerminalHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		shell, err := validateHostShell(r.URL.Query().Get("shell"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_shell", "shell 非法:"+err.Error())
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns(r)})
		if err != nil {
			return
		}
		conn.SetReadLimit(wsReadLimit)

		sessCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 主机 shell:cmd 仅为 [shell](array,无拼接)。
		sess, err := svc.ExecInteractive(sessCtx, id, []string{shell})
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, truncateWSReason(humanTerminalError(err)))
			return
		}
		defer func() { _ = sess.Close() }()

		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionServerTerminal,
			TargetType: audit.TargetServer,
			TargetID:   id,
			Detail:     map[string]any{"shell": shell, "target": "host"},
			IP:         clientIP(r),
		})

		pumpTerminal(sessCtx, cancel, conn, sess)
	}
}

// pumpTerminal 在 WS 与交互式 SSH 会话间双向泵数据,直到任一侧结束。
//   - WS → SSH:文本帧若是 resize 控制 JSON → WindowChange;否则原样写入 stdin。
//   - SSH → WS:PTY 输出按块读出 → 以二进制帧发回 WS。
//
// 任一方向出错/结束都 cancel,使另一方向的阻塞读/写解除,随后 defer 关 WS + 关 SSH 会话(不泄漏)。
func pumpTerminal(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, sess target.Session) {
	// SSH PTY → WS。
	go func() {
		defer cancel()
		buf := make([]byte, ptyReadChunk)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					// 远端异常中止:以 close 帧人读告知(不泄密)。
					_ = conn.Close(websocket.StatusNormalClosure, "session ended")
				} else {
					_ = conn.Close(websocket.StatusNormalClosure, "session ended")
				}
				return
			}
		}
	}()

	// WS → SSH PTY(本 goroutine)。
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			// 尝试解析 resize 控制帧;非控制帧则当普通输入写入 stdin。
			if cols, rows, ok := parseResize(data); ok {
				_ = sess.Resize(cols, rows)
				continue
			}
		}
		if len(data) > 0 {
			if _, err := sess.Write(data); err != nil {
				return
			}
		}
	}
}

// resizeMsg 是前端发来的窗口尺寸控制帧(JSON,文本帧)。
type resizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// parseResize 尝试把一条文本帧解析为 resize 控制帧。仅当 type=="resize" 时返回 ok=true。
// 失败/非控制帧返回 ok=false(调用方据此把数据当普通终端输入)。
func parseResize(data []byte) (cols, rows int, ok bool) {
	// 控制帧体积很小;非 JSON / 非 resize 一律视为普通输入(快速短路:必须以 '{' 起头)。
	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "{") {
		return 0, 0, false
	}
	var m resizeMsg
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return 0, 0, false
	}
	if m.Type != "resize" {
		return 0, 0, false
	}
	return m.Cols, m.Rows, true
}

// humanTerminalError 把领域错误映射为人读 WS 关闭原因(绝不含凭据明文/内部栈)。
func humanTerminalError(err error) string {
	switch {
	case errors.Is(err, target.ErrAuth):
		return "SSH 认证失败:密钥或口令无效,或无登录权限"
	case errors.Is(err, target.ErrUnreachable):
		return "无法连接服务器:端口未开放、主机不可达或超时"
	case errors.Is(err, target.ErrInvalidCredential):
		return "凭据不是可用的 SSH 私钥或口令"
	case errors.Is(err, target.ErrVaultUnconfigured):
		return "保险库未配置 master key,无法取 SSH 凭据"
	case errors.Is(err, target.ErrCredentialNotFound):
		return "引用的 SSH 凭据不存在"
	default:
		return "打开容器终端失败:连接或命令执行错误"
	}
}

// originPatterns 计算同源放行清单:仅放行请求自身的 Host(同源)。
// coder/websocket 的 Accept 默认即拒绝跨站 Origin;此处显式放行本机 Host,确保同源页面可连。
func originPatterns(r *http.Request) []string {
	host := r.Host
	if host == "" {
		return nil
	}
	return []string{host}
}

// truncateWSReason 截断 WS 关闭原因到协议允许的上限(125 字节),避免 Close 失败。
func truncateWSReason(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max]
}
