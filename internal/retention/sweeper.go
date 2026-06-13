package retention

import (
	"context"
	"log"
	"sync"
	"time"
)

// Pruner 是 Sweeper 依赖的清理能力(由 Service 实现;便于测试注入)。
type Pruner interface {
	Prune(ctx context.Context, now time.Time) (int, error)
}

// Sweeper 是后台保留清理器:按固定间隔调用 Prune 裁剪过期运行数据。
// 这是平台首个**通用周期性维护任务**载体(区别于 cron 的「触发流水线运行」),
// 后续日报/提醒等周期任务可循此模式扩展。
type Sweeper struct {
	pruner   Pruner
	interval time.Duration

	stop chan struct{}
	wg   sync.WaitGroup
}

// NewSweeper 构造清理器。interval<=0 时用默认 1 小时。未 Start 不起 goroutine。
func NewSweeper(pruner Pruner, interval time.Duration) *Sweeper {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Sweeper{pruner: pruner, interval: interval, stop: make(chan struct{})}
}

// Start 启动后台清理 goroutine:启动后稍作延迟先跑一次,之后每 interval 跑一次。
// Stop / ctx 取消时退出。清理出错仅记日志,不影响平台。
func (s *Sweeper) Start(ctx context.Context) {
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

func (s *Sweeper) runOnce(ctx context.Context) {
	n, err := s.pruner.Prune(ctx, time.Now())
	if err != nil {
		log.Printf("[retention] 清理失败:%v", err)
		return
	}
	if n > 0 {
		log.Printf("[retention] 已清理 %d 条过期运行(连带其日志/步骤/产物)", n)
	}
}

// Stop 停止清理 goroutine(幂等)并等待退出。
func (s *Sweeper) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.wg.Wait()
}
