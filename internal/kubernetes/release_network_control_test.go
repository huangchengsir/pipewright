package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestNetworkDenyPlanRequiresSameNamespaceExactTargetPositiveControl(t *testing.T) {
	f, p := releaseStoreFixture(t, storetest.Open(t))
	c := NewReleaseController(f.s, nil)
	for _, scenario := range []string{"missing", "wrong-kind", "wrong-target", "wrong-namespace", "matching"} {
		t.Run(scenario, func(t *testing.T) {
			checks := []pipeline.KubernetesReleaseCheck{{Kind: "network-deny", Name: "deny", Target: "10.0.0.1:8080", PositiveControl: "allow"}, {Kind: "network-allow", Name: "allow", Target: "10.0.0.1:8080"}}
			switch scenario {
			case "missing":
				checks = checks[:1]
			case "wrong-kind":
				checks[1].Kind = "tcp"
			case "wrong-target":
				checks[1].Target = "10.0.0.2:8080"
			case "wrong-namespace":
				checks[1].Namespace = "other"
			}
			cfg := pipeline.KubernetesReleaseConfig{Namespace: "app", Checks: checks}
			body, _ := json.Marshal(cfg)
			plan := ReleasePlan{Version: 2, Mode: "manifest", TimeoutSeconds: 30}
			api := &releaseControllerAPI{objects: map[string]map[string]any{}}
			_, err := c.appendReleaseChecks(context.Background(), api, &plan, p.Authorization, body)
			if scenario != "matching" {
				if !errors.Is(err, ErrInvalid) || api.writes != 0 {
					t.Fatal("invalid positive-control pair accepted")
				}
				return
			}
			if err != nil || len(plan.Resources) != 2 || plan.Resources[0].ID.Name != "allow" || plan.Resources[1].ID.Name != "deny" || plan.Resources[1].PositiveControl != "allow" {
				t.Fatal("matching pair not frozen in positive-before-negative order")
			}
		})
	}
}

type networkControlAPI struct {
	*probeControllerAPI
	mode               string
	denyCreates        int
	denyBeforeVerified bool
}

func (a *networkControlAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, intent ReleaseActionIntent, path string) (ReleaseAction, error) {
	if intent.Verb == "create" && intent.Resource.Name == "allow" && a.mode == "unknown" {
		fact, err := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, intent)
		if err != nil {
			return fact, err
		}
		return fact, ErrOutcomeUnknown
	}
	if intent.Verb == "create" && intent.Resource.Name == "deny" {
		a.denyCreates++
		facts, err := a.f.s.ListReleaseActions(ctx, id)
		if err != nil {
			return ReleaseAction{}, err
		}
		observed := false
		control := a.objects["/apis/batch/v1/namespaces/app/jobs/allow"]
		for _, fact := range facts {
			if fact.Intent.Resource.Name == "allow" && fact.State == "verified" && fact.Receipt != nil && fact.Receipt.Health.State == "ready" && fact.Receipt.UID == releaseMap(control["metadata"])["uid"] && fact.ID == releaseMap(releaseMap(control["metadata"])["annotations"])[ReleaseActionAnnotation] {
				observed = true
			}
		}
		a.denyBeforeVerified = !observed
	}
	fact, err := a.probeControllerAPI.ExecuteAction(ctx, id, owner, revision, intent, path)
	if err == nil && intent.Verb == "create" && intent.Resource.Name == "allow" {
		actual := a.objects["/apis/batch/v1/namespaces/app/jobs/allow"]
		if a.mode == "failed" {
			actual["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True", "reason": "BackoffLimitExceeded"}}}
		}
		if a.mode == "wrong-uid" {
			releaseMap(actual["metadata"])["uid"] = "externally-recreated-uid"
		}
	}
	return fact, err
}
func TestNetworkDenyControllerRequiresVerifiedSuccessfulControlBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"ready", "failed", "wrong-uid", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			ctx := context.Background()
			scopes := []ReleaseAccess{}
			for _, name := range []string{"allow", "deny"} {
				scopes = append(scopes, ReleaseAccess{ID: ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: name}, Actions: []string{"get", "create", "delete", "probe", "related-pods"}, Containers: []string{"probe"}})
			}
			if _, err := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Enabled: true, RequireApproval: true, Resources: scopes}); err != nil {
				t.Fatal(err)
			}
			request := p.Authorization.Request
			request.Resources = scopes
			auth, err := f.s.AuthorizeRelease(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			api := &networkControlAPI{mode: mode, probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}, cleanup: true}}
			c := NewReleaseController(f.s, nil)
			c.poll = time.Millisecond
			plan := ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, TimeoutSeconds: 30}
			checks := []byte(`{"namespace":"app","checks":[{"kind":"network-deny","name":"deny","target":"10.0.0.1:8080","positiveControl":"allow","labels":{"test":"deny"}},{"kind":"network-allow","name":"allow","target":"10.0.0.1:8080","labels":{"test":"allow"}}]}`)
			extra, err := c.appendReleaseChecks(ctx, api, &plan, auth, checks)
			if err != nil {
				t.Fatal(err)
			}
			request.Resources = extra
			plan.Authorization, err = f.s.AuthorizeRelease(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			record, err := f.s.CreateRelease(ctx, "network-control-run", "node", "owner", plan)
			if err != nil {
				t.Fatal(err)
			}
			record, err = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
			if err != nil {
				t.Fatal(err)
			}
			api.record = record
			registerControllerTestWriter(t, f, record)
			c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
			result, err := c.Run(ctx, record.ID, record.OwnerEpoch, nil)
			if mode == "ready" {
				if err != nil || !result.Successful() || api.denyCreates != 1 || api.denyBeforeVerified {
					t.Fatal("deny did not follow durable exact-UID positive completion", result.State, err)
				}
			} else if err == nil || api.denyCreates != 0 || result.Successful() {
				t.Fatal("failed/unverified positive control allowed negative dispatch", mode, result.State)
			}
		})
	}
}
