package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 容器管理 —— 一键清理(docker system df / prune)。
//
// 在界面看一台主机的 Docker 磁盘占用(各类型大小 + 可回收量),并一键清理停止的容器 /
// 悬空镜像 / 无用卷 / 构建缓存 / 全部,免逐台 SSH 敲 `docker system prune`。
//
// AC-SEC-02 核心(要害):
//   - df 采集命令是**纯静态命令 array**(`docker system df --format '{{json .}}'`),绝不接受任何
//     用户输入拼接 —— 无注入面;
//   - prune 的 scope 走**枚举白名单** {containers,images,volumes,builder,all},非法一律 400;
//     命令一律 array 化(`docker <obj> prune -f`),经 target.Exec 各参数 shell 转义,**绝不拼 shell**;
//   - all 走 `docker system prune -f`,**不加 `--volumes`**(避免误删数据卷),UI 文案明示 all 不含卷。
//
// 容错纪律:
//   - df 只读、不过 CSRF:某台不可达/认证失败 → reachable:false + 人读 error,不 500;
//     目标机未装 Docker / 守护进程不可用 → reachable:true + error,entries 空,不 500。
//   - prune 写操作、过 CSRF + 审计(ActionSystemPrune):SSH/命令失败 → 200 + ok:false + 人读 error,
//     不 500;成功投递后写 append-only 审计(detail 仅 scope/ok,无自由输出/凭据)。

const (
	// dfCmdTimeout 是 `docker system df` 的整体超时(只读、轻量)。
	dfCmdTimeout = 15 * time.Second
	// pruneCmdTimeout 是 prune 的整体超时(builder/全量清理在大主机上可能耗时,给足余量)。
	pruneCmdTimeout = 120 * time.Second
	// pruneOutMax 是命令 stdout 解析/回显前的截断上限(防超大输出撑爆内存)。
	pruneOutMax = 64 * 1024
)

// cmdSystemDf 是 df 采集命令(AC-SEC-02:固定静态 array,绝不含任何用户输入)。
// `--format '{{json .}}'` 逐行输出 JSON 对象,字段 Type/TotalCount/Active/Size/Reclaimable。
var cmdSystemDf = []string{"docker", "system", "df", "--format", "{{json .}}"}

// pruneScopes 是允许的清理范围 → 对应命令 array 的白名单(AC-SEC-02)。
//   - containers → 删除所有已停止容器
//   - images     → 删除悬空(dangling)镜像(**不做** image prune -a,避免误删在用镜像)
//   - volumes    → 删除未被任何容器引用的卷
//   - builder    → 清理构建缓存
//   - all        → docker system prune(停止容器 + 悬空镜像 + 未用网络 + 构建缓存;**不含数据卷**)
var pruneScopes = map[string][]string{
	"containers": {"docker", "container", "prune", "-f"},
	"images":     {"docker", "image", "prune", "-f"},
	"volumes":    {"docker", "volume", "prune", "-f"},
	"builder":    {"docker", "builder", "prune", "-f"},
	"all":        {"docker", "system", "prune", "-f"},
}

// dfEntryDTO 是 `docker system df` 单行(单个对象类型)的结构化视图(冻结契约字段形状)。
//   - Type ∈ {Images, Containers, "Local Volumes", "Build Cache"}
//   - TotalCount/Active 为数量;Size/Reclaimable 为 docker 已人读化的串(如 "1.6GB"、"1.2GB (75%)")。
type dfEntryDTO struct {
	Type        string `json:"type"`
	TotalCount  int    `json:"totalCount"`
	Active      int    `json:"active"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

// systemDfDTO 是 GET /system/df 响应体(冻结契约)。
//   - reachable:false → SSH/连接/认证失败,entries 空,error 人读非空。
//   - reachable:true 但 dockerAvailable:false → 目标机未装 Docker / 守护进程不可用,error 人读非空。
//   - reachable:true 且 dockerAvailable:true → entries 为各类型占用,error 为空。
type systemDfDTO struct {
	ServerID        string       `json:"serverId"`
	Reachable       bool         `json:"reachable"`
	DockerAvailable bool         `json:"dockerAvailable"`
	Entries         []dfEntryDTO `json:"entries"`
	Error           string       `json:"error"`
}

// dfRawLine 是 `{{json .}}` 单行的原始形状。docker CLI 把 TotalCount/Active/Size/Reclaimable
// 均序列化为**字符串**(底层是 formatter 的 String() 方法),故全用 string 接,再做转换。
type dfRawLine struct {
	Type        string `json:"Type"`
	TotalCount  string `json:"TotalCount"`
	Active      string `json:"Active"`
	Size        string `json:"Size"`
	Reclaimable string `json:"Reclaimable"`
}

// parseDf 解析 `docker system df --format '{{json .}}'` 的逐行 JSON 输出为 DTO 列表。
// best-effort:坏行/空行跳过,不报错;计数字段解析失败归 0。无任何有效行 → 返回空切片。
func parseDockerDf(stdout string) []dfEntryDTO {
	out := make([]dfEntryDTO, 0, 4)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw dfRawLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if raw.Type == "" {
			continue
		}
		out = append(out, dfEntryDTO{
			Type:        raw.Type,
			TotalCount:  atoiSafe(raw.TotalCount),
			Active:      atoiSafe(raw.Active),
			Size:        raw.Size,
			Reclaimable: raw.Reclaimable,
		})
	}
	return out
}

// atoiSafe 把计数串转 int,失败归 0(docker 偶有 "N/A" 之类的占位)。
func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// validatePruneScope 校验 scope 是否在枚举白名单内(AC-SEC-02)。合法返回 nil。
func validatePruneScope(scope string) error {
	if _, ok := pruneScopes[scope]; !ok {
		return errors.New("非法 scope(仅支持 containers/images/volumes/builder/all)")
	}
	return nil
}

// buildPruneCmd 据 scope 返回对应命令 array(不拼 shell)。调用前须先过 validatePruneScope;
// 未命中白名单返回 nil。
func buildPruneCmd(scope string) []string {
	cmd, ok := pruneScopes[scope]
	if !ok {
		return nil
	}
	// 拷贝一份,避免调用方意外改动包级白名单切片。
	out := make([]string, len(cmd))
	copy(out, cmd)
	return out
}

// systemPruneRequest 是 POST /system/prune 请求体(冻结契约)。
type systemPruneRequest struct {
	Scope string `json:"scope"`
}

// systemPruneDTO 是 POST /system/prune 响应体(冻结契约)。ok=false 时 error 为人读串(绝无凭据);
// 成功时 output 为命令 stdout(含释放空间汇总,如 "Total reclaimed space: 1.2GB")。
type systemPruneDTO struct {
	ServerID string `json:"serverId"`
	Scope    string `json:"scope"`
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	Error    string `json:"error"`
}

// makeSystemDfHandler 返回 GET /api/servers/{id}/system/df(认证,只读 → 豁免 CSRF)。
// 服务器不存在/凭据不存在/保险库未配 → 标准状态码;连接/认证失败 → 200 + reachable:false;
// 未装 Docker → 200 + reachable:true + dockerAvailable:false。均不 500。
func makeSystemDfHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		// 先确认服务器存在(404 在写任何 200 体之前)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), dfCmdTimeout)
		defer cancel()

		out := systemDfDTO{ServerID: id, Entries: []dfEntryDTO{}}
		res, err := svc.Exec(ctx, id, cmdSystemDf)
		if err != nil {
			// 定位类错误(凭据不存在 / 保险库未配)→ 走标准映射(422/503),而非 reachable:false。
			if isLocateError(err) {
				writeServerError(w, err)
				return
			}
			// 连接/认证类 → 200 + reachable:false + 人读 error,不 500。
			out.Reachable = false
			out.Error = humanServiceError(err)
			writeJSON(w, http.StatusOK, out)
			return
		}

		// SSH 可达。命令非零退出(未装 docker / 守护进程不可用 / 无权限)→ reachable:true +
		// dockerAvailable:false + 人读 error,不 500。
		out.Reachable = true
		if res.ExitCode != 0 {
			out.DockerAvailable = false
			out.Error = "无法读取 Docker 磁盘占用:目标机未安装 Docker、守护进程不可用或当前用户无权限"
			writeJSON(w, http.StatusOK, out)
			return
		}

		stdout := res.Stdout
		if len(stdout) > pruneOutMax {
			stdout = stdout[:pruneOutMax]
		}
		out.DockerAvailable = true
		out.Entries = parseDockerDf(stdout)
		writeJSON(w, http.StatusOK, out)
	}
}

// makeSystemPruneHandler 返回 POST /api/servers/{id}/system/prune(认证 + CSRF;写操作)。
// scope 严格枚举白名单 → 非法 400 invalid_prune_scope;服务器不存在 404;SSH/命令失败 →
// 200 + ok:false + 人读 error,不 500;成功投递后写审计(ActionSystemPrune,detail 仅 scope/ok)。
func makeSystemPruneHandler(svc target.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req systemPruneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		// AC-SEC-02:scope 严格枚举白名单,非法一律 400(命令不构造、不执行)。
		if err := validatePruneScope(req.Scope); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_prune_scope", "清理范围非法:"+err.Error())
			return
		}

		// 先确认服务器存在(404 在写任何 200 体之前)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		cmd := buildPruneCmd(req.Scope)
		out := systemPruneDTO{ServerID: id, Scope: req.Scope}

		// 审计 helper:写操作的**任何尝试**都留痕(NFR-8 取证)。detail 仅白名单结构化字段
		// (scope/ok,无自由输出/凭据);Recorder 再过 Masker。
		auditOp := func(ok bool) {
			recordAudit(r.Context(), aud, audit.Entry{
				Actor:      auditActor,
				Action:     audit.ActionSystemPrune,
				TargetType: audit.TargetServer,
				TargetID:   id,
				Detail:     map[string]any{"scope": req.Scope, "ok": ok},
				IP:         clientIP(r),
			})
		}

		ctx, cancel := context.WithTimeout(r.Context(), pruneCmdTimeout)
		defer cancel()

		res, err := svc.Exec(ctx, id, cmd)
		if err != nil {
			// 定位类错误:服务器不存在无可审计目标;凭据/vault 错误是对真实服务器的写尝试 → 留痕。
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
			// 连接/认证/执行类 → 200 + ok:false + 人读 error,不 500。
			out.OK = false
			out.Error = humanServiceError(err)
			auditOp(false)
			writeJSON(w, http.StatusOK, out)
			return
		}

		// 命令已投递。非零退出(未装 docker / 守护进程不可用 / 无权限)→ ok:false + stderr 人读。
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(res.Stdout)
			}
			if msg == "" {
				msg = "清理命令以非零状态退出(目标机未装 Docker、守护进程不可用或无权限)"
			}
			out.OK = false
			out.Output = truncateLog(strings.TrimSpace(res.Stdout), pruneOutMax)
			out.Error = truncateLog(msg, 1024)
		} else {
			out.OK = true
			out.Output = truncateLog(strings.TrimSpace(res.Stdout), pruneOutMax)
		}

		// 写操作受审计(NFR-8):命令已投递,按退出码记 ok 真伪。
		auditOp(out.OK)

		writeJSON(w, http.StatusOK, out)
	}
}
