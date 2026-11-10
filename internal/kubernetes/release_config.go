package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"gopkg.in/yaml.v3"
)

const releaseConfigPurpose = "pipewright.k8s.release-config.v2"

var releaseKeepMarker = regexp.MustCompile(`^pwkeep_[a-f0-9]{32}$`)

// Only actual native Pod template command arrays are reviewable. A custom
// object or Secret containing keys named containers/args remains private.
var releaseReviewCommandPath = regexp.MustCompile(`^source/(?:v1/Pod/[^/]+/[^/]+/spec|apps/v1/(?:Deployment|StatefulSet|DaemonSet)/[^/]+/[^/]+/spec/template/spec|batch/v1/Job/[^/]+/[^/]+/spec/template/spec|batch/v1/CronJob/[^/]+/[^/]+/spec/jobTemplate/spec/template/spec)/(?:containers|initContainers)/[0-9]+/(?:command|args)/[0-9]+$`)

type releaseKeep struct {
	Kind string     `json:"kind,omitempty"`
	Path string     `json:"path"`
	JSON any        `json:"json,omitempty"`
	YAML *yaml.Node `json:"yaml,omitempty"`
}
type releasePrivateConfig struct {
	Project string                 `json:"project"`
	Node    string                 `json:"node"`
	Config  map[string]any         `json:"config"`
	Public  map[string]any         `json:"public"`
	Keeps   map[string]releaseKeep `json:"keeps"`
}

func cloneReleaseConfig(v map[string]any) (map[string]any, error) {
	b, e := json.Marshal(v)
	if e != nil || len(b) > MaxReleaseInputBytes {
		return nil, ErrInvalid
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil {
		return nil, ErrInvalid
	}
	return out, nil
}
func (s *Service) privateReleaseConfig(ctx context.Context, q dbtx, id, project, node string) (releasePrivateConfig, error) {
	var out releasePrivateConfig
	if !validID(id) || !strings.HasPrefix(id, "kcfg_") {
		return out, ErrInvalid
	}
	var p, n string
	var cipher []byte
	e := q.QueryRowContext(ctx, `SELECT project_id,node_key,ciphertext FROM k8s_config_payloads WHERE id=?`, id).Scan(&p, &n, &cipher)
	if e != nil {
		return out, rowError(e)
	}
	if p != project || n != node {
		return out, ErrDenied
	}
	b, e := s.open(releaseConfigPurpose, id, 1, cipher)
	if e != nil {
		return out, e
	}
	defer clear(b)
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil || out.Project != project || out.Node != node {
		return out, ErrDecrypt
	}
	return out, nil
}
func releaseKeepToken() string { return "pwkeep_" + strings.ReplaceAll(uuid.NewString(), "-", "") }
func releaseYAMLIdentity(doc *yaml.Node, defaultNS string) string {
	if doc.Kind != yaml.MappingNode {
		return ""
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		fields[doc.Content[i].Value] = doc.Content[i+1]
	}
	value := func(n *yaml.Node) string {
		if n == nil {
			return ""
		}
		return n.Value
	}
	metadata := fields["metadata"]
	meta := map[string]*yaml.Node{}
	if metadata != nil {
		for i := 0; i+1 < len(metadata.Content); i += 2 {
			meta[metadata.Content[i].Value] = metadata.Content[i+1]
		}
	}
	ns := value(meta["namespace"])
	if ns == "" {
		ns = defaultNS
	}
	return strings.Join([]string{value(fields["apiVersion"]), value(fields["kind"]), ns, value(meta["name"])}, "/")
}

const releaseSecretAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
const releaseSecretCopyKeepKind = "native-secret-last-applied-v1"

func releaseYAMLField(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// The reserved copied-object annotation is opaque private data, including
// invalid JSON. Identity scalars may become native Secret after interpolation;
// classify this known annotation before rendering without resolving variables.
// This does not classify arbitrary custom-resource password-like fields.
func releaseSecretAppliedValue(n *yaml.Node) *yaml.Node {
	api, kind := releaseYAMLField(n, "apiVersion"), releaseYAMLField(n, "kind")
	if api == nil || kind == nil || api.Kind != yaml.ScalarNode || kind.Kind != yaml.ScalarNode {
		return nil
	}
	return releaseYAMLField(releaseYAMLField(releaseYAMLField(n, "metadata"), "annotations"), releaseSecretAppliedAnnotation)
}
func releaseNeedsCopiedSecretProtection(config map[string]any) bool {
	source := releaseConfigSource(config, "source")
	if source["kind"] != "inline" {
		return false
	}
	decoder := yaml.NewDecoder(strings.NewReader(releaseString(source["content"])))
	count := 0
	var plain func(*yaml.Node, int) bool
	plain = func(n *yaml.Node, depth int) bool {
		count++
		if depth > 48 || count > 200000 {
			return true
		}
		if value := releaseSecretAppliedValue(n); value != nil && !releaseKeepMarker.MatchString(value.Value) {
			return true
		}
		for _, child := range n.Content {
			if plain(child, depth+1) {
				return true
			}
		}
		return false
	}
	for {
		var doc yaml.Node
		e := decoder.Decode(&doc)
		if e == io.EOF {
			return false
		}
		if e != nil {
			return true
		}
		if plain(&doc, 0) {
			return true
		}
	}
}
func releasePrivateYAML(content, prefix, defaultNS string, all, protect bool, old map[string]releaseKeep, next map[string]releaseKeep) (string, error) {
	if len(content) > MaxReleaseManifestBytes {
		return "", ErrInvalid
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	defer encoder.Close()
	count := 0
	copiedSecretValues := map[*yaml.Node]bool{}
	var visit func(*yaml.Node, string, bool, int) error
	visit = func(n *yaml.Node, p string, sensitive bool, depth int) error {
		count++
		if depth > 48 || count > 200000 || n.Anchor != "" || n.Alias != nil {
			return ErrInvalid
		}
		if n.Kind == yaml.MappingNode {
			if value := releaseSecretAppliedValue(n); value != nil {
				if value.Kind != yaml.ScalarNode {
					return ErrInvalid
				}
				copiedSecretValues[value] = true
			}
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				if i+1 >= len(n.Content) || n.Content[i].Tag != "!!str" || seen[n.Content[i].Value] {
					return ErrInvalid
				}
				key := n.Content[i].Value
				seen[key] = true
				private := sensitive || copiedSecretValues[n.Content[i+1]] || key == "data" || key == "binaryData" || key == "stringData" || key == "command" || key == "args" || key == "value"
				if e := visit(n.Content[i+1], p+"/"+key, private, depth+1); e != nil {
					return e
				}
			}
			return nil
		}
		if n.Kind == yaml.SequenceNode {
			for i, c := range n.Content {
				if e := visit(c, p+"/"+strconvI(i), sensitive, depth+1); e != nil {
					return e
				}
			}
			return nil
		}
		if n.Kind != yaml.ScalarNode {
			return ErrInvalid
		}
		keepKind := ""
		if copiedSecretValues[n] {
			keepKind = releaseSecretCopyKeepKind
		}
		if !protect && releaseKeepMarker.MatchString(n.Value) {
			keep, ok := old[n.Value]
			if !ok || keep.Path != p || keep.YAML == nil || keep.Kind != keepKind {
				return ErrDenied
			}
			*n = *cloneYAML(keep.YAML)
			return nil
		}
		if protect && sensitive {
			token := releaseKeepToken()
			next[token] = releaseKeep{Kind: keepKind, Path: p, YAML: cloneYAML(n)}
			n.Kind = yaml.ScalarNode
			n.Tag = "!!str"
			n.Value = token
			n.Style = yaml.SingleQuotedStyle
		}
		return nil
	}
	documents := 0
	for {
		var n yaml.Node
		e := decoder.Decode(&n)
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", ErrInvalid
		}
		if len(n.Content) == 0 {
			continue
		}
		documents++
		if documents > MaxReleaseResources {
			return "", ErrInvalid
		}
		root := n.Content[0]
		identity := releaseYAMLIdentity(root, defaultNS)
		if all {
			identity = "values"
		}
		if e = visit(root, prefix+"/"+identity, all, 0); e != nil {
			return "", e
		}
		if e = encoder.Encode(&n); e != nil {
			return "", ErrInvalid
		}
	}
	if documents == 0 {
		return "", ErrInvalid
	}
	return output.String(), nil
}
func releaseConfigSource(config map[string]any, key string) map[string]any {
	v := config[key]
	if raw, ok := v.(string); ok {
		var out map[string]any
		if json.Unmarshal([]byte(raw), &out) == nil {
			config[key] = out
			return out
		}
	}
	return releaseMap(v)
}
func releaseConfigVariables(config map[string]any) []any {
	v := config["variables"]
	if raw, ok := v.(string); ok {
		var out []any
		if json.Unmarshal([]byte(raw), &out) == nil {
			config["variables"] = out
			return out
		}
	}
	return releaseItems(v)
}
func transformReleasePrivateFields(config map[string]any, protect bool, old map[string]releaseKeep, next map[string]releaseKeep) error {
	namespace := releaseString(config["namespace"])
	source := releaseConfigSource(config, "source")
	if source["kind"] == "inline" {
		text, ok := source["content"].(string)
		if !ok {
			return ErrInvalid
		}
		out, e := releasePrivateYAML(text, "source", namespace, false, protect, old, next)
		if e != nil {
			return e
		}
		source["content"] = out
	}
	helm := releaseConfigSource(config, "helm")
	if helm != nil {
		values := releaseConfigSource(helm, "values")
		if values["kind"] == "inline" {
			text, ok := values["content"].(string)
			if !ok {
				return ErrInvalid
			}
			out, e := releasePrivateYAML(text, "helm/values", namespace, true, protect, old, next)
			if e != nil {
				return e
			}
			values["content"] = out
		}
	}
	// Probe literals may contain private URLs, expected values or credentials.
	checks := config["checks"]
	if raw, ok := checks.(string); ok {
		var parsed []any
		if json.Unmarshal([]byte(raw), &parsed) != nil {
			return ErrInvalid
		}
		checks = parsed
		config["checks"] = checks
	}
	for _, item := range releaseItems(checks) {
		check := releaseMap(item)
		for _, field := range []string{"target", "server", "expected"} {
			if field == "target" && (check["kind"] == "workload" || check["kind"] == "condition") {
				continue
			}
			v, ok := check[field].(string)
			if !ok || v == "" {
				continue
			}
			checkNS := releaseString(check["namespace"])
			if checkNS == "" {
				checkNS = namespace
			}
			identity, _ := json.Marshal([]string{checkNS, releaseString(check["kind"]), releaseString(check["name"]), field})
			key := "checks/" + string(identity)
			if protect {
				token := releaseKeepToken()
				next[token] = releaseKeep{Path: key, JSON: v}
				check[field] = token
			} else if releaseKeepMarker.MatchString(v) {
				keep, found := old[v]
				if !found || keep.Path != key || keep.YAML != nil {
					return ErrDenied
				}
				check[field] = keep.JSON
			}
		}
	}
	for _, item := range releaseConfigVariables(config) {
		v := releaseMap(item)
		if v["source"] != "fixed" {
			continue
		}
		key := "variables/" + releaseString(v["name"]) + "/value"
		if protect {
			token := releaseKeepToken()
			next[token] = releaseKeep{Path: key, JSON: v["value"]}
			v["value"] = token
		} else if token, ok := v["value"].(string); ok && releaseKeepMarker.MatchString(token) {
			keep, found := old[token]
			if !found || keep.Path != key || keep.YAML != nil {
				return ErrDenied
			}
			v["value"] = keep.JSON
		}
	}
	return nil
}

// ProtectReleaseConfig is implemented as the pipeline's optional ConfigProtector.
// Only randomized references and write-only keep markers enter pipeline/config
// draft JSON. Removing/replacing a YAML key is an explicit edit; retaining its
// unforgeable, path-bound marker preserves the original value without revealing it.
func (s *Service) ProtectReleaseConfig(ctx context.Context, project, node string, in map[string]any) (map[string]any, error) {
	if !validID(project) || !cleanText(node, 128) {
		return nil, ErrInvalid
	}
	full, e := cloneReleaseConfig(in)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	e = s.write(ctx, func(ctx context.Context, q dbtx) error {
		keeps := map[string]releaseKeep{}
		if ref, ok := full["privateRef"].(string); ok && ref != "" {
			previous, e := s.privateReleaseConfig(ctx, q, ref, project, node)
			if e != nil {
				return e
			}
			if fingerprint(full) == fingerprint(previous.Public) && !releaseNeedsCopiedSecretProtection(previous.Public) {
				out, e = cloneReleaseConfig(previous.Public)
				return e
			}
			keeps = previous.Keeps
		}
		delete(full, "privateRef")
		if e := transformReleasePrivateFields(full, false, keeps, nil); e != nil {
			return e
		}
		safe, e := cloneReleaseConfig(full)
		if e != nil {
			return e
		}
		next := map[string]releaseKeep{}
		if e = transformReleasePrivateFields(safe, true, nil, next); e != nil {
			return e
		}
		id := "kcfg_" + uuid.NewString()
		safe["privateRef"] = id
		private := releasePrivateConfig{Project: project, Node: node, Config: full, Public: safe, Keeps: next}
		body, e := json.Marshal(private)
		if e != nil || len(body) > 64<<20 {
			return ErrInvalid
		}
		defer clear(body)
		cipher, e := s.seal(releaseConfigPurpose, id, 1, body)
		if e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, `INSERT INTO k8s_config_payloads(id,project_id,node_key,ciphertext,created_at) VALUES(?,?,?,?,?)`, id, project, node, cipher, s.now().UnixMilli())
		out = safe
		return e
	})
	return out, e
}
func (s *Service) ResolveReleaseConfig(ctx context.Context, project, node string, safe map[string]any) (map[string]any, error) {
	ref, _ := safe["privateRef"].(string)
	if ref == "" {
		return cloneReleaseConfig(safe)
	}
	p, e := s.privateReleaseConfig(ctx, configstate.Queryer(ctx, s.db), ref, project, node)
	if e != nil {
		return nil, e
	}
	if fingerprint(safe) != fingerprint(p.Public) {
		return nil, ErrConflict
	}
	return cloneReleaseConfig(p.Config)
}

// ReleaseDraftConfig is an internal adapter: it returns only this exact node's
// protected payload, never decrypted credentials or private scalar contents.
func (s *Service) ReleaseDraftConfig(ctx context.Context, project, node, ref string) (map[string]any, error) {
	p, e := s.privateReleaseConfig(ctx, configstate.Queryer(ctx, s.db), ref, project, node)
	if e != nil {
		return nil, e
	}
	return cloneReleaseConfig(p.Public)
}
func (s *Service) PrepareReleaseDraftConfig(ctx context.Context, project, node string, cfg map[string]any) (map[string]any, error) {
	safe, e := s.ProtectReleaseConfig(ctx, project, node, cfg)
	if e != nil {
		return nil, e
	}
	full, e := s.ResolveReleaseConfig(ctx, project, node, safe)
	if e != nil {
		return nil, e
	}
	if _, e = pipeline.ParseKubernetesReleaseConfig(full); e != nil {
		return nil, ErrInvalid
	}
	return safe, nil
}

// ReleaseReviewConfig is only for authenticated human review. Commands are
// reviewable, while data, env literals, values and fixed bindings stay opaque.
func (s *Service) ReleaseReviewConfig(ctx context.Context, project, node, ref string) (map[string]any, error) {
	p, e := s.privateReleaseConfig(ctx, configstate.Queryer(ctx, s.db), ref, project, node)
	if e != nil {
		return nil, e
	}
	out, e := cloneReleaseConfig(p.Public)
	if e != nil {
		return nil, e
	}
	source := releaseConfigSource(out, "source")
	if source["kind"] != "inline" {
		return out, nil
	}

	decoder := yaml.NewDecoder(strings.NewReader(releaseString(source["content"])))
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	defer enc.Close()
	var visit func(*yaml.Node)
	visit = func(n *yaml.Node) {
		if value := releaseSecretAppliedValue(n); value != nil && !releaseKeepMarker.MatchString(value.Value) {
			*value = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: releaseKeepToken(), Style: yaml.SingleQuotedStyle}
		}
		if n.Kind == yaml.ScalarNode {
			if keep, ok := p.Keeps[n.Value]; ok && keep.YAML != nil && releaseReviewCommandPath.MatchString(keep.Path) {
				*n = *cloneYAML(keep.YAML)
			}
		}
		for _, child := range n.Content {
			visit(child)
		}
	}
	for {
		var doc yaml.Node
		e := decoder.Decode(&doc)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, ErrInvalid
		}
		visit(&doc)
		if enc.Encode(&doc) != nil {
			return nil, ErrInvalid
		}
	}
	source["content"] = b.String()
	return out, nil
}
