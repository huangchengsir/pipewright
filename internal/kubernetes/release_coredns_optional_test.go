package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	corev1 "k8s.io/api/core/v1"
)

type coreDNSOptionalImportAPI struct{ *coreDNSPlanAPI }

func (a *coreDNSOptionalImportAPI) DryRun(ctx context.Context, in kubeprotocol.Request) (map[string]any, error) {
	object, err := a.releaseControllerAPI.DryRun(ctx, in)
	if err != nil || object["kind"] != "ConfigMap" {
		return object, err
	}
	// Use the official API type's actual JSON encoding: Data has omitempty.
	body, _ := json.Marshal(object)
	var typed corev1.ConfigMap
	if json.Unmarshal(body, &typed) != nil {
		return nil, ErrInvalid
	}
	body, _ = json.Marshal(typed)
	var admitted map[string]any
	if json.Unmarshal(body, &admitted) != nil {
		return nil, ErrInvalid
	}
	return admitted, nil
}
func TestCoreDNSMissingOptionalImportSurvivesActualConfigMapOmission(t *testing.T) {
	ctx := context.Background()
	fixture := newCoreDNSFixture(t, storetest.Open(t))
	deployment := fixture.api.objects["/apis/apps/v1/namespaces/app/deployments/dns"]
	pod := releaseMap(releaseMap(releaseMap(deployment["spec"])["template"])["spec"])
	pod["volumes"] = append(releaseItems(pod["volumes"]), map[string]any{"name": "custom", "configMap": map[string]any{"name": "dns-custom", "optional": true}})
	api := &coreDNSOptionalImportAPI{fixture.api}
	controller := NewReleaseController(fixture.f.s, nil)
	config, _ := json.Marshal(fixture.config)
	plan := fixture.plan
	if _, err := controller.prepareCoreDNSPlan(ctx, api, &plan, fixture.policy.Authorization, config); err != nil {
		t.Fatal("CoreDNS fixture preparation failed")
	}
	if plan.CoreDNS == nil || len(plan.CoreDNS.Missing) != 1 || plan.CoreDNS.Missing[0].Name != "dns-custom" {
		t.Fatal("optional original absence proof lost")
	}
	var candidate ReleaseResource
	for _, resource := range plan.Resources {
		if resource.ID.Kind == "ConfigMap" && resource.ID.Name == "pw-dns-import-1" {
			candidate = resource
		}
	}
	if candidate.ID.Name == "" {
		t.Fatal("isolated empty import not generated")
	}
	controller.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	dryPlan := ReleasePlan{Version: 2, Mode: "coredns", Resources: []ReleaseResource{candidate}, Authorization: fixture.policy.Authorization}
	if err := controller.dryRunReleaseResources(ctx, "owner", &dryPlan); err != nil {
		t.Fatal("synthetic optional import failed real ConfigMap JSON omission", err)
	}
	if _, declared := dryPlan.Resources[0].Object["data"]; declared {
		t.Fatal("synthetic import declares empty field omitted by API")
	}
	if err := controller.verifyCoreDNSPopulation(ctx, api, plan); err != nil {
		t.Fatal("original absence verification failed", err)
	}
	api.objects["/api/v1/namespaces/app/configmaps/dns-custom"] = releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"dns-custom","namespace":"app","uid":"new-external-import","resourceVersion":"9"},"data":{"custom":"external"}}`)
	if err := controller.verifyCoreDNSPopulation(ctx, api, plan); !errors.Is(err, ErrSuperseded) {
		t.Fatal("previously absent import appeared without invalidating plan")
	}
	// The generic matcher still rejects loss of an explicitly declared field.
	declared := map[string]any{"data": map[string]any{"custom": "approved"}}
	if ReleasePodTemplateMatches(declared, map[string]any{}) || ReleasePodTemplateMatches(map[string]any{"data": map[string]any{}}, map[string]any{}) {
		t.Fatal("generic declared-field matcher was weakened")
	}
}
