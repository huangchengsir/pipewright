package kubeagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

type cappedOutput struct {
	data     []byte
	limit    int
	exceeded bool
}

func (w *cappedOutput) Write(b []byte) (int, error) {
	n := len(b)
	left := w.limit - len(w.data)
	if left < len(b) {
		w.exceeded = true
		b = b[:max(0, left)]
	}
	w.data = append(w.data, b...)
	return n, nil
}
func validChartArchive(data []byte, depth int) bool {
	budget := int64(0)
	entries := 0
	return validChartArchiveBudget(data, depth, &budget, &entries)
}
func validChartArchiveBudget(data []byte, depth int, budget *int64, entries *int) bool {
	if depth > 8 || len(data) > kubeprotocol.MaxInputBytes {
		return false
	}
	z, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return false
	}
	defer z.Close()
	reader := tar.NewReader(io.LimitReader(z, kubeprotocol.MaxInputBytes+1))
	total, count, metadata := int64(0), 0, false
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return false
		}
		count++
		*entries++
		name := header.Name
		if count > 4096 || *entries > 4096 || header.Size < 0 || header.Size > 1<<20 || path.IsAbs(name) || path.Clean(name) != strings.TrimSuffix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00:") {
			return false
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return false
		}
		total += header.Size
		*budget += header.Size
		if total > kubeprotocol.MaxInputBytes || *budget > kubeprotocol.MaxInputBytes {
			return false
		}
		body, e := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if e != nil || int64(len(body)) != header.Size {
			return false
		}
		if strings.HasSuffix(name, "/Chart.yaml") {
			metadata = true
		}
		if strings.HasSuffix(name, ".tgz") && !validChartArchiveBudget(body, depth+1, budget, entries) {
			return false
		}
		if strings.HasSuffix(name, "values.schema.json") {
			var schema any
			if json.Unmarshal(body, &schema) != nil || !localSchemaReferences(schema) {
				return false
			}
		}
	}
	return metadata
}
func localSchemaReferences(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "$ref" {
				s, ok := v.(string)
				if !ok || !strings.HasPrefix(s, "#") {
					return false
				}
			}
			if !localSchemaReferences(v) {
				return false
			}
		}
	case []any:
		for _, v := range x {
			if !localSchemaReferences(v) {
				return false
			}
		}
	}
	return true
}
func (a *Agent) startHelm(ctx context.Context, in kubeprotocol.HelmTask) kubeprotocol.Reply {
	if !namePattern.MatchString(in.Name) || len(in.Name) > 53 || !namePattern.MatchString(in.Namespace) || len(in.Namespace) > 63 || in.TimeoutSeconds < 30 || in.TimeoutSeconds > 1800 {
		return reply("helm_rejected")
	}
	planning := in.Action == "plan-install" || in.Action == "plan-upgrade"
	if planning {
		in.Action = strings.TrimPrefix(in.Action, "plan-")
	}
	switch in.Action {
	case "template", "install", "upgrade", "status", "history", "rollback":
	default:
		return reply("helm_rejected")
	}
	if in.Action == "rollback" && in.Revision < 1 {
		return reply("helm_revision_rejected")
	}
	a.mu.Lock()
	if !a.initialized || a.helmPath == "" || a.helmVersion != "v4.3.1" || a.gateway == "" {
		a.mu.Unlock()
		return reply("helm_unavailable")
	}
	for _, t := range a.tasks {
		if !t.done {
			a.mu.Unlock()
			return reply("helm_task_active")
		}
	}
	helmPath, gateway, version := a.helmPath, a.gateway, a.helmVersion
	chart, values := []byte(nil), []byte(nil)
	if in.Action == "template" || in.Action == "install" || in.Action == "upgrade" {
		u := a.uploads[in.ChartStream]
		if u == nil || !u.done || !validChartArchive(u.body, 0) {
			a.mu.Unlock()
			return reply("helm_chart_rejected")
		}
		chart = append([]byte(nil), u.body...)
		if in.ValuesStream != "" {
			u = a.uploads[in.ValuesStream]
			if u == nil || !u.done {
				a.mu.Unlock()
				return reply("helm_values_rejected")
			}
			values = append([]byte(nil), u.body...)
		}
	}
	id := randomID()
	a.tasks[id] = &taskState{}
	a.mu.Unlock()
	go func() {
		exit, revision := 1, 0
		defer func() {
			a.mu.Lock()
			a.planningDiscovery = nil
			a.mu.Unlock()
			clear(chart)
			clear(values)
			a.mu.Lock()
			a.tasks[id].done = true
			a.tasks[id].exit = exit
			a.tasks[id].revision = revision
			a.mu.Unlock()
		}()
		work, cancel := context.WithTimeout(context.Background(), time.Duration(in.TimeoutSeconds)*time.Second)
		defer cancel()
		environment := []string{"HOME=/run/pipewright", "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "KUBECONFIG=/run/pipewright/gateway-kubeconfig", "HELM_CACHE_HOME=/run/pipewright/helm-cache", "HELM_CONFIG_HOME=/run/pipewright/helm-config", "HELM_DATA_HOME=/run/pipewright/helm-data", "HELM_PLUGINS=/run/pipewright/no-plugins", "HELM_DRIVER=secret", "GODEBUG=http2client=0", helmWatchListEnvironment}
		versionCmd := exec.CommandContext(work, helmPath, "version", "--template={{.Version}}")
		versionCmd.Env = environment
		var checked cappedOutput
		checked.limit = 256
		versionCmd.Stdout = &checked
		versionCmd.Stderr = io.Discard
		if versionCmd.Run() != nil || checked.exceeded || strings.TrimSpace(string(checked.data)) != version {
			return
		}
		if planning {
			catalog, e := a.chartDiscovery(work, chart)
			if e != nil {
				return
			}
			a.mu.Lock()
			a.planningDiscovery = catalog
			a.mu.Unlock()
		}
		config := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": "guarded", "clusters": []any{map[string]any{"name": "gateway", "cluster": map[string]any{"server": gateway}}}, "users": []any{map[string]any{"name": "none", "user": map[string]any{}}}, "contexts": []any{map[string]any{"name": "guarded", "context": map[string]any{"cluster": "gateway", "user": "none", "namespace": in.Namespace}}}}
		body, _ := json.Marshal(config)
		if os.WriteFile("/run/pipewright/gateway-kubeconfig", body, 0600) != nil {
			return
		}
		chartPath := "/run/pipewright/chart-" + id + ".tgz"
		valuesPath := "/run/pipewright/values-" + id + ".yaml"
		if len(chart) > 0 {
			if os.WriteFile(chartPath, chart, 0600) != nil {
				return
			}
			defer os.Remove(chartPath)
		}
		if len(values) > 0 {
			if os.WriteFile(valuesPath, values, 0600) != nil {
				return
			}
			defer os.Remove(valuesPath)
		}
		valuesArg := ""
		if len(values) > 0 {
			valuesArg = valuesPath
		}
		args := helmCommandArgs(in, planning, chartPath, valuesArg, "/run/pipewright/gateway-kubeconfig")
		command := exec.CommandContext(work, helmPath, args...)
		command.Env = environment
		output := &cappedOutput{limit: kubeprotocol.MaxResponseBytes}
		command.Stdout = output
		command.Stderr = io.Discard
		e := command.Run()
		if e != nil || output.exceeded {
			clear(output.data)
			return
		}
		if in.Action == "rollback" {
			clear(output.data)
			output = &cappedOutput{limit: kubeprotocol.MaxResponseBytes}
			status := exec.CommandContext(work, helmPath, "status", in.Name, "--namespace", in.Namespace, "--kubeconfig=/run/pipewright/gateway-kubeconfig", "--output=json")
			status.Env = environment
			status.Stdout = output
			status.Stderr = io.Discard
			if status.Run() != nil || output.exceeded {
				clear(output.data)
				return
			}
		}
		if in.Action == "template" || planning {
			a.mu.Lock()
			a.uploads[id] = &upload{body: append([]byte(nil), output.data...), size: len(output.data), done: true, digest: privateDigest(output.data)}
			a.mu.Unlock()
		} else {
			var release struct {
				Version int `json:"version"`
			}
			if json.Unmarshal(output.data, &release) == nil {
				revision = release.Version
			}
		}
		clear(output.data)
		exit = 0
	}()
	r := reply("ok")
	r.TaskID = id
	return r
}

// helmCommandArgs is shared by planning and execution; paths are host-selected
// private tmpfs files, never arguments supplied by a chart.
func helmCommandArgs(in kubeprotocol.HelmTask, planning bool, chartPath, valuesPath, kubeconfigPath string) []string {
	args := []string{in.Action, in.Name, "--namespace", in.Namespace, "--kubeconfig=" + kubeconfigPath}
	if in.Action == "template" || in.Action == "install" || in.Action == "upgrade" {
		args = append(args, chartPath)
		if valuesPath != "" {
			args = append(args, "--values", valuesPath)
		}
		if !in.Hooks {
			args = append(args, "--no-hooks")
		}
		if in.Action != "template" {
			// The private gateway deliberately returns bounded JSON. client-go's
			// client OpenAPI v2 decoder requires protobuf, so disable only this
			// incompatible local validation. Host Strict dry-runs and exact
			// frozen request/permit checks still gate every business mutation.
			args = append(args, "--disable-openapi-validation", "--skip-crds", "--wait=hookOnly", "--timeout", strconv.Itoa(in.TimeoutSeconds)+"s", "--output=json")
		}
		if in.Action == "upgrade" {
			args = append(args, "--server-side=true")
			args = append(args, "--history-max=0")
		}
	}
	if in.Action == "status" || in.Action == "history" {
		args = append(args, "--output=json")
	}
	if in.Action == "rollback" {
		args = append(args, strconv.Itoa(in.Revision), "--wait=hookOnly", "--timeout", strconv.Itoa(in.TimeoutSeconds)+"s", "--history-max=0", "--server-side=true")
		if !in.Hooks {
			args = append(args, "--no-hooks")
		}
	}
	if planning {
		args = append(args, "--dry-run=server")
	}
	return args
}
