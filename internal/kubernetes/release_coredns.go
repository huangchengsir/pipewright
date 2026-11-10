package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"net"
	"sort"
	"strings"
)

type ReleaseCoreDNSPod struct {
	Image string `json:"image"`
	UID   string `json:"uid"`
	IP    string `json:"ip"`
}
type ReleaseCoreDNSPlan struct {
	Finalize      *ReleaseResource    `json:"finalize,omitempty"`
	Container     string              `json:"container"`
	LiveIndex     int                 `json:"liveIndex"`
	Canary        string              `json:"canary"`
	Deployment    ReleaseResource     `json:"deployment"`
	Dependencies  []ReleaseResource   `json:"dependencies"`
	Missing       []ReleaseResourceID `json:"missing,omitempty"`
	Pods          []ReleaseCoreDNSPod `json:"pods"`
	PriorityClass *ReleaseResourceID  `json:"priorityClass,omitempty"`
}

func coreDNSPodImage(pod map[string]any, container string) string {
	for _, value := range releaseItems(releaseMap(pod["status"])["containerStatuses"]) {
		status := releaseMap(value)
		if status["name"] == container {
			value := releaseString(status["imageID"])
			value = strings.TrimPrefix(value, "docker-pullable://")
			if ValidImageReference(value) {
				return value
			}
		}
	}
	for _, value := range releaseItems(releaseMap(pod["spec"])["containers"]) {
		item := releaseMap(value)
		if item["name"] == container && ValidImageReference(releaseString(item["image"])) {
			return releaseString(item["image"])
		}
	}
	return ""
}
func (c *ReleaseController) prepareCoreDNSPlan(ctx context.Context, api releaseAPI, plan *ReleasePlan, catalog ReleaseAuthorization, config []byte) ([]ReleaseAccess, error) {
	var cfg pipeline.KubernetesReleaseConfig
	if json.Unmarshal(config, &cfg) != nil {
		return nil, ErrInvalid
	}
	if cfg.CoreDNS == nil {
		return nil, nil
	}
	d := cfg.CoreDNS
	if plan.Mode != "manifest" || len(plan.Resources) != 1 || len(d.Records) == 0 || len(d.Records) > 4 || len(d.CandidatePrefix) > 30 || !validName(d.CandidatePrefix, 30) || d.DNSPort < 1 || d.DNSPort > 65535 || net.ParseIP(d.CandidateServiceIP) == nil {
		return nil, ErrInvalid
	}
	source := plan.Resources[0]
	if source.ID.Kind != "ConfigMap" || source.ID.Group != "" || source.ID.Namespace != cfg.Namespace || source.ID.Name != d.ConfigMap || releaseString(releaseMap(source.Object["data"])[d.Key]) == "" {
		return nil, ErrInvalid
	}
	original, e := readReleaseObject(ctx, api, source.ID)
	if e != nil {
		return nil, e
	}
	if original["immutable"] == true {
		return nil, ErrInvalid
	}
	deploymentID, e := releaseWorkloadID("Deployment", cfg.Namespace, d.Deployment)
	if e != nil {
		return nil, e
	}
	deployment, e := readReleaseObject(ctx, api, deploymentID)
	if e != nil {
		return nil, e
	}
	originalWorkload := releaseResourceFromObject(deploymentID, releaseCanonicalObject(deployment), "workload")
	originalWorkload.ExpectedUID = releaseString(releaseMap(deployment["metadata"])["uid"])
	if e = validateReleaseWorkloadStrategy(originalWorkload); e != nil {
		return nil, e
	}
	related, e := c.related(ctx, api, originalWorkload)
	if e != nil {
		return nil, e
	}
	if EvaluateReleaseHealth(originalWorkload, deployment, related).State != "ready" {
		return nil, ErrRollout
	}
	maintenance := &ReleaseCoreDNSPlan{Container: d.Container, Deployment: releaseResourceFromObject(deploymentID, deployment, "workload")}
	image := ""
	for _, pod := range related {
		if pod["kind"] != "Pod" || releasePodHealth(pod, false).State != "ready" {
			continue
		}
		ip := releaseString(releaseMap(pod["status"])["podIP"])
		uid := releaseString(releaseMap(pod["metadata"])["uid"])
		observed := coreDNSPodImage(pod, d.Container)
		if net.ParseIP(ip) == nil || uid == "" || observed == "" || image != "" && image != observed {
			return nil, ErrRuntimeUnverified
		}
		image = observed
		maintenance.Pods = append(maintenance.Pods, ReleaseCoreDNSPod{UID: uid, IP: ip, Image: observed})
	}
	if len(maintenance.Pods) == 0 || len(maintenance.Pods) > 8 || len(maintenance.Pods) != int(releaseInt(releaseMap(deployment["spec"])["replicas"])) {
		return nil, ErrRollout
	}
	sort.Slice(maintenance.Pods, func(i, j int) bool { return maintenance.Pods[i].UID < maintenance.Pods[j].UID })
	dnsID := ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: cfg.Namespace, Name: d.Service}
	dnsService, e := readReleaseObject(ctx, api, dnsID)
	if e != nil {
		return nil, e
	}
	serviceSelector := releaseMap(releaseMap(dnsService["spec"])["selector"])
	if len(serviceSelector) == 0 {
		return nil, ErrInvalid
	}
	refID := checkResourceID(d.ServiceResource, d.ServiceResource.Namespace)
	if refID.Kind != "Service" || refID.Group != "" || refID.Version != "v1" || refID.Resource != "services" {
		return nil, ErrInvalid
	}
	reference, e := readReleaseObject(ctx, api, refID)
	if e != nil {
		return nil, e
	}
	serviceIP := releaseString(releaseMap(reference["spec"])["clusterIP"])
	if net.ParseIP(serviceIP) == nil {
		return nil, ErrInvalid
	}
	maintenance.Dependencies = append(maintenance.Dependencies, releaseResourceFromObject(dnsID, dnsService, "declaration"), releaseResourceFromObject(refID, reference, "declaration"))
	extra := []ReleaseAccess{releaseRequestedAccess(deploymentID, "get"), {ID: dnsID, Actions: []string{"get", "coredns"}}, {ID: refID, Actions: []string{"get", "coredns"}}}
	extra[0].Actions = uniqueReleaseActions(append(extra[0].Actions, "coredns"))
	// Freeze every ConfigMap volume used by imports as well as the root Corefile.
	// Candidates never read a moving ConfigMap under its original name.
	live := releaseCanonicalObject(original)
	if live["data"] == nil {
		live["data"] = map[string]any{}
	}
	for key, value := range releaseMap(source.Object["data"]) {
		releaseMap(live["data"])[key] = value
	}
	for key, value := range releaseMap(source.Object["binaryData"]) {
		if live["binaryData"] == nil {
			live["binaryData"] = map[string]any{}
		}
		releaseMap(live["binaryData"])[key] = value
	}
	finalize := releaseResourceFromObject(source.ID, releaseObjectCopy(live), "declaration")
	finalize.ExpectedUID = releaseString(releaseMap(original["metadata"])["uid"])
	maintenance.Finalize = &finalize
	maintenance.Canary = "pw-" + strings.ReplaceAll(uuid.NewString(), "-", "") + ".invalid"
	corefile := releaseString(releaseMap(live["data"])[d.Key])
	releaseMap(live["data"])[d.Key] = corefile + coreDNSCanarySuffix(maintenance.Canary, d.DNSPort)
	candidateCMID := source.ID
	candidateCMID.Name = d.CandidatePrefix + "-config"
	candidateCM := releaseObjectCopy(live)
	candidateCM["metadata"] = map[string]any{"name": candidateCMID.Name, "namespace": cfg.Namespace}
	priorityID := ReleaseResourceID{Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses", Kind: "PriorityClass", Name: d.CandidatePrefix + "-nonpreempting"}
	if _, err := readReleaseObject(ctx, api, priorityID); !errors.Is(err, ErrNotFound) {
		if err == nil {
			return nil, ErrInUse
		}
		return nil, err
	}
	maintenance.PriorityClass = &priorityID
	priority := map[string]any{"apiVersion": "scheduling.k8s.io/v1", "kind": "PriorityClass", "metadata": map[string]any{"name": priorityID.Name}, "value": 0, "globalDefault": false, "preemptionPolicy": "Never"}
	candidates := []ReleaseResource{releaseResourceFromObject(priorityID, priority, "declaration"), releaseResourceFromObject(candidateCMID, candidateCM, "declaration")}
	candidates[0].Phase = 0
	candidate := releaseCanonicalObject(deployment)
	candidateID := deploymentID
	candidateID.Name = d.CandidatePrefix + "-dns"
	candidate["metadata"] = map[string]any{"name": candidateID.Name, "namespace": cfg.Namespace}
	spec := releaseMap(candidate["spec"])
	spec["replicas"] = 1
	token := uuid.NewString()
	labels := map[string]any{"pipewright.io/coredns-candidate": token}
	spec["selector"] = map[string]any{"matchLabels": labels}
	template := releaseMap(spec["template"])
	if template["metadata"] == nil {
		template["metadata"] = map[string]any{}
	}
	releaseMap(template["metadata"])["labels"] = labels
	podSpec := releaseMap(template["spec"])
	if podSpec["hostNetwork"] == true || podSpec["hostPID"] == true || podSpec["hostIPC"] == true {
		return nil, ErrInvalid
	}
	podSpec["priorityClassName"] = priorityID.Name
	delete(podSpec, "priority")
	podSpec["preemptionPolicy"] = "Never"
	found := false
	for _, value := range releaseItems(podSpec["containers"]) {
		container := releaseMap(value)
		if container["name"] == d.Container {
			container["image"] = image
			found = true
		}
	}
	if !found {
		return nil, ErrInvalid
	}
	mapped := map[string]string{d.ConfigMap: candidateCMID.Name}
	mounted := false
	for _, value := range releaseItems(podSpec["volumes"]) {
		volume := releaseMap(value)
		if volume["hostPath"] != nil || volume["secret"] != nil || volume["projected"] != nil {
			return nil, ErrInvalid
		}
		cm := releaseMap(volume["configMap"])
		if cm == nil {
			continue
		}
		name := releaseString(cm["name"])
		if mapped[name] != "" {
			cm["name"] = mapped[name]
			if name == d.ConfigMap {
				mounted = true
			}
			continue
		}
		id := source.ID
		id.Name = name
		object, e := readReleaseObject(ctx, api, id)
		if errors.Is(e, ErrNotFound) && cm["optional"] == true {
			maintenance.Missing = append(maintenance.Missing, id)
			// A missing optional volume has no content. ConfigMap's API JSON
			// omits an empty data map; do not freeze a field the API removes.
			object = map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": name, "namespace": cfg.Namespace}}
		} else if e != nil {
			return nil, e
		} else {
			maintenance.Dependencies = append(maintenance.Dependencies, releaseResourceFromObject(id, object, "declaration"))
		}
		extra = append(extra, ReleaseAccess{ID: id, Actions: []string{"get", "coredns"}})
		copy := releaseCanonicalObject(object)
		id.Name = d.CandidatePrefix + "-import-" + strconvI(len(mapped))
		copy["metadata"] = map[string]any{"name": id.Name, "namespace": cfg.Namespace}
		candidates = append(candidates, releaseResourceFromObject(id, copy, "declaration"))
		mapped[name] = id.Name
		cm["name"] = id.Name
	}
	if !mounted {
		return nil, ErrInvalid
	}
	candidates = append(candidates, releaseResourceFromObject(candidateID, candidate, "workload"))
	candidateServiceID := dnsID
	candidateServiceID.Name = d.CandidatePrefix + "-service"
	svc := map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": candidateServiceID.Name, "namespace": cfg.Namespace}, "spec": map[string]any{"clusterIP": d.CandidateServiceIP, "selector": labels, "ports": []any{map[string]any{"name": "dns-udp", "port": d.DNSPort, "targetPort": d.DNSPort, "protocol": "UDP"}, map[string]any{"name": "dns-tcp", "port": d.DNSPort, "targetPort": d.DNSPort, "protocol": "TCP"}}}}
	candidates = append(candidates, releaseResourceFromObject(candidateServiceID, svc, "declaration"))
	for index := range candidates {
		candidates[index].Ephemeral = true
		plan.Deletes = append(plan.Deletes, candidates[index].ID)
	}
	records := append([]pipeline.KubernetesDNSRecord{{Name: maintenance.Canary, Expected: "192.0.2.1"}, {Name: d.ServiceDomain, Expected: serviceIP}}, d.Records...)
	wait := min(300, plan.TimeoutSeconds-plan.ObserveSeconds-20)
	if wait < 30 {
		return nil, ErrInvalid
	}
	makeChecks := func(prefix, ip string) []pipeline.KubernetesReleaseCheck {
		checks := []pipeline.KubernetesReleaseCheck{}
		for index, record := range records {
			if net.ParseIP(record.Expected) == nil || record.Name == "" || len(record.Name) > 253 || strings.ContainsAny(record.Name, "\x00\r\n ") {
				return nil
			}
			for _, protocol := range []string{"udp", "tcp"} {
				checks = append(checks, pipeline.KubernetesReleaseCheck{Kind: "dns", Name: d.CandidatePrefix + "-" + prefix + "-" + strconvI(index) + "-" + protocol, Namespace: cfg.Namespace, Target: record.Name, Expected: record.Expected, Protocol: protocol, Server: net.JoinHostPort(ip, strconvI(d.DNSPort)), WaitSeconds: wait})
			}
		}
		return checks
	}
	addChecks := func(checks []pipeline.KubernetesReleaseCheck) ([]ReleaseResource, error) {
		if len(checks) == 0 {
			return nil, ErrInvalid
		}
		before := len(plan.Resources)
		data, _ := json.Marshal(map[string]any{"namespace": cfg.Namespace, "checks": checks})
		scopes, e := c.appendReleaseChecks(ctx, api, plan, catalog, data)
		clear(data)
		extra = append(extra, scopes...)
		if e != nil {
			return nil, e
		}
		for index := before; index < len(plan.Resources); index++ {
			plan.Resources[index].Ephemeral = true
		}
		return append([]ReleaseResource(nil), plan.Resources[before:]...), nil
	}
	plan.Resources = candidates
	candidateChecks, e := addChecks(makeChecks("candidate", d.CandidateServiceIP))
	if e != nil {
		return nil, e
	}
	maintenance.LiveIndex = len(plan.Resources)
	liveResource := releaseResourceFromObject(source.ID, live, "declaration")
	plan.Resources = append(plan.Resources, liveResource)
	for index, pod := range maintenance.Pods {
		if _, e = addChecks(makeChecks("live-"+strconvI(index), pod.IP)); e != nil {
			return nil, e
		}
	}
	_ = candidateChecks
	if len(plan.Resources) > MaxReleaseResources {
		return nil, ErrPrivateLimit
	}
	plan.Mode = "coredns"
	plan.CoreDNS = maintenance
	plan.Verify = append(plan.Verify, originalWorkload)
	return extra, nil
}
func (c *ReleaseController) verifyCoreDNSPopulation(ctx context.Context, api releaseAPI, plan ReleasePlan) error {
	maintenance := plan.CoreDNS
	if maintenance == nil {
		return ErrInvalid
	}
	object, e := readReleaseObject(ctx, api, maintenance.Deployment.ID)
	if e != nil {
		return e
	}
	if !releaseBaselineMatches(maintenance.Deployment.Object, object) {
		return ErrSuperseded
	}
	related, e := c.related(ctx, api, maintenance.Deployment)
	if e != nil {
		return e
	}
	resource := maintenance.Deployment
	resource.Object = releaseCanonicalObject(resource.Object)
	if EvaluateReleaseHealth(resource, object, related).State != "ready" {
		return ErrRollout
	}
	current := map[string]string{}
	images := map[string]string{}
	for _, pod := range related {
		if pod["kind"] == "Pod" && releasePodHealth(pod, false).State == "ready" {
			current[releaseString(releaseMap(pod["metadata"])["uid"])] = releaseString(releaseMap(pod["status"])["podIP"])
			images[releaseString(releaseMap(pod["metadata"])["uid"])] = coreDNSPodImage(pod, maintenance.Container)
		}
	}
	if len(current) != len(maintenance.Pods) {
		return ErrSuperseded
	}
	for _, pod := range maintenance.Pods {
		if current[pod.UID] != pod.IP || images[pod.UID] != pod.Image {
			return ErrSuperseded
		}
	}
	for _, dependency := range maintenance.Dependencies {
		object, e := readReleaseObject(ctx, api, dependency.ID)
		if e != nil {
			return e
		}
		if !releaseBaselineMatches(dependency.Object, object) {
			return ErrSuperseded
		}
	}
	for _, id := range maintenance.Missing {
		if _, e := readReleaseObject(ctx, api, id); !errors.Is(e, ErrNotFound) {
			if e == nil {
				return ErrSuperseded
			}
			return e
		}
	}
	return nil
}
