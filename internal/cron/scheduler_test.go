package cron

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	entries []Entry
	err     error
}

func (f fakeStore) ListEnabled(context.Context) ([]Entry, error) { return f.entries, f.err }

type fakeRunner struct {
	mu    sync.Mutex
	calls []Entry // 记录 (projectID, branch)
	err   error
}

func (r *fakeRunner) CreateScheduledRun(_ context.Context, projectID, branch string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, Entry{ProjectID: projectID, Branch: branch})
	return "run-" + projectID, r.err
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestTickFiresOnMatch(t *testing.T) {
	store := fakeStore{entries: []Entry{
		{ProjectID: "p1", Expression: "30 2 * * *", Branch: "main"},
		{ProjectID: "p2", Expression: "0 9 * * *", Branch: "release"}, // 不命中 02:30
	}}
	runner := &fakeRunner{}
	s := NewScheduler(store, runner)

	now := time.Date(2026, 5, 31, 2, 30, 0, 0, time.UTC)
	s.Tick(context.Background(), now)

	if runner.count() != 1 {
		t.Fatalf("应仅 p1 触发,实际 %d 次", runner.count())
	}
	if runner.calls[0].ProjectID != "p1" || runner.calls[0].Branch != "main" {
		t.Errorf("触发参数不符:%+v", runner.calls[0])
	}
}

func TestTickDedupSameMinute(t *testing.T) {
	store := fakeStore{entries: []Entry{{ProjectID: "p1", Expression: "*/1 * * * *", Branch: "main"}}}
	runner := &fakeRunner{}
	s := NewScheduler(store, runner)

	now := time.Date(2026, 5, 31, 2, 30, 0, 0, time.UTC)
	s.Tick(context.Background(), now)
	s.Tick(context.Background(), now) // 同一分钟再次 → 去重
	if runner.count() != 1 {
		t.Fatalf("同一分钟应只触发一次,实际 %d", runner.count())
	}
	// 下一分钟应再次触发。
	s.Tick(context.Background(), now.Add(time.Minute))
	if runner.count() != 2 {
		t.Fatalf("下一分钟应再触发,实际 %d", runner.count())
	}
}

func TestTickSkipsInvalidExpression(t *testing.T) {
	store := fakeStore{entries: []Entry{
		{ProjectID: "bad", Expression: "not a cron", Branch: "main"},
		{ProjectID: "ok", Expression: "*/1 * * * *", Branch: "main"},
	}}
	runner := &fakeRunner{}
	s := NewScheduler(store, runner)
	s.Tick(context.Background(), time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC))
	if runner.count() != 1 || runner.calls[0].ProjectID != "ok" {
		t.Errorf("非法表达式应跳过、合法的照常触发;calls=%+v", runner.calls)
	}
}

func TestTickRunnerErrorDoesNotPanic(t *testing.T) {
	store := fakeStore{entries: []Entry{{ProjectID: "p1", Expression: "*/1 * * * *"}}}
	runner := &fakeRunner{err: errors.New("boom")}
	s := NewScheduler(store, runner)
	// 不应 panic;错误仅记日志。
	s.Tick(context.Background(), time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC))
	if runner.count() != 1 {
		t.Errorf("应尝试创建一次")
	}
}

func TestStartStopClean(t *testing.T) {
	store := fakeStore{}
	s := NewScheduler(store, &fakeRunner{})
	s.Start(context.Background())
	s.Stop() // 应干净退出
	s.Stop() // 幂等
}
