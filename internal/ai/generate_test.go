package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubProposalJSON 是 stub LLM 返回的固定提案 JSON(无 fence)。
const stubProposalJSON = `{
  "stages": [
    {"name":"流水线源","kind":"source","jobs":[{"name":"源","type":"git_source","summary":"仓库"}]},
    {"name":"构建","kind":"build","jobs":[{"name":"隔离构建","type":"build_image","summary":"node:22 容器构建"}]},
    {"name":"部署","kind":"deploy","jobs":[{"name":"部署","type":"deploy","summary":"部署到环境"}]}
  ],
  "build": {"model":"toolchain","toolchain":{"language":"node","version":"22"},"artifactType":"image","dockerfilePath":""},
  "branchMappings": [{"branchPattern":"main","environment":"生产"}],
  "rationale": "检测到 Node 22"
}`

// configureEnabled 把 svc 配置为指定 provider + baseURL(指向 stub)+ 启用 + 含 key。
func configureEnabled(t *testing.T, svc Service, provider, baseURL string) {
	t.Helper()
	in := SaveInput{
		Provider: provider,
		BaseURL:  baseURL,
		Model:    "test-model",
		Enabled:  true,
	}
	if provider != ProviderOllama {
		in.APIKey = strp("sk-secret-KEY9")
	}
	if _, err := svc.Save(ctx(), in); err != nil {
		t.Fatalf("Save(%s): %v", provider, err)
	}
}

// stubLLM 起一个返回指定 provider 形状响应的 httptest server,断言请求绝不泄漏明文 key 到 body。
func stubLLM(t *testing.T, provider, text string) *httptest.Server {
	t.Helper()
	body := wrapProviderResponse(provider, text)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证请求不含明文 key（key 应在 header,而非 body)。
		reqBody, _ := io.ReadAll(r.Body)
		if strings.Contains(string(reqBody), "sk-secret-KEY9") {
			t.Errorf("请求 body 泄漏了明文 key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wrapProviderResponse 按 provider 把助手文本包成对应 chat 响应 JSON。
func wrapProviderResponse(provider, text string) string {
	esc := jsonEscape(text)
	switch provider {
	case ProviderClaude:
		return `{"content":[{"type":"text","text":"` + esc + `"}]}`
	case ProviderOpenAI:
		return `{"choices":[{"message":{"role":"assistant","content":"` + esc + `"}}]}`
	case ProviderOllama:
		return `{"message":{"role":"assistant","content":"` + esc + `"}}`
	default:
		return "{}"
	}
}

func jsonEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

func TestGenerateThreeProviders(t *testing.T) {
	for _, provider := range []string{ProviderClaude, ProviderOpenAI, ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			srv := stubLLM(t, provider, stubProposalJSON)
			svc, _, _ := newService(t, srv.Client())
			configureEnabled(t, svc, provider, srv.URL)

			p, err := svc.Generate(ctx(), GenerateInput{
				Analysis: RepoAnalysis{Cloned: true, Language: "node", LanguageVersion: "22"},
				NL:       "push main 部署到生产",
			})
			if err != nil {
				t.Fatalf("Generate(%s): %v", provider, err)
			}
			if len(p.Stages) != 3 {
				t.Fatalf("stages = %d, want 3", len(p.Stages))
			}
			if p.Build.Model != "toolchain" || p.Build.Toolchain.Version != "22" {
				t.Fatalf("build 解析错误: %+v", p.Build)
			}
			if len(p.BranchMappings) != 1 || p.BranchMappings[0].Environment != "生产" {
				t.Fatalf("branchMappings 解析错误: %+v", p.BranchMappings)
			}
			// 每 stage/job/mapping 应有 p_ 前缀 id。
			for _, st := range p.Stages {
				if !strings.HasPrefix(st.ID, "p_s") {
					t.Fatalf("stage id 缺 p_s 前缀: %q", st.ID)
				}
				for _, jb := range st.Jobs {
					if !strings.HasPrefix(jb.ID, "p_j") {
						t.Fatalf("job id 缺 p_j 前缀: %q", jb.ID)
					}
				}
			}
			if !strings.HasPrefix(p.BranchMappings[0].ID, "p_m") {
				t.Fatalf("mapping id 缺 p_m 前缀: %q", p.BranchMappings[0].ID)
			}
		})
	}
}

// TestGenerateStripsMarkdownFence 验证容错剥离 ```json fence。
func TestGenerateStripsMarkdownFence(t *testing.T) {
	fenced := "这是给你的配置:\n```json\n" + stubProposalJSON + "\n```\n希望有帮助"
	srv := stubLLM(t, ProviderOpenAI, fenced)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	p, err := svc.Generate(ctx(), GenerateInput{Analysis: RepoAnalysis{Cloned: true}})
	if err != nil {
		t.Fatalf("Generate with fence: %v", err)
	}
	if len(p.Stages) != 3 {
		t.Fatalf("fence 剥离后 stages = %d, want 3", len(p.Stages))
	}
}

// TestGenerateNotConfigured 验证未配置 → ErrAINotConfigured(优雅降级)。
func TestGenerateNotConfigured(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)
	_, err := svc.Generate(ctx(), GenerateInput{})
	if err != ErrAINotConfigured {
		t.Fatalf("err = %v, want ErrAINotConfigured", err)
	}
}

// TestGenerateConfiguredButDisabled 验证已配但未启用 → ErrAINotConfigured。
func TestGenerateConfiguredButDisabled(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)
	if _, err := svc.Save(ctx(), SaveInput{
		Provider: ProviderClaude,
		Model:    "m",
		APIKey:   strp("sk-x"),
		Enabled:  false, // 未启用
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := svc.Generate(ctx(), GenerateInput{}); err != ErrAINotConfigured {
		t.Fatalf("disabled → err = %v, want ErrAINotConfigured", err)
	}
}

// TestGenerateParseFailHumanReadableNoKey 验证解析失败返回人读错误,且错误绝无明文 key。
func TestGenerateParseFailHumanReadableNoKey(t *testing.T) {
	srv := stubLLM(t, ProviderClaude, "对不起,我无法生成有效配置。") // 非 JSON
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderClaude, srv.URL)

	_, err := svc.Generate(ctx(), GenerateInput{Analysis: RepoAnalysis{Cloned: true}})
	if err == nil {
		t.Fatalf("解析失败应返回错误")
	}
	if strings.Contains(err.Error(), "sk-secret-KEY9") {
		t.Fatalf("错误信息泄漏明文 key: %v", err)
	}
	if !strings.Contains(err.Error(), "ai: generate failed") {
		t.Fatalf("应为 ErrGenerateFailed 包裹: %v", err)
	}
}

// TestGenerateHTTPErrorNoKey 验证 LLM 返回非 2xx → 人读错误,无明文 key。
func TestGenerateHTTPErrorNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	t.Cleanup(srv.Close)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderClaude, srv.URL)

	_, err := svc.Generate(ctx(), GenerateInput{})
	if err == nil {
		t.Fatalf("HTTP 401 应返回错误")
	}
	if strings.Contains(err.Error(), "sk-secret-KEY9") {
		t.Fatalf("错误信息泄漏明文 key: %v", err)
	}
}

// TestBuildPromptNoSecret 验证 prompt 不含任何明文密钥(仅分析 + NL + schema)。
func TestBuildPromptNoSecret(t *testing.T) {
	prompt := buildPrompt(GenerateInput{
		Analysis: RepoAnalysis{Cloned: true, Language: "node", LanguageVersion: "22", Signals: []string{"package.json"}},
		NL:       "部署到生产",
	})
	if !strings.Contains(prompt, "node") || !strings.Contains(prompt, "部署到生产") {
		t.Fatalf("prompt 应含分析与 NL: %s", prompt)
	}
	if strings.Contains(strings.ToLower(prompt), "sk-") {
		t.Fatalf("prompt 不应含任何 key 形态字串")
	}
}
