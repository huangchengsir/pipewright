package deploy

// image_release.go 把 image 产物也纳入「蓝绿(stage→cutover→回滚)」编排(Story 8-8 续 / FR-8-8):
//
// 容器无文件软链可切,故 image 蓝绿用「**全机先 pull(预备,不切换)→ 全机统一停旧起新(切换)→
// 切换阶段任一机健康失败则把已切换成功的机回滚到上一镜像**」的语义:
//   - stageImageOne   : docker pull 新镜像(只拉,不停旧容器)+ 探测当前容器镜像(回滚目标)。
//   - activateImageOne: docker rm -f 旧容器 + docker run 新镜像 + 切后健康门控;失败回滚到上一镜像。
//
// 与 release 文件模式一致的安全/语义:命令 array 化(不拼 shell)、错误不上抛(映射 status+人读)、
// message 无明文密钥。非 proxy 流量切分(单二进制轻量定位不引入 LB);提供机群一致切换 + 回滚。

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// imageState 是一台机的 image 蓝绿中间态(stageImageOne 产出,activateImageOne 消费)。
type imageState struct {
	name      string   // 容器名(cfg["containerName"] 优先,否则 sanitizeName(产物名))
	ref       string   // 本次新镜像 ref(repo:tag / image id)
	prevImage string   // 切换前容器所用镜像(回滚目标;"" = 无上一容器,首次部署)
	runArgs   []string // docker run 的附加参数(端口映射 / env 等;由 cfg 解析,参数自由)
}

// imageContainerName 取部署容器名:cfg["containerName"] 显式优先(净化),否则产物名净化(空 → app)。
func imageContainerName(a run.Artifact, cfg map[string]string) string {
	if c := sanitizeName(strings.TrimSpace(cfg["containerName"])); c != "" {
		return c
	}
	n := sanitizeName(a.Name)
	if n == "" {
		n = "app"
	}
	return n
}

// imageRunArgs 解析 docker run 的附加参数(端口映射 / 自定义 run 参数;参数自由,不写死)。
// 各参数原样作为 array 元素(绝不拼接 shell;AC-SEC-02),依序拼到 `docker run -d --name <name>` 之后、
// 镜像 ref 之前:
//   - cfg["ports"]   : 端口映射,逗号 / 空白分隔,每项展开为 `-p <map>`(如 "8080:80,9000:9000")。
//   - cfg["runArgs"] : 任意 docker run 参数,按空白切词逐元素追加(如 "-e KEY=v --restart always")。
//
// 注意:凭据绝不经此进命令(registry 凭据沿用既有 docker login 模式);此处仅承载端口 / 运行参数。
func imageRunArgs(cfg map[string]string) []string {
	if cfg == nil {
		return nil
	}
	var args []string
	for _, p := range splitImageList(cfg["ports"]) {
		args = append(args, "-p", p)
	}
	args = append(args, strings.Fields(cfg["runArgs"])...)
	return args
}

// splitImageList 按逗号 / 空白切分并去空(端口列表用)。
func splitImageList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// dockerRunCmd 组装 `docker run -d --name <name> [runArgs...] <ref>`(各元素独立,不拼 shell)。
func dockerRunCmd(name string, runArgs []string, ref string) []string {
	cmd := []string{"docker", "run", "-d", "--name", name}
	cmd = append(cmd, runArgs...)
	cmd = append(cmd, ref)
	return cmd
}

// stageImageOne 执行 image 蓝绿**预备阶段**:pull 新镜像(不停旧容器)+ 探测当前容器镜像(回滚目标)。
// 拉取失败 → (中间态, 人读 message, false)。
func (s *service) stageImageOne(ctx context.Context, srv *target.Server, a run.Artifact, cfg map[string]string) (imageState, string, bool) {
	st := imageState{name: imageContainerName(a, cfg), ref: strings.TrimSpace(a.Reference), runArgs: imageRunArgs(cfg)}
	if st.ref == "" {
		return st, "image 产物缺少 reference(repo:tag 或镜像 id)", false
	}
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	// 探测当前同名容器所用镜像(供回滚);无容器 / 读失败 → 空(首次部署,无可回滚)。
	st.prevImage = s.readContainerImage(execCtx, srv.ID, st.name)

	// 仅 pull,不动旧容器(零中断预备)。
	if failMsg, ok := s.runStep(execCtx, srv.ID, [][]string{{"docker", "pull", st.ref}}); !ok {
		return st, failMsg, false
	}
	return st, "", true
}

// deployImageOne 执行**滚动 / 金丝雀**单机 image 部署:pull 新镜像(捕获上一镜像作回滚目标)→
// 停旧起新 + 切后健康门控 → 健康失败回滚到上一镜像。复用蓝绿单机原语 stageImageOne/activateImageOne,
// 但每机独立(无机群协调)。
//
// 这补齐了此前缺口:旧的 buildImageDeploy 扁平命令路径(pull→rm→run)**无任何回滚** —— 新容器起不来
// 或切后健康失败时,旧容器已被 rm,目标机被留在「无容器 / 坏容器」状态。现在与蓝绿一致:失败即回滚
// 到上一镜像(首次部署无上一镜像可回滚则记 failed)。
func (s *service) deployImageOne(ctx context.Context, srv *target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck, started time.Time) TargetResult {
	st, failMsg, ok := s.stageImageOne(ctx, srv, a, cfg)
	if !ok {
		return finishFailed(TargetResult{ServerID: srv.ID, ServerName: srv.Name, StartedAt: started}, failMsg)
	}
	return s.activateImageOne(ctx, srv, a, hc, st, started, "")
}

// activateImageOne 执行 image **切换阶段**:停旧容器 + 起新镜像容器 + 切后健康门控 + 失败回滚到上一镜像。
// modeLabel 仅用于成功文案区分编排策略("蓝绿" / 空=滚动/金丝雀);回滚文案策略无关。
func (s *service) activateImageOne(ctx context.Context, srv *target.Server, a run.Artifact, hc *HealthCheck, st imageState, started time.Time, modeLabel string) TargetResult {
	res := TargetResult{ServerID: srv.ID, ServerName: srv.Name, StartedAt: started}
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	// 切换:移除同名旧容器(幂等)→ 后台起新镜像容器(带 cfg 解析的端口 / run 参数)。
	swap := [][]string{
		{"docker", "rm", "-f", st.name},
		dockerRunCmd(st.name, st.runArgs, st.ref),
	}
	if failMsg, ok := s.runStep(execCtx, srv.ID, swap); !ok {
		// 起新容器失败:尽力回滚到上一镜像(与健康失败同语义),避免目标机被留在坏状态。
		return s.rollbackImage(execCtx, srv, res, st, failMsg)
	}

	// 切后健康门控;失败触发回滚到上一镜像。
	if hc.enabled() {
		if herr := s.runHealthCheck(execCtx, srv.ID, hc); herr != nil {
			return s.rollbackImage(execCtx, srv, res, st, herr.Error())
		}
	}

	finish := time.Now().UTC()
	res.Status = run.TargetSuccess
	if hc.enabled() {
		res.Message = fmt.Sprintf("image %s部署完成 → 容器 %s(%s,健康检查通过)", modeLabel, st.name, st.ref)
	} else {
		res.Message = fmt.Sprintf("image %s部署完成 → 容器 %s(%s)", modeLabel, st.name, st.ref)
	}
	res.FinishedAt = &finish
	return res
}

// rollbackImage 在健康失败后把容器回滚到上一镜像(rm 新容器 → run 上一镜像)。
// 无上一镜像(首次部署)→ failed;回滚命令失败仍记 rolled_back(尽力)。
func (s *service) rollbackImage(ctx context.Context, srv *target.Server, res TargetResult, st imageState, healthMsg string) TargetResult {
	finish := time.Now().UTC()
	res.FinishedAt = &finish
	if st.prevImage == "" {
		res.Status = run.TargetFailed
		res.Message = fmt.Sprintf("健康检查失败且无上一镜像可回滚(首次部署):%s", healthMsg)
		return res
	}
	var rbErr error
	for _, cmd := range [][]string{
		{"docker", "rm", "-f", st.name},
		dockerRunCmd(st.name, st.runArgs, st.prevImage),
	} {
		if _, e := s.targets.Exec(ctx, srv.ID, cmd); e != nil {
			rbErr = e
			break
		}
	}
	res.Status = run.TargetRolledBack
	if rbErr != nil {
		res.Message = fmt.Sprintf("健康检查失败,已尝试回滚容器 %s → 上一镜像 %s,但回滚命令失败:%s(健康原因:%s)",
			st.name, st.prevImage, humanExecError(rbErr), healthMsg)
		return res
	}
	res.Message = fmt.Sprintf("健康检查失败,已回滚容器 %s → 上一镜像 %s(健康原因:%s)", st.name, st.prevImage, healthMsg)
	return res
}

// fleetRollbackImageOne 把一台「本机切换成功、但机群其它机失败」的容器回滚到上一镜像(蓝绿机群级原子性)。
func (s *service) fleetRollbackImageOne(ctx context.Context, srv *target.Server, st imageState, res *TargetResult) {
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	var rbErr error
	for _, cmd := range [][]string{
		{"docker", "rm", "-f", st.name},
		dockerRunCmd(st.name, st.runArgs, st.prevImage),
	} {
		if _, e := s.targets.Exec(execCtx, srv.ID, cmd); e != nil {
			rbErr = e
			break
		}
	}
	finish := time.Now().UTC()
	res.Status = run.TargetRolledBack
	res.FinishedAt = &finish
	if rbErr != nil {
		res.Message = "蓝绿:其它机切换失败,本机尝试回滚到上一镜像但回滚命令失败:" + humanExecError(rbErr)
		return
	}
	res.Message = "蓝绿:其它机切换失败,本机已回滚到上一镜像(机群级原子性:要么全切要么全退)"
}

// readContainerImage 读同名容器当前所用镜像(docker inspect);无容器 / 读失败 → ""。
func (s *service) readContainerImage(ctx context.Context, serverID, name string) string {
	out, err := s.targets.Exec(ctx, serverID, []string{"docker", "inspect", "--format", "{{.Config.Image}}", name})
	if err != nil || out == nil || out.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(out.Stdout)
}
