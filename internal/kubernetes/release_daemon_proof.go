package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"strings"
)

const releaseDaemonProofVersion = 1
const releaseDaemonRevisionAction = "related-controllerrevisions"
const releaseDaemonHashLabel = "controller-revision-hash"

func releaseDaemonID(id ReleaseResourceID) bool {
	return id.Group == "apps" && id.Version == "v1" && id.Resource == "daemonsets" && id.Kind == "DaemonSet" && id.Namespace != ""
}
func releaseExactJSON(a, b any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	defer clear(x)
	y, e := json.Marshal(b)
	defer clear(y)
	return e == nil && bytes.Equal(x, y)
}
func releaseDaemonOwner(obj, ds map[string]any) bool {
	m := releaseMap(ds["metadata"])
	count := 0
	for _, v := range releaseItems(releaseMap(obj["metadata"])["ownerReferences"]) {
		o := releaseMap(v)
		if o["controller"] == true {
			if o["apiVersion"] != "apps/v1" || o["kind"] != "DaemonSet" || o["uid"] != m["uid"] || o["name"] != m["name"] {
				return false
			}
			count++
		}
	}
	return count == 1 && releaseString(m["uid"]) != "" && releaseMap(obj["metadata"])["namespace"] == m["namespace"]
}

// Select only one owned current history by its complete admitted template, never
// by numeric revision order or a locally recreated version-sensitive hash.
func releaseDaemonCurrentHash(ds map[string]any, related []map[string]any) (string, bool) {
	current := releaseMap(releaseMap(ds["spec"])["template"])
	hash := ""
	seen := map[string]bool{}
	for _, obj := range related {
		if obj["kind"] != "ControllerRevision" {
			continue
		}
		if obj["apiVersion"] != "apps/v1" || !releaseDaemonOwner(obj, ds) {
			return "", false
		}
		m := releaseMap(obj["metadata"])
		uid := releaseString(m["uid"])
		if uid == "" || seen[uid] || m["deletionTimestamp"] != nil || releaseInt(obj["revision"]) < 1 {
			return "", false
		}
		seen[uid] = true
		data := releaseMap(obj["data"])
		spec := releaseMap(data["spec"])
		template := releaseMap(spec["template"])
		if len(data) != 1 || len(spec) != 1 || template == nil || template["$patch"] != "replace" {
			return "", false
		}
		template = releaseObjectCopy(template)
		delete(template, "$patch")
		if !releaseExactJSON(template, current) {
			continue
		}
		candidate := releaseString(releaseMap(m["labels"])[releaseDaemonHashLabel])
		if !validName(candidate, 63) || !strings.HasSuffix(releaseString(m["name"]), "-"+candidate) || hash != "" {
			return "", false
		}
		hash = candidate
	}
	return hash, hash != ""
}

// Mirror only the pinned native AddOrUpdate semantics; no typed Pod roundtrip
// may discard unknown fields. Other custom entries and their order survive.
func releaseDaemonTolerations(spec map[string]any) bool {
	values := releaseItems(spec["tolerations"])
	if spec["tolerations"] != nil {
		if _, ok := spec["tolerations"].([]any); !ok {
			return false
		}
	}
	for _, v := range values {
		if _, ok := releaseReadToleration(v); !ok {
			return false
		}
	}
	keys := []string{"not-ready", "unreachable", "disk-pressure", "memory-pressure", "pid-pressure", "unschedulable"}
	if releaseBool(spec["hostNetwork"]) {
		keys = append(keys, "network-unavailable")
	}
	for index, key := range keys {
		effect := "NoSchedule"
		if index < 2 {
			effect = "NoExecute"
		}
		target := map[string]any{"key": "node.kubernetes.io/" + key, "operator": "Exists", "effect": effect}
		want, _ := releaseReadToleration(target)
		next := []any{}
		updated, same := false, false
		for _, raw := range values {
			v, _ := releaseReadToleration(raw)
			if v.key == want.key && v.operator == want.operator && v.value == want.value && v.effect == want.effect {
				if v == want {
					same = true
					break
				}
				next = append(next, target)
				updated = true
			} else {
				next = append(next, raw)
			}
		}
		if same {
			continue
		}
		if !updated {
			next = append(next, target)
		}
		values = next
	}
	spec["tolerations"] = values
	return true
}
func releaseDaemonPodMatches(ds, pod map[string]any, hash string) bool {
	if pod["apiVersion"] != "v1" || pod["kind"] != "Pod" || releaseString(releaseMap(pod["metadata"])["uid"]) == "" || !releaseDaemonOwner(pod, ds) || releaseMap(releaseMap(pod["metadata"])["labels"])[releaseDaemonHashLabel] != hash {
		return false
	}
	template := releaseMap(releaseMap(ds["spec"])["template"])
	expected := releaseObjectCopy(releaseMap(template["spec"]))
	actual := releaseMap(pod["spec"])
	node := releaseString(actual["nodeName"])
	if expected == nil || !validName(node, 253) || expected["nodeName"] != nil && expected["nodeName"] != "" && expected["nodeName"] != node || !releaseDaemonTolerations(expected) {
		return false
	}
	// A scheduled DaemonSet Pod contains exactly the native target-node selector.
	required := map[string]any{"nodeSelectorTerms": []any{map[string]any{"matchFields": []any{map[string]any{"key": "metadata.name", "operator": "In", "values": []any{node}}}}}}
	affinity := releaseMap(expected["affinity"])
	if affinity == nil {
		affinity = map[string]any{}
		expected["affinity"] = affinity
	}
	nodeAffinity := releaseMap(affinity["nodeAffinity"])
	if nodeAffinity == nil {
		nodeAffinity = map[string]any{}
		affinity["nodeAffinity"] = nodeAffinity
	}
	nodeAffinity["requiredDuringSchedulingIgnoredDuringExecution"] = required
	if !releaseExactJSON(required, releaseMap(releaseMap(actual["affinity"])["nodeAffinity"])["requiredDuringSchedulingIgnoredDuringExecution"]) {
		return false
	}
	if !releaseExactJSON(affinity, actual["affinity"]) {
		return false
	}
	want, got := releaseItems(expected["tolerations"]), releaseItems(actual["tolerations"])
	if len(want) != len(got) {
		return false
	}
	for i, v := range want {
		w, ok := releaseReadToleration(v)
		g, valid := releaseReadToleration(got[i])
		if !ok || !valid || w != g {
			return false
		}
	}
	// The native hash label replaces only that reserved label; all declared user
	// labels/annotations remain checked, as do all non-rewritten Pod fields.
	sourceMeta := releaseMap(template["metadata"])
	meta := map[string]any{}
	// RealPodControl copies exactly labels, annotations and finalizers; the
	// template creationTimestamp is not the new Pod's creation timestamp.
	for _, key := range []string{"labels", "annotations", "finalizers"} {
		if sourceMeta[key] != nil {
			meta[key] = releaseObjectCopy(map[string]any{"v": sourceMeta[key]})["v"]
		}
	}
	if meta != nil {
		labels := releaseMap(meta["labels"])
		delete(labels, releaseDaemonHashLabel)
		if !ReleasePodTemplateMatches(meta, pod["metadata"]) {
			return false
		}
	}
	return releasePodSpecMatches(expected, actual)
}
func validReleaseDaemonProof(plan ReleasePlan) bool {
	groups := [][]ReleaseResource{plan.Resources, plan.Verify, plan.Baseline}
	groups = append(groups, plan.Batches...)
	if plan.Helm != nil {
		groups = append(groups, plan.Helm.Rendered)
	}
	for _, group := range groups {
		for _, r := range group {
			if r.DaemonSetProofVersion == 0 {
				continue
			}
			if r.DaemonSetProofVersion != releaseDaemonProofVersion || !releaseDaemonID(r.ID) {
				return false
			}
			found := false
			for _, a := range plan.Authorization.Request.Resources {
				if a.ID == r.ID && releaseStringSetContains(a.Actions, []string{"get", releaseDaemonRevisionAction, "related-pods"}) {
					found = true
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}
func (s *ReleaseSession) requireDaemonRevisionCapability(ctx context.Context) error {
	reply, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "ping"})
	if e != nil || reply.Code != "ok" || !releaseStringSetContains(reply.Capabilities, []string{kubeprotocol.DaemonSetRevisionReadCapability}) {
		return ErrVersion
	}
	return nil
}

// Every newly prepared plan uses the explicit proof; omitted historical fields
// remain untouched when loading/running an already sealed older plan.
func (c *ReleaseController) prepareDaemonRevisionProof(ctx context.Context, plan *ReleasePlan) error {
	ids := map[ReleaseResourceID]bool{}
	mark := func(group []ReleaseResource) {
		for i := range group {
			if releaseDaemonID(group[i].ID) {
				group[i].DaemonSetProofVersion = releaseDaemonProofVersion
				ids[group[i].ID] = true
			}
		}
	}
	mark(plan.Resources)
	mark(plan.Verify)
	mark(plan.Baseline)
	for _, batch := range plan.Batches {
		mark(batch)
	}
	if plan.Helm != nil {
		mark(plan.Helm.Rendered)
	}
	if len(ids) == 0 {
		return nil
	}
	request := plan.Authorization.Request
	request.Resources = append([]ReleaseAccess(nil), request.Resources...)
	for id := range ids {
		found := false
		for i, a := range request.Resources {
			if a.ID == id {
				request.Resources[i].Actions = uniqueReleaseActions(append(append([]string(nil), a.Actions...), "get", "related-pods", releaseDaemonRevisionAction))
				found = true
			}
		}
		if !found {
			request.Resources = append(request.Resources, ReleaseAccess{ID: id, Actions: []string{"get", "related-pods", releaseDaemonRevisionAction}})
		}
	}
	auth, e := c.store.AuthorizeRelease(ctx, request)
	if e != nil {
		return e
	}
	plan.Authorization = auth
	return nil
}

func checkDaemonRevisionRead(ctx context.Context, api releaseAPI, plan ReleasePlan) error {
	seen := map[ReleaseResourceID]bool{}
	for _, scope := range plan.Authorization.Request.Resources {
		if !releaseStringSetContains(scope.Actions, []string{releaseDaemonRevisionAction}) || seen[scope.ID] {
			continue
		}
		seen[scope.ID] = true
		if session, ok := api.(*ReleaseSession); ok {
			if e := session.requireDaemonRevisionCapability(ctx); e != nil {
				return e
			}
		}
		if _, e := readReleaseObject(ctx, api, scope.ID); e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		raw, e := api.Read(ctx, "/apis/apps/v1/namespaces/"+scope.ID.Namespace+"/controllerrevisions", "limit=200")
		if e != nil {
			return e
		}
		var list map[string]any
		e = json.Unmarshal(raw, &list)
		clear(raw)
		if e != nil || kubeprotocol.BindTypedList(list, "apps/v1", "ControllerRevision") != nil {
			return ErrInvalid
		}
	}
	return nil
}
