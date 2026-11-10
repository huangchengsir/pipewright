package pipeline

import (
	"bytes"
	"encoding/json"
	"io"
	"path"
	"reflect"
	"regexp"
	"strings"
)

// KubernetesReleaseConfig is deliberately separate from the v1 structs: adding
// defaults to a v1 Authorization/Intent changes the JSON used by old approvals.
type KubernetesReleaseConfig struct {
	CoreDNS        *KubernetesCoreDNS       `json:"coreDNS,omitempty"`
	PrivateRef     string                   `json:"privateRef,omitempty"`
	Version        int                      `json:"version"`
	Mode           string                   `json:"mode"`
	ConnectionID   string                   `json:"connectionId"`
	CredentialID   string                   `json:"credentialId"`
	Context        string                   `json:"context"`
	Namespace      string                   `json:"namespace"`
	Executor       KubernetesExecutor       `json:"executor"`
	TimeoutSeconds int                      `json:"timeoutSeconds,omitempty"`
	ObserveSeconds *int                     `json:"observeSeconds,omitempty"`
	Workload       *KubernetesWorkload      `json:"workload,omitempty"`
	Images         []KubernetesImageBinding `json:"images,omitempty"`
	Source         *KubernetesReleaseSource `json:"source,omitempty"`
	Variables      []KubernetesVariable     `json:"variables,omitempty"`
	Helm           *KubernetesHelm          `json:"helm,omitempty"`
	Progressive    *KubernetesProgressive   `json:"progressive,omitempty"`
	Checks         []KubernetesReleaseCheck `json:"checks,omitempty"`
}
type KubernetesCoreDNS struct {
	ConfigMap          string                  `json:"configMap"`
	Key                string                  `json:"key"`
	Deployment         string                  `json:"deployment"`
	Container          string                  `json:"container"`
	Service            string                  `json:"service"`
	CandidatePrefix    string                  `json:"candidatePrefix"`
	CandidateServiceIP string                  `json:"candidateServiceIP"`
	DNSPort            int                     `json:"dnsPort"`
	ServiceDomain      string                  `json:"serviceDomain"`
	ServiceResource    KubernetesCheckResource `json:"serviceResource"`
	Records            []KubernetesDNSRecord   `json:"records"`
}
type KubernetesDNSRecord struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
}
type KubernetesWorkload struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}
type KubernetesImageBinding struct {
	Container string                `json:"container"`
	Source    KubernetesImageSource `json:"source"`
}
type KubernetesImageSource struct {
	Kind         string                    `json:"kind"`
	Reference    string                    `json:"reference,omitempty"`
	CredentialID string                    `json:"credentialId,omitempty"`
	Artifact     *KubernetesArtifactSource `json:"artifact,omitempty"`
}
type KubernetesReleaseSource struct {
	Kind     string                    `json:"kind"`
	Path     string                    `json:"path,omitempty"`
	Content  string                    `json:"content,omitempty"`
	Artifact *KubernetesArtifactSource `json:"artifact,omitempty"`
}
type KubernetesVariable struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
	Value  any    `json:"value,omitempty"`
}
type KubernetesHelm struct {
	Release string                   `json:"release"`
	Values  *KubernetesReleaseSource `json:"values,omitempty"`
	Hooks   string                   `json:"hooks,omitempty"`
}
type KubernetesProgressive struct {
	Service    string `json:"service"`
	Candidate  string `json:"candidate"`
	TrackKey   string `json:"trackKey"`
	TrackValue string `json:"trackValue"`
	Batches    []int  `json:"batches"`
}
type KubernetesReleaseCheck struct {
	WaitSeconds      int                      `json:"waitSeconds,omitempty"`
	Target           string                   `json:"target,omitempty"`
	Server           string                   `json:"server,omitempty"`
	Protocol         string                   `json:"protocol,omitempty"`
	PositiveControl  string                   `json:"positiveControl,omitempty"`
	ServiceAccount   string                   `json:"serviceAccount,omitempty"`
	Labels           map[string]string        `json:"labels,omitempty"`
	ImagePullSecrets []string                 `json:"imagePullSecrets,omitempty"`
	Resource         *KubernetesCheckResource `json:"resource,omitempty"`
	Restart          bool                     `json:"restart,omitempty"`
	Kind             string                   `json:"kind"`
	Name             string                   `json:"name"`
	Namespace        string                   `json:"namespace,omitempty"`
	Predicate        string                   `json:"predicate,omitempty"`
	Expected         string                   `json:"expected,omitempty"`
}

type KubernetesCheckResource struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

func IsKubernetesReleaseConfig(c map[string]any) bool { _, ok := c["version"]; return ok }

// StrictKubernetesReleaseJSON rejects duplicate, differently-cased and unknown
// keys recursively, including objects encoded as strings by the node form.
func StrictKubernetesReleaseJSON(data []byte, out any) error {
	if len(data) == 0 || len(data) > 16<<20 {
		return ErrKubernetesConfig
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	count := 0
	var value func(int) (any, error)
	value = func(depth int) (any, error) {
		count++
		if count > 200000 || depth > 48 {
			return nil, ErrKubernetesConfig
		}
		token, err := d.Token()
		if err != nil {
			return nil, ErrKubernetesConfig
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return token, nil
		}
		switch delim {
		case '{':
			m := map[string]any{}
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, ErrKubernetesConfig
				}
				key, ok := k.(string)
				if !ok || seen[strings.ToLower(key)] {
					return nil, ErrKubernetesConfig
				}
				seen[strings.ToLower(key)] = true
				v, e := value(depth + 1)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			t, e := d.Token()
			if e != nil || t != json.Delim('}') {
				return nil, ErrKubernetesConfig
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := value(depth + 1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			t, e := d.Token()
			if e != nil || t != json.Delim(']') {
				return nil, ErrKubernetesConfig
			}
			return a, nil
		}
		return nil, ErrKubernetesConfig
	}
	raw, err := value(0)
	if err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return ErrKubernetesConfig
	}
	var exact func(any, reflect.Type) bool
	exact = func(v any, t reflect.Type) bool {
		for t.Kind() == reflect.Pointer {
			if v == nil {
				return true
			}
			t = t.Elem()
		}
		if t.Kind() == reflect.Interface {
			return true
		}
		switch t.Kind() {
		case reflect.Struct:
			m, ok := v.(map[string]any)
			if !ok {
				return false
			}
			fields := map[string]reflect.Type{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				key := strings.Split(f.Tag.Get("json"), ",")[0]
				if key != "" && key != "-" {
					fields[key] = f.Type
				}
			}
			for k, x := range m {
				typ, ok := fields[k]
				if !ok || !exact(x, typ) {
					return false
				}
			}
		case reflect.Slice:
			if v == nil {
				return true
			}
			a, ok := v.([]any)
			if !ok {
				return false
			}
			for _, x := range a {
				if !exact(x, t.Elem()) {
					return false
				}
			}
		}
		return true
	}
	typ := reflect.TypeOf(out)
	if typ == nil || typ.Kind() != reflect.Pointer || !exact(raw, typ.Elem()) {
		return ErrKubernetesConfig
	}
	if json.Unmarshal(data, out) != nil {
		return ErrKubernetesConfig
	}
	return nil
}

var releaseVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func validReleasePath(v string) bool {
	return v != "" && len(v) <= 1024 && path.IsAbs(v) == false && path.Clean(v) == v && v != ".." && !strings.HasPrefix(v, "../") && !strings.ContainsAny(v, "\\:\x00\r\n")
}
func validReleaseSelector(a *KubernetesArtifactSource) bool {
	return a != nil && k8sID.MatchString(a.StageID) && k8sID.MatchString(a.JobID) && a.DeclarationIndex != nil && *a.DeclarationIndex >= 0 && *a.DeclarationIndex < 1024
}
func validateReleaseSource(s *KubernetesReleaseSource, chart bool) bool {
	if s == nil {
		return false
	}
	switch s.Kind {
	case "inline":
		return !chart && s.Content != "" && len(s.Content) <= 4<<20 && s.Path == "" && s.Artifact == nil
	case "git":
		return validReleasePath(s.Path) && s.Content == "" && s.Artifact == nil
	case "artifact":
		return validReleaseSelector(s.Artifact) && s.Content == "" && (s.Path == "" || validReleasePath(s.Path))
	}
	return false
}
func ParseKubernetesReleaseConfig(config map[string]any) (KubernetesReleaseConfig, error) {
	var c KubernetesReleaseConfig
	canonical := map[string]any{}
	for k, v := range config {
		canonical[k] = v
	}
	types := map[string]any{"executor": &KubernetesExecutor{}, "workload": &KubernetesWorkload{}, "images": &[]KubernetesImageBinding{}, "source": &KubernetesReleaseSource{}, "variables": &[]KubernetesVariable{}, "helm": &KubernetesHelm{}, "progressive": &KubernetesProgressive{}, "checks": &[]KubernetesReleaseCheck{}, "coreDNS": &KubernetesCoreDNS{}}
	for key, p := range types {
		if raw, ok := canonical[key].(string); ok {
			if StrictKubernetesReleaseJSON([]byte(raw), p) != nil {
				return c, ErrKubernetesConfig
			}
			canonical[key] = p
		}
	}
	for _, key := range []string{"version", "timeoutSeconds", "observeSeconds"} {
		if raw, ok := canonical[key].(string); ok {
			var n int
			if json.Unmarshal([]byte(raw), &n) != nil || strings.TrimSpace(raw) != raw {
				return c, ErrKubernetesConfig
			}
			canonical[key] = n
		}
	}
	b, e := json.Marshal(canonical)
	if e != nil || StrictKubernetesReleaseJSON(b, &c) != nil {
		return c, ErrKubernetesConfig
	}
	if c.PrivateRef != "" && (!k8sID.MatchString(c.PrivateRef) || !strings.HasPrefix(c.PrivateRef, "kcfg_")) {
		return c, ErrKubernetesConfig
	}
	if c.Version != 2 || !k8sID.MatchString(c.ConnectionID) || !k8sID.MatchString(c.CredentialID) || !strings.HasPrefix(c.CredentialID, "kcred_") || c.Context == "" || len(c.Context) > 128 || strings.TrimSpace(c.Context) != c.Context || strings.ContainsAny(c.Context, "\x00\r\n\t") || len(c.Namespace) > 63 || !k8sDNS.MatchString(c.Namespace) {
		return c, ErrKubernetesConfig
	}
	if c.Executor.Kind == "local" {
		if c.Executor.ServerID != "" {
			return c, ErrKubernetesConfig
		}
	} else if c.Executor.Kind != "ssh" || !k8sID.MatchString(c.Executor.ServerID) {
		return c, ErrKubernetesConfig
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 300
	}
	if c.TimeoutSeconds < 30 || c.TimeoutSeconds > 1800 {
		return c, ErrKubernetesConfig
	}
	if c.ObserveSeconds == nil {
		v := 30
		c.ObserveSeconds = &v
	}
	if *c.ObserveSeconds < 0 || *c.ObserveSeconds > 600 || *c.ObserveSeconds >= c.TimeoutSeconds {
		return c, ErrKubernetesConfig
	}
	switch c.Mode {
	case "image", "progressive":
		if c.Workload == nil || (c.Workload.Kind != "Deployment" && c.Workload.Kind != "StatefulSet" && c.Workload.Kind != "DaemonSet") || !validReleaseObjectName(c.Workload.Name) || len(c.Images) == 0 || len(c.Images) > 32 || c.Source != nil || c.Helm != nil {
			return c, ErrKubernetesConfig
		}
		seen := map[string]bool{}
		for _, i := range c.Images {
			if len(i.Container) > 63 || !k8sDNS.MatchString(i.Container) || seen[i.Container] {
				return c, ErrKubernetesConfig
			}
			seen[i.Container] = true
			switch i.Source.Kind {
			case "artifact":
				if !validReleaseSelector(i.Source.Artifact) || i.Source.Reference != "" || i.Source.CredentialID != "" {
					return c, ErrKubernetesConfig
				}
			case "external":
				if i.Source.Artifact != nil || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@-]{0,511}$`).MatchString(i.Source.Reference) || strings.HasPrefix(i.Source.CredentialID, "kcred_") {
					return c, ErrKubernetesConfig
				}
			default:
				return c, ErrKubernetesConfig
			}
		}
		if c.Mode == "progressive" {
			p := c.Progressive
			if c.Workload.Kind != "Deployment" || p == nil || !validReleaseObjectName(p.Service) || !validReleaseObjectName(p.Candidate) || p.Candidate == c.Workload.Name || p.TrackKey == "" || len(p.TrackKey) > 253 || p.TrackValue == "" || len(p.Batches) == 0 || len(p.Batches) > 32 {
				return c, ErrKubernetesConfig
			}
			last := 0
			for _, n := range p.Batches {
				if n <= last || n > 10000 {
					return c, ErrKubernetesConfig
				}
				last = n
			}
		} else if c.Progressive != nil {
			return c, ErrKubernetesConfig
		}
	case "manifest":
		if !validateReleaseSource(c.Source, false) || c.Workload != nil || len(c.Images) > 0 || c.Helm != nil || c.Progressive != nil {
			return c, ErrKubernetesConfig
		}
	case "helm":
		if !validateReleaseSource(c.Source, true) || c.Helm == nil || !validReleaseObjectName(c.Helm.Release) || len(c.Helm.Release) > 53 || c.Workload != nil || len(c.Images) > 0 || c.Progressive != nil || (c.Helm.Values != nil && !validateReleaseSource(c.Helm.Values, false)) || (c.Helm.Hooks != "" && c.Helm.Hooks != "disabled" && c.Helm.Hooks != "approved-jobs") {
			return c, ErrKubernetesConfig
		}
	default:
		return c, ErrKubernetesConfig
	}
	if c.CoreDNS != nil {
		d := c.CoreDNS
		if c.Mode != "manifest" || !validReleaseObjectName(d.ConfigMap) || !validReleaseObjectName(d.Deployment) || !validReleaseObjectName(d.Service) || !validReleaseObjectName(d.Container) || !validReleaseObjectName(d.CandidatePrefix) || len(d.CandidatePrefix) > 30 || d.Key == "" || len(d.Key) > 128 || d.ServiceDomain == "" || len(d.ServiceDomain) > 253 || len(d.Records) < 1 || len(d.Records) > 4 || d.CandidateServiceIP == "" || d.DNSPort < 1 || d.DNSPort > 65535 {
			return c, ErrKubernetesConfig
		}
		r := d.ServiceResource
		if r.Group != "" || r.Version != "v1" || r.Resource != "services" || r.Kind != "Service" || !validReleaseObjectName(r.Name) || r.Namespace == "" {
			return c, ErrKubernetesConfig
		}
	}
	if len(c.Variables) > 256 || len(c.Checks) > 128 {
		return c, ErrKubernetesConfig
	}
	seen := map[string]bool{}
	for _, v := range c.Variables {
		if !releaseVariableName.MatchString(v.Name) || seen[v.Name] {
			return c, ErrKubernetesConfig
		}
		seen[v.Name] = true
		switch v.Type {
		case "string", "number", "boolean", "object", "array", "null":
		default:
			return c, ErrKubernetesConfig
		}
		switch v.Source {
		case "fixed":
			if v.Ref != "" {
				return c, ErrKubernetesConfig
			}
		case "parameter", "environment", "output", "secret":
			if v.Ref == "" || v.Value != nil || v.Source == "secret" && strings.HasPrefix(v.Ref, "kcred_") {
				return c, ErrKubernetesConfig
			}
		default:
			return c, ErrKubernetesConfig
		}
	}
	for _, check := range c.Checks {
		if !validReleaseObjectName(check.Name) || ((check.Kind != "workload" && check.Kind != "condition") && (len(check.Name) > 63 || !k8sDNS.MatchString(check.Name))) || (check.Namespace != "" && (len(check.Namespace) > 63 || !k8sDNS.MatchString(check.Namespace))) {
			return c, ErrKubernetesConfig
		}
		switch check.Kind {
		case "workload", "dns", "http", "tcp", "network-allow", "network-deny", "storage", "condition":
			if check.WaitSeconds < 0 || check.WaitSeconds > 600 || check.WaitSeconds >= c.TimeoutSeconds || (check.Kind == "storage" || check.Kind == "network-deny") && check.WaitSeconds != 0 {
				return c, ErrKubernetesConfig
			}
			if len(check.Target) > 2048 || len(check.Expected) > 4096 || len(check.Server) > 253 || len(check.Labels) > 32 || len(check.ImagePullSecrets) > 16 {
				return c, ErrKubernetesConfig
			}
			if check.Kind != "workload" && check.Kind != "condition" && check.Target == "" {
				return c, ErrKubernetesConfig
			}
			if check.Kind == "dns" && (check.Protocol != "udp" && check.Protocol != "tcp" || check.Server == "" || check.Expected == "") {
				return c, ErrKubernetesConfig
			}
			if check.Kind == "network-deny" && check.PositiveControl == "" {
				return c, ErrKubernetesConfig
			}
			if check.Restart && check.Kind != "workload" {
				return c, ErrKubernetesConfig
			}
			if check.ServiceAccount != "" && !k8sDNS.MatchString(check.ServiceAccount) {
				return c, ErrKubernetesConfig
			}
			for _, name := range check.ImagePullSecrets {
				if !validReleaseObjectName(name) {
					return c, ErrKubernetesConfig
				}
			}
			for key, value := range check.Labels {
				if key == "" || len(key) > 253 || len(value) > 63 || strings.ContainsAny(key+value, "\x00\r\n") {
					return c, ErrKubernetesConfig
				}
			}
			if check.Resource != nil {
				r := check.Resource
				if !validReleaseObjectName(r.Name) || !validReleaseObjectName(r.Resource) || r.Version == "" || r.Kind == "" || strings.ContainsAny(r.Group+r.Version+r.Kind+r.Namespace, "*\x00\r\n") {
					return c, ErrKubernetesConfig
				}
			}
		default:
			return c, ErrKubernetesConfig
		}
	}
	return c, nil
}
func validReleaseObjectName(s string) bool {
	if len(s) > 253 || s == "" {
		return false
	}
	for _, p := range strings.Split(s, ".") {
		if len(p) > 63 || !k8sDNS.MatchString(p) {
			return false
		}
	}
	return true
}

func validateKubernetesReleaseStage(spec Spec, st Stage, c KubernetesReleaseConfig) error {
	if st.Kind != KindDeploy || len(st.Jobs) != 1 || len(st.Matrix) > 0 || st.MatrixParentID != "" || len(st.Services) > 0 || len(st.Post) > 0 || len(st.Jobs[0].Needs) > 0 || st.AllowFailure {
		return ErrKubernetesConfig
	}
	deps := StageDependencies(spec.Stages)
	ancestors := map[string]bool{}
	queue := append([]string(nil), deps[st.ID]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if ancestors[id] {
			continue
		}
		ancestors[id] = true
		queue = append(queue, deps[id]...)
	}
	selectors := []struct {
		a     *KubernetesArtifactSource
		image bool
	}{}
	for _, i := range c.Images {
		if i.Source.Artifact != nil {
			selectors = append(selectors, struct {
				a     *KubernetesArtifactSource
				image bool
			}{i.Source.Artifact, true})
		}
	}
	for _, s := range []*KubernetesReleaseSource{c.Source, func() *KubernetesReleaseSource {
		if c.Helm != nil {
			return c.Helm.Values
		}
		return nil
	}()} {
		if s != nil && s.Artifact != nil {
			selectors = append(selectors, struct {
				a     *KubernetesArtifactSource
				image bool
			}{s.Artifact, false})
		}
	}
	for _, src := range selectors {
		if !ancestors[src.a.StageID] {
			return ErrKubernetesConfig
		}
		found := false
		for _, producer := range spec.Stages {
			if producer.ID != src.a.StageID {
				continue
			}
			if len(producer.Matrix) > 0 {
				return ErrKubernetesConfig
			}
			for _, j := range producer.Jobs {
				if j.ID == src.a.JobID && (!src.image || j.Type == "push_image") {
					found = true
				}
			}
		}
		if !found {
			return ErrKubernetesConfig
		}
	}
	return nil
}
