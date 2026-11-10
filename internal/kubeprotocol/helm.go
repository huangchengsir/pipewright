package kubeprotocol

import (
	"strconv"
	"strings"
)

// HelmStorageRevision expands only the separately granted release-storage
// capability. Ordinary Secret get/create permissions remain exact names.
func HelmStorageRevision(anchor, name string) (int, bool) {
	if !strings.HasPrefix(anchor, "sh.helm.release.v1.") || anchor == "sh.helm.release.v1." || !strings.HasPrefix(name, anchor+".v") {
		return 0, false
	}
	suffix := strings.TrimPrefix(name, anchor+".v")
	version, e := strconv.Atoi(suffix)
	return version, e == nil && version > 0 && version <= 10000 && strconv.Itoa(version) == suffix
}
func HelmStorageResource(scope Resource, name string) bool {
	if scope.Group != "" || scope.Version != "v1" || scope.Resource != "secrets" || scope.Kind != "Secret" {
		return false
	}
	explicit := false
	for _, action := range scope.Actions {
		explicit = explicit || action == "helm-storage"
	}
	_, ok := HelmStorageRevision(scope.Name, name)
	return explicit && ok
}
