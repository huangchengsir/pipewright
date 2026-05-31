package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupPromotionServer 构造带 auth + project + run + approvals + promotion 的测试 server。
func setupPromotionServer(t *testing.T) (*httptest.Server, *http.Client, string, string, run.Service) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	rsvc := run.New(st.DB)
	coord := approval.New()
	astore := approval.NewStore(st.DB)
	pstore := promotion.NewStore(st.DB)
	pool := run.NewWorkerPool(rsvc)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithRuns(rsvc, pool),
		WithApprovals(coord, astore), WithPromotion(pstore)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_promo")
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

// seedSuccessRun 直接落一个 success 运行(绕过 worker;晋级只需 status=success)。
func seedSuccessRun(t *testing.T, rsvc run.Service, projID string) string {
	t.Helper()
	r, err := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	// 推进到 success(queued→running→success)以满足晋级前置。
	if err := rsvc.MarkWaitingApproval(context.Background(), r.ID); err == nil {
		_ = rsvc.ResumeFromApproval(context.Background(), r.ID)
	}
	// 强制置 success:复用 SetDeployTerminal(直接覆盖终态)。
	if err := rsvc.SetDeployTerminal(context.Background(), r.ID, run.StatusSuccess); err != nil {
		t.Fatalf("force success: %v", err)
	}
	return r.ID
}

func TestEnvironmentsConfigAndPromoteNonGated(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupPromotionServer(t)

	// 配置链 dev→prod(prod gated)+ dev 变量(含 secret 引用)。
	body := `{"environments":[{"name":"dev","gated":false},{"name":"prod","gated":true}],
	          "variables":{"dev":[{"key":"LOG_LEVEL","value":"info"},{"key":"TOKEN","secret":true,"credentialId":"c1"}]}}`
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/projects/"+projID+"/environments", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT environments = %d: %s", resp.StatusCode, raw)
	}

	// GET 回读:secret 不含明文。
	gresp, gerr := client.Get(srv.URL + "/api/projects/" + projID + "/environments")
	if gerr != nil {
		t.Fatalf("GET environments: %v", gerr)
	}
	defer gresp.Body.Close()
	graw, _ := io.ReadAll(gresp.Body)
	var got struct {
		Environments []map[string]any            `json:"environments"`
		Variables    map[string][]map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(graw, &got)
	if len(got.Environments) != 2 {
		t.Fatalf("environments len = %d: %s", len(got.Environments), graw)
	}
	for _, v := range got.Variables["dev"] {
		if v["secret"] == true && v["value"] != "" {
			t.Fatalf("secret var leaked plaintext in API: %v", v)
		}
	}

	// 晋级 success 运行到 dev(非 gated → 同步 promoted)。
	runID := seedSuccessRun(t, rsvc, projID)
	presp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/promote", csrf, `{"targetEnvironment":"dev"}`)
	defer presp.Body.Close()
	praw, _ := io.ReadAll(presp.Body)
	if presp.StatusCode != http.StatusOK {
		t.Fatalf("POST promote = %d: %s", presp.StatusCode, praw)
	}
	var rec map[string]any
	_ = json.Unmarshal(praw, &rec)
	if rec["status"] != "promoted" || rec["targetEnvironment"] != "dev" {
		t.Fatalf("promote record wrong: %s", praw)
	}

	// 历史列表含一条。
	hresp, herr := client.Get(srv.URL + "/api/runs/" + runID + "/promotions")
	if herr != nil {
		t.Fatalf("GET promotions: %v", herr)
	}
	defer hresp.Body.Close()
	hraw, _ := io.ReadAll(hresp.Body)
	var hist struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(hraw, &hist)
	if len(hist.Items) != 1 {
		t.Fatalf("promotion history len = %d: %s", len(hist.Items), hraw)
	}
}

func TestPromoteGatedWaitsForApproval(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupPromotionServer(t)

	// 链只有 prod(gated)。
	body := `{"environments":[{"name":"prod","gated":true}]}`
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/projects/"+projID+"/environments", csrf, body)
	resp.Body.Close()

	runID := seedSuccessRun(t, rsvc, projID)

	// 并发:发起晋级(阻塞等待审批),另一协程经 /approve 批准。
	done := make(chan int, 1)
	go func() {
		presp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/promote", csrf, `{"targetEnvironment":"prod"}`)
		io.Copy(io.Discard, presp.Body)
		presp.Body.Close()
		done <- presp.StatusCode
	}()

	// 轮询直到该晋级门进入等待,再批准(stageId = "promote:prod")。
	approveDeadline := pollApprove(t, client, srv.URL, csrf, runID, "promote:prod")
	if !approveDeadline {
		t.Fatalf("approve never succeeded (gate not waiting)")
	}

	if code := <-done; code != http.StatusOK {
		t.Fatalf("gated promote final status = %d; want 200", code)
	}
}

// pollApprove 轮询投递 approve 直到成功(门进入等待)或超时。
func pollApprove(t *testing.T, client *http.Client, srvURL, csrf, runID, stageID string) bool {
	t.Helper()
	for i := 0; i < 200; i++ {
		resp := doJSON(t, client, http.MethodPost, srvURL+"/api/runs/"+runID+"/approve", csrf,
			`{"stageId":"`+stageID+`"}`)
		code := resp.StatusCode
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if code == http.StatusOK {
			return true
		}
		// 409 not_waiting:门尚未进入等待,稍后重试。
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
