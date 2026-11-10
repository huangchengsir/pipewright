package kubernetes

import "testing"

func TestReview107SAVolumeRenamingRetainsWholeIntent(t *testing.T) {
	source := releaseTestObject(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"app","namespace":"app"},"spec":{"containers":[{"name":"app","image":"approved","command":["one","two"],"args":["a","b"],"env":[{"name":"A","value":"seed"},{"name":"B","value":"$(A)"}],"envFrom":[{"configMapRef":{"name":"one"}},{"secretRef":{"name":"two"}}],"volumeMounts":[{"name":"data","mountPath":"/data"}]}],"initContainers":[{"name":"first","image":"init"},{"name":"second","image":"init"}],"volumes":[{"name":"data","projected":{"sources":[{"configMap":{"name":"user-one"}},{"secret":{"name":"user-two"}}]}}]}}`)
	for _, scenario := range []string{"valid", "audience-bound", "duration-bound", "default-mode-bound", "token-path", "expiration", "audience", "default-mode", "root-ca", "downward-path", "source-order", "extra-source", "mount-path", "mount-readonly", "mount-subpath", "missing-init-mount", "missing-link", "collision", "ambiguous", "invalid-name", "declared-generated", "automount-disabled", "declared-volume", "command-order", "args-order", "env-order", "init-order", "envFrom-order", "user-projected-order"} {
		t.Run(scenario, func(t *testing.T) {
			original := releaseObjectCopy(source)
			dry := review107AdmitPod(source, "kube-api-access-dry01")
			live := review107AdmitPod(source, "kube-api-access-live1")
			volume := releaseMap(specVolume107(live)[1])
			projection := releaseMap(volume["projected"])
			sources := releaseItems(projection["sources"])
			token := releaseMap(releaseMap(sources[0])["serviceAccountToken"])
			container := releaseMap(releaseItems(releaseMap(live["spec"])["containers"])[0])
			mounts := releaseItems(container["volumeMounts"])
			good := scenario == "valid" || scenario == "audience-bound" || scenario == "duration-bound" || scenario == "default-mode-bound"
			switch scenario {
			case "audience-bound":
				token["audience"] = "approved"
				releaseMap(releaseMap(releaseItems(releaseMap(releaseMap(specVolume107(dry)[1])["projected"])["sources"])[0])["serviceAccountToken"])["audience"] = "approved"
			case "duration-bound":
				token["expirationSeconds"] = 7200
				releaseMap(releaseMap(releaseItems(releaseMap(releaseMap(specVolume107(dry)[1])["projected"])["sources"])[0])["serviceAccountToken"])["expirationSeconds"] = 7200
			case "default-mode-bound":
				projection["defaultMode"] = 384
				releaseMap(releaseMap(specVolume107(dry)[1])["projected"])["defaultMode"] = 384
			case "token-path":
				token["path"] = "different"
			case "expiration":
				token["expirationSeconds"] = 7200
			case "audience":
				token["audience"] = "unapproved"
			case "default-mode":
				projection["defaultMode"] = 384
			case "root-ca":
				releaseMap(releaseMap(sources[1])["configMap"])["name"] = "other-ca"
			case "downward-path":
				releaseMap(releaseItems(releaseMap(releaseMap(sources[2])["downwardAPI"])["items"])[0])["path"] = "other"
			case "source-order":
				sources[0], sources[1] = sources[1], sources[0]
			case "extra-source":
				projection["sources"] = append(sources, map[string]any{"secret": map[string]any{"name": "foreign"}})
			case "mount-path":
				releaseMap(mounts[1])["mountPath"] = "/unapproved"
			case "mount-readonly":
				releaseMap(mounts[1])["readOnly"] = false
			case "mount-subpath":
				releaseMap(mounts[1])["subPath"] = "token"
			case "missing-init-mount":
				delete(releaseMap(releaseItems(releaseMap(live["spec"])["initContainers"])[0]), "volumeMounts")
			case "missing-link":
				releaseMap(mounts[1])["name"] = "unlinked"
			case "collision":
				releaseMap(specVolume107(live)[0])["name"] = "kube-api-access-live1"
			case "ambiguous":
				extra := releaseObjectCopy(volume)
				extra["name"] = "kube-api-access-live2"
				releaseMap(live["spec"])["volumes"] = append(specVolume107(live), extra)
			case "invalid-name":
				volume["name"] = "kube-api-access-arbitrary-long"
			case "declared-generated":
				original = releaseObjectCopy(dry)
			case "automount-disabled":
				releaseMap(original["spec"])["automountServiceAccountToken"] = false
			case "declared-volume":
				items := releaseItems(releaseMap(releaseMap(specVolume107(live)[0])["projected"])["sources"])
				items[0], items[1] = items[1], items[0]
			case "command-order":
				container["command"] = []any{"two", "one"}
			case "args-order":
				container["args"] = []any{"b", "a"}
			case "env-order":
				items := releaseItems(container["env"])
				items[0], items[1] = items[1], items[0]
			case "init-order":
				items := releaseItems(releaseMap(live["spec"])["initContainers"])
				items[0], items[1] = items[1], items[0]
			case "envFrom-order":
				items := releaseItems(container["envFrom"])
				items[0], items[1] = items[1], items[0]
			case "user-projected-order":
				items := releaseItems(releaseMap(releaseMap(specVolume107(live)[0])["projected"])["sources"])
				items[0], items[1] = items[1], items[0]
			}
			resource := ReleaseResource{ID: ReleaseResourceID{Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "app", Name: "app"}, Object: original, Health: "workload"}
			resource.Expected = releaseAdmittedExpected(resource, dry)
			if releaseResourceExpectedMatches(resource, live) != good {
				t.Fatal("SA identity exception changed an unapproved field", scenario)
			}
		})
	}
}
