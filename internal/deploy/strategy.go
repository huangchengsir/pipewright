package deploy

// strategy.go 实现「部署策略」(Story 8-8 / FR-8-8):在既有多机扇出之上叠加 **金丝雀(canary)**
// 与 **蓝绿(blue-green)** 两种发布编排,默认 **滚动(rolling)** = 原有 deployFanout 行为不变。
//
// 策略是**机群级编排**关注点(如何在 N 台之间排序 / 门控 / 统一切换),不改单机执行语义:
//   - rolling   : 全机有界并行,各机独立成败,失败机自行回滚(deployFanout,原状)。
//   - canary    : 先发**金丝雀子集**(默认 1 台)→ 全过才铺其余;金丝雀任一失败 → 中止其余
//                 (其余标 failed 人读「未部署,仍运行旧版本」)。复用 deployOne,任意产物类型。
//   - blue_green: **stage-all → cutover-all**(release 类产物 dist/jar):全机先就绪发布目录(不切换),
//                 全部就绪才统一原子切换 + 健康;切换阶段任一失败 → 把**已切换成功**的机一并回滚到上一发布
//                 (机群级原子性)。非 release 产物(image/archive)无 stage/cutover 之分 → 退化 rolling。
//
// 安全不变量沿用:命令 array 化(不拼 shell)、单机 panic recover、有界并发(maxParallelDeploys)、
// message 无明文密钥、错误不上抛(映射 status + 人读)。

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 部署策略枚举(DeployInput.Strategy / Config["strategy"];空 / 未知 → rolling)。
const (
	// StrategyRolling 滚动发布(默认):全机有界并行,各机独立成败。
	StrategyRolling = "rolling"
	// StrategyCanary 金丝雀:先发小批,健康门控通过才铺其余,否则中止。
	StrategyCanary = "canary"
	// StrategyBlueGreen 蓝绿:全机先就绪、统一切换、机群级失败回滚(release 类产物)。
	StrategyBlueGreen = "blue_green"
)

// NormalizeStrategy 归一策略串(大小写 / 连字符容错;空 / 未知 → rolling)。
func NormalizeStrategy(s string) string {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case StrategyCanary:
		return StrategyCanary
	case StrategyBlueGreen, "blue-green", "bluegreen", "blue green":
		return StrategyBlueGreen
	default:
		return StrategyRolling
	}
}

// deployWithStrategy 按策略调度多机部署。结果按 servers 输入顺序对齐(稳定可断言)。
func (s *service) deployWithStrategy(ctx context.Context, servers []*target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck, strategy string) []TargetResult {
	switch strategy {
	case StrategyCanary:
		return s.deployCanary(ctx, servers, a, cfg, hc)
	case StrategyBlueGreen:
		// 蓝绿需 stage/cutover 两阶段:release 类文件产物(dist/jar/archive)走软链切换;image 走
		// pull→停旧起新→回滚上一镜像;其余类型无两阶段语义 → 退化滚动。
		switch {
		case releaseModeArtifact(a):
			return s.deployBlueGreen(ctx, servers, a, cfg, hc)
		case a.Type == run.ArtifactImage:
			return s.deployBlueGreenImage(ctx, servers, a, cfg, hc)
		default:
			return s.deployFanout(ctx, servers, a, cfg, hc)
		}
	default:
		return s.deployFanout(ctx, servers, a, cfg, hc)
	}
}

// canaryCount 解析金丝雀批量(Config["canaryCount"] 优先;否则 Config["canaryPercent"] 向上取整)。
// 默认 1 台。夹紧:总数 1 → 1(整体即金丝雀);否则 [1, total-1](至少留 1 台在「其余」)。
func canaryCount(cfg map[string]string, total int) int {
	if total <= 1 {
		return total
	}
	n := 1
	if raw := strings.TrimSpace(cfg["canaryCount"]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			n = v
		}
	} else if raw := strings.TrimSpace(cfg["canaryPercent"]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			n = (total*v + 99) / 100 // 向上取整
		}
	}
	if n >= total {
		n = total - 1
	}
	if n < 1 {
		n = 1
	}
	return n
}

// allSuccess 报告一批结果是否全为 success(canary 门控 / 蓝绿 cutover 判定)。
func allSuccess(results []TargetResult) bool {
	for i := range results {
		if results[i].Status != run.TargetSuccess {
			return false
		}
	}
	return len(results) > 0
}

// abortedResult 合成「因门控中止、本机未部署」的结果(canary 中止其余 / 蓝绿预备中止)。
func abortedResult(srv *target.Server, msg string) TargetResult {
	now := time.Now().UTC()
	return TargetResult{
		ServerID:   srv.ID,
		ServerName: srv.Name,
		Status:     run.TargetFailed,
		Message:    msg,
		StartedAt:  now,
		FinishedAt: &now,
	}
}

// deployCanary 金丝雀发布:先发金丝雀子集,健康门控通过才铺其余;否则中止其余。
func (s *service) deployCanary(ctx context.Context, servers []*target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck) []TargetResult {
	total := len(servers)
	if total == 0 {
		return nil
	}
	n := canaryCount(cfg, total)
	results := make([]TargetResult, total)

	// 1) 金丝雀批次(servers[:n])。
	canaryRes := s.deployFanout(ctx, servers[:n], a, cfg, hc)
	copy(results[:n], canaryRes)

	rest := servers[n:]
	if len(rest) == 0 {
		return results // 单机或全是金丝雀:无「其余」可铺。
	}

	// 2) 金丝雀全过 → 铺其余;否则中止其余(标 failed 人读,本机未部署)。
	if allSuccess(canaryRes) {
		restRes := s.deployFanout(ctx, rest, a, cfg, hc)
		copy(results[n:], restRes)
	} else {
		for i, srv := range rest {
			results[n+i] = abortedResult(srv, fmt.Sprintf("金丝雀批次(%d 台)未全部通过,已中止后续 %d 台部署(本机未部署,仍运行旧版本)", n, len(rest)))
		}
	}
	return results
}

// deployBlueGreen 蓝绿发布(release 类产物):stage-all → cutover-all → 失败机群回滚。
//
//  1. **预备(stage-all)**:全机并行就绪发布目录(写产物,不切换 current)。任一就绪失败 →
//     中止:已就绪的机不切换(仍运行旧版本),标 failed 人读;整体无任何切换(安全)。
//  2. **切换(cutover-all)**:全机并行原子切换 current + 切后健康门控(activateReleaseOne)。
//  3. **机群回滚**:cutover 阶段任一非 success(failed / 自身已 rolled_back)→ 把**本阶段切换成功**
//     且有上一发布的机一并回滚到上一发布(标 rolled_back 人读),实现机群级「要么全切要么全退」。
func (s *service) deployBlueGreen(ctx context.Context, servers []*target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck) []TargetResult {
	n := len(servers)
	if n == 0 {
		return nil
	}
	results := make([]TargetResult, n)
	states := make([]releaseState, n)
	staged := make([]bool, n)
	started := make([]time.Time, n)

	// ── 阶段 1:全机就绪(不切换)──────────────────────────────────────────────
	s.forEachServer(servers, func(idx int, srv *target.Server) {
		started[idx] = time.Now().UTC()
		st, failMsg, ok := s.stageReleaseOne(ctx, srv, a, cfg)
		states[idx] = st
		if !ok {
			results[idx] = finishFailed(TargetResult{ServerID: srv.ID, ServerName: srv.Name, StartedAt: started[idx]}, failMsg)
			return
		}
		staged[idx] = true
	}, func(idx int, srv *target.Server) {
		results[idx] = abortedResult(srv, "蓝绿预备阶段执行异常中断(本机未切换)")
	})

	// 任一就绪失败 → 中止:已就绪机不切换(仍运行旧版本),标 failed 人读。
	allStaged := true
	for i := range staged {
		if !staged[i] {
			allStaged = false
			break
		}
	}
	if !allStaged {
		for i, srv := range servers {
			if staged[i] {
				results[i] = abortedResult(srv, "蓝绿:预备阶段其它机失败,已中止本机切换(仍运行旧版本)")
			}
		}
		return results
	}

	// ── 阶段 2:全机统一切换 + 健康 ───────────────────────────────────────────
	s.forEachServer(servers, func(idx int, srv *target.Server) {
		results[idx] = s.activateReleaseOne(ctx, srv, a, cfg, hc, states[idx], started[idx])
	}, func(idx int, srv *target.Server) {
		results[idx] = abortedResult(srv, "蓝绿切换阶段执行异常中断")
	})

	// ── 阶段 3:切换阶段任一失败 → 已成功切换的机群回滚到上一发布 ──────────────
	if !allSuccess(results) {
		s.forEachServer(servers, func(idx int, srv *target.Server) {
			if results[idx].Status != run.TargetSuccess {
				return // 失败 / 已自行回滚的机不动。
			}
			if states[idx].prev == "" {
				return // 首次部署无上一发布可回滚:保留(无更好选择;切换阶段它本机是健康的)。
			}
			s.fleetRollbackOne(ctx, srv, states[idx], &results[idx])
		}, func(idx int, srv *target.Server) {
			// 回滚异常:保留原成功结果,不致命(尽力回滚)。
		})
	}
	return results
}

// deployBlueGreenImage 镜像蓝绿:stage-all(全机 pull)→ cutover-all(全机停旧起新+健康)→
// 切换阶段任一失败则把已成功切换的机一并回滚到上一镜像(机群级原子性)。语义同 deployBlueGreen,
// 只是单机原语换成 stageImageOne/activateImageOne(容器 pull/swap 而非软链切换)。
func (s *service) deployBlueGreenImage(ctx context.Context, servers []*target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck) []TargetResult {
	n := len(servers)
	if n == 0 {
		return nil
	}
	results := make([]TargetResult, n)
	states := make([]imageState, n)
	staged := make([]bool, n)
	started := make([]time.Time, n)

	// 阶段 1:全机 pull(不停旧容器)。
	s.forEachServer(servers, func(idx int, srv *target.Server) {
		started[idx] = time.Now().UTC()
		st, failMsg, ok := s.stageImageOne(ctx, srv, a, cfg)
		states[idx] = st
		if !ok {
			results[idx] = finishFailed(TargetResult{ServerID: srv.ID, ServerName: srv.Name, StartedAt: started[idx]}, failMsg)
			return
		}
		staged[idx] = true
	}, func(idx int, srv *target.Server) {
		results[idx] = abortedResult(srv, "蓝绿预备(pull)阶段执行异常中断(本机未切换)")
	})

	allStaged := true
	for i := range staged {
		if !staged[i] {
			allStaged = false
			break
		}
	}
	if !allStaged {
		for i, srv := range servers {
			if staged[i] {
				results[i] = abortedResult(srv, "蓝绿:预备(pull)阶段其它机失败,已中止本机切换(旧容器仍在跑)")
			}
		}
		return results
	}

	// 阶段 2:全机统一停旧起新 + 健康。
	s.forEachServer(servers, func(idx int, srv *target.Server) {
		results[idx] = s.activateImageOne(ctx, srv, a, hc, states[idx], started[idx])
	}, func(idx int, srv *target.Server) {
		results[idx] = abortedResult(srv, "蓝绿切换阶段执行异常中断")
	})

	// 阶段 3:切换任一失败 → 已成功切换且有上一镜像的机一并回滚。
	if !allSuccess(results) {
		s.forEachServer(servers, func(idx int, srv *target.Server) {
			if results[idx].Status != run.TargetSuccess || states[idx].prevImage == "" {
				return
			}
			s.fleetRollbackImageOne(ctx, srv, states[idx], &results[idx])
		}, func(idx int, srv *target.Server) {})
	}
	return results
}

// fleetRollbackOne 把一台「本机切换成功、但机群中其它机失败」的目标回滚到上一发布(蓝绿阶段 3)。
// 原子切回 current → prev;就地改写该机结果为 rolled_back + 人读。回滚命令失败仍记 rolled_back(意图)。
func (s *service) fleetRollbackOne(ctx context.Context, srv *target.Server, st releaseState, res *TargetResult) {
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	var rbErr error
	for _, cmd := range atomicSymlinkCmds(st.prev, st.current) {
		if _, e := s.targets.Exec(execCtx, srv.ID, cmd); e != nil {
			rbErr = e
			break
		}
	}
	finish := time.Now().UTC()
	res.Status = run.TargetRolledBack
	res.FinishedAt = &finish
	if rbErr != nil {
		res.Message = "蓝绿:其它机切换失败,本机尝试回滚到上一发布但回滚命令执行失败:" + humanExecError(rbErr)
		return
	}
	res.Message = "蓝绿:其它机切换失败,本机已回滚到上一发布(机群级原子性:要么全切要么全退)"
}

// forEachServer 以有界并发(maxParallelDeploys)对每台 server 跑 work;每 goroutine recover 兜底,
// panic → 调用 onPanic(由其写入该机失败结果)。work / onPanic 各自写入自己的索引槽,不共享可变状态外的竞争。
func (s *service) forEachServer(servers []*target.Server, work func(idx int, srv *target.Server), onPanic func(idx int, srv *target.Server)) {
	sem := make(chan struct{}, maxParallelDeploys)
	var wg sync.WaitGroup
	for i := range servers {
		wg.Add(1)
		go func(idx int, srv *target.Server) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if rec := recover(); rec != nil {
					onPanic(idx, srv)
				}
			}()
			work(idx, srv)
		}(i, servers[i])
	}
	wg.Wait()
}
