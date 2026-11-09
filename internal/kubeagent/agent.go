// Package kubeagent implements the private tool's credential holder and guarded
// API client. It never logs credentials, Kubernetes objects or CLI output.
package kubeagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"gopkg.in/yaml.v3"
)

var errDenied = errors.New("kube_tool_request_denied")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

type upload struct {
	parts  [][]byte
	size   int
	digest string
	next   int
	done   bool
	body   []byte
}
type writeRequest struct {
	public    kubeprotocol.PendingWrite
	decision  chan kubeprotocol.Permit
	permitted bool
}
type taskState struct {
	done     bool
	exit     int
	revision int
}
type Agent struct {
	mu                             sync.Mutex
	writeMu                        sync.Mutex
	initialized                    bool
	client                         *http.Client
	endpoint                       *url.URL
	token                          string
	resources                      []kubeprotocol.Resource
	cache                          map[string]map[string]any
	uploads                        map[string]*upload
	downloads                      map[string][]byte
	pending                        map[string]*writeRequest
	pendingOrder                   []string
	results                        []kubeprotocol.APIResult
	consumed                       map[string]bool
	tasks                          map[string]*taskState
	helmPath, helmVersion, gateway string
	planningDiscovery              map[string][]map[string]any
	deadline                       time.Time
}

func New() *Agent {
	return &Agent{cache: map[string]map[string]any{}, uploads: map[string]*upload{}, downloads: map[string][]byte{}, pending: map[string]*writeRequest{}, consumed: map[string]bool{}, tasks: map[string]*taskState{}}
}
func randomID() string              { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func privateDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func reply(code string) kubeprotocol.Reply {
	return kubeprotocol.Reply{Version: kubeprotocol.Version, Code: code}
}
func decodeBounded(r io.Reader, out any, max int64) error {
	b, e := io.ReadAll(io.LimitReader(r, max+1))
	if e != nil || len(b) > int(max) {
		return errDenied
	}
	defer clear(b)
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return errDenied
	}
	return nil
}
func (a *Agent) Initialize(in kubeprotocol.Init) error {
	if len(in.Kubeconfig) == 0 || len(in.Kubeconfig) > 32<<10 || len(in.Resources) == 0 || len(in.Resources) > 512 || in.Deadline <= time.Now().UnixMilli() || in.Deadline > time.Now().Add(30*time.Minute).UnixMilli() {
		return errDenied
	}
	var cfg struct {
		Clusters []struct {
			Name    string `yaml:"name"`
			Cluster struct {
				Server  string `yaml:"server"`
				CA      string `yaml:"certificate-authority-data"`
				TLSName string `yaml:"tls-server-name"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			Name string `yaml:"name"`
			User struct {
				Token string `yaml:"token"`
				Cert  string `yaml:"client-certificate-data"`
				Key   string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
		Contexts []struct {
			Name    string `yaml:"name"`
			Context struct {
				Cluster string `yaml:"cluster"`
				User    string `yaml:"user"`
			} `yaml:"context"`
		} `yaml:"contexts"`
		Current string `yaml:"current-context"`
	}
	if yaml.Unmarshal(in.Kubeconfig, &cfg) != nil {
		return errDenied
	}
	clusterName, userName := "", ""
	for _, c := range cfg.Contexts {
		if c.Name == cfg.Current {
			clusterName = c.Context.Cluster
			userName = c.Context.User
		}
	}
	if clusterName == "" || userName == "" {
		return errDenied
	}
	server, ca, tlsName, token, cert, key := "", "", "", "", "", ""
	for _, c := range cfg.Clusters {
		if c.Name == clusterName {
			server = c.Cluster.Server
			ca = c.Cluster.CA
			tlsName = c.Cluster.TLSName
		}
	}
	for _, u := range cfg.Users {
		if u.Name == userName {
			token = u.User.Token
			cert = u.User.Cert
			key = u.User.Key
		}
	}
	endpoint, e := url.Parse(server)
	if e != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Hostname() == "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errDenied
	}
	certCA, e := base64.StdEncoding.DecodeString(ca)
	if e != nil {
		return errDenied
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certCA) {
		return errDenied
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: tlsName}
	if cert != "" || key != "" {
		c, e := base64.StdEncoding.DecodeString(cert)
		if e != nil {
			return errDenied
		}
		k, e := base64.StdEncoding.DecodeString(key)
		if e != nil {
			return errDenied
		}
		pair, e := tls.X509KeyPair(c, k)
		clear(k)
		if e != nil {
			return errDenied
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	if token == "" && len(tlsConfig.Certificates) == 0 {
		return errDenied
	}
	for _, s := range in.Resources {
		if has(s.Actions, "related-controllerrevisions") && (s.Group != "apps" || s.Version != "v1" || s.Resource != "daemonsets" || s.Kind != "DaemonSet" || s.Namespace == "") {
			return errDenied
		}
		if !namePattern.MatchString(s.Resource) || !namePattern.MatchString(s.Name) || strings.ContainsAny(s.Group+s.Version+s.Namespace, "\\/?%\x00") || s.Version == "" {
			return errDenied
		}
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, ResponseHeaderTimeout: 10 * time.Second, DisableCompression: true}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.initialized {
		return errDenied
	}
	a.endpoint = endpoint
	a.client = client
	a.token = token
	a.resources = append([]kubeprotocol.Resource(nil), in.Resources...)
	a.helmPath = in.HelmPath
	a.helmVersion = in.HelmVersion
	a.deadline = time.UnixMilli(in.Deadline)
	a.initialized = true
	return nil
}
func resourcePath(r kubeprotocol.Resource, collection bool) string {
	prefix := "/api/" + r.Version
	if r.Group != "" {
		prefix = "/apis/" + r.Group + "/" + r.Version
	}
	if r.Namespace != "" {
		prefix += "/namespaces/" + r.Namespace
	}
	prefix += "/" + r.Resource
	if !collection {
		prefix += "/" + r.Name
	}
	return prefix
}
func has(actions []string, want string) bool {
	for _, s := range actions {
		if s == want {
			return true
		}
	}
	return false
}
func mapValue(v any) map[string]any { m, _ := v.(map[string]any); return m }
func stringValue(v any) string      { s, _ := v.(string); return s }
func listValue(v any) []any         { s, _ := v.([]any); return s }
func ownerUID(m map[string]any) string {
	for _, v := range listValue(mapValue(m["metadata"])["ownerReferences"]) {
		ref := mapValue(v)
		if ref["controller"] == true {
			return stringValue(ref["uid"])
		}
	}
	return ""
}
func (a *Agent) readPermission(path string) bool {
	if path == "/version" || path == "/api" || path == "/apis" || path == "/openapi/v3" || path == "/openapi/v2" {
		return true
	}
	for _, s := range a.resources {
		base := "/api/" + s.Version
		if s.Group != "" {
			base = "/apis/" + s.Group + "/" + s.Version
		}
		if path == base || path == "/openapi/v3"+base || (s.Group != "" && path == "/apis/"+s.Group) {
			return true
		}
		if has(s.Actions, "get") && strings.HasPrefix(path, resourcePath(s, true)+"/") && kubeprotocol.HelmStorageResource(s, strings.TrimPrefix(path, resourcePath(s, true)+"/")) {
			return true
		}
		if resourcePath(s, false) == path && has(s.Actions, "get") {
			return true
		}
	}
	return false
}
func (a *Agent) collectionPermission(path string) bool {
	_, ok := a.collectionResource(path)
	return ok
}

// collectionResource binds response TypeMeta to the same closed GVR capabilities
// that authorize the collection. Conflicting scopes cannot choose a response GVK.
func (a *Agent) collectionResource(path string) (kubeprotocol.Resource, bool) {
	var bound kubeprotocol.Resource
	found := false
	for _, s := range a.resources {
		var candidate kubeprotocol.Resource
		switch {
		case resourcePath(s, true) == path && has(s.Actions, "get"):
			candidate = s
		case s.Namespace == "":
			continue
		case path == "/apis/autoscaling/v2/namespaces/"+s.Namespace+"/horizontalpodautoscalers" && has(s.Actions, "related-hpas") && s.Group == "apps" && s.Kind == "Deployment":
			candidate = kubeprotocol.Resource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler"}
		case path == "/api/v1/namespaces/"+s.Namespace+"/pods" && has(s.Actions, "related-pods") && (s.Kind == "Deployment" || s.Kind == "StatefulSet" || s.Kind == "DaemonSet" || s.Kind == "Job" || s.Kind == "CronJob"):
			candidate = kubeprotocol.Resource{Version: "v1", Resource: "pods", Kind: "Pod"}
		case path == "/apis/apps/v1/namespaces/"+s.Namespace+"/controllerrevisions" && has(s.Actions, "related-controllerrevisions") && s.Group == "apps" && s.Version == "v1" && s.Resource == "daemonsets" && s.Kind == "DaemonSet":
			candidate = kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "controllerrevisions", Kind: "ControllerRevision"}
		case path == "/apis/apps/v1/namespaces/"+s.Namespace+"/replicasets" && has(s.Actions, "related-replicasets") && s.Group == "apps" && s.Kind == "Deployment":
			candidate = kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "replicasets", Kind: "ReplicaSet"}
		case path == "/apis/discovery.k8s.io/v1/namespaces/"+s.Namespace+"/endpointslices" && has(s.Actions, "related-endpointslices") && s.Group == "" && s.Kind == "Service":
			candidate = kubeprotocol.Resource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices", Kind: "EndpointSlice"}
		case path == "/apis/batch/v1/namespaces/"+s.Namespace+"/jobs" && has(s.Actions, "related-jobs") && s.Group == "batch" && s.Kind == "CronJob":
			candidate = kubeprotocol.Resource{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job"}
		default:
			continue
		}
		if candidate.Kind == "" || candidate.Version == "" || (found && (bound.Group != candidate.Group || bound.Version != candidate.Version || bound.Resource != candidate.Resource || bound.Kind != candidate.Kind)) {
			return kubeprotocol.Resource{}, false
		}
		bound, found = candidate, true
	}
	return bound, found
}
func collectionAPIVersion(r kubeprotocol.Resource) string {
	if r.Group != "" {
		return r.Group + "/" + r.Version
	}
	return r.Version
}
func (a *Agent) filterCollection(path string, body []byte) ([]byte, error) {
	var list map[string]any
	bound, allowed := a.collectionResource(path)
	if !allowed || json.Unmarshal(body, &list) != nil || kubeprotocol.BindTypedList(list, collectionAPIVersion(bound), bound.Kind) != nil {
		return nil, errDenied
	}
	items := listValue(list["items"])
	if len(items) > 4096 {
		return nil, errDenied
	}
	keep := []any{}
	for _, v := range items {
		obj := mapValue(v)
		meta := mapValue(obj["metadata"])
		name, ns, kind := stringValue(meta["name"]), stringValue(meta["namespace"]), stringValue(obj["kind"])
		allowed := false
		for _, s := range a.resources {
			if resourcePath(s, true) == path && s.Namespace == ns && has(s.Actions, "get") && kubeprotocol.HelmStorageResource(s, name) {
				allowed = true
				break
			}
			if resourcePath(s, true) == path && s.Name == name && s.Namespace == ns && has(s.Actions, "get") {
				allowed = true
				break
			}
			if s.Namespace != ns {
				continue
			}
			if kind == "HorizontalPodAutoscaler" && s.Group == "apps" && s.Kind == "Deployment" && has(s.Actions, "related-hpas") {
				target := mapValue(mapValue(obj["spec"])["scaleTargetRef"])
				if target["apiVersion"] == "apps/v1" && target["kind"] == "Deployment" && target["name"] == s.Name {
					allowed = true
					break
				}
			}
			anchor := a.cache[resourcePath(s, false)]
			if anchor == nil || s.Namespace != ns {
				continue
			}

			action := ""
			switch kind {
			case "Pod":
				action = "related-pods"
			case "ControllerRevision":
				action = "related-controllerrevisions"
				if !daemonRevisionOwner(s, anchor, obj) {
					continue
				}
			case "ReplicaSet":
				action = "related-replicasets"
			case "Job":
				action = "related-jobs"
			case "EndpointSlice":
				action = "related-endpointslices"
			}
			if action == "" || !has(s.Actions, action) {
				continue
			}
			uid := ownerUID(obj)
			root := stringValue(mapValue(anchor["metadata"])["uid"])
			if uid == root && !(kind == "Pod" && (s.Kind == "Deployment" || s.Kind == "CronJob")) {
				allowed = true
				break
			}
			if kind == "Pod" {
				for _, parent := range a.cache {
					if stringValue(mapValue(parent["metadata"])["uid"]) == uid && ownerUID(parent) == root && (parent["kind"] == "ReplicaSet" && s.Kind == "Deployment" || parent["kind"] == "Job" && s.Kind == "CronJob") {
						allowed = true
						break
					}
				}
			}
		}
		if allowed {
			keep = append(keep, obj)
			a.cache[path+"/"+name] = obj
		}
	}
	list["items"] = keep
	meta := mapValue(list["metadata"])
	delete(meta, "continue")
	delete(meta, "remainingItemCount")
	return json.Marshal(list)
}
func (a *Agent) Do(ctx context.Context, in kubeprotocol.Request) (kubeprotocol.APIResult, error) {
	return a.do(ctx, in, 5)
}

func (a *Agent) do(ctx context.Context, in kubeprotocol.Request, watchSeconds int) (kubeprotocol.APIResult, error) {
	result := kubeprotocol.APIResult{}
	a.mu.Lock()
	if !a.initialized || time.Now().After(a.deadline) {
		a.mu.Unlock()
		return result, errDenied
	}
	if !strings.HasPrefix(in.Path, "/") || strings.ContainsAny(in.Path, "?%\\\x00") || strings.Contains(in.Path, "//") || strings.Contains(in.Path, "/../") || len(in.Body) > 1<<20 || in.ContentType == helmStorageProtobufType {
		a.mu.Unlock()
		return result, errDenied
	}
	query, e := url.ParseQuery(in.Query)
	if e != nil || validateTransportTimeout(query) != nil {
		a.mu.Unlock()
		return result, errDenied
	}
	watching := query.Get("watch") == "true"
	watchScope := kubeprotocol.Resource{}
	if query.Get("watch") != "" && query.Get("watch") != "false" {
		var allowed bool
		watchScope, allowed = a.watchPermission(in.Path, query)
		if !watching || !allowed {
			a.mu.Unlock()
			return result, errDenied
		}
		query.Set("timeoutSeconds", strconv.Itoa(watchSeconds))
	}
	for key := range query {
		switch key {
		case "labelSelector", "fieldSelector", "limit", "continue", "resourceVersion", "timeoutSeconds", "timeout", "fieldManager", "fieldValidation", "dryRun", "force", "hash", "watch", "allowWatchBookmarks":
		default:
			a.mu.Unlock()
			return result, errDenied
		}
	}
	if query.Get("force") == "true" {
		a.mu.Unlock()
		return result, errDenied
	}
	isRead := in.Method == http.MethodGet
	collection := false
	if isRead {
		if !a.readPermission(in.Path) {
			if !a.collectionPermission(in.Path) {
				a.mu.Unlock()
				return result, errDenied
			}
			collection = true
		}
	} else {
		verb := ""
		switch in.Method {
		case http.MethodPost:
			verb = "create"
		case http.MethodPut:
			verb = "update"
		case http.MethodPatch:
			verb = "patch"
		case http.MethodDelete:
			verb = "delete"
		default:
			a.mu.Unlock()
			return result, errDenied
		}
		allowed := false
		for _, s := range a.resources {
			if s.Namespace != "" && s.Resource == "secrets" && s.Kind == "Secret" && has(s.Actions, "helm-storage") {
				var object map[string]any
				if json.Unmarshal(in.Body, &object) == nil {
					name := stringValue(mapValue(object["metadata"])["name"])
					if kubeprotocol.HelmStorageResource(s, name) {
						s.Name = name
					}
				}
			}
			target := resourcePath(s, false)
			if verb == "create" {
				target = resourcePath(s, true)
			}
			if target == in.Path && has(s.Actions, verb) && namedWriteAllowed(in, s) {
				allowed = true
				break
			}
		}
		if !allowed {
			a.mu.Unlock()
			return result, errDenied
		}
	}
	endpoint := *a.endpoint
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + in.Path
	endpoint.RawQuery = query.Encode()
	token, client := a.token, a.client
	a.mu.Unlock()
	if !isRead && query.Get("dryRun") != "All" {
		a.writeMu.Lock()
		defer a.writeMu.Unlock()
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
	}
	if in.Method == "DELETE" {
		for _, scope := range a.resources {
			if resourcePath(scope, false) == in.Path && has(scope.Actions, "helm-hook") && has(scope.Actions, "get") {
				observed, e := a.Do(ctx, kubeprotocol.Request{Method: "GET", Path: in.Path})
				if e != nil {
					return result, e
				}
				if observed.Status == 404 {
					return observed, nil
				}
				clear(observed.Body)
				break
			}
		}
	}
	var decision kubeprotocol.Permit
	if !isRead && query.Get("dryRun") != "All" {
		pending := kubeprotocol.PendingWrite{ID: randomID(), Request: in}
		raw, _ := json.Marshal(in)
		pending.Digest = privateDigest(raw)
		w := &writeRequest{public: pending, decision: make(chan kubeprotocol.Permit, 1)}
		a.mu.Lock()
		if len(a.pending) >= 32 {
			a.mu.Unlock()
			return result, errDenied
		}
		a.pending[pending.ID] = w
		a.pendingOrder = append(a.pendingOrder, pending.ID)
		a.mu.Unlock()
		defer func() { a.mu.Lock(); delete(a.pending, pending.ID); a.mu.Unlock() }()
		select {
		case decision = <-w.decision:
		case <-ctx.Done():
			return result, errDenied
		case <-time.After(30 * time.Second):
			return result, errDenied
		}
		if !decision.Allow || decision.Digest != pending.Digest || decision.Expires < time.Now().UnixMilli() || decision.Expires > time.Now().Add(10*time.Second).UnixMilli() {
			return result, errDenied
		}
		result.RequestID = pending.ID
		result.ActionID = decision.ActionID
		if decision.BodyStream != "" {
			a.mu.Lock()
			u := a.uploads[decision.BodyStream]
			if u == nil || !u.done {
				a.mu.Unlock()
				return result, errDenied
			}
			replacement := append([]byte(nil), u.body...)
			clear(u.body)
			delete(a.uploads, decision.BodyStream)
			a.mu.Unlock()
			if len(replacement) > 1<<20 {
				return result, errDenied
			}
			in.Body = replacement
			if !a.writePermission(in) {
				clear(replacement)
				return result, errDenied
			}
			defer clear(replacement)
		}
	}
	if decision.ContentType != "" || decision.FieldManager != "" {
		next, e := a.permitMutationRequest(in, decision)
		if e != nil {
			return result, e
		}
		in = next
		query.Set("fieldManager", decision.FieldManager)
		query.Del("force")
		endpoint.RawQuery = query.Encode()
	}
	if decision.CreateOnly {
		next, e := a.createOnlyRequest(in, decision)
		if e != nil {
			return result, e
		}
		in = next
		endpoint.Path = strings.TrimSuffix(a.endpoint.Path, "/") + in.Path
		query.Del("force")
		endpoint.RawQuery = query.Encode()
	}
	if !a.deadline.IsZero() && time.Now().After(a.deadline) {
		return result, errDenied
	}
	request, e := http.NewRequestWithContext(ctx, in.Method, endpoint.String(), bytes.NewReader(in.Body))
	if e != nil {
		return result, errDenied
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if in.Method == http.MethodPatch {
		contentType := in.ContentType
		if contentType == "" {
			contentType = "application/apply-patch+yaml"
		}
		switch contentType {
		case "application/apply-patch+yaml", "application/merge-patch+json", "application/strategic-merge-patch+json", "application/json-patch+json":
		default:
			return result, errDenied
		}
		request.Header.Set("Content-Type", contentType)
	} else {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	response, e := client.Do(request)
	if e != nil {
		result.Code = "api_outcome_unknown"
		a.recordResult(result)
		return result, errDenied
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, kubeprotocol.MaxResponseBytes+1))
	if e != nil || len(body) > kubeprotocol.MaxResponseBytes {
		clear(body)
		result.Code = "api_response_limit"
		a.recordResult(result)
		return result, errDenied
	}
	result.Status = response.StatusCode
	if result.Status >= 300 && result.Status < 400 {
		clear(body)
		result.Code = "api_redirect_rejected"
		a.recordResult(result)
		return result, errDenied
	}
	a.mu.Lock()
	if watching && result.Status == 200 {
		filtered, e := filterNamedWatch(body, watchScope)
		clear(body)
		if e != nil {
			a.mu.Unlock()
			return result, e
		}
		body = filtered
	}
	if collection && !watching && result.Status == 200 {
		bound, allowed := a.collectionResource(in.Path)
		a.mu.Unlock()
		if !allowed {
			clear(body)
			return result, errDenied
		}
		body, e = a.collectPages(ctx, client, endpoint, token, body, bound)
		if e != nil {
			return result, e
		}
		a.mu.Lock()
		filtered, e := a.filterCollection(in.Path, body)
		clear(body)
		if e != nil {
			a.mu.Unlock()
			return result, e
		}
		body = filtered
	}
	if isRead && !collection && !watching && result.Status == 200 {
		var object map[string]any
		if json.Unmarshal(body, &object) == nil && object["metadata"] != nil {
			a.cache[in.Path] = object
		}
	}
	a.mu.Unlock()
	result.Body = body
	a.recordResult(result)
	return result, nil
}
func (a *Agent) recordResult(r kubeprotocol.APIResult) {
	if r.RequestID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.results) < 256 {
		r.Body = append([]byte(nil), r.Body...)
		a.results = append(a.results, r)
	}
}
func (a *Agent) Handle(ctx context.Context, f kubeprotocol.Frame) kubeprotocol.Reply {
	if f.Version != kubeprotocol.Version {
		return reply("protocol_version")
	}
	switch f.Command {
	case "ping":
		r := reply("ok")
		r.Capabilities = []string{kubeprotocol.PermitCASMergeCapability, kubeprotocol.HelmStorageJSONCapability, kubeprotocol.HelmHookOpenAPICapability, kubeprotocol.HelmHookWatchResyncCapability, kubeprotocol.DaemonSetRevisionReadCapability}
		return r
	case "download":
		a.mu.Lock()
		defer a.mu.Unlock()
		body, ok := a.downloads[f.StreamID]
		if !ok || f.Index < 0 || f.Index >= (len(body)+kubeprotocol.MaxChunkBytes-1)/kubeprotocol.MaxChunkBytes {
			return reply("input_rejected")
		}
		r := reply("ok")
		start := f.Index * kubeprotocol.MaxChunkBytes
		r.Data = append([]byte(nil), body[start:min(len(body), start+kubeprotocol.MaxChunkBytes)]...)
		return r
	case "forget":
		a.mu.Lock()
		defer a.mu.Unlock()
		clear(a.downloads[f.StreamID])
		delete(a.downloads, f.StreamID)
		return reply("ok")
	case "init":
		if f.Init == nil && f.StreamID != "" {
			a.mu.Lock()
			u := a.uploads[f.StreamID]
			if u != nil && u.done {
				var input kubeprotocol.Init
				if json.Unmarshal(u.body, &input) == nil {
					f.Init = &input
				}
				clear(u.body)
				delete(a.uploads, f.StreamID)
			}
			a.mu.Unlock()
		}
		if f.Init == nil || a.Initialize(*f.Init) != nil {
			return reply("initialization_rejected")
		}
		r := reply("ok")
		r.Gateway = a.gateway
		return r
	case "upload":
		a.mu.Lock()
		defer a.mu.Unlock()
		if !idPattern.MatchString(f.StreamID) || len(f.Data) > kubeprotocol.MaxChunkBytes || f.Count < 1 || f.Count > 128 || f.Index < 0 || f.Index >= f.Count || f.Size < 0 || f.Size > kubeprotocol.MaxInputBytes {
			return reply("input_rejected")
		}
		u := a.uploads[f.StreamID]
		if u == nil {
			total := 0
			for _, existing := range a.uploads {
				total += existing.size
			}
			if total+f.Size > kubeprotocol.MaxInputBytes {
				return reply("input_limit")
			}
			if f.Index != 0 || len(a.uploads) >= 16 {
				return reply("input_order")
			}
			u = &upload{parts: make([][]byte, f.Count), size: f.Size, digest: f.Digest}
			a.uploads[f.StreamID] = u
		}
		if u.done || u.next != f.Index || len(u.parts) != f.Count || u.size != f.Size || u.digest != f.Digest {
			return reply("input_order")
		}
		u.parts[f.Index] = append([]byte(nil), f.Data...)
		u.next++
		if u.next == len(u.parts) {
			for _, p := range u.parts {
				u.body = append(u.body, p...)
				clear(p)
			}
			u.parts = nil
			if len(u.body) != u.size || privateDigest(u.body) != u.digest {
				clear(u.body)
				delete(a.uploads, f.StreamID)
				return reply("input_digest")
			}
			u.done = true
		}
		return reply("ok")
	case "dry-run":
		if f.Request == nil || f.Request.Method == "GET" {
			return reply("request_rejected")
		}
		q, e := url.ParseQuery(f.Request.Query)
		if e != nil || q.Get("dryRun") != "All" {
			return reply("request_rejected")
		}
		result, e := a.Do(ctx, *f.Request)
		if e != nil {
			return reply("request_unverified")
		}
		r := reply("ok")
		r.Result = &result
		return r
	case "read":
		if f.Request == nil || f.Request.Method != "GET" {
			return reply("request_rejected")
		}
		result, e := a.Do(ctx, *f.Request)
		if e != nil {
			return reply("request_unverified")
		}
		r := reply("ok")
		r.Result = &result
		return r
	case "write":
		if f.Request == nil || f.Request.Method == "GET" {
			return reply("request_rejected")
		}
		go func() { _, _ = a.Do(context.WithoutCancel(ctx), *f.Request) }()
		return reply("accepted")
	case "poll":
		a.mu.Lock()
		defer a.mu.Unlock()
		r := reply("ok")
		for _, id := range a.pendingOrder {
			if w := a.pending[id]; w != nil && !w.permitted {
				r.Pending = &w.public
				break
			}
		}
		if len(a.pendingOrder) > 256 {
			a.pendingOrder = nil
			for id := range a.pending {
				a.pendingOrder = append(a.pendingOrder, id)
			}
		}
		if len(a.results) > 0 {
			copyResult := a.results[0]
			a.results = a.results[1:]
			r.Result = &copyResult
		}
		return r
	case "permit":
		if f.Permit == nil {
			return reply("permit_rejected")
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		p := *f.Permit
		w := a.pending[p.RequestID]
		if w == nil || p.ActionID == "" || a.consumed[p.ActionID] || p.Digest != w.public.Digest || p.Expires < time.Now().UnixMilli() || p.Expires > time.Now().Add(10*time.Second).UnixMilli() {
			return reply("permit_rejected")
		}
		select {
		case w.decision <- p:
			a.consumed[p.ActionID] = true
			w.permitted = true
			return reply("ok")
		default:
			return reply("permit_rejected")
		}
	case "helm-start":
		if f.Helm == nil {
			return reply("helm_rejected")
		}
		return a.startHelm(ctx, *f.Helm)
	case "helm-output":
		a.mu.Lock()
		defer a.mu.Unlock()
		task := a.tasks[f.StreamID]
		u := a.uploads[f.StreamID]
		if task == nil || !task.done || task.exit != 0 || u == nil || !u.done {
			return reply("task_missing")
		}
		r := reply("ok")
		r.Data = append([]byte(nil), u.body...)
		clear(u.body)
		delete(a.uploads, f.StreamID)
		return r
	case "helm-poll":
		a.mu.Lock()
		defer a.mu.Unlock()
		task := a.tasks[f.StreamID]
		if task == nil {
			return reply("task_missing")
		}
		r := reply("ok")
		r.TaskID = f.StreamID
		r.Done = task.done
		r.ExitCode = task.exit
		r.Revision = task.revision
		return r
	}
	return reply("command_rejected")
}
func (a *Agent) Serve(ctx context.Context, path string) error {
	if os.Geteuid() == 0 {
		return errDenied
	}
	if path != kubeprotocol.SocketPath {
		return errDenied
	}
	if e := os.MkdirAll("/run/pipewright", 0700); e != nil {
		return e
	}
	listener, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	defer listener.Close()
	if e = os.Chmod(path, 0600); e != nil {
		return e
	}
	gateway, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer gateway.Close()
	a.gateway = "http://" + gateway.Addr().String()
	proxy := &http.Server{Handler: http.HandlerFunc(a.proxy), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, MaxHeaderBytes: 16 << 10}
	go proxy.Serve(gateway)
	defer proxy.Close()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10, Handler: a.rpcHandler()}
	go func() { <-ctx.Done(); server.Close() }()
	return server.Serve(listener)
}
func (a *Agent) rpcHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/rpc" {
			w.WriteHeader(403)
			return
		}
		var frame kubeprotocol.Frame
		if decodeBounded(r.Body, &frame, 256<<10) != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := a.Handle(r.Context(), frame)
		// Handle(download) already returns one bounded raw chunk. Base64 makes
		// its JSON envelope larger than MaxChunkBytes; wrapping it again would
		// recursively chunk the chunk. The envelope remains below the existing
		// 1 MiB private transport budget. All other replies retain normal framing.
		if frame.Command != "download" {
			result = a.WrapReply(result)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}
func (a *Agent) proxy(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" || r.URL.IsAbs() || r.Host != strings.TrimPrefix(a.gateway, "http://") {
		w.WriteHeader(403)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if e != nil || len(body) > 1<<20 {
		w.WriteHeader(413)
		return
	}
	defer clear(body)
	request := kubeprotocol.Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body, ContentType: r.Header.Get("Content-Type")}
	if request.ContentType == helmStorageProtobufType {
		request, e = a.normalizeHelmStorageRequest(request)
		if e != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnsupportedMediaType)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"ToolRequestUnverified"}`))
			return
		}
		defer clear(request.Body)
	}
	if request.Method == "DELETE" {
		request, e = a.normalizeHelmHookDeleteRequest(request)
		if e != nil {
			w.WriteHeader(502)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"ToolRequestUnverified"}`))
			return
		}
		defer clear(request.Body)
	}
	nativeSchema := nativeHelmOpenAPIRequest(r)
	var result kubeprotocol.APIResult
	if nativeSchema {
		result, e = a.readNativeHelmOpenAPI(r.Context(), request)
	} else if helmHookCollectionWatch(request) {
		result, e = a.readHelmHookWatch(r.Context(), request)
	} else {
		result, e = a.planningRequest(r.Context(), request)
	}
	if e != nil {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"ToolRequestUnverified"}`))
		return
	}
	contentType := "application/json"
	if nativeSchema {
		contentType = helmOpenAPIProtobufContentType
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
	clear(result.Body)
}
func Call(ctx context.Context, f kubeprotocol.Frame) (kubeprotocol.Reply, error) {
	var out kubeprotocol.Reply
	b, e := json.Marshal(f)
	if e != nil || len(b) > 256<<10 {
		return out, errDenied
	}
	defer clear(b)
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", kubeprotocol.SocketPath)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	request, _ := http.NewRequestWithContext(ctx, "POST", "http://unix/rpc", bytes.NewReader(b))
	response, e := client.Do(request)
	if e != nil {
		return out, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || decodeBounded(response.Body, &out, 6<<20) != nil {
		return out, errDenied
	}
	return out, nil
}

// Stable ordering is used by tests and diagnostics without exposing cached data.
func (a *Agent) PendingIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []string{}
	for id := range a.pending {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// WrapReply keeps each Docker/SSH private response comfortably below its fixed
// 1 MiB limit. Large API/schema replies use the same checked 128 KiB framing.
func (a *Agent) WrapReply(r kubeprotocol.Reply) kubeprotocol.Reply {
	body, e := json.Marshal(r)
	if e != nil {
		return reply("response_rejected")
	}
	if len(body) <= kubeprotocol.MaxChunkBytes {
		return r
	}
	if len(body) > 8<<20 {
		clear(body)
		return reply("response_limit")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, b := range a.downloads {
		total += len(b)
	}
	if total+len(body) > 16<<20 {
		clear(body)
		return reply("response_limit")
	}
	id := randomID()
	a.downloads[id] = body
	out := reply("chunked")
	out.StreamID = id
	out.Size = len(body)
	out.Count = (len(body) + kubeprotocol.MaxChunkBytes - 1) / kubeprotocol.MaxChunkBytes
	out.Digest = privateDigest(body)
	return out
}

// collectPages follows only server-issued continuation tokens on the exact
// already-authorized collection. No unfiltered page leaves the private gateway.
func (a *Agent) collectPages(ctx context.Context, client *http.Client, endpoint url.URL, token string, first []byte, bound kubeprotocol.Resource) ([]byte, error) {
	var head map[string]any
	if json.Unmarshal(first, &head) != nil || kubeprotocol.BindTypedList(head, collectionAPIVersion(bound), bound.Kind) != nil {
		clear(first)
		return nil, errDenied
	}
	total := len(first)
	clear(first)
	items := append([]any(nil), listValue(head["items"])...)
	seen := map[string]bool{}
	next := stringValue(mapValue(head["metadata"])["continue"])
	for next != "" {
		if seen[next] || len(seen) >= 64 || len(items) > 4096 {
			return nil, errDenied
		}
		seen[next] = true
		q := endpoint.Query()
		q.Set("continue", next)
		endpoint.RawQuery = q.Encode()
		request, e := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
		if e != nil {
			return nil, errDenied
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		request.Header.Set("Accept", "application/json")
		response, e := client.Do(request)
		if e != nil {
			return nil, errDenied
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return nil, errDenied
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, int64(kubeprotocol.MaxResponseBytes-total)+1))
		response.Body.Close()
		if e != nil || len(raw) > kubeprotocol.MaxResponseBytes-total {
			clear(raw)
			return nil, errDenied
		}
		total += len(raw)
		var page map[string]any
		e = json.Unmarshal(raw, &page)
		clear(raw)
		if e != nil || kubeprotocol.BindTypedList(page, collectionAPIVersion(bound), bound.Kind) != nil {
			return nil, errDenied
		}
		items = append(items, listValue(page["items"])...)
		next = stringValue(mapValue(page["metadata"])["continue"])
	}
	if len(items) > 4096 {
		return nil, errDenied
	}
	head["items"] = items
	meta := mapValue(head["metadata"])
	delete(meta, "continue")
	delete(meta, "remainingItemCount")
	return json.Marshal(head)
}

// ControllerRevision bodies are private and available only through an explicit
// exact DaemonSet anchor. A matching untyped owner UID alone is insufficient.
func daemonRevisionOwner(s kubeprotocol.Resource, anchor, obj map[string]any) bool {
	m := mapValue(anchor["metadata"])
	if s.Group != "apps" || s.Version != "v1" || s.Resource != "daemonsets" || s.Kind != "DaemonSet" || s.Namespace == "" || anchor["apiVersion"] != "apps/v1" || anchor["kind"] != "DaemonSet" || m["name"] != s.Name || m["namespace"] != s.Namespace || stringValue(m["uid"]) == "" {
		return false
	}
	count := 0
	for _, v := range listValue(mapValue(obj["metadata"])["ownerReferences"]) {
		o := mapValue(v)
		if o["controller"] == true {
			if o["apiVersion"] != "apps/v1" || o["kind"] != "DaemonSet" || o["name"] != s.Name || o["uid"] != m["uid"] {
				return false
			}
			count++
		}
	}
	return count == 1
}
