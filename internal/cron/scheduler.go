package cron

import (
	"context"
	"log"
	"sync"
	"time"
)

// Clock 抽象当前时间(便于注入测试)。
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Entry 是一条启用的项目定时配置。
type Entry struct {
	ProjectID  string
	Expression string
	Branch     string
}

// Store 列出当前启用的定时配置(由持久层实现)。
type Store interface {
	ListEnabled(ctx context.Context) ([]Entry, error)
}

// Runner 为某项目创建一次「定时触发」的运行,返回 runID。
// 由 run.Service 在 main 装配时适配(cron 包不 import run,避免耦合)。
type Runner interface {
	CreateScheduledRun(ctx context.Context, projectID, branch string) (runID string, err error)
}

// Scheduler 是分钟粒度的定时调度器:每分钟取启用配置,匹配当前分钟的逐个创建运行。
// 同一分钟内对同一项目只触发一次(去重),避免重复运行(防滥用:最小粒度=1 分钟)。
type Scheduler struct {
	store  Store
	runner Runner
	clock  Clock

	mu        sync.Mutex
	lastFired map[string]string // projectID → 已触发的分钟键 "2006-01-02T15:04"

	stop chan struct{}
	wg   sync.WaitGroup
}

// Option 配置 Scheduler。
type Option func(*Scheduler)

// WithClock 注入时钟(测试用;缺省真实时钟)。
func WithClock(c Clock) Option {
	return func(s *Scheduler) {
		if c != nil {
			s.clock = c
		}
	}
}

// NewScheduler 构造调度器(未 Start 时不起任何 goroutine,不驻留)。
func NewScheduler(store Store, runner Runner, opts ...Option) *Scheduler {
	s := &Scheduler{
		store:     store,
		runner:    runner,
		clock:     realClock{},
		lastFired: make(map[string]string),
		stop:      make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Tick 评估一个分钟时刻:对所有启用且命中的配置创建运行(每项目每分钟至多一次)。
// 表达式非法的配置跳过(记日志,不阻断其它)。本方法与真实定时解耦,便于注入时钟单测。
func (s *Scheduler) Tick(ctx context.Context, now time.Time) {
	entries, err := s.store.ListEnabled(ctx)
	if err != nil {
		log.Printf("[cron] 列举启用配置失败:%v", err)
		return
	}
	minuteKey := now.Format("2006-01-02T15:04")
	for _, e := range entries {
		sched, perr := Parse(e.Expression)
		if perr != nil {
			log.Printf("[cron] 项目 %s 表达式非法,跳过:%v", e.ProjectID, perr)
			continue
		}
		if !sched.Matches(now) {
			continue
		}
		// 去重:同一项目同一分钟只触发一次。
		s.mu.Lock()
		dup := s.lastFired[e.ProjectID] == minuteKey
		if !dup {
			s.lastFired[e.ProjectID] = minuteKey
		}
		s.mu.Unlock()
		if dup {
			continue
		}
		if _, rerr := s.runner.CreateScheduledRun(ctx, e.ProjectID, e.Branch); rerr != nil {
			log.Printf("[cron] 项目 %s 定时触发创建运行失败:%v", e.ProjectID, rerr)
		} else {
			log.Printf("[cron] 项目 %s 定时触发(%s @ %s)", e.ProjectID, e.Expression, minuteKey)
		}
	}
}

// Start 启动后台调度 goroutine:对齐到每个整分钟边界后 Tick 一次。Stop / ctx 取消时退出。
func (s *Scheduler) Start(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			now := s.clock.Now()
			next := now.Truncate(time.Minute).Add(time.Minute)
			timer := time.NewTimer(next.Sub(now))
			select {
			case <-s.stop:
				timer.Stop()
				return
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				s.Tick(ctx, s.clock.Now())
			}
		}
	}()
}

// Stop 停止调度 goroutine(幂等)并等待退出。
func (s *Scheduler) Stop() {
	select {
	case <-s.stop:
		// 已关闭
	default:
		close(s.stop)
	}
	s.wg.Wait()
}
