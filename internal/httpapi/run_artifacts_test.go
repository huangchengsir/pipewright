package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
)

// TestRunDetailArtifactsSlot 验证成功 run 的 run-detail DTO 填了 artifacts slot(FR-6),
// 且产物形状(type 枚举 + reference + sizeBytes + metadata + createdAt)符合冻结契约。
func TestRunDetailArtifactsSlot(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, err := rsvc.Create(context.Background(), projID,
		run.Trigger{Type: run.TriggerManual, Branch: "main", Commit: "a1b2c3d4", Actor: "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dto := waitRunStatus(t, client, srv, csrf, r.ID, run.StatusSuccess)

	rawArts, ok := dto["artifacts"]
	if !ok {
		t.Fatalf("run-detail DTO 缺 artifacts slot: %v", dto)
	}
	arts, ok := rawArts.([]any)
	if !ok {
		t.Fatalf("artifacts 应为数组, got %T", rawArts)
	}
	if len(arts) == 0 {
		t.Fatalf("成功 run 应有桩产物")
	}
	a0, _ := arts[0].(map[string]any)
	for _, k := range []string{"id", "type", "name", "reference", "sizeBytes", "metadata", "createdAt"} {
		if _, ok := a0[k]; !ok {
			t.Fatalf("artifact DTO 缺字段 %q: %v", k, a0)
		}
	}
	typ, _ := a0["type"].(string)
	switch typ {
	case "image", "jar", "dist", "archive":
	default:
		t.Fatalf("artifact type 非冻结枚举: %q", typ)
	}
	if ref, _ := a0["reference"].(string); ref == "" {
		t.Fatalf("artifact reference 不应为空")
	}
}

// TestRunArtifactsEndpoint 验证 GET /api/runs/{id}/artifacts → {artifacts:[…]}(认证只读)。
func TestRunArtifactsEndpoint(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, err := rsvc.Create(context.Background(), projID,
		run.Trigger{Type: run.TriggerManual, Branch: "main", Commit: "deadbeef", Actor: "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitRunStatus(t, client, srv, csrf, r.ID, run.StatusSuccess)

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/"+r.ID+"/artifacts", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifacts status = %d: %s", resp.StatusCode, raw)
	}

	var body struct {
		Artifacts []struct {
			ID        string         `json:"id"`
			Type      string         `json:"type"`
			Name      string         `json:"name"`
			Reference string         `json:"reference"`
			SizeBytes int64          `json:"sizeBytes"`
			Metadata  map[string]any `json:"metadata"`
			CreatedAt string         `json:"createdAt"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode artifacts: %v: %s", err, raw)
	}
	if len(body.Artifacts) == 0 {
		t.Fatalf("成功 run 应有桩产物")
	}
	for _, a := range body.Artifacts {
		switch a.Type {
		case "image", "jar", "dist", "archive":
		default:
			t.Fatalf("artifact type 非冻结枚举: %q", a.Type)
		}
		if a.Reference == "" {
			t.Fatalf("artifact reference 不应为空")
		}
		// 桩产物诚实标注 stub=true。
		if stub, ok := a.Metadata["stub"]; !ok || stub != true {
			t.Fatalf("桩产物应标注 metadata.stub=true: %#v", a.Metadata)
		}
	}
}

// TestRunArtifactsNotFound 验证不存在 run → 404 run_not_found。
func TestRunArtifactsNotFound(t *testing.T) {
	srv, client, csrf, _, _ := setupRunServer(t)

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/no-such-run/artifacts", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在 run 应 404, got %d: %s", resp.StatusCode, raw)
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	if e.Error.Code != "run_not_found" {
		t.Fatalf("错误码应为 run_not_found, got %q: %s", e.Error.Code, raw)
	}
}
