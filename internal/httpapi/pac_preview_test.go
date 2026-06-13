package httpapi

import (
	"net/http"
	"testing"
)

const validPacYAML = `stages:
  - name: 源码
    kind: source
    jobs:
      - name: checkout
        type: source
  - name: 构建
    kind: build
    jobs:
      - name: compile
        type: script
        script:
          commands:
            - make build
      - name: package
        type: script
        script:
          commands:
            - make package
`

// TestPacPreviewValid 验证按默认分支拉取合法 .pipewright.yml → found=true valid=true + 阶段摘要。
func TestPacPreviewValid(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{".pipewright.yml": validPacYAML})
	srv, client, csrf, projID := setupSourceServer(t, url)

	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/pac/preview", csrf)
	if code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", code, raw)
	}
	if m["found"] != true || m["valid"] != true {
		t.Fatalf("应 found=true valid=true, got %s", raw)
	}
	if m["ref"] != "main" {
		t.Fatalf("ref 应默认默认分支 main, got %v", m["ref"])
	}
	if m["file"] != ".pipewright.yml" {
		t.Fatalf("file 应为 .pipewright.yml, got %v", m["file"])
	}
	if sc, _ := m["stageCount"].(float64); sc != 2 {
		t.Fatalf("stageCount 应为 2, got %v (%s)", m["stageCount"], raw)
	}
	stages, ok := m["stages"].([]any)
	if !ok || len(stages) != 2 {
		t.Fatalf("stages 应有 2 项, got %s", raw)
	}
	build, _ := stages[1].(map[string]any)
	if build["kind"] != "build" {
		t.Fatalf("第二阶段 kind 应为 build, got %v", build["kind"])
	}
	if jc, _ := build["jobCount"].(float64); jc != 2 {
		t.Fatalf("build 阶段 jobCount 应为 2, got %v", build["jobCount"])
	}
	if m["error"] != "" {
		t.Fatalf("合法配置 error 应为空, got %v", m["error"])
	}
}

// TestPacPreviewInvalid 验证非法 YAML → found=true valid=false error 非空 stages=[]。
func TestPacPreviewInvalid(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{
		".pipewright.yml": "stages:\n  - name: x\n    kind: not_a_real_kind\n",
	})
	srv, client, csrf, projID := setupSourceServer(t, url)

	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/pac/preview", csrf)
	if code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", code, raw)
	}
	if m["found"] != true || m["valid"] != false {
		t.Fatalf("非法 YAML 应 found=true valid=false, got %s", raw)
	}
	if es, _ := m["error"].(string); es == "" {
		t.Fatalf("非法 YAML error 应非空, got %s", raw)
	}
	if stages, ok := m["stages"].([]any); !ok || len(stages) != 0 {
		t.Fatalf("非法 YAML stages 应为 [], got %s", raw)
	}
}

// TestPacPreviewNotFound 验证仓库无 .pipewright.yml → found=false(不报错)。
func TestPacPreviewNotFound(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"README.md": "# hi\n"})
	srv, client, csrf, projID := setupSourceServer(t, url)

	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/pac/preview", csrf)
	if code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", code, raw)
	}
	if m["found"] != false {
		t.Fatalf("无文件应 found=false, got %s", raw)
	}
}

// TestPacPreviewProjectNotFound 验证未知项目 → 404 project_not_found。
func TestPacPreviewProjectNotFound(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{".pipewright.yml": validPacYAML})
	srv, client, csrf, _ := setupSourceServer(t, url)

	code, _, _ := getJSON(t, client, srv.URL+"/api/projects/no-such-id/pac/preview", csrf)
	if code != http.StatusNotFound {
		t.Fatalf("未知项目应 404, got %d", code)
	}
}
