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
	name      string // 容器名(sanitizeName(产物名))
	ref       string // 本次新镜像 ref(repo:tag / image id)
	prevImage string // 切换前容器所用镜像(回滚目标;"" = 无上一容器,首次部署)
}

// imageContainerName 取部署容器名(产物名净化;空 → app)。
func imageContainerName(a run.Artifact) string {
	n := sanitizeName(a.Name)
	if n == "" {
		n = "app"
	}
	return n
}

// stageImageOne 执行 image 蓝绿**预备阶段**:pull 新镜像(不停旧容器)+ 探测当前容器镜像(回滚目标)。
// 拉取失败 → (中间态, 人读 message, false)。
func (s *service) stageImageOne(ctx context.Context, srv *target.Server, a run.Artifact) (imageState, string, bool) {
	st := imageState{name: imageContainerName(a), ref: strings.TrimSpace(a.Reference)}
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

// activateImageOne 执行 image 蓝绿**切换阶段**:停旧容器 + 起新镜像容器 + 切后健康门控 + 失败回滚。
func (s *service) activateImageOne(ctx context.Context, srv *target.Server, a run.Artifact, hc *HealthCheck, st imageState, started time.Time) TargetResult {
	res := TargetResult{ServerID: srv.ID, ServerName: srv.Name, StartedAt: started}
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	// 切换:移除同名旧容器(幂等)→ 后台起新镜像容器。
	swap := [][]string{
		{"docker", "rm", "-f", st.name},
		{"docker", "run", "-d", "--name", st.name, st.ref},
	}
	if failMsg, ok := s.runStep(execCtx, srv.ID, swap); !ok {
		return finishFailed(res, failMsg)
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
		res.Message = fmt.Sprintf("image 蓝绿部署完成 → 容器 %s(%s,健康检查通过)", st.name, st.ref)
	} else {
		res.Message = fmt.Sprintf("image 蓝绿部署完成 → 容器 %s(%s)", st.name, st.ref)
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
		{"docker", "run", "-d", "--name", st.name, st.prevImage},
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
		{"docker", "run", "-d", "--name", st.name, st.prevImage},
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
