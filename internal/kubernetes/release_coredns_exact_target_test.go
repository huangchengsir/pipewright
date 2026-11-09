package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

func TestCoreDNS105ExactRequestedCorefileAndRollback(t *testing.T) {
	for _, exactStage := range []string{"forward", "rollback"} {
		failed := false
		t.Run(exactStage, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				fixture := newCoreDNSFixture(t, st)
				initialCorefile := releaseString(releaseMap(fixture.api.objects["/api/v1/namespaces/app/configmaps/dns-config"]["data"])["Corefile"])
				f := fixture.f
				c := NewReleaseController(f.s, nil)
				c.poll = time.Millisecond
				api := &coreDNSExecutionAPI{coreDNSPlanAPI: fixture.api, failCandidate: failed}
				cfg := fixture.config
				data, _ := json.Marshal(cfg)
				scout := fixture.plan
				extra, e := c.prepareCoreDNSPlan(ctx, api, &scout, fixture.policy.Authorization, data)
				if e != nil {
					t.Fatal(e)
				}
				scopes := append([]ReleaseAccess(nil), extra...)
				for _, resource := range scout.Resources {
					scope := ReleaseAccess{ID: resource.ID, Actions: []string{"get", "create", "patch", "delete", "coredns", "probe", "related-pods", "related-replicasets", "related-endpointslices"}, Containers: releaseContainerNames(resource)}
					scopes = append(scopes, scope)
				}
				scopes = mergeReleaseScopes(scopes)
				for i := range scopes {
					scopes[i].Actions = uniqueReleaseActions(append(scopes[i].Actions, "rollback"))
				}
				if _, e = f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); e != nil {
					t.Fatal(e)
				}
				auth, e := f.s.ReleasePolicyCatalog(ctx, fixture.policy.Authorization.Request)
				if e != nil {
					t.Fatal(e)
				}
				c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
				c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
				files, _ := InlineReleaseFile(stringMustJSON(fixture.desired))
				record, e := c.PrepareManifest(ctx, "coredns-run", "node", "owner", auth, files, nil, data, 600, 0)
				if e != nil {
					t.Fatal(e)
				}
				record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				api.record = record
				registerControllerTestWriter(t, f, record)
				result, e := c.Run(ctx, record.ID, record.OwnerEpoch, nil)
				if failed {
					if e == nil || result.State != "partial_failed" || api.liveWrites != 0 {
						t.Fatal("candidate failure changed live Corefile", result.State, api.liveWrites, e)
					}
				} else if e != nil || !result.Successful() || api.liveWrites != 2 {
					t.Fatal("verified candidate/live sequence failed", result.State, result.Cleanup, api.liveWrites, e)
				}
				for _, resource := range record.Plan.Resources {
					if !resource.Ephemeral {
						continue
					}
					path, _ := resourceAPI(resource.ID).Path(resource.ID.Namespace, resource.ID.Name)
					if api.objects[path] != nil {
						t.Fatal("owned temporary resource was not cleaned", resource.ID.Name)
					}
				}
				live := api.objects["/api/v1/namespaces/app/configmaps/dns-config"]
				if exactStage == "forward" && releaseString(releaseMap(live["data"])["Corefile"]) != releaseString(releaseMap(fixture.desired["data"])["Corefile"]) {
					t.Fatal("successful CoreDNS release retained unrequested canary instead of exact requested Corefile bytes")
				}
				if strings.Contains(releaseString(releaseMap(live["data"])["Corefile"]), record.Plan.CoreDNS.Canary) {
					t.Fatal("temporary live reload canary was retained after completion")
				}
				if !failed {
					restored, e := c.PrepareRollback(ctx, "coredns-rollback", "rollback-node", "rollback-owner", result.ID, result.Revision, result.PlanID)
					if e != nil {
						t.Fatal("CoreDNS rollback prepare", e)
					}
					if restored.Plan.CoreDNS == nil || restored.Plan.CoreDNS.Canary == record.Plan.CoreDNS.Canary || !restored.Plan.ForceApproval || restored.Plan.RollbackOf != record.ID {
						t.Fatal("CoreDNS rollback skipped fresh candidate/canary/approval")
					}
					restored, e = f.s.ApproveRelease(ctx, restored.ID, restored.OwnerEpoch, restored.Revision, restored.PlanID)
					if e != nil {
						t.Fatal(e)
					}
					api.record = restored
					registerControllerTestWriter(t, f, restored)
					restored, e = c.Run(ctx, restored.ID, restored.OwnerEpoch, nil)
					if e != nil || restored.State != "rolled_back" || api.liveWrites != 4 {
						t.Fatal("CoreDNS rollback reload sequence", restored.State, e)
					}
					current := api.objects["/api/v1/namespaces/app/configmaps/dns-config"]
					if releaseString(releaseMap(current["data"])["Corefile"]) != initialCorefile {
						t.Fatal("successful CoreDNS rollback did not restore exact initial Corefile bytes")
					}
				}

			})
		})
	}
}
