package ai

import (
	"strings"
	"testing"
)

// buildPrompt 必须把可用节点目录(内置 + 自定义)当工具清单写进 prompt,
// 否则 LLM 不知道这些节点存在、只会产粗粒度三段式。
func TestBuildPromptIncludesCatalog(t *testing.T) {
	in := GenerateInput{
		Analysis: RepoAnalysis{Cloned: true, Language: "node"},
		Catalog: append(BuiltinNodeCatalog(), NodeKind{
			Type: "templated", Label: "我的扫描节点", Category: "custom",
			Description: "跑安全扫描", Custom: true,
		}),
	}
	p := buildPrompt(in)
	for _, want := range []string{"build_frontend", "build_backend", "push_image", "health_check", "notify", "可用节点类型"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt 缺内置节点 %q", want)
		}
	}
	if !strings.Contains(p, "我的扫描节点") {
		t.Fatalf("prompt 应含复用库自定义节点名")
	}
}

// 空 Catalog 时回退内置目录(仍把全部内置节点喂 LLM)。
func TestBuildPromptCatalogFallback(t *testing.T) {
	p := buildPrompt(GenerateInput{Analysis: RepoAnalysis{Cloned: true}})
	if !strings.Contains(p, "build_image") || !strings.Contains(p, "deploy_ssh") {
		t.Fatalf("空 Catalog 应回退内置目录")
	}
}
