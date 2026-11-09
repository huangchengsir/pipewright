package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

func TestCoreDNSPlanFreezesIsolatedCandidateAndEveryOriginalInstance(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		fixture := newCoreDNSFixture(t, st)
		f, p, wrapper, plan, cfg, dnsService := fixture.f, fixture.policy, fixture.api, fixture.plan, fixture.config, fixture.dnsService
		sourceID := fixture.plan.Resources[0].ID
		data, _ := json.Marshal(cfg)
		controller := NewReleaseController(f.s, nil)
		scopes, e := controller.prepareCoreDNSPlan(ctx, wrapper, &plan, p.Authorization, data)
		if e != nil {
			t.Fatal(e)
		}
		if plan.Mode != "coredns" || plan.CoreDNS == nil || len(plan.CoreDNS.Pods) != 2 || plan.CoreDNS.LiveIndex != 10 || len(plan.Resources) != 23 || len(scopes) == 0 {
			t.Fatal("candidate/live action barrier or instance probes missing", plan.CoreDNS, len(plan.Resources), len(scopes))
		}
		if plan.Resources[plan.CoreDNS.LiveIndex].ID != sourceID || releaseMap(plan.Resources[1].Object["data"])["NodeHosts"] != "preserved-import-value" {
			t.Fatal("live boundary or frozen import data changed")
		}
		if !strings.Contains(releaseString(releaseMap(plan.Resources[1].Object["data"])["Corefile"]), plan.CoreDNS.Canary) {
			t.Fatal("fresh per-plan reload canary missing")
		}
		for _, resource := range plan.Resources[:3] {
			if !resource.Ephemeral {
				t.Fatal("candidate cleanup not declared")
			}
			if releaseMap(resource.Object["metadata"])["ownerReferences"] != nil || releaseMap(resource.Object["metadata"])["finalizers"] != nil || releaseMap(releaseMap(resource.Object["metadata"])["annotations"])["deployment.kubernetes.io/revision"] != nil {
				t.Fatal("candidate inherited a source controller identity")
			}
			if resource.ID.Kind == "Deployment" {
				labels := releaseMap(releaseMap(releaseMap(resource.Object["spec"])["template"])["metadata"])["labels"]
				if releaseLabelsMatch(releaseMap(releaseMap(dnsService["spec"])["selector"]), releaseMap(labels)) {
					t.Fatal("candidate joins live DNS service")
				}
			}
		}
		if e = controller.verifyCoreDNSPopulation(ctx, wrapper, plan); e != nil {
			t.Fatal(e)
		}
		wrapper.replaced = true
		if e = controller.verifyCoreDNSPopulation(ctx, wrapper, plan); !errors.Is(e, ErrSuperseded) {
			t.Fatal("replacement original Pod admitted to frozen plan", e)
		}
	})
}

type coreDNSPlanAPI struct {
	*progressiveControllerAPI
	replaced bool
}

func (a *coreDNSPlanAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	raw, e := a.progressiveControllerAPI.Read(ctx, path, query)
	if e != nil {
		return raw, e
	}
	if !strings.HasSuffix(path, "/pods") {
		return raw, nil
	}
	var list struct {
		API   string           `json:"apiVersion"`
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return nil, ErrInvalid
	}
	for index, pod := range list.Items {
		suffix := releaseString(releaseMap(pod["metadata"])["uid"])
		ip := "10.1.0.2"
		if strings.HasSuffix(suffix, "-1") {
			ip = "10.1.0.3"
		}
		if !strings.HasPrefix(suffix, "dns-deploy-uid-") {
			ip = "10.1.0.50"
		}
		releaseMap(pod["status"])["podIP"] = ip
		if a.replaced && index == 0 {
			releaseMap(pod["metadata"])["uid"] = "external-replacement"
		}
	}
	return json.Marshal(list)
}

type coreDNSFixture struct {
	f          *fixture
	policy     ReleasePlan
	api        *coreDNSPlanAPI
	plan       ReleasePlan
	config     pipeline.KubernetesReleaseConfig
	desired    map[string]any
	dnsService map[string]any
}

func newCoreDNSFixture(t *testing.T, st *store.Store) *coreDNSFixture {
	f, p := releaseStoreFixture(t, st)
	deployment := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"dns","namespace":"app","uid":"dns-deploy-uid","resourceVersion":"17","generation":1},"spec":{"replicas":2,"selector":{"matchLabels":{"k8s-app":"kube-dns"}},"template":{"metadata":{"labels":{"k8s-app":"kube-dns"}},"spec":{"serviceAccountName":"dns","containers":[{"name":"coredns","image":"registry.invalid/coredns@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","volumeMounts":[{"name":"config","mountPath":"/etc/coredns"}]}],"volumes":[{"name":"config","configMap":{"name":"dns-config"}}]}}},"status":{"observedGeneration":1,"replicas":2,"updatedReplicas":2,"availableReplicas":2}}`)
	cm := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"dns-config","namespace":"app","uid":"config-uid","resourceVersion":"17"},"data":{"Corefile":".:53 {\n forward . 1.1.1.1\n reload\n}\n","NodeHosts":"preserved-import-value"}}`)
	dnsService := releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"dns-service","namespace":"app","uid":"dns-service-uid","resourceVersion":"17"},"spec":{"clusterIP":"10.0.0.10","selector":{"k8s-app":"kube-dns"},"ports":[{"port":53,"protocol":"UDP"}]}}`)
	reference := releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"kubernetes","namespace":"default","uid":"api-service-uid","resourceVersion":"17"},"spec":{"clusterIP":"10.0.0.1","ports":[{"port":443}]}}`)
	releaseMap(deployment["metadata"])["annotations"] = map[string]any{"deployment.kubernetes.io/revision": "7"}
	releaseMap(deployment["metadata"])["ownerReferences"] = []any{map[string]any{"uid": "external-controller", "controller": true}}
	releaseMap(deployment["metadata"])["finalizers"] = []any{"external-controller.example/finalizer"}
	api := &progressiveControllerAPI{releaseControllerAPI: releaseControllerAPI{f: f, objects: map[string]map[string]any{"/apis/apps/v1/namespaces/app/deployments/dns": deployment, "/api/v1/namespaces/app/configmaps/dns-config": cm, "/api/v1/namespaces/app/services/dns-service": dnsService, "/api/v1/namespaces/default/services/kubernetes": reference}}}
	// Real API health carries a pod IP for each observed original instance.
	wrapper := &coreDNSPlanAPI{progressiveControllerAPI: api}
	sourceID := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "dns-config"}
	desired := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"dns-config","namespace":"app"},"data":{"Corefile":".:53 {\n hosts {\n 192.0.2.5 custom.invalid\n fallthrough\n }\n forward . 1.1.1.1\n reload\n}\n"}}`)
	plan := ReleasePlan{Version: 2, Mode: "manifest", Resources: []ReleaseResource{releaseResourceFromObject(sourceID, desired, "declaration")}, TimeoutSeconds: 600, ObserveSeconds: 0}
	cfg := pipeline.KubernetesReleaseConfig{Namespace: "app", CoreDNS: &pipeline.KubernetesCoreDNS{ConfigMap: "dns-config", Key: "Corefile", Deployment: "dns", Container: "coredns", Service: "dns-service", CandidatePrefix: "pw-dns", CandidateServiceIP: "10.0.0.20", DNSPort: 53, ServiceDomain: "kubernetes.default.svc.cluster.local", ServiceResource: pipeline.KubernetesCheckResource{Version: "v1", Resource: "services", Kind: "Service", Namespace: "default", Name: "kubernetes"}, Records: []pipeline.KubernetesDNSRecord{{Name: "custom.invalid", Expected: "192.0.2.5"}}}}
	return &coreDNSFixture{f: f, policy: p, api: wrapper, plan: plan, config: cfg, desired: desired, dnsService: dnsService}
}

type coreDNSExecutionAPI struct {
	*coreDNSPlanAPI
	failCandidate bool
	liveWrites    int
}

func (a *coreDNSExecutionAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	switch path {
	case "/api/v1":
		return []byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","kind":"ConfigMap","namespaced":true},{"name":"services","kind":"Service","namespaced":true},{"name":"pods","kind":"Pod","namespaced":true}]}`), nil
	case "/apis/apps/v1":
		return []byte(`{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true},{"name":"replicasets","kind":"ReplicaSet","namespaced":true}]}`), nil
	case "/apis/scheduling.k8s.io/v1":
		return []byte(`{"kind":"APIResourceList","groupVersion":"scheduling.k8s.io/v1","resources":[{"name":"priorityclasses","kind":"PriorityClass","namespaced":false}]}`), nil
	case "/apis/batch/v1":
		return []byte(`{"kind":"APIResourceList","groupVersion":"batch/v1","resources":[{"name":"jobs","kind":"Job","namespaced":true}]}`), nil
	}
	raw, e := a.coreDNSPlanAPI.Read(ctx, path, query)
	if e != nil || !strings.HasSuffix(path, "/pods") {
		return raw, e
	}
	var list struct {
		API   string           `json:"apiVersion"`
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return nil, ErrInvalid
	}
	for _, object := range a.objects {
		if object["kind"] != "Job" {
			continue
		}
		meta := releaseMap(object["metadata"])
		spec := releaseMap(releaseMap(releaseMap(object["spec"])["template"])["spec"])
		statuses := []any{}
		for _, value := range releaseItems(spec["containers"]) {
			container := releaseMap(value)
			statuses = append(statuses, map[string]any{"name": container["name"], "image": container["image"], "state": map[string]any{"terminated": map[string]any{"exitCode": 0}}})
		}
		list.Items = append(list.Items, map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "pod-" + releaseString(meta["name"]), "namespace": meta["namespace"], "uid": "pod-" + releaseString(meta["uid"]), "ownerReferences": []any{map[string]any{"uid": meta["uid"], "controller": true}}}, "spec": spec, "status": map[string]any{"phase": "Succeeded", "containerStatuses": statuses}})
	}
	return json.Marshal(list)
}
func (a *coreDNSExecutionAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if in.Verb != "delete" && in.Resource.Kind == "Deployment" {
		return a.progressiveControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	}
	base := &probeControllerAPI{releaseControllerAPI: &a.releaseControllerAPI}
	action, e := base.ExecuteAction(ctx, id, owner, revision, in, path)
	if e != nil {
		return action, e
	}
	if in.Resource.Kind == "ConfigMap" && in.Resource.Name == "dns-config" && in.Verb != "delete" {
		a.liveWrites++
	}
	if a.failCandidate && in.Resource.Kind == "Job" && strings.Contains(in.Resource.Name, "-candidate-") && in.Verb != "delete" {
		named, _ := resourceAPI(in.Resource).Path(in.Resource.Namespace, in.Resource.Name)
		a.objects[named]["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True"}}}
	}
	return action, nil
}
func TestCoreDNSControllerCandidateBarrierAndExactCleanup(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified-reload", true: "candidate-failed"}[failed], func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				fixture := newCoreDNSFixture(t, st)
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
					if strings.Contains(releaseString(releaseMap(current["data"])["Corefile"]), record.Plan.CoreDNS.Canary) || strings.Contains(releaseString(releaseMap(current["data"])["Corefile"]), restored.Plan.CoreDNS.Canary) {
						t.Fatal("rollback re-used old success rather than probing restored file")
					}
				}

			})
		})
	}
}
