package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/workspace"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type workspaceTestContext struct{}
type workspaceReaderFixture struct {
	calls  int
	filter workspace.ApplicationFilter
	bound  bool
	result workspace.ApplicationList
	err    error
}

func (f *workspaceReaderFixture) ListApplications(ctx context.Context, in workspace.ApplicationFilter) (workspace.ApplicationList, error) {
	f.calls++
	f.filter = in
	f.bound = ctx.Value(workspaceTestContext{}) == true
	return f.result, f.err
}
func workspaceFixture() (Dependencies, *workspaceReaderFixture, *auth.MCPToken) {
	id := uuid.NewString()
	reader := &workspaceReaderFixture{result: workspace.ApplicationList{Items: []workspace.Application{{Project: project.Project{ID: id, Name: "Example", RepoURL: "https://secret.invalid/"}, Assignment: workspace.Assignment{ApplicationID: id, ProductID: uuid.NewString(), BusinessProjectID: uuid.NewString(), Revision: 3}}}}}
	d := Dependencies{Workspace: reader, BindToken: func(ctx context.Context, _ *auth.MCPToken) (context.Context, error) {
		return context.WithValue(ctx, workspaceTestContext{}, true), nil
	}}
	return d, reader, &auth.MCPToken{ID: "token", ProjectIDs: []string{id}}
}
func TestWorkspaceMCPRejectsOutsideLegacyTokenBeforeRead(t *testing.T) {
	d, r, token := workspaceFixture()
	_, err := d.workspaceApplication(context.Background(), token, uuid.NewString(), func(context.Context) (*auth.MCPToken, error) { return token, nil })
	if !errors.Is(err, auth.ErrMCPForbidden) || r.calls != 0 {
		t.Fatalf("scope was not applied before native read: %v calls %d", err, r.calls)
	}
}
func TestWorkspaceMCPProjectionAndExactFilter(t *testing.T) {
	d, r, token := workspaceFixture()
	r.result.Items[0].Project.Name = "password=secret"
	out, err := d.workspaceApplication(context.Background(), token, token.ProjectIDs[0], func(context.Context) (*auth.MCPToken, error) { return token, nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.filter.ApplicationID != token.ProjectIDs[0] || !r.filter.IdentityOnly || r.filter.Page != 1 || r.filter.PageSize != 1 {
		t.Fatalf("unbounded read: %+v", r.filter)
	}
	raw, _ := json.Marshal(out)
	for _, s := range []string{"secret.invalid", "password=secret", "repoUrl", "total", "latestRun"} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("unrequested metadata exposed: %s", raw)
		}
	}
	if !strings.Contains(string(raw), "[redacted]") {
		t.Fatal(string(raw))
	}
}
func TestWorkspaceMCPRefusesUnavailableRevokedAndMismatchedReads(t *testing.T) {
	for _, kind := range []string{"binding", "native_denied", "narrowed", "different_item", "multiple", "broken_assignment"} {
		t.Run(kind, func(t *testing.T) {
			d, r, token := workspaceFixture()
			fresh := *token
			switch kind {
			case "binding":
				d.BindToken = nil
			case "native_denied":
				r.err = tenancy.ErrForbidden
			case "narrowed":
				fresh.ProjectIDs = nil
			case "different_item":
				r.result.Items[0].Project.ID = uuid.NewString()
			case "multiple":
				r.result.Items = append(r.result.Items, r.result.Items[0])
			case "broken_assignment":
				r.result.Items[0].BusinessProjectID = ""
			}
			out, err := d.workspaceApplication(context.Background(), token, token.ProjectIDs[0], func(context.Context) (*auth.MCPToken, error) { return &fresh, nil })
			if err == nil || out != nil {
				t.Fatalf("unsafe result %v %v", out, err)
			}
		})
	}
}
func TestWorkspaceMCPSDKToolPreservesExistingIDsAndBindsContext(t *testing.T) {
	d, r, token := workspaceFixture()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := NewServer(d, func(context.Context) (*auth.MCPToken, error) { return token, nil })
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "workspace-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
		if tool.Name == "workspace_application_get" && !tool.Annotations.ReadOnlyHint {
			t.Fatal("workspace query is not read-only")
		}
	}
	for _, name := range []string{"projects_list", "pipeline_get", "pipeline_trigger", "run_get", "run_wait", "deployments_get", "workspace_application_get"} {
		if !names[name] {
			t.Fatal("missing preserved tool", name)
		}
	}
	if len(names) != 7 {
		t.Fatal(names)
	}
	result, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "workspace_application_get", Arguments: map[string]any{"projectId": token.ProjectIDs[0]}})
	if err != nil || result.IsError || !r.bound {
		t.Fatalf("query or bound context failed: %+v %v bound %v", result, err, r.bound)
	}
	calls := r.calls
	result, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "workspace_application_get", Arguments: json.RawMessage(`{"projectId":"` + token.ProjectIDs[0] + `","projectId":"` + token.ProjectIDs[0] + `"}`)})
	if err != nil || !result.IsError || r.calls != calls {
		t.Fatalf("ambiguous request was read: %+v %v", result, err)
	}
}

func TestWorkspaceMCPNativeServiceLegacyTokenIntersection(t *testing.T) {
	db := storetest.OpenDB(t)
	ctx := context.Background()
	credential, allowed, other, business, productID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.ExecContext(ctx, `INSERT INTO credentials(id,name,type,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, credential, "test", "git_token", []byte("test-ciphertext"), "masked", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	for n, id := range []string{allowed, other} {
		_, err = db.ExecContext(ctx, `INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, []string{"allowed", "unrelated"}[n], "https://test.invalid/repository", credential, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `INSERT INTO workspace_business_projects(id,name,revision) VALUES(?,?,1)`, business, "business")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO workspace_products(id,business_project_id,name,revision) VALUES(?,?,?,1)`, productID, business, "product")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{allowed, other} {
		_, err = db.ExecContext(ctx, `INSERT INTO workspace_applications(application_id,product_id,revision) VALUES(?,?,1)`, id, productID)
		if err != nil {
			t.Fatal(err)
		}
	}
	d := Dependencies{Workspace: workspace.New(db, project.New(db, nil, nil)), BindToken: func(ctx context.Context, _ *auth.MCPToken) (context.Context, error) { return ctx, nil }}
	token := &auth.MCPToken{ID: "legacy", ProjectIDs: []string{allowed}}
	verify := func(context.Context) (*auth.MCPToken, error) { return token, nil }
	out, err := d.workspaceApplication(ctx, token, allowed, verify)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), other) || strings.Contains(string(raw), "unrelated") || !strings.Contains(string(raw), productID) {
		t.Fatalf("native scope result: %s", raw)
	}
	if _, err = d.workspaceApplication(ctx, token, other, verify); !errors.Is(err, auth.ErrMCPForbidden) {
		t.Fatalf("other application readable: %v", err)
	}
}
