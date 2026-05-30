package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/huangjiawei/devopstool/internal/mask"
)

// fakeRunSecret 是测试用的「假 secret」,模拟桩失败日志内嵌的凭据。
const fakeRunSecret = "SECRET_LEAK_zzz9k3"

// failureLogWithSecret 是含假 secret 的多行失败日志(供脱敏断言)。
const failureLogWithSecret = "npm ERR! code E404\n" +
	"npm ERR! 404 missing: leftpad@^1.0.0 from the registry\n" +
	"npm config set //registry.npmjs.org/:_authToken=" + fakeRunSecret + "\n" +
	"Build failed with exit code 1\n"

// stubDiagnosisJSON 是 stub LLM 返回的固定诊断 JSON(无 fence)。
const stubDiagnosisJSON = `{
  "hypothesis": "最可能的根因是缺少依赖 leftpad(假说,非结论)",
  "confidence": "high",
  "alternateCauses": ["也可能是 lockfile 未提交"],
  "fixSuggestions": ["在 package.json 声明 leftpad 并提交 lockfile"],
  "evidenceLines": [2]
}`

// diagStubLLM 起一个返回诊断响应的 httptest server,并捕获请求 body 供脱敏断言。
func diagStubLLM(t *testing.T, provider, text string) (*httptest.Server, *capturedBody) {
	t.Helper()
	body := wrapProviderResponse(provider, text)
	cap := &capturedBody{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, _ := io.ReadAll(r.Body)
		cap.set(string(reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

type capturedBody struct {
	mu sync.Mutex
	s  string
}

func (c *capturedBody) set(s string) { c.mu.Lock(); c.s = s; c.mu.Unlock() }
func (c *capturedBody) get() string  { c.mu.Lock(); defer c.mu.Unlock(); return c.s }

// maskerWithSecret 构造已登记假 secret 的 Masker。
func maskerWithSecret() *mask.Masker {
	m := mask.NewMasker()
	m.RegisterSecret(fakeRunSecret)
	return m
}

// TestDiagnoseReady 验证 stub LLM 正常返回 → status=ready，且诊断字段对齐契约。
func TestDiagnoseReady(t *testing.T) {
	for _, provider := range []string{ProviderClaude, ProviderOpenAI, ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			srv, _ := diagStubLLM(t, provider, stubDiagnosisJSON)
			svc, _, _ := newService(t, srv.Client())
			configureEnabled(t, svc, provider, srv.URL)

			d, err := svc.Diagnose(ctx(), DiagnoseInput{
				FailureLog: failureLogWithSecret,
				StepName:   "构建镜像",
				Masker:     maskerWithSecret(),
			})
			if err != nil {
				t.Fatalf("Diagnose: %v", err)
			}
			if d.Status != diagStatusReady {
				t.Fatalf("status = %q, want ready (reason=%q)", d.Status, d.Reason)
			}
			if !strings.Contains(d.Hypothesis, "leftpad") {
				t.Fatalf("hypothesis 缺失内容: %q", d.Hypothesis)
			}
			if d.Confidence != confHigh {
				t.Fatalf("confidence = %q, want high", d.Confidence)
			}
			if len(d.FixSuggestions) == 0 {
				t.Fatalf("fixSuggestions 应非空")
			}
			if len(d.Evidence) == 0 {
				t.Fatalf("evidence 应非空")
			}
			// 命中行(line=2)应高亮且取自脱敏后日志。
			if d.Evidence[0].Line != 2 || !d.Evidence[0].Highlight {
				t.Fatalf("evidence[0] 应为命中行 2 且高亮: %+v", d.Evidence[0])
			}
		})
	}
}

// TestDiagnoseMasksSecret 验证出网前脱敏铁律:prompt(出网 body）/响应/evidence/诊断字段
// 绝无明文假 secret(被 [MASKED] 替换)。
func TestDiagnoseMasksSecret(t *testing.T) {
	// 让 stub LLM 把含 secret 的日志行回显到 fixSuggestions/hypothesis,验二次脱敏。
	echoJSON := `{
	  "hypothesis": "根因含 ` + fakeRunSecret + ` 的行(假说)",
	  "confidence": "low",
	  "alternateCauses": ["可能涉及 ` + fakeRunSecret + `"],
	  "fixSuggestions": ["移除 ` + fakeRunSecret + ` 泄漏"],
	  "evidenceLines": [3]
	}`
	srv, cap := diagStubLLM(t, ProviderOpenAI, echoJSON)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	d, err := svc.Diagnose(ctx(), DiagnoseInput{
		FailureLog: failureLogWithSecret,
		Masker:     maskerWithSecret(),
	})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	// 1) 出网 prompt(请求 body)绝无明文 secret。
	if strings.Contains(cap.get(), fakeRunSecret) {
		t.Fatalf("出网 prompt 泄漏了明文 secret")
	}
	// 2) hypothesis / alternateCauses / fixSuggestions 绝无明文 secret(二次脱敏)。
	if strings.Contains(d.Hypothesis, fakeRunSecret) {
		t.Fatalf("hypothesis 泄漏明文 secret: %q", d.Hypothesis)
	}
	for _, a := range d.AlternateCauses {
		if strings.Contains(a, fakeRunSecret) {
			t.Fatalf("alternateCauses 泄漏明文 secret: %q", a)
		}
	}
	for _, f := range d.FixSuggestions {
		if strings.Contains(f, fakeRunSecret) {
			t.Fatalf("fixSuggestions 泄漏明文 secret: %q", f)
		}
	}
	// 3) evidence(命中行 3,正含 secret 的那行)取自脱敏日志,应为 [MASKED] 而非明文。
	foundMasked := false
	for _, e := range d.Evidence {
		if strings.Contains(e.Text, fakeRunSecret) {
			t.Fatalf("evidence 泄漏明文 secret: %+v", e)
		}
		if strings.Contains(e.Text, mask.Placeholder) {
			foundMasked = true
		}
	}
	if !foundMasked {
		t.Fatalf("命中行证据应含 [MASKED] 占位,实际 evidence=%+v", d.Evidence)
	}
}

// TestDiagnoseUnavailableNotConfigured 验证未配/未启用 → status=unavailable + reason，绝不 error。
func TestDiagnoseUnavailableNotConfigured(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient) // 从未 Save 配置
	d, err := svc.Diagnose(ctx(), DiagnoseInput{
		FailureLog: failureLogWithSecret,
		Masker:     maskerWithSecret(),
	})
	if err != nil {
		t.Fatalf("Diagnose 未配应降级不返 error,得 %v", err)
	}
	if d.Status != diagStatusUnavailable || d.Reason == "" {
		t.Fatalf("未配应 unavailable + reason: %+v", d)
	}
	if d.Hypothesis != "" {
		t.Fatalf("unavailable 时 hypothesis 应空: %q", d.Hypothesis)
	}
}

// TestDiagnoseUnavailableNoLog 验证无失败日志 → unavailable(不出网)。
func TestDiagnoseUnavailableNoLog(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)
	d, err := svc.Diagnose(ctx(), DiagnoseInput{FailureLog: "   ", Masker: maskerWithSecret()})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.Status != diagStatusUnavailable {
		t.Fatalf("无日志应 unavailable: %+v", d)
	}
}

// TestDiagnoseUnparseable 验证 LLM 返回不可解析 → unavailable，绝不 error / 500。
func TestDiagnoseUnparseable(t *testing.T) {
	srv, _ := diagStubLLM(t, ProviderOpenAI, "抱歉,我无法分析这段日志。")
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	d, err := svc.Diagnose(ctx(), DiagnoseInput{FailureLog: failureLogWithSecret, Masker: maskerWithSecret()})
	if err != nil {
		t.Fatalf("不可解析应降级不返 error,得 %v", err)
	}
	if d.Status != diagStatusUnavailable {
		t.Fatalf("不可解析应 unavailable: %+v", d)
	}
}

// TestDiagnoseEmptyHypothesis 验证空 hypothesis（质量门控）→ unavailable。
func TestDiagnoseEmptyHypothesis(t *testing.T) {
	emptyHyp := `{"hypothesis":"","confidence":"high","fixSuggestions":["x"],"evidenceLines":[1]}`
	srv, _ := diagStubLLM(t, ProviderOpenAI, emptyHyp)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	d, err := svc.Diagnose(ctx(), DiagnoseInput{FailureLog: failureLogWithSecret, Masker: maskerWithSecret()})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.Status != diagStatusUnavailable {
		t.Fatalf("空 hypothesis 应 unavailable: %+v", d)
	}
}

// TestDiagnoseStripsFence 验证容错剥离 ```json fence。
func TestDiagnoseStripsFence(t *testing.T) {
	fenced := "这是诊断:\n```json\n" + stubDiagnosisJSON + "\n```"
	srv, _ := diagStubLLM(t, ProviderOpenAI, fenced)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	d, err := svc.Diagnose(ctx(), DiagnoseInput{FailureLog: failureLogWithSecret, Masker: maskerWithSecret()})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.Status != diagStatusReady {
		t.Fatalf("带 fence 应仍 ready: %+v", d)
	}
}
