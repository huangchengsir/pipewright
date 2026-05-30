package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/target"
)

// Story 6.2(FR-16):服务日志查看(历史 + 实时 tail,经 SSH)。
//
// AC-SEC-02 核心:source ∈ {journald|file|docker} 白名单;target 经严格正则校验;
// 命令按 source 构造为 cmd []string,经 target.Exec/ExecStream 各参数 shell 转义后执行,
// **绝不让任意 shell 注入**。校验不过 → 400 invalid_log_target。
// SSH/命令失败 → 200 + 空 lines + error 字段(人读),**不 500**。

const (
	// logLinesDefault 是未指定 lines 时的默认回看行数。
	logLinesDefault = 200
	// logLinesMax 是历史回看的上限(契约 N≤2000;防超大输出撑爆响应/内存)。
	logLinesMax = 2000
	// logBytesMax 是历史 stdout 截断上限(防单行/总量过大)。
	logBytesMax = 1 << 20 // 1 MiB
	// logStreamLineMax 是实时单行的最大字节(超长行截断,防内存膨胀)。
	logStreamLineMax = 64 * 1024
)

// 校验正则(AC-SEC-02):
//   - journald unit:`[\w.@-]+`(字母数字下划线 + 点/@/-,如 nginx.service、ssh@1.service)。
//   - docker 容器名/ID:`[\w.-]+`。
//
// file 路径单独校验(绝对路径、无 `..`、无 shell 元字符),不用宽松正则。
var (
	reJournaldUnit = regexp.MustCompile(`^[\w.@-]+$`)
	reDockerName   = regexp.MustCompile(`^[\w.-]+$`)
	// shellMetaChars 是文件路径里一律禁止的危险字符(纵深防御;命令本就 array 化不拼 shell)。
	shellMetaChars = ";|&$`<>(){}[]!*?#~\n\r\t\"'\\ "
)

// logLineDTOOut 是单行日志 DTO(契约:{"text":"…","ts":null})。
// 复用 runs.go 已有 logLineDTO 名称会冲突,故本 story 用独立形状(ts 恒 null,行无独立时间戳)。
type serverLogLineDTO struct {
	Text string  `json:"text"`
	TS   *string `json:"ts"`
}

// serverLogsDTO 是历史日志响应体(冻结契约)。
type serverLogsDTO struct {
	ServerID  string             `json:"serverId"`
	Source    string             `json:"source"`
	Target    string             `json:"target"`
	Lines     []serverLogLineDTO `json:"lines"`
	Truncated bool               `json:"truncated"`
	// Error 为人读错误(SSH/命令失败时非空),其余为 ""(omitempty 不输出)。绝不含凭据明文。
	Error string `json:"error,omitempty"`
}

// validateLogTarget 据 source 严格校验 target,合法返回 nil。非法返回 error(handler 映射 400)。
// AC-SEC-02 要害:绝不放过可能被远端 shell 解释的危险输入。
func validateLogTarget(source, tgt string) error {
	if tgt == "" {
		return errors.New("target 不能为空")
	}
	if len(tgt) > 512 {
		return errors.New("target 过长")
	}
	switch source {
	case "journald":
		if !reJournaldUnit.MatchString(tgt) {
			return errors.New("非法 journald unit 名")
		}
	case "docker":
		if !reDockerName.MatchString(tgt) {
			return errors.New("非法 docker 容器名")
		}
	case "file":
		if !strings.HasPrefix(tgt, "/") {
			return errors.New("文件路径须为绝对路径")
		}
		if strings.Contains(tgt, "..") {
			return errors.New("文件路径不得含 ..")
		}
		if strings.ContainsAny(tgt, shellMetaChars) {
			return errors.New("文件路径含非法字符")
		}
	default:
		return errors.New("非法 source")
	}
	return nil
}

// buildLogCmd 据 source/target/lines 构造历史日志命令(array,不拼 shell)。follow=true 时
// 构造实时流命令(tail -f / journalctl -f / docker logs -f)。调用前须先过 validateLogTarget。
func buildLogCmd(source, tgt string, lines int, follow bool) []string {
	switch source {
	case "file":
		args := []string{"tail", "-n", strconv.Itoa(lines)}
		if follow {
			args = append(args, "-f")
		}
		return append(args, tgt)
	case "journald":
		args := []string{"journalctl", "-u", tgt, "-n", strconv.Itoa(lines), "--no-pager"}
		if follow {
			args = append(args, "-f")
		}
		return args
	case "docker":
		args := []string{"docker", "logs", "--tail", strconv.Itoa(lines)}
		if follow {
			args = append(args, "-f")
		}
		return append(args, tgt)
	default:
		return nil
	}
}

// clampLines 解析并约束 lines 查询参数到 [1, logLinesMax];缺省 logLinesDefault。
func clampLines(raw string) int {
	if raw == "" {
		return logLinesDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return logLinesDefault
	}
	if n > logLinesMax {
		return logLinesMax
	}
	return n
}

// makeServerLogsHandler 返回 GET /api/servers/{id}/logs handler(认证,只读;历史)。
// source/target 严格校验 → 非法 400 invalid_log_target;SSH/命令失败 → 200 + error,不 500。
func makeServerLogsHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		source := r.URL.Query().Get("source")
		tgt := r.URL.Query().Get("target")
		lines := clampLines(r.URL.Query().Get("lines"))

		if err := validateLogTarget(source, tgt); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_log_target", "日志源或目标非法:"+err.Error())
			return
		}

		// 先确认服务器存在(404 在写任何 200 体之前)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		cmd := buildLogCmd(source, tgt, lines, false)
		res, err := svc.Exec(r.Context(), id, cmd)
		out := serverLogsDTO{ServerID: id, Source: source, Target: tgt, Lines: []serverLogLineDTO{}}
		if err != nil {
			// 定位类错误(服务器/凭据不存在、保险库未配)→ 走标准映射(404/422/503)。
			if errors.Is(err, target.ErrNotFound) ||
				errors.Is(err, target.ErrCredentialNotFound) ||
				errors.Is(err, target.ErrVaultUnconfigured) {
				writeServerError(w, err)
				return
			}
			// 连接/认证/执行类 → 200 + 人读 error,不 500(契约作者定)。
			out.Error = humanLogError(err)
			writeJSON(w, http.StatusOK, out)
			return
		}

		// 远端命令非零退出(文件不存在/无该 unit/无该容器)→ stderr 作为人读 error;不 500。
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = "日志命令以非零状态退出"
			}
			out.Error = truncateLog(msg, 1024)
			writeJSON(w, http.StatusOK, out)
			return
		}

		text := res.Stdout
		truncated := false
		if len(text) > logBytesMax {
			text = text[:logBytesMax]
			truncated = true
		}
		out.Lines = splitLogLines(text)
		out.Truncated = truncated
		writeJSON(w, http.StatusOK, out)
	}
}

// makeServerLogsStreamHandler 返回 GET /api/servers/{id}/logs/stream handler(认证,SSE;实时 tail)。
// SSE event: logline data:{"text":"…"};客户端断开 → ctx 取消 → 关 SSH session(不泄漏)。
func makeServerLogsStreamHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		source := r.URL.Query().Get("source")
		tgt := r.URL.Query().Get("target")
		lines := clampLines(r.URL.Query().Get("lines"))

		if err := validateLogTarget(source, tgt); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_log_target", "日志源或目标非法:"+err.Error())
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "internal", "服务器不支持流式响应")
			return
		}

		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		cmd := buildLogCmd(source, tgt, lines, true)
		rc, err := svc.ExecStream(r.Context(), id, cmd)
		if err != nil {
			// 定位类错误未写任何流帧前 → 标准状态码。
			if errors.Is(err, target.ErrNotFound) ||
				errors.Is(err, target.ErrCredentialNotFound) ||
				errors.Is(err, target.ErrVaultUnconfigured) {
				writeServerError(w, err)
				return
			}
			// 连接/认证类:已是 SSE 语义,开流后以 error 事件人读告知(不 500)。
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			writeSSE(w, flusher, "error", logErrPayload(humanLogError(err)))
			return
		}
		// 客户端断开 / handler 退出 → 关流 → 关 SSH session(防泄漏)。
		defer func() { _ = rc.Close() }()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		ctx := r.Context()
		scanner := bufio.NewScanner(rc)
		scanner.Buffer(make([]byte, 0, 64*1024), logStreamLineMax)

		// 在独立 goroutine 扫描(Scan 阻塞在 SSH 读);经 channel 把行送回主循环,
		// 让 ctx 取消(客户端断开)能即时退出 + defer 关流。
		linesCh := make(chan string, 64)
		go func() {
			defer close(linesCh)
			for scanner.Scan() {
				select {
				case linesCh <- scanner.Text():
				case <-ctx.Done():
					return
				}
			}
		}()

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
					return
				}
				flusher.Flush()
			case line, more := <-linesCh:
				if !more {
					// 流结束(远端进程退出 / 连接关闭)。
					return
				}
				writeSSE(w, flusher, "logline", logLinePayload(line))
			}
		}
	}
}

// splitLogLines 把 stdout 文本按行切为 DTO(ts 恒 null;尾部空行剔除)。
func splitLogLines(text string) []serverLogLineDTO {
	if text == "" {
		return []serverLogLineDTO{}
	}
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	// 去掉末尾空行(tail 输出常以换行结尾)。
	for len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	out := make([]serverLogLineDTO, 0, len(raw))
	for _, l := range raw {
		out = append(out, serverLogLineDTO{Text: l, TS: nil})
	}
	return out
}

// logLinePayload 构造 logline SSE 事件 JSON(经 json.Marshal 安全转义)。
func logLinePayload(text string) string {
	b, _ := json.Marshal(map[string]string{"text": text})
	return string(b)
}

// logErrPayload 构造 error SSE 事件 JSON(人读;绝不含凭据明文)。
func logErrPayload(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

// humanLogError 把领域错误映射为人读文案(绝不含凭据明文/内部栈)。
func humanLogError(err error) string {
	switch {
	case errors.Is(err, target.ErrAuth):
		return "SSH 认证失败:密钥或口令无效,或无登录权限"
	case errors.Is(err, target.ErrUnreachable):
		return "无法连接服务器:端口未开放、主机不可达或超时"
	case errors.Is(err, target.ErrInvalidCredential):
		return "凭据不是可用的 SSH 私钥或口令"
	default:
		return "取日志失败:连接或命令执行错误"
	}
}

// truncateLog 截断人读文案到 max 字节。
func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(已截断)"
}
