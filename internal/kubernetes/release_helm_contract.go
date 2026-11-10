package kubernetes

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"io"
	"strconv"
	"strings"
)

type ReleaseHelmPlan struct {
	TargetRevision int               `json:"targetRevision,omitempty"`
	Rendered       []ReleaseResource `json:"rendered,omitempty"`
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	Action         string            `json:"action"`
	Revision       int               `json:"revision"`
	Hooks          bool              `json:"hooks"`
	StorageAnchor  ReleaseResourceID `json:"storageAnchor"`
	Storage        []ReleaseResource `json:"storage,omitempty"`
	Release        map[string]any    `json:"release"`
}

func releaseHelmStorage(plan ReleasePlan, id ReleaseResourceID) bool {
	if plan.Helm == nil {
		return false
	}
	anchor := plan.Helm.StorageAnchor
	if id.Group != anchor.Group || id.Version != anchor.Version || id.Resource != anchor.Resource || id.Kind != anchor.Kind || id.Namespace != anchor.Namespace {
		return false
	}
	version, ok := kubeprotocol.HelmStorageRevision(anchor.Name, id.Name)
	if !ok {
		return false
	}
	if version == plan.Helm.Revision || version == plan.Helm.Revision-1 {
		return true
	}
	if plan.Helm.Action == "rollback" {
		for _, prior := range plan.Helm.Storage {
			if prior.ID == id {
				release, e := decodeHelmRelease(prior.Object)
				return e == nil && releaseMap(release["info"])["status"] == "deployed"
			}
		}
	}
	return false
}
func releaseActionGateID(plan ReleasePlan, id ReleaseResourceID) ReleaseResourceID {
	if releaseHelmStorage(plan, id) {
		return plan.Helm.StorageAnchor
	}
	return id
}
func decodeHelmRelease(object map[string]any) (map[string]any, error) {
	data := releaseMap(object["data"])
	if len(data) != 1 || object["type"] != "helm.sh/release.v1" {
		return nil, ErrInvalid
	}
	encoded := releaseString(data["release"])
	if len(encoded) > MaxReleaseObjectBytes {
		return nil, ErrPrivateLimit
	}
	first, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil {
		return nil, ErrInvalid
	}
	defer clear(first)
	compressed, e := base64.StdEncoding.DecodeString(string(first))
	if e != nil {
		return nil, ErrInvalid
	}
	defer clear(compressed)
	raw := compressed
	if len(compressed) > 3 && bytes.Equal(compressed[:3], []byte{0x1f, 0x8b, 0x08}) {
		reader, e := gzip.NewReader(bytes.NewReader(compressed))
		if e != nil {
			return nil, ErrInvalid
		}
		raw, e = io.ReadAll(io.LimitReader(reader, MaxReleaseManifestBytes+1))
		reader.Close()
		if e != nil || len(raw) > MaxReleaseManifestBytes {
			return nil, ErrPrivateLimit
		}
		defer clear(raw)
	}
	var release map[string]any
	if json.Unmarshal(raw, &release) != nil {
		return nil, ErrInvalid
	}
	return release, nil
}
func canonicalHelmRelease(release map[string]any) map[string]any {
	out := releaseObjectCopy(release)
	info := releaseMap(out["info"])
	for _, key := range []string{"first_deployed", "last_deployed", "deleted", "status", "description"} {
		delete(info, key)
	}
	for _, hook := range releaseItems(out["hooks"]) {
		delete(releaseMap(hook), "last_run")
	}
	return out
}
func releaseHelmStorageAllowed(plan ReleasePlan, id ReleaseResourceID, object map[string]any) bool {
	if !releaseHelmStorage(plan, id) {
		return false
	}
	helm := plan.Helm
	version, _ := kubeprotocol.HelmStorageRevision(helm.StorageAnchor.Name, id.Name)
	release, e := decodeHelmRelease(object)
	if e != nil || release["name"] != helm.Name || release["namespace"] != helm.Namespace || int(releaseInt(release["version"])) != version {
		return false
	}
	status := releaseString(releaseMap(release["info"])["status"])
	switch status {
	case "pending-install", "pending-upgrade", "pending-rollback", "deployed", "failed", "superseded":
	default:
		return false
	}
	expected := helm.Release
	if version != helm.Revision {
		expected = nil
		for _, old := range helm.Storage {
			if old.ID == id {
				expected, _ = decodeHelmRelease(old.Object)
				break
			}
		}
		if expected == nil || status != "superseded" && status != "deployed" {
			return false
		}
	}
	if !ReleaseObjectMatches(canonicalHelmRelease(expected), canonicalHelmRelease(release)) || !ReleaseObjectMatches(canonicalHelmRelease(release), canonicalHelmRelease(expected)) {
		return false
	}
	meta := releaseMap(object["metadata"])
	labels := releaseMap(meta["labels"])
	if labels["name"] != helm.Name || labels["owner"] != "helm" || labels["version"] != strconv.Itoa(version) || labels["status"] != status {
		return false
	}
	for key, value := range labels {
		switch key {
		case "name", "owner", "status", "version":
		case "createdAt", "modifiedAt":
			text := releaseString(value)
			if len(text) > 20 {
				return false
			}
			if _, e := strconv.ParseInt(text, 10, 64); e != nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func validReleaseHelmHook(resource ReleaseResource) bool {
	if resource.ID.Kind != "Job" || resource.ID.Group != "batch" {
		return false
	}
	spec := releaseMap(releaseMap(releaseMap(resource.Object["spec"])["template"])["spec"])
	if spec["automountServiceAccountToken"] != false || releaseString(spec["serviceAccountName"]) == "" || spec["hostNetwork"] == true || spec["hostPID"] == true || spec["hostIPC"] == true || releaseMap(spec["securityContext"])["runAsNonRoot"] != true {
		return false
	}
	for _, volume := range releaseItems(spec["volumes"]) {
		if releaseMap(volume)["hostPath"] != nil {
			return false
		}
	}
	containers := append(append([]any(nil), releaseItems(spec["containers"])...), releaseItems(spec["initContainers"])...)
	if len(containers) == 0 {
		return false
	}
	for _, value := range containers {
		container := releaseMap(value)
		security := releaseMap(container["securityContext"])
		if !ValidImageReference(releaseString(container["image"])) || security["privileged"] == true || security["allowPrivilegeEscalation"] != false || security["readOnlyRootFilesystem"] != true || len(releaseItems(releaseMap(security["capabilities"])["add"])) > 0 {
			return false
		}
	}
	annotations := releaseMap(releaseMap(resource.Object["metadata"])["annotations"])
	policy := releaseString(annotations["helm.sh/hook-delete-policy"])
	for _, entry := range strings.Split(policy, ",") {
		if entry != "" && entry != "before-hook-creation" && entry != "hook-succeeded" && entry != "hook-failed" {
			return false
		}
	}
	return true
}
