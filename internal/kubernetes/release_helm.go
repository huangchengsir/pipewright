package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"time"
)

func (s *ReleaseSession) HelmRender(ctx context.Context, task kubeprotocol.HelmTask, chart, values []byte) ([]byte, error) {
	if e := s.Upload(ctx, "chart", chart); e != nil {
		return nil, e
	}
	task.ChartStream = "chart"
	if len(values) > 0 {
		if e := s.Upload(ctx, "values", values); e != nil {
			return nil, e
		}
		task.ValuesStream = "values"
	}
	started, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "helm-start", Helm: &task})
	if e != nil || started.Code != "ok" || started.TaskID == "" {
		return nil, ErrTool
	}
	for {
		r, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "helm-poll", StreamID: started.TaskID})
		if e != nil || r.Code != "ok" {
			return nil, ErrTool
		}
		if r.Done {
			if r.ExitCode != 0 {
				return nil, ErrTool
			}
			output, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "helm-output", StreamID: started.TaskID})
			if e != nil || output.Code != "ok" {
				return nil, ErrTool
			}
			return output.Data, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (c *ReleaseController) PrepareHelm(ctx context.Context, run, node, owner string, auth ReleaseAuthorization, namespace, name string, chart, values []byte, hooks bool, config []byte, timeout, observe int) (out ReleaseRecord, resultErr error) {
	session, e := c.runtime.OpenReleasePreview(ctx, auth, owner)
	if e != nil {
		return out, e
	}
	defer func() {
		if session != nil {
			_, e := session.Close(context.WithoutCancel(ctx))
			resultErr = errors.Join(resultErr, e)
		}
	}()
	if e := session.requireHelmCapabilities(ctx, hooks); e != nil {
		return out, e
	}
	storageID := ReleaseResourceID{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: namespace, Name: "sh.helm.release.v1." + name}
	raw, e := session.Read(ctx, "/api/v1/namespaces/"+namespace+"/secrets", "labelSelector=owner%3Dhelm%2Cname%3D"+name+"&limit=200")
	if e != nil {
		return out, e
	}
	defer clear(raw)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil || len(list.Items) > 128 {
		return out, ErrPrivateLimit
	}
	helm := &ReleaseHelmPlan{Name: name, Namespace: namespace, Action: "install", Hooks: hooks, StorageAnchor: storageID}
	latest := 0
	for _, item := range list.Items {
		id := releaseString(releaseMap(item["metadata"])["name"])
		version, valid := kubeprotocol.HelmStorageRevision(storageID.Name, id)
		if !valid {
			return out, ErrDenied
		}
		if version > latest {
			latest = version
		}
		copyID := storageID
		copyID.Name = id
		helm.Storage = append(helm.Storage, releaseResourceFromObject(copyID, item, "declaration"))
	}
	if latest > 0 {
		helm.Action = "upgrade"
	}
	helm.Revision = latest + 1
	if helm.Revision > 10000 {
		return out, ErrPrivateLimit
	}
	rendered, e := session.HelmRender(ctx, kubeprotocol.HelmTask{Action: "plan-" + helm.Action, Name: name, Namespace: namespace, Hooks: hooks, TimeoutSeconds: timeout}, chart, values)
	if e != nil {
		return out, e
	}
	defer clear(rendered)
	var release map[string]any
	if json.Unmarshal(rendered, &release) != nil || release["name"] != name || release["namespace"] != namespace || int(releaseInt(release["version"])) != helm.Revision {
		return out, ErrTool
	}
	helm.Release = release
	manifest := releaseString(release["manifest"])
	if hooks {
		for _, value := range releaseItems(release["hooks"]) {
			hook := releaseMap(value)
			hookManifest := releaseString(hook["manifest"])
			if hookManifest == "" {
				return out, ErrInvalid
			}
			manifest += "\n---\n" + hookManifest
		}
	}
	files, e := InlineReleaseFile(manifest)
	if e != nil {
		return out, e
	}
	if _, e = session.Close(context.WithoutCancel(ctx)); e != nil {
		return out, e
	}
	session = nil
	return c.prepareManifest(ctx, run, node, owner, auth, files, nil, config, timeout, observe, helm, chart, values)
}
