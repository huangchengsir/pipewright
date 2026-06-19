package previewenv

// sweeper.go 是「Per-PR 预览环境自动回收」的后台周期任务(R4 E4.1 收尾)。
//
// R4 交付时回收只有手动 API 入口(因当时没有 PR 关闭信号接入);本 sweeper 补上自动回收:
// 按固定间隔遍历全部 active 预览环境,逐个查其 PR 是否已 closed / merged,**确证已终结**才回收
// (删反代路由 + 标记 reclaimed)。安全铁律见 service.SweepReclaim / prstate.go:一切不确定不回收。
//
// 形态对齐 internal/retention.Sweeper(ticker + ctx 取消 + Stop 幂等 + recover 包裹永不被 panic 杀死)。

import (
	"context"
	"log"
	"sync"
	"time"
)

// reclaimSweeper 抽象 sweeper 依赖的「一轮自动回收」能力(由 service 实现;便于测试注入)。
type reclaimSweeper interface {
	SweepReclaim(ctx context.Context, checker PRStateChecker) (int, error)
}

// Sweeper 是预览环境自动回收的后台周期器:按固定间隔调用 SweepReclaim 回收 PR 已关闭/合并的环境。
type Sweeper struct {
	svc      reclaimSweeper
	checker  PRStateChecker
	interval time.Duration

	stop chan struct{}
	wg   sync.WaitGroup
}

// NewSweeper 构造自动回收器。interval<=0 用默认 5 分钟。checker 为 nil 时 sweep 为 no-op(优雅降级:
// 无 PR 状态读取能力 → 不回收任何环境,仅保留手动回收)。未 Start 不起 goroutine。
func NewSweeper(svc reclaimSweeper, checker PRStateChecker, interval time.Duration) *Sweeper {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Sweeper{svc: svc, checker: checker, interval: interval, stop: make(chan struct{})}
}

// Start 启动后台回收 goroutine:启动后稍作延迟先跑一次,之后每 interval 跑一次。
// Stop / ctx 取消时退出。单轮 panic 经 recover 兜住,绝不杀死循环。
func (s *Sweeper) Start(ctx context.Context) {
	if s.svc == nil || s.checker == nil {
		// 无回收能力 / 无 PR 状态读取器:不起后台任务(纯手动回收)。
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 启动后延迟首扫,避开启动高峰。
		first := time.NewTimer(time.Minute)
		defer first.Stop()
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-first.C:
			s.runOnce(ctx)
		}
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

// runOnce 跑一轮回收;recover 包裹:任何 panic 仅记日志,绝不杀死循环。
func (s *Sweeper) runOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[previewenv] 自动回收单轮 panic 已恢复:%v", r)
		}
	}()
	n, err := s.svc.SweepReclaim(ctx, s.checker)
	if err != nil {
		log.Printf("[previewenv] 自动回收失败:%v", err)
		return
	}
	if n > 0 {
		log.Printf("[previewenv] 已自动回收 %d 个 PR 已关闭/合并的预览环境", n)
	}
}

// Stop 停止回收 goroutine(幂等)并等待退出。
func (s *Sweeper) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.wg.Wait()
}
