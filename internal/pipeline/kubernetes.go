package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const KubernetesJobType = "deploy_kubernetes"

var ErrKubernetesConfig = errors.New("kubernetes_config_invalid")
var ErrProtectedCredential = errors.New("protected_credential_not_allowed")
var k8sDNS = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var k8sID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type KubernetesExecutor struct {
	Kind     string `json:"kind"`
	ServerID string `json:"serverId,omitempty"`
}
type KubernetesArtifactSource struct {
	StageID          string `json:"stageId"`
	JobID            string `json:"jobId"`
	DeclarationIndex *int   `json:"declarationIndex"`
}
type KubernetesConfig struct {
	ConnectionID   string             `json:"connectionId"`
	CredentialID   string             `json:"credentialId"`
	Context        string             `json:"context"`
	Namespace      string             `json:"namespace"`
	Deployment     string             `json:"deployment"`
	Container      string             `json:"container"`
	ArtifactSource string             `json:"artifactSource"`
	Executor       KubernetesExecutor `json:"executor"`
	TimeoutSeconds int                `json:"timeoutSeconds,omitempty"`
}

func strictKubernetesJSON(data []byte, out any) error {
	if !uniqueKubernetesJSON(data) {
		return ErrKubernetesConfig
	}

	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return ErrKubernetesConfig
	}
	return nil
}
func ParseKubernetesConfig(config map[string]any) (KubernetesConfig, KubernetesArtifactSource, error) {
	var c KubernetesConfig
	var src KubernetesArtifactSource

	// Node forms use a flat string map. Only these two typed fields admit a
	// strict string encoding; all other fields keep their exact schema.
	canonical := make(map[string]any, len(config))
	for k, v := range config {
		canonical[k] = v
	}
	if raw, ok := canonical["executor"].(string); ok {
		var ex KubernetesExecutor
		if strictKubernetesJSON([]byte(raw), &ex) != nil {
			return c, src, ErrKubernetesConfig
		}
		canonical["executor"] = ex
	}
	if raw, ok := canonical["timeoutSeconds"].(string); ok {
		if !regexp.MustCompile(`^[1-9][0-9]{1,3}$`).MatchString(raw) {
			return c, src, ErrKubernetesConfig
		}
		n, e := strconv.Atoi(raw)
		if e != nil {
			return c, src, ErrKubernetesConfig
		}
		canonical["timeoutSeconds"] = n
	}
	b, e := json.Marshal(canonical)
	if e != nil || len(b) > 16384 {
		return c, src, ErrKubernetesConfig
	}
	if strictKubernetesJSON(b, &c) != nil {
		return c, src, ErrKubernetesConfig
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 300
	}
	text := func(s string) bool {
		return len(s) > 0 && len(s) <= 128 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n\t\x00")
	}
	name := func(s string) bool { return len(s) <= 63 && k8sDNS.MatchString(s) }
	if !k8sID.MatchString(c.ConnectionID) || !k8sID.MatchString(c.CredentialID) || !strings.HasPrefix(c.CredentialID, "kcred_") || !text(c.Context) || !name(c.Namespace) || !name(c.Container) || len(c.Deployment) > 253 || c.TimeoutSeconds < 30 || c.TimeoutSeconds > 1800 {
		return c, src, ErrKubernetesConfig
	}
	for _, part := range strings.Split(c.Deployment, ".") {
		if !name(part) {
			return c, src, ErrKubernetesConfig
		}
	}
	if c.Executor.Kind == "local" {
		if c.Executor.ServerID != "" {
			return c, src, ErrKubernetesConfig
		}
	} else if c.Executor.Kind != "ssh" || !k8sID.MatchString(c.Executor.ServerID) {
		return c, src, ErrKubernetesConfig
	}
	if strictKubernetesJSON([]byte(c.ArtifactSource), &src) != nil || !k8sID.MatchString(src.StageID) || !k8sID.MatchString(src.JobID) || src.DeclarationIndex == nil || *src.DeclarationIndex < 0 || *src.DeclarationIndex >= 1024 {
		return c, src, ErrKubernetesConfig
	}
	return c, src, nil
}

// RejectProtectedReferences inspects references, not arbitrary script text. It
// is reusable for generic settings/vars and never opens a credential.
func RejectProtectedReferences(value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return ErrProtectedCredential
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return ErrProtectedCredential
	}
	var visit func(any) bool
	visit = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				key := strings.ToLower(k)
				if strings.Contains(key, "credential") {
					if s, ok := child.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "kcred_") {
						return true
					}
				}
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range x {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	if visit(v) {
		return ErrProtectedCredential
	}
	return nil
}

// ValidateKubernetesStages is shared by stored/PAC config and execution. A
// selector narrows existing dependencies and can never grant new visibility.
func ValidateKubernetesStages(spec Spec) error {
	deps := StageDependencies(spec.Stages)
	by := map[string]Stage{}
	for _, st := range spec.Stages {
		by[st.ID] = st
	}
	for _, st := range spec.Stages {
		if e := RejectProtectedReferences(st.Post); e != nil {
			return e
		}
		if e := RejectProtectedReferences(st.Services); e != nil {
			return e
		}
		for _, job := range st.Jobs {
			if job.Type != KubernetesJobType {
				if e := RejectProtectedReferences(job.Config); e != nil {
					return e
				}
				continue
			}
			_, source, e := ParseKubernetesConfig(job.Config)
			if e != nil {
				return e
			}
			if st.Kind != KindDeploy || len(st.Jobs) != 1 || len(st.Matrix) > 0 || st.MatrixParentID != "" || len(st.Services) > 0 || len(st.Post) > 0 || len(job.Needs) > 0 || st.AllowFailure {
				return ErrKubernetesConfig
			}
			if source.StageID == st.ID {
				return ErrKubernetesConfig
			}
			found := false
			visited := map[string]bool{}
			queue := append([]string(nil), deps[st.ID]...)
			for len(queue) > 0 {
				id := queue[0]
				queue = queue[1:]
				if visited[id] {
					continue
				}
				visited[id] = true
				if id == source.StageID {
					found = true
				}
				queue = append(queue, deps[id]...)
			}
			if !found {
				return ErrKubernetesConfig
			}
			producer, ok := by[source.StageID]
			if !ok || len(producer.Matrix) > 0 {
				return ErrKubernetesConfig
			}
			found = false
			for _, j := range producer.Jobs {
				if j.ID == source.JobID && j.Type == "push_image" {
					found = true
				}
			}
			if !found {
				return ErrKubernetesConfig
			}
		}
	}
	return nil
}

// Encoding/json normally accepts duplicate and case-insensitive field names.
// Reject both before schema decode so approval hashes have one interpretation.
func uniqueKubernetesJSON(data []byte) bool {
	if len(data) > 16384 {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	allowed := map[string]bool{}
	for _, k := range []string{"connectionId", "credentialId", "context", "namespace", "deployment", "container", "artifactSource", "executor", "timeoutSeconds", "kind", "serverId", "stageId", "jobId", "declarationIndex"} {
		allowed[k] = true
	}
	count := 0
	var walk func(int) bool
	walk = func(depth int) bool {
		count++
		if depth > 8 || count > 256 {
			return false
		}
		token, e := d.Token()
		if e != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				v, e := d.Token()
				if e != nil {
					return false
				}
				k, ok := v.(string)
				if !ok || !allowed[k] || seen[strings.ToLower(k)] {
					return false
				}
				seen[strings.ToLower(k)] = true
				if !walk(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim(']')
		}
		return false
	}
	if !walk(0) {
		return false
	}
	_, e := d.Token()
	return e == io.EOF
}
