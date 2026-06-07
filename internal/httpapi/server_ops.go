package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Story 6.3(FR-17):服务操作(重启/停止/启动),经界面对目标机上的 systemd 单元或 docker
// 容器执行运维操作,免去逐台 SSH 登录。写操作,受审计。
//
// AC-SEC-02 核心(要害):
//   - type ∈ {systemd|docker} 白名单;target 经严格正则校验,**首字符强制 [\w]**(禁 `-` 开头,
//     杜绝 `systemctl restart --now`/`docker restart --help` 之类把参数当 flag 注入),且无 shell 元字符;
//   - action ∈ {restart|stop|start} 枚举;
//   - 命令一律 array 化(`systemctl <action> <unit>` / `docker <action> <name>`),经 target.Exec
//     各参数 shell 转义后执行,**绝不拼 shell 字符串**。
//
// 校验不过 → 400 invalid_service_target。SSH/命令失败 → 200 + ok:false + 人读 error(绝无密钥),
// **不 500**。操作成功(命令成功投递,无论退出码)后写 append-only 审计(detail 脱敏)。

// 复用 6-2(server_logs.go)的校验风格:首字符 [\w] 防 flag 注入;正则本身排除所有 shell 元字符。
//   - systemd unit:`^[\w][\w.@-]*$`(如 nginx、nginx.service、ssh@1.service)。
//   - docker 容器名/ID:`^[\w][\w.-]*$`。
var (
	reSystemdUnit = regexp.MustCompile(`^[\w][\w.@-]*$`)
	reDockerTgt   = regexp.MustCompile(`^[\w][\w.-]*$`)
)

// serviceActionRequest 是 POST /api/servers/{id}/service/action 请求体(冻结契约)。
type serviceActionRequest struct {
	Type   string `json:"type"`   // systemd|docker
	Target string `json:"target"` // unit / 容器名
	Action string `json:"action"` // restart|stop|start
}

// serviceActionDTO 是响应体(冻结契约)。ok=false 时 error 为人读串(绝无密钥);成功时 error 为 ""。
type serviceActionDTO struct {
	ServerID string `json:"serverId"`
	Type     string `json:"type"`
	Target   string `json:"target"`
	Action   string `json:"action"`
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	Error    string `json:"error"`
}

// systemdActions / dockerActions 是按 type 区分的 action 白名单(枚举)。
//   - systemd:仅 restart/stop/start(单元生命周期)。
//   - docker:容器生命周期更全 —— 起停重启 + 暂停/恢复 + kill + 删除(Portainer 式)。
//     全部恰好是 `docker <action> <name>` 的合法子命令,无需特判;`rm` 不带 `-f`(运行中容器
//     删除会失败 → ok:false,迫使先停再删,安全且留痕)。
var (
	systemdActions = map[string]bool{"restart": true, "stop": true, "start": true}
	dockerActions  = map[string]bool{
		"restart": true, "stop": true, "start": true,
		"pause": true, "unpause": true, "kill": true, "rm": true,
	}
)

// validateServiceParams 据 type 严格校验 target 与 action 枚举,合法返回 nil。
// AC-SEC-02 要害:绝不放过可能被远端当 flag / shell 解释的危险输入。action 白名单按 type 区分。
func validateServiceParams(typ, tgt, action string) error {
	if tgt == "" {
		return errors.New("target 不能为空")
	}
	if len(tgt) > 256 {
		return errors.New("target 过长")
	}
	switch typ {
	case "systemd":
		if !systemdActions[action] {
			return errors.New("非法 action(systemd 仅支持 restart/stop/start)")
		}
		if !reSystemdUnit.MatchString(tgt) {
			return errors.New("非法 systemd unit 名")
		}
	case "docker":
		if !dockerActions[action] {
			return errors.New("非法 action(docker 仅支持 restart/stop/start/pause/unpause/kill/rm)")
		}
		if !reDockerTgt.MatchString(tgt) {
			return errors.New("非法 docker 容器名")
		}
	default:
		return errors.New("非法 type(仅支持 systemd/docker)")
	}
	return nil
}

// buildServiceCmd 据 type/action/target 构造命令 array(不拼 shell)。调用前须先过 validateServiceParams。
//   - systemd:`systemctl <action> <unit>`
//   - docker: `docker <action> <name>`
func buildServiceCmd(typ, action, tgt string) []string {
	switch typ {
	case "systemd":
		return []string{"systemctl", action, tgt}
	case "docker":
		return []string{"docker", action, tgt}
	default:
		return nil
	}
}

// humanServiceError 把领域错误映射为人读文案(绝不含凭据明文/内部栈)。
func humanServiceError(err error) string {
	switch {
	case errors.Is(err, target.ErrAuth):
		return "SSH 认证失败:密钥或口令无效,或无登录权限"
	case errors.Is(err, target.ErrUnreachable):
		return "无法连接服务器:端口未开放、主机不可达或超时"
	case errors.Is(err, target.ErrInvalidCredential):
		return "凭据不是可用的 SSH 私钥或口令"
	default:
		return "执行服务操作失败:连接或命令执行错误"
	}
}

// makeServiceActionHandler 返回 POST /api/servers/{id}/service/action handler(认证 + CSRF;写操作)。
// type/target/action 严格白名单 → 非法 400 invalid_service_target;服务器不存在 404;
// SSH/命令失败 → 200 + ok:false + 人读 error,不 500;成功后写审计(detail 脱敏)。
func makeServiceActionHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req serviceActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		// AC-SEC-02:严格白名单校验(type/target/action),非法一律 400(命令不构造、不执行)。
		if err := validateServiceParams(req.Type, req.Target, req.Action); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_service_target", "服务类型/目标/操作非法:"+err.Error())
			return
		}

		// 先确认服务器存在(404 在写任何 200 体之前)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		cmd := buildServiceCmd(req.Type, req.Action, req.Target)
		out := serviceActionDTO{ServerID: id, Type: req.Type, Target: req.Target, Action: req.Action}

		// 审计 helper(code-review P5):写操作的**任何尝试**都留痕(NFR-8 取证),
		// 不只成功投递。connect/auth/凭据失败等也是需取证的安全事件(谁反复戳哪台机)。
		// detail 仅白名单结构化字段(无自由输出/凭据);Recorder 再过 Masker。
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor:      auditActor,
				Action:     audit.ActionServiceOp,
				TargetType: audit.TargetServer,
				TargetID:   id,
				Detail:     map[string]any{"type": req.Type, "target": req.Target, "action": req.Action, "ok": ok},
				IP:         clientIP(r),
			})
		}

		res, err := svc.Exec(r.Context(), id, cmd)
		if err != nil {
			// 定位类错误(服务器/凭据不存在、保险库未配)→ 走标准映射(404/422/503)。
			// 服务器不存在无可审计目标;凭据/vault 错误是对真实服务器的写尝试 → 留痕。
			if errors.Is(err, target.ErrNotFound) {
				writeServerError(w, err)
				return
			}
			if errors.Is(err, target.ErrCredentialNotFound) ||
				errors.Is(err, target.ErrVaultUnconfigured) {
				auditOp(false)
				writeServerError(w, err)
				return
			}
			// 连接/认证/执行类 → 200 + ok:false + 人读 error,不 500(契约作者定)。
			out.OK = false
			out.Error = humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}

		// 命令已投递。退出码非零(无该 unit/无该容器/docker 未装)→ ok:false + stderr 人读,不 500。
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(res.Stdout)
			}
			if msg == "" {
				msg = "服务操作命令以非零状态退出"
			}
			out.OK = false
			out.Output = truncateLog(strings.TrimSpace(res.Stdout), 4096)
			out.Error = truncateLog(msg, 1024)
		} else {
			out.OK = true
			out.Output = truncateLog(strings.TrimSpace(res.Stdout), 4096)
		}

		// 写操作受审计(NFR-8):命令已投递,按退出码记 ok 真伪。
		auditOp(out.OK)

		writeJSON(w, http.StatusOK, out)
	}
}
