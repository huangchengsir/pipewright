package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCronJobExplicitRunFreezesSeparatelyNamedJobAndExactAuthority(t *testing.T) {
	ctx := context.Background()
	c := NewReleaseController(nil, nil)
	cronID := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "cronjobs", Kind: "CronJob", Namespace: "app", Name: "schedule"}
	cron := releaseResourceFromObject(cronID, releaseTestObject(`{"apiVersion":"batch/v1","kind":"CronJob","metadata":{"name":"schedule","namespace":"app"},"spec":{"schedule":"0 * * * *","jobTemplate":{"metadata":{"labels":{"purpose":"scheduled"}},"spec":{"template":{"spec":{"restartPolicy":"Never","containers":[{"name":"task","image":"registry.invalid/task@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","args":["work"]}]}}}}}}`), "declaration")
	service := releaseResourceFromObject(ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: "app", Name: "schedule"}, releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"schedule","namespace":"app"}}`), "declaration")
	plan := ReleasePlan{Resources: []ReleaseResource{service, cron}, TimeoutSeconds: 300}
	api := &releaseControllerAPI{objects: map[string]map[string]any{}}
	scopes, e := c.appendReleaseChecks(ctx, api, &plan, ReleaseAuthorization{}, []byte(`{"namespace":"app","checks":[{"kind":"workload","name":"schedule","target":"manual-job"}]}`))
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Resources) != 3 || plan.Resources[2].ID.Kind != "Job" || plan.Resources[2].ID.Name != "manual-job" || plan.Resources[2].Probe || plan.Resources[2].Health != "workload" {
		t.Fatal("explicit CronJob run did not create a real Job completion gate")
	}
	if len(scopes) != 1 || scopes[0].ID != plan.Resources[2].ID || !hasReleaseAction(scopes[0].Actions, "create") || !hasReleaseAction(scopes[0].Actions, "related-pods") || hasReleaseAction(scopes[0].Actions, "related-jobs") || len(scopes[0].Containers) != 1 || scopes[0].Containers[0] != "task" {
		t.Fatal("manual Job widened unrelated derived reads")
	}
	copy := releaseObjectCopy(cron.Object)
	releaseMap(releaseMap(releaseMap(copy["spec"])["jobTemplate"])["spec"])["backoffLimit"] = float64(7)
	if releaseMap(plan.Resources[2].Object["spec"])["backoffLimit"] != nil {
		t.Fatal("frozen manual Job changed with source object")
	}
	for _, config := range []string{`{"namespace":"app","checks":[{"kind":"workload","name":"schedule"}]}`, `{"namespace":"app","checks":[{"kind":"workload","name":"schedule","target":"manual-job","restart":true}]}`} {
		candidate := ReleasePlan{Resources: []ReleaseResource{cron}, TimeoutSeconds: 300}
		if _, e = c.appendReleaseChecks(ctx, api, &candidate, ReleaseAuthorization{}, []byte(config)); !errors.Is(e, ErrInvalid) {
			t.Fatal("ambiguous CronJob execution silently became a declaration check", e)
		}
	}
	external := releaseObjectCopy(cron.Object)
	releaseMap(external["metadata"])["uid"] = "cron-uid"
	releaseMap(external["metadata"])["resourceVersion"] = "17"
	api.objects["/apis/batch/v1/namespaces/app/cronjobs/schedule"] = external
	candidate := ReleasePlan{Resources: []ReleaseResource{service}, TimeoutSeconds: 300}
	config := map[string]any{"namespace": "app", "checks": []any{map[string]any{"kind": "workload", "name": "schedule", "target": "external-job", "resource": cronID}}}
	raw, _ := json.Marshal(config)
	scopes, e = c.appendReleaseChecks(ctx, api, &candidate, ReleaseAuthorization{}, raw)
	if e != nil || len(candidate.Verify) != 1 || candidate.Verify[0].ExpectedUID != "cron-uid" || len(scopes) != 2 {
		t.Fatal("existing CronJob identity not frozen separately", e)
	}
}
