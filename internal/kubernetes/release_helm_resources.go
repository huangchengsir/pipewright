package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"path"
	"sort"
	"strings"
)

// ReadReleaseChartCRDs reads only committed, packaged crds/ documents (including
// packaged dependencies). It never templates CRDs or fetches dependencies.
func ReadReleaseChartCRDs(data []byte) ([]ReleaseInputFile, error) {
	files := []ReleaseInputFile{}
	budget := int64(0)
	entries := 0
	var walk func([]byte, string, int) error
	walk = func(data []byte, prefix string, depth int) error {
		if depth > 8 || len(data) > MaxReleaseInputBytes {
			return ErrPrivateLimit
		}
		z, e := gzip.NewReader(bytes.NewReader(data))
		if e != nil {
			return ErrManifest
		}
		defer z.Close()
		r := tar.NewReader(z)
		seen := map[string]bool{}
		for {
			h, e := r.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return ErrManifest
			}
			entries++
			name := strings.TrimSuffix(h.Name, "/")
			if entries > 4096 || h.Size < 0 || h.Size > MaxReleaseObjectBytes || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") || seen[name] {
				return ErrManifest
			}
			seen[name] = true
			if h.Typeflag == tar.TypeDir {
				continue
			}
			if h.Typeflag != tar.TypeReg {
				return ErrManifest
			}
			budget += h.Size
			if budget > MaxReleaseInputBytes {
				return ErrPrivateLimit
			}
			body, e := io.ReadAll(io.LimitReader(r, h.Size+1))
			if e != nil || int64(len(body)) != h.Size {
				return ErrManifest
			}
			if strings.HasSuffix(name, ".tgz") {
				if e = walk(body, prefix+name+"/", depth+1); e != nil {
					return e
				}
				continue
			}
			parts := strings.Split(name, "/")
			inCRD := false
			if len(parts) > 2 {
				for _, part := range parts[1 : len(parts)-1] {
					if part == "crds" {
						inCRD = true
					}
				}
			}
			ext := strings.ToLower(path.Ext(name))
			if inCRD && (ext == ".yaml" || ext == ".yml" || ext == ".json") {
				files = append(files, ReleaseInputFile{Path: prefix + name, Content: body, Digest: releaseByteDigest(body)})
				if len(files) > MaxReleaseResources {
					return ErrPrivateLimit
				}
			}
		}
		return nil
	}
	if e := walk(data, "", 0); e != nil {
		return nil, e
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}
func (c *ReleaseController) appendHelmDeletions(ctx context.Context, api releaseAPI, d *ReleaseDiscovery, plan *ReleasePlan) error {
	helm := plan.Helm
	if helm == nil || helm.Action == "install" {
		return nil
	}
	var previous map[string]any
	for _, storage := range helm.Storage {
		value, e := decodeHelmRelease(storage.Object)
		if e != nil {
			return e
		}
		if int(releaseInt(value["version"])) == helm.Revision-1 {
			previous = value
		}
	}
	if previous == nil {
		return ErrRollbackBaseline
	}
	text := releaseString(previous["manifest"])
	if strings.TrimSpace(text) == "" {
		return nil
	}
	files, e := InlineReleaseFile(text)
	if e != nil {
		return e
	}
	old, e := freezeReleaseManifest(files, nil, d, helm.Namespace, false)
	if e != nil {
		return e
	}
	for _, resource := range old.Resources {
		found := false
		for _, target := range plan.Resources {
			if target.ID == resource.ID {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if releaseMap(releaseMap(resource.Object["metadata"])["annotations"])["helm.sh/resource-policy"] == "keep" {
			plan.Retained = append(plan.Retained, ReleaseRollbackRetention{Resource: resource.ID, Reason: "helm_resource_policy_keep"})
			continue
		}
		current, e := readReleaseObject(ctx, api, resource.ID)
		if e != nil {
			return e
		}
		ann := releaseMap(releaseMap(current["metadata"])["annotations"])
		if ann["meta.helm.sh/release-name"] != helm.Name || ann["meta.helm.sh/release-namespace"] != helm.Namespace {
			return ErrSuperseded
		}
		target := releaseResourceFromObject(resource.ID, releaseCanonicalObject(current), resource.Health)
		target.Delete = true
		target.Phase = 100 - resource.Phase
		plan.Resources = append(plan.Resources, target)
		plan.Deletes = append(plan.Deletes, resource.ID)
		plan.ForceApproval = true
	}
	return nil
}
