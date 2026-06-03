package run

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStubRunnerSynthesizesFailureLog 验证桩 runner 失败路径合成失败日志并经 sink 持久化,
// 且日志含假 secret(供脱敏链路验证)。
func TestStubRunnerSynthesizesFailureLog(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc, WithRunner(&StubRunner{Steps: []string{"拉取源码", "构建镜像"}, FailAt: 1}))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForStatus(t, svc, r.ID, StatusFailed)

	logText, err := svc.GetFailureLog(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("GetFailureLog: %v", err)
	}
	if strings.TrimSpace(logText) == "" {
		t.Fatalf("失败 run 应捕获失败日志")
	}
	if !strings.Contains(logText, "npm ERR!") {
		t.Fatalf("失败日志应含真实感错误内容: %q", logText)
	}
	if !strings.Contains(logText, StubFailureSecret) {
		t.Fatalf("桩失败日志应内嵌假 secret 以验脱敏: %q", logText)
	}
}

// TestSuccessRunHasNoFailureLog 验证成功 run 不留失败日志。
func TestSuccessRunHasNoFailureLog(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
	waitForStatus(t, svc, r.ID, StatusSuccess)

	logText, err := svc.GetFailureLog(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("GetFailureLog: %v", err)
	}
	if logText != "" {
		t.Fatalf("成功 run 不应有失败日志: %q", logText)
	}
}

// TestSaveAndGetDiagnosis 验证诊断持久化往返(SaveDiagnosis/GetDiagnosis)+ Get 填值。
func TestSaveAndGetDiagnosis(t *testing.T) {
	db := testDB(t)
	svc := newService(db)
	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})

	// 初始未诊断 → nil。
	if d, err := svc.GetDiagnosis(context.Background(), r.ID); err != nil || d != nil {
		t.Fatalf("未诊断应 nil, nil;得 %+v, %v", d, err)
	}
	if got, _ := svc.Get(context.Background(), r.ID); got.Diagnosis != nil {
		t.Fatalf("未诊断 run.Diagnosis 应 nil")
	}

	want := &Diagnosis{
		Status:          DiagnosisReady,
		Hypothesis:      "最可能的根因是缺少依赖(假说,非结论)",
		Confidence:      "high",
		AlternateCauses: []string{"也可能是 lockfile 未提交"},
		FixSuggestions:  []string{"声明依赖并提交 lockfile"},
		FixScript:       "npm install leftpad@^1.3.0 --save\ngit add package-lock.json",
		Evidence:        []DiagnosisEvidence{{Line: 2, Text: "npm ERR! missing", Highlight: true}},
		GeneratedAt:     time.Now().UTC().Truncate(time.Second),
	}
	if err := svc.SaveDiagnosis(context.Background(), r.ID, want); err != nil {
		t.Fatalf("SaveDiagnosis: %v", err)
	}

	got, err := svc.GetDiagnosis(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("GetDiagnosis: %v", err)
	}
	if got == nil || got.Status != DiagnosisReady || got.Hypothesis != want.Hypothesis {
		t.Fatalf("诊断往返不一致: %+v", got)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Line != 2 || !got.Evidence[0].Highlight {
		t.Fatalf("evidence 往返不一致: %+v", got.Evidence)
	}
	if got.FixScript != want.FixScript {
		t.Fatalf("fixScript 往返不一致: %q vs %q", got.FixScript, want.FixScript)
	}
	if !got.GeneratedAt.Equal(want.GeneratedAt) {
		t.Fatalf("generatedAt 往返不一致: %v vs %v", got.GeneratedAt, want.GeneratedAt)
	}

	// Get 填值。
	full, _ := svc.Get(context.Background(), r.ID)
	if full.Diagnosis == nil || full.Diagnosis.Hypothesis != want.Hypothesis {
		t.Fatalf("Get 应填诊断: %+v", full.Diagnosis)
	}
}

// TestSetFailureLogNotFound 验证不存在 run → ErrNotFound。
func TestSetFailureLogNotFound(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	if err := svc.SetFailureLog(context.Background(), "nope", "x"); err == nil {
		t.Fatal("不存在 run 应返回错误")
	}
	if _, err := svc.GetFailureLog(context.Background(), "nope"); err == nil {
		t.Fatal("不存在 run GetFailureLog 应返回错误")
	}
	if err := svc.SaveDiagnosis(context.Background(), "nope", &Diagnosis{Status: "ready"}); err == nil {
		t.Fatal("不存在 run SaveDiagnosis 应返回错误")
	}
}

// TestDiagnoseHookInvokedOnFailure 验证 run→failed 后注入的 best-effort 诊断钩子被调用;
// 成功 run 不触发;钩子 panic 不连累 run 终态。
func TestDiagnoseHookInvokedOnFailure(t *testing.T) {
	db := testDB(t)
	svc := New(db)

	var mu sync.Mutex
	called := map[string]int{}
	hook := func(_ context.Context, runID string) {
		mu.Lock()
		called[runID]++
		mu.Unlock()
		panic("hook boom") // 钩子 panic 须被 recover,不连累 run 终态
	}
	pool := NewWorkerPool(svc, WithRunner(&branchRunner{}), WithDiagnoseHook(hook))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	failRun, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "fail"})
	okRun, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "ok"})

	gotFail := waitForStatus(t, svc, failRun.ID, StatusFailed)
	waitForStatus(t, svc, okRun.ID, StatusSuccess)
	if gotFail.Status != StatusFailed {
		t.Fatalf("失败 run 终态应为 failed(钩子 panic 不应破坏)")
	}

	// 给钩子 goroutine 一点时间执行(它在 run 终态之后异步触发)。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := called[failRun.ID]
		ok := called[okRun.ID]
		mu.Unlock()
		if n >= 1 {
			if ok != 0 {
				t.Fatalf("成功 run 不应触发诊断钩子")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("失败 run 应触发诊断钩子")
}

// TestCanceledRunDoesNotTriggerDiagnose 验证 #1:取消复用 StatusFailed,但被取消的 run 落 failed 后
// **不**触发自动诊断——runCtx 已被取消(非真实执行失败),诊断「被取消的 run」无意义且白打 LLM/解密 key。
// (真实失败仍触发,见 TestDiagnoseHookInvokedOnFailure;手动诊断不受影响。)
func TestCanceledRunDoesNotTriggerDiagnose(t *testing.T) {
	db := testDB(t)
	svc := New(db)

	var mu sync.Mutex
	called := 0
	hook := func(_ context.Context, _ string) {
		mu.Lock()
		called++
		mu.Unlock()
	}
	gate := make(chan struct{})
	pool := NewWorkerPool(svc, WithRunner(&blockingRunner{gate: gate}), WithDiagnoseHook(hook))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})

	// 等 run 进入 running 后取消;runner 观察到 ctx 取消 → 落 failed。
	waitForStatus(t, svc, r.ID, StatusRunning)
	if _, err := svc.Cancel(context.Background(), r.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(gate)
	got := waitForStatus(t, svc, r.ID, StatusFailed)
	if got.Status != StatusFailed {
		t.Fatalf("取消应落 failed, got %q", got.Status)
	}

	// 给「假如会误触发」的钩子 goroutine 充分时间,再断言它从未被调用。
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	n := called
	mu.Unlock()
	if n != 0 {
		t.Fatalf("被取消的 run 不应触发自动诊断, 但触发了 %d 次", n)
	}
}

// TestDecodeDiagnosisBadData 验证坏 JSON 容错为 nil(不阻断 Get)。
func TestDecodeDiagnosisBadData(t *testing.T) {
	if d, _ := decodeDiagnosis("not json"); d != nil {
		t.Fatalf("坏数据应容错为 nil, 得 %+v", d)
	}
	if d, _ := decodeDiagnosis(""); d != nil {
		t.Fatalf("空串应为 nil")
	}
}
