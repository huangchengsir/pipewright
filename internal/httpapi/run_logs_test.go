package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/mask"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/run"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// setupLogsServer 构造带 masker + 失败桩 runner 的测试 server(逐行 emit 日志含假 secret)。
func setupLogsServer(t *testing.T) (*httptest.Server, *http.Client, string, string, run.Service) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	rsvc := run.New(st.DB)

	masker := mask.NewMasker()
	masker.RegisterSecret(run.StubFailureSecret)
	pool := run.NewWorkerPool(rsvc,
		run.WithRunner(&run.StubRunner{Steps: []string{"构建镜像"}, FailAt: 0}),
		run.WithLogMasker(masker),
	)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithRuns(rsvc, pool)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_run")
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf,
		`{"name":"acme","repoUrl":"https://gitee.com/acme/shop.git","credentialId":"`+credID+`"}`)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	projID, _ := p["id"].(string)
	if projID == "" {
		t.Fatalf("create project failed: %s", raw)
	}
	return srv, client, csrf, projID, rsvc
}

// TestRunLogsEndpointPagingMasked 验证 GET /api/runs/{id}/logs:历史回放 + sinceSeq 分页 +
// complete=终态 + 假 secret 一律 [MASKED](AC-SEC-04;响应绝无明文)。
func TestRunLogsEndpointPagingMasked(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupLogsServer(t)
	r, _ := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual})
	waitRunStatus(t, client, srv, csrf, r.ID, run.StatusFailed)

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/"+r.ID+"/logs", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs status = %d: %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), run.StubFailureSecret) {
		t.Fatalf("logs 响应含明文假 secret(脱敏失败): %s", raw)
	}

	var dto struct {
		Lines []struct {
			Seq         int    `json:"seq"`
			Ts          string `json:"ts"`
			Stream      string `json:"stream"`
			StepOrdinal int    `json:"stepOrdinal"`
			Text        string `json:"text"`
		} `json:"lines"`
		NextSeq  int  `json:"nextSeq"`
		Complete bool `json:"complete"`
	}
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("decode logs: %v: %s", err, raw)
	}
	if len(dto.Lines) == 0 {
		t.Fatalf("失败 run 应有日志行")
	}
	if !dto.Complete {
		t.Fatalf("终态 run 的 complete 应为 true")
	}
	// 冻结字段 + seq 单调升序。
	for i, l := range dto.Lines {
		if l.Seq != i+1 {
			t.Fatalf("seq 应从 1 起连续, line[%d].seq=%d", i, l.Seq)
		}
		if l.Stream != "stdout" && l.Stream != "stderr" {
			t.Fatalf("stream 非法: %q", l.Stream)
		}
	}
	last := dto.Lines[len(dto.Lines)-1].Seq
	if dto.NextSeq != last+1 {
		t.Fatalf("nextSeq 应为末行 seq+1=%d, got %d", last+1, dto.NextSeq)
	}
	sawMasked := false
	for _, l := range dto.Lines {
		if strings.Contains(l.Text, mask.Placeholder) {
			sawMasked = true
		}
	}
	if !sawMasked {
		t.Fatalf("应有一行被替换为 %s", mask.Placeholder)
	}

	// sinceSeq 分页:取 nextSeq-1 之后应为空(已读完)。
	resp2 := doJSON(t, client, http.MethodGet,
		srv.URL+"/api/runs/"+r.ID+"/logs?sinceSeq=1", csrf, "")
	raw2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	var dto2 struct {
		Lines []any `json:"lines"`
	}
	_ = json.Unmarshal(raw2, &dto2)
	if len(dto2.Lines) != len(dto.Lines)-1 {
		t.Fatalf("sinceSeq=1 应返回 %d 行, got %d", len(dto.Lines)-1, len(dto2.Lines))
	}
}

// TestRunLogsNotFound 验证 run 不存在 → 404 run_not_found。
func TestRunLogsNotFound(t *testing.T) {
	srv, client, csrf, _, _ := setupLogsServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/nope/logs", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在 run logs 应 404, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "run_not_found") {
		t.Fatalf("应返回 run_not_found: %s", raw)
	}
}

// TestRunLogsRequiresAuth 验证未认证访问 logs 端点 → 401。
func TestRunLogsRequiresAuth(t *testing.T) {
	srv, _, _, _, _ := setupLogsServer(t)
	anon := newTestClient(t)
	resp, err := anon.Get(srv.URL + "/api/runs/x/logs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证 logs 应 401, got %d", resp.StatusCode)
	}
}

// TestSSEReplaysHistoryLogEvents 验证 SSE 建连时按 seq 升序补发历史 log 事件(脱敏后),
// 让刷新者拿到完整日志(AC-2)。
func TestSSEReplaysHistoryLogEvents(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupLogsServer(t)
	r, _ := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual})
	// 等终态确保日志已全部落库(回放路径)。
	waitRunStatus(t, client, srv, csrf, r.ID, run.StatusFailed)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/runs/"+r.ID+"/events", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE GET: %v", err)
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	sawLog := false
	leak := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: log") {
				sawLog = true
			}
			if strings.Contains(line, run.StubFailureSecret) {
				leak = true
			}
			// 终态 run:回放后随即收到终态 status 帧并收流。
			if strings.Contains(line, `"status":"failed"`) && sawLog {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	if !sawLog {
		t.Fatal("SSE 建连应回放历史 log 事件")
	}
	if leak {
		t.Fatal("SSE log 事件含明文假 secret(脱敏失败)")
	}
}
