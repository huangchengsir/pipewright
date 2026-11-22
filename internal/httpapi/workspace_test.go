package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/workspace"
)

func TestWorkspaceApplicationCardStatusUsesExistingDisplayContract(t *testing.T) {
	for domain, expected := range map[string]string{
		"success":          "成功",
		"failed":           "失败",
		"partial_failed":   "部分失败",
		"rolled_back":      "已回滚",
		"queued":           "排队中",
		"running":          "进行中",
		"waiting_approval": "进行中",
		"waiting_action":   "进行中",
	} {
		dto := toWorkspaceApplicationDTO(workspace.Application{LatestRun: &workspace.LatestRun{Status: domain}})
		if dto.LastRunStatus == nil || *dto.LastRunStatus != expected || dto.LatestRun.Status != domain {
			t.Fatalf("domain %s did not retain both contract values", domain)
		}
	}
	dto := toWorkspaceApplicationDTO(workspace.Application{LatestRun: &workspace.LatestRun{Status: "future"}})
	if dto.LastRunStatus != nil || dto.LatestRun.Status != "future" {
		t.Fatal("unknown display status should leave the original optional field empty")
	}
}

func TestWorkspaceRejectsAmbiguousMutationPayload(t *testing.T) {
	for _, payload := range []string{
		`{"name":"A","Name":"B"}`,
		`{"name":"A"}{"name":"B"}`,
		`{"name":"A","unexpected":true}`,
		`[{"name":"A"}]`,
	} {
		var req workspaceNameRequest
		w := httptest.NewRecorder()
		if decodeWorkspace(w, httptest.NewRequest(http.MethodPost, "/workspace/business-projects", strings.NewReader(payload)), &req) || w.Code != http.StatusBadRequest {
			t.Fatal("workspace accepted ambiguous input")
		}
	}
	var req workspaceNameRequest
	if !decodeWorkspace(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/workspace/business-projects", strings.NewReader(`{"name":"A","expectedRevision":1}`)), &req) || req.Name != "A" || req.ExpectedRevision != 1 {
		t.Fatal(req)
	}
}
