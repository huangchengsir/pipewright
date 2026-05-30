package deploy

// release.go 实现「零停机切换 + 失败回滚」(FR-11 / FR-13;Story 4.4)。
//
// dist / jar 类产物走「发布目录 + current 软链原子切换」模式(image 类型本期仍 docker run,
// 零停机切换留后续):
//
//	<base>/
//	  releases/
//	    <runId-1>/   ← 历史发布(回滚目标)
//	    <runId-2>/   ← 本次发布
//	  current  →  releases/<runId-2>   （软链;ln -sfn 原子替换,切换瞬时,零停机）
//
// 流程(deployReleaseOne):
//  1. 探测上一发布:readlink <base>/current(无 → 首次部署,无可回滚)。
//  2. mkdir -p <base>/releases/<runId> → 把产物负载写入该发布目录。
//  3. **原子切换** ln -sfn releases/<runId> <base>/current(幂等;切换瞬时)。
//  4. (4-3 健康门控)切换之后跑健康探测:
//       - 通过 → 该机 success(保留上一发布供回滚)+ keepReleases 清理旧发布。
//       - 失败 + 有上一发布 → **回滚**:ln -sfn <上一发布> <base>/current + status=rolled_back + 人读 message。
//       - 失败 + 无上一发布(首次)→ status=failed(无可回滚)+ 人读 message。
//       - 回滚动作本身失败 → 仍记录 rolled_back + 人读(不 500;尽力回滚)。
//
// 全程命令 **array 化**([]string)经 target.Exec(AC-SEC-02 不拼 shell);切换 / 回滚命令幂等。

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// defaultKeepReleases 是未显式配置时保留的旧发布份数(FR-11:默认留上一版本 1 份)。
// 注意:current 指向的「本次发布」始终保留;keepReleases 指的是 current 之外额外保留的旧发布数。
const defaultKeepReleases = 1

// maxKeepReleases 夹紧保留份数上限(防误配留太多撑爆磁盘)。
const maxKeepReleases = 50

// releaseModeArtifact 判定产物是否走「发布目录 + current 软链」零停机模式。
// dist / jar 走 release 模式;image 本期仍 docker run(commands.go),archive 维持文件部署。
func releaseModeArtifact(a run.Artifact) bool {
	switch a.Type {
	case run.ArtifactDist, run.ArtifactJar:
		return true
	default:
		return false
	}
}

// releaseBase 解析发布根目录(零停机切换的 <base>):
//   - Config["releaseBase"] 显式优先;
//   - 否则从 Config["path"](4-2 既有部署目录)推导;
//   - 否则 defaultDeployRoot/<产物名净化>。
//
// release 模式下产物落 <base>/releases/<runId>,current 软链落 <base>/current。
func releaseBase(a run.Artifact, cfg map[string]string) string {
	if b := strings.TrimSpace(cfg["releaseBase"]); b != "" {
		return b
	}
	if p := strings.TrimSpace(cfg["path"]); p != "" {
		return p
	}
	name := sanitizeName(a.Name)
	if name == "" {
		name = "app"
	}
	return path.Join(defaultDeployRoot, name)
}

// keepReleases 归一并夹紧保留份数(<=0 → 默认 1;> 上限 → 上限)。
func keepReleases(cfg map[string]string) int {
	raw := strings.TrimSpace(cfg["keepReleases"])
	if raw == "" {
		return defaultKeepReleases
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultKeepReleases
	}
	if n > maxKeepReleases {
		return maxKeepReleases
	}
	return n
}

// deployReleaseOne 在一台目标机上执行「发布目录 + current 软链原子切换 + 健康门控 + 失败回滚」。
// 仅在 releaseModeArtifact(a) 为真时被 deployOne 调用。执行错误**不上抛**:映射为 status=failed /
// rolled_back + 人读 message(绝无明文密钥)。
func (s *service) deployReleaseOne(ctx context.Context, srv *target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck) TargetResult {
	started := time.Now().UTC()
	res := TargetResult{ServerID: srv.ID, ServerName: srv.Name, StartedAt: started}

	base := releaseBase(a, cfg)
	releasesDir := path.Join(base, "releases")
	current := path.Join(base, "current")
	release := path.Join(releasesDir, sanitizeRunID(a.RunID))
	file := path.Join(release, deployFileName(a))

	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	// 1) 探测上一发布(readlink current);失败 / 无软链 → prev 为空(首次部署,无可回滚)。
	prev := s.readCurrentRelease(execCtx, srv.ID, current)

	// 2) mkdir 发布目录 → 写入产物负载(base64 经 array 命令落地,不拼 shell)。
	payload := base64.StdEncoding.EncodeToString([]byte(a.Reference + "\n"))
	placeCmds := [][]string{
		{"mkdir", "-p", release},
		{"sh", "-c", `printf '%s' "$1" | base64 -d > "$0"`, file, payload},
	}
	if a.Type == run.ArtifactJar {
		// jar:放置后探测启动命令(目标无 java → 非零退出 → 该机 failed 人读)。
		placeCmds = append(placeCmds, []string{"java", "-jar", file, "--version"})
	}
	if failMsg, ok := s.runStep(execCtx, srv.ID, placeCmds); !ok {
		return finishFailed(res, failMsg)
	}

	// 3) **真·原子**切换 current 软链 → 本次发布(code-review P2)。
	// `ln -sfn` 在 current 已存在时是 unlink+symlink 两步,中间有 current 不存在的窗口(并发请求 404),
	// 破坏「零停机」。改为「ln 到临时名 + `mv -T` 原子 rename」:rename(2) 是 POSIX 原子,无窗口。
	if failMsg, ok := s.runStep(execCtx, srv.ID, atomicSymlinkCmds(release, current)); !ok {
		return finishFailed(res, failMsg)
	}

	// 4) 切换之后跑健康门控(4-3);失败触发回滚。
	if hc.enabled() {
		if herr := s.runHealthCheck(execCtx, srv.ID, hc); herr != nil {
			return s.rollback(execCtx, srv, res, current, prev, release, herr.Error())
		}
	}

	// 5) 成功(健康通过或未配置健康检查)→ 清理超 keepReleases 的旧发布(尽力;失败不影响成功态)。
	keep := keepReleases(cfg)
	s.pruneReleases(execCtx, srv.ID, releasesDir, sanitizeRunID(a.RunID), prev, keep)

	finish := time.Now().UTC()
	res.Status = run.TargetSuccess
	if hc.enabled() {
		res.Message = fmt.Sprintf("%s 零停机部署完成 → current → %s(健康检查通过)", a.Type, release)
	} else {
		res.Message = fmt.Sprintf("%s 零停机部署完成 → current → %s", a.Type, release)
	}
	res.FinishedAt = &finish
	return res
}

// rollback 在健康门控失败后回滚 current 软链到上一发布。
//   - 有上一发布:ln -sfn <上一发布> current → status=rolled_back + 人读(说明回滚到哪个 release)。
//     回滚命令本身失败 → 仍记 rolled_back(尽力回滚)+ 人读说明回滚未确认(不 500)。
//   - 无上一发布(首次部署):无可回滚 → status=failed + 人读。
func (s *service) rollback(ctx context.Context, srv *target.Server, res TargetResult, current, prev, release, healthMsg string) TargetResult {
	finish := time.Now().UTC()
	res.FinishedAt = &finish

	if prev == "" {
		// 首次部署 + 健康失败 → 无可回滚。坏版本已在 current,但无上一可切;记 failed 人读。
		res.Status = run.TargetFailed
		res.Message = fmt.Sprintf("健康检查失败且无上一发布可回滚(首次部署):%s", healthMsg)
		return res
	}

	// 回滚:把 current 软链原子切回上一发布(code-review P2:同样 ln tmp + mv -T,避免回滚窗口)。
	var rbErr error
	for _, cmd := range atomicSymlinkCmds(prev, current) {
		if _, e := s.targets.Exec(ctx, srv.ID, cmd); e != nil {
			rbErr = e
			break
		}
	}
	res.Status = run.TargetRolledBack
	prevName := path.Base(prev)
	if rbErr != nil {
		// 回滚动作本身失败:仍记 rolled_back(语义:意图回滚),人读说明回滚未确认。
		res.Message = fmt.Sprintf("健康检查失败,已尝试回滚 current → 上一发布 %s,但回滚命令执行失败:%s(健康原因:%s)",
			prevName, humanExecError(rbErr), healthMsg)
		return res
	}
	res.Message = fmt.Sprintf("健康检查失败,已回滚 current → 上一发布 %s(失败发布 %s 保留供排查;健康原因:%s)",
		prevName, path.Base(release), healthMsg)
	return res
}

// readCurrentRelease 经 readlink 读 current 软链指向的发布绝对路径(无软链 / 读失败 → "")。
// 用于回滚目标探测;首次部署时 current 不存在 → 返回 ""(无可回滚)。
func (s *service) readCurrentRelease(ctx context.Context, serverID, current string) string {
	out, err := s.targets.Exec(ctx, serverID, []string{"readlink", current})
	if err != nil || out == nil || out.ExitCode != 0 {
		return ""
	}
	link := strings.TrimSpace(out.Stdout)
	if link == "" {
		return ""
	}
	// readlink 可能返回相对路径(ln -sfn 用绝对则为绝对);归一为绝对(相对则挂回 current 所在目录)。
	if !path.IsAbs(link) {
		link = path.Join(path.Dir(current), link)
	}
	return link
}

// pruneReleases 清理 releasesDir 下超出 keepReleases 的旧发布(尽力;失败不影响成功态)。
// 保留:当前发布(curRunID)+ 上一发布(prev,回滚目标)+ 最近 keep-1 个其它发布。
// 用 find + sort 经单条 array 化 sh -c(脚本体为固定模板,目录 / 保留名作位置参数传入,不拼 shell)。
func (s *service) pruneReleases(ctx context.Context, serverID, releasesDir, curRunID, prev string, keep int) {
	prevName := ""
	if prev != "" {
		prevName = path.Base(prev)
	}
	// 固定脚本:列 releasesDir 下直接子目录(按 mtime 新→旧),跳过 current 与 prev,
	// 保留前 keep 个,其余 rm -rf。目录 / 保留名 / keep 作位置参数($0..$3),绝不拼进脚本体。
	// code-review P3:`for d in $(ls)` 默认按空白词分裂 + 路径名展开(glob)→ 含空格/`*` 的目录会
	// 拆错或 `rm -rf` 误删。`set -f` 禁 glob + `IFS=换行` 只按行分(不拆空格),且 for(非管道)保留 n 计数。
	script := `dir="$0"; keep="$1"; cur="$2"; prev="$3"; ` +
		`[ -d "$dir" ] || exit 0; ` +
		"set -f; IFS='\n'; n=0; " +
		`for d in $(ls -1t "$dir" 2>/dev/null); do ` +
		`  [ -d "$dir/$d" ] || continue; ` +
		`  if [ "$d" = "$cur" ] || [ "$d" = "$prev" ]; then continue; fi; ` +
		`  n=$((n+1)); ` +
		`  if [ "$n" -gt "$keep" ]; then rm -rf "$dir/$d"; fi; ` +
		`done`
	_, _ = s.targets.Exec(ctx, serverID, []string{
		"sh", "-c", script, releasesDir, strconv.Itoa(keep), curRunID, prevName,
	})
}

// atomicSymlinkCmds 返回「原子替换软链 link → target」的命令序列(code-review P2):
// `ln -sfn target link.tmp`(新建临时软链,不碰 link)+ `mv -T link.tmp link`(rename 原子替换,
// `-T` 防 link 为目录软链时把 tmp 移进目标目录)。避免 `ln -sfn` 直接覆盖的 unlink+symlink 窗口。
func atomicSymlinkCmds(target, link string) [][]string {
	tmp := link + ".tmp"
	return [][]string{
		{"ln", "-sfn", target, tmp},
		{"mv", "-T", tmp, link},
	}
}

// runStep 顺序执行一组 array 命令;任一执行错误 / 非零退出 → 返回 (人读 message, false)。
// 全部成功 → ("", true)。供 deployReleaseOne 的放置 / 切换阶段复用。
func (s *service) runStep(ctx context.Context, serverID string, cmds [][]string) (string, bool) {
	for _, cmd := range cmds {
		out, eerr := s.targets.Exec(ctx, serverID, cmd)
		if eerr != nil {
			return humanExecError(eerr), false
		}
		if out != nil && out.ExitCode != 0 {
			return fmt.Sprintf("部署命令退出码 %d:%s", out.ExitCode, truncate(strings.TrimSpace(out.Stderr))), false
		}
	}
	return "", true
}

// finishFailed 把结果置 failed + 人读 message + 结束时间。
func finishFailed(res TargetResult, msg string) TargetResult {
	finish := time.Now().UTC()
	res.Status = run.TargetFailed
	res.Message = msg
	res.FinishedAt = &finish
	return res
}

// sanitizeRunID 把 runId 净化为安全发布目录段(仅字母数字 . _ -;其余替为 _)。
// 复用 sanitizeName 规则;runId 通常是 uuid,净化为防御性措施。
func sanitizeRunID(id string) string {
	s := sanitizeName(strings.TrimSpace(id))
	if s == "" {
		s = "release"
	}
	return s
}
