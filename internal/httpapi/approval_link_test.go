package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/approval"
)

func newTestSigner() *approval.Signer {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 7)
	}
	return approval.NewSigner(k)
}

// GET 必须无副作用:确认页渲染后,门仍在等待(不被自动解析)。
func TestApprovalPageGetHasNoSideEffect(t *testing.T) {
	signer := newTestSigner()
	coord := approval.New()
	key := approval.Key("run-1", "stage-1")
	ch := coord.Wait(key) // 模拟门已在等待
	_ = ch

	tok := signer.Sign("run-1", "stage-1", time.Now().Add(time.Hour))
	h := makeApprovalPageHandler(signer, coord)

	req := httptest.NewRequest(http.MethodGet, "/approvals?token="+url.QueryEscape(tok), nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET confirm page: want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "确认审批") || !strings.Contains(body, "/approvals/act") {
		t.Fatalf("confirm page missing expected content: %q", body)
	}
	// 关键:GET 没有解析门,门必须仍在等待。
	if !coord.IsWaiting(key) {
		t.Fatal("GET must NOT resolve the gate (no side effect) — but gate is no longer waiting")
	}
}

// GET 对无效 token → 友好错误页(400)。
func TestApprovalPageGetInvalidToken(t *testing.T) {
	signer := newTestSigner()
	coord := approval.New()
	h := makeApprovalPageHandler(signer, coord)

	req := httptest.NewRequest(http.MethodGet, "/approvals?token=garbage", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid token: want 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "链接无效或已过期") {
		t.Fatalf("invalid token page missing friendly message: %q", rec.Body.String())
	}
}

// POST approve 解析门:Resolve 后 worker 侧 channel 收到 Approved=true,页面回「已批准」。
func TestApprovalActApproveResolvesGate(t *testing.T) {
	signer := newTestSigner()
	coord := approval.New()
	key := approval.Key("run-9", "stage-deploy")
	gateCh := coord.Wait(key)

	tok := signer.Sign("run-9", "stage-deploy", time.Now().Add(time.Hour))
	h := makeApprovalActHandler(signer, coord, nil) // nil 审计:recordAudit 静默跳过

	form := url.Values{"token": {tok}, "decision": {"approve"}}
	req := httptest.NewRequest(http.MethodPost, "/approvals/act", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST approve: want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "已批准") {
		t.Fatalf("approve result page missing 已批准: %q", rec.Body.String())
	}
	// 门内 worker 必须收到批准决定。
	select {
	case d := <-gateCh:
		if !d.Approved {
			t.Fatalf("gate received Approved=false, want true")
		}
		if d.Actor != approvalActor {
			t.Fatalf("gate actor = %q, want %q", d.Actor, approvalActor)
		}
	case <-time.After(time.Second):
		t.Fatal("gate did not receive the approval decision")
	}
}

// POST reject 解析门:worker 侧收到 Approved=false,页面回「已拒绝」。
func TestApprovalActReject(t *testing.T) {
	signer := newTestSigner()
	coord := approval.New()
	key := approval.Key("run-r", "stage-r")
	gateCh := coord.Wait(key)

	tok := signer.Sign("run-r", "stage-r", time.Now().Add(time.Hour))
	h := makeApprovalActHandler(signer, coord, nil)

	form := url.Values{"token": {tok}, "decision": {"reject"}}
	req := httptest.NewRequest(http.MethodPost, "/approvals/act", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "已拒绝") {
		t.Fatalf("reject: code=%d body=%q", rec.Code, rec.Body.String())
	}
	select {
	case d := <-gateCh:
		if d.Approved {
			t.Fatal("gate received Approved=true on reject")
		}
	case <-time.After(time.Second):
		t.Fatal("gate did not receive the reject decision")
	}
}

// POST 对未在等待的门 → 409「已不在等待」。
func TestApprovalActNotWaiting(t *testing.T) {
	signer := newTestSigner()
	coord := approval.New() // 无任何等待者
	tok := signer.Sign("run-x", "stage-x", time.Now().Add(time.Hour))
	h := makeApprovalActHandler(signer, coord, nil)

	form := url.Values{"token": {tok}, "decision": {"approve"}}
	req := httptest.NewRequest(http.MethodPost, "/approvals/act", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("not-waiting gate: want 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "已不在等待审批") {
		t.Fatalf("not-waiting page missing message: %q", rec.Body.String())
	}
}

// POST 对无效 token → 400,且绝不解析任何门。
func TestApprovalActInvalidToken(t *testing.T) {
	signer := newTestSigner()
	coord := approval.New()
	key := approval.Key("run-1", "stage-1")
	gateCh := coord.Wait(key)
	h := makeApprovalActHandler(signer, coord, nil)

	form := url.Values{"token": {"forged"}, "decision": {"approve"}}
	req := httptest.NewRequest(http.MethodPost, "/approvals/act", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid token act: want 400, got %d", rec.Code)
	}
	select {
	case <-gateCh:
		t.Fatal("invalid token must NOT resolve the gate")
	default:
	}
}

// POST 对非法 decision → 400(防 GET 预取式自动操作之外的乱传)。
func TestApprovalActBadDecision(t *testing.T) {
	signer := newTestSigner()
	coord := approval.New()
	coord.Wait(approval.Key("run-1", "stage-1"))
	tok := signer.Sign("run-1", "stage-1", time.Now().Add(time.Hour))
	h := makeApprovalActHandler(signer, coord, nil)

	form := url.Values{"token": {tok}, "decision": {"maybe"}}
	req := httptest.NewRequest(http.MethodPost, "/approvals/act", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decision: want 400, got %d", rec.Code)
	}
}

// 禁用签名器(无 master key):页面提示不可用,绝不解析门。
func TestApprovalDisabledSigner(t *testing.T) {
	coord := approval.New()
	coord.Wait(approval.Key("run-1", "stage-1"))
	disabled := approval.NewSigner(nil)

	get := makeApprovalPageHandler(disabled, coord)
	reqG := httptest.NewRequest(http.MethodGet, "/approvals?token=whatever", nil)
	recG := httptest.NewRecorder()
	get(recG, reqG)
	if !strings.Contains(recG.Body.String(), "未启用") {
		t.Fatalf("disabled signer GET should show unavailable: %q", recG.Body.String())
	}
}
