package kubernetes

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const MaxReleaseInputBytes = 16 << 20
const MaxReleaseManifestBytes = 4 << 20
const MaxReleaseResources = 128
const MaxReleaseObjectBytes = 1 << 20

var ErrManifest = errors.New("kubernetes_manifest_invalid")
var ErrVariable = errors.New("kubernetes_variable_unavailable")
var ErrDiscovery = errors.New("kubernetes_resource_not_discovered")
var placeholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

type ReleaseVariableValue struct {
	Type   string
	Value  any
	Secret bool
}
type ReleaseInputFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Digest  string `json:"digest"`
}

// FrozenManifest contains private input. It must only be saved inside a sealed
// plan, never returned as an API DTO or used as an audit/log value.
type FrozenManifest struct {
	Files     []ReleaseInputFile `json:"files"`
	Resources []ReleaseResource  `json:"resources"`
	Sensitive bool               `json:"sensitive"`
}
type ReleaseResourceID struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

func (id ReleaseResourceID) Key() string {
	return strings.Join([]string{id.Group, id.Resource, id.Namespace, id.Name}, "/")
}

type ReleaseResource struct {
	DaemonSetProofVersion int                `json:"daemonSetProofVersion,omitempty"`
	Mutation              *ReleaseMutation   `json:"mutation,omitempty"`
	HelmCRD               bool               `json:"helmCrd,omitempty"`
	VerifyOnly            bool               `json:"verifyOnly,omitempty"`
	RollbackImages        map[string]string  `json:"rollbackImages,omitempty"`
	Delete                bool               `json:"delete,omitempty"`
	Ephemeral             bool               `json:"ephemeral,omitempty"`
	ImageOnly             bool               `json:"imageOnly,omitempty"`
	Supplemental          bool               `json:"supplemental,omitempty"`
	Probe                 bool               `json:"probe,omitempty"`
	PositiveControl       string             `json:"positiveControl,omitempty"`
	ExpectedUID           string             `json:"expectedUid,omitempty"`
	RequiredCRD           *ReleaseResourceID `json:"requiredCrd,omitempty"`
	SchemaDigest          string             `json:"schemaDigest,omitempty"`
	Predicates            []ReleasePredicate `json:"predicates,omitempty"`
	Expected              map[string]any     `json:"expected,omitempty"`
	ID                    ReleaseResourceID  `json:"id"`
	Object                map[string]any     `json:"object"`
	Digest                string             `json:"digest"`
	Phase                 int                `json:"phase"`
	Health                string             `json:"health"`
}

func releaseByteDigest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

// ReadReleaseFiles accepts only a trusted checkout root and relative path. An
// os.Root constrains every open even if a symlink is swapped after inspection.
func ReadReleaseFiles(base, relative string) ([]ReleaseInputFile, error) {
	if relative == "" || filepath.IsAbs(relative) || path.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../") || strings.ContainsAny(relative, "\x00:") {
		return nil, ErrManifest
	}
	root, e := os.OpenRoot(base)
	if e != nil {
		return nil, ErrManifest
	}
	defer root.Close()
	path := filepath.ToSlash(relative)
	// Reject links even when they would resolve within the checkout: the freeze
	// is a deterministic set of repository entries, not a host filesystem view.
	current := ""
	for _, part := range strings.Split(path, "/") {
		if current != "" {
			current += "/"
		}
		current += part
		st, e := root.Lstat(current)
		if e != nil || st.Mode()&os.ModeSymlink != 0 {
			return nil, ErrManifest
		}
	}
	st, e := root.Stat(path)
	if e != nil {
		return nil, ErrManifest
	}
	var names []string
	if st.IsDir() {
		e = fs.WalkDir(root.FS(), path, func(name string, d fs.DirEntry, err error) error {
			if err != nil || d.Type()&os.ModeSymlink != 0 {
				return ErrManifest
			}
			if !d.IsDir() {
				ext := strings.ToLower(filepath.Ext(name))
				if ext == ".yaml" || ext == ".yml" || ext == ".json" {
					names = append(names, name)
					if len(names) > MaxReleaseResources {
						return ErrManifest
					}
				}
			}
			return nil
		})
		if e != nil {
			return nil, ErrManifest
		}
	} else if st.Mode().IsRegular() {
		names = []string{path}
	} else {
		return nil, ErrManifest
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, ErrManifest
	}
	total := 0
	out := make([]ReleaseInputFile, 0, len(names))
	for _, name := range names {
		f, e := root.Open(name)
		if e != nil {
			return nil, ErrManifest
		}
		b, e := io.ReadAll(io.LimitReader(f, int64(MaxReleaseInputBytes-total)+1))
		f.Close()
		if e != nil || len(b) > MaxReleaseInputBytes-total {
			return nil, ErrManifest
		}
		total += len(b)
		out = append(out, ReleaseInputFile{Path: name, Content: b, Digest: releaseByteDigest(b)})
	}
	return out, nil
}
func InlineReleaseFile(content string) ([]ReleaseInputFile, error) {
	if len(content) == 0 || len(content) > MaxReleaseManifestBytes {
		return nil, ErrManifest
	}
	b := []byte(content)
	return []ReleaseInputFile{{Path: "inline.yaml", Content: b, Digest: releaseByteDigest(b)}}, nil
}

func variableNode(v ReleaseVariableValue) (*yaml.Node, error) {
	b, e := json.Marshal(v.Value)
	if e != nil {
		return nil, ErrVariable
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil {
		return nil, ErrVariable
	}
	valid := false
	switch v.Type {
	case "string":
		_, valid = decoded.(string)
	case "number":
		_, valid = decoded.(json.Number)
	case "boolean":
		_, valid = decoded.(bool)
	case "object":
		_, valid = decoded.(map[string]any)
	case "array":
		_, valid = decoded.([]any)
	case "null":
		valid = decoded == nil
	}
	if !valid {
		return nil, ErrVariable
	}
	var n yaml.Node
	if yaml.Unmarshal(b, &n) != nil || len(n.Content) != 1 {
		return nil, ErrVariable
	}
	return n.Content[0], nil
}
func cloneYAML(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = nil
	for _, v := range n.Content {
		c.Content = append(c.Content, cloneYAML(v))
	}
	return &c
}
func renderReleaseNode(n *yaml.Node, vars map[string]ReleaseVariableValue, sensitive *bool, depth int, count *int) error {
	*count++
	if depth > 48 || *count > 200000 || n.Alias != nil || n.Anchor != "" || n.Kind == yaml.AliasNode {
		return ErrManifest
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return ErrManifest
		}
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			if i+1 >= len(n.Content) {
				return ErrManifest
			}
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || placeholder.MatchString(k.Value) || seen[k.Value] {
				return ErrManifest
			}
			seen[k.Value] = true
		}
	case yaml.SequenceNode:
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!int", "!!float", "!!bool", "!!null":
		default:
			return ErrManifest
		}
		if n.Tag != "!!str" {
			return nil
		}
		return renderReleaseScalar(n, vars, sensitive, depth, count)
	default:
		return ErrManifest
	}
	for _, child := range n.Content {
		if e := renderReleaseNode(child, vars, sensitive, depth+1, count); e != nil {
			return e
		}
	}
	return nil
}

// Variable values are literal: a value containing another placeholder is not
// evaluated a second time, and typed values cannot inject YAML documents.
func renderReleaseNodeLiteral(n *yaml.Node, depth int, count *int) error {
	if depth > 48 {
		return ErrManifest
	}
	*count++
	if *count > 200000 {
		return ErrManifest
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			if i+1 >= len(n.Content) || n.Content[i].Tag != "!!str" || seen[n.Content[i].Value] {
				return ErrManifest
			}
			seen[n.Content[i].Value] = true
		}
	}
	for _, c := range n.Content {
		if e := renderReleaseNodeLiteral(c, depth+1, count); e != nil {
			return e
		}
	}
	return nil
}
func normalizeReleaseSecret(obj map[string]any) error {
	if obj["kind"] != "Secret" {
		return nil
	}
	data := map[string]any{}
	if raw, ok := obj["data"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return ErrManifest
		}
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				return ErrManifest
			}
			if _, e := base64.StdEncoding.DecodeString(s); e != nil {
				return ErrManifest
			}
			data[k] = s
		}
	}
	if raw, ok := obj["stringData"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return ErrManifest
		}
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				return ErrManifest
			}
			data[k] = base64.StdEncoding.EncodeToString([]byte(s))
		}
		delete(obj, "stringData")
	}
	obj["data"] = data
	return nil
}

func FreezeReleaseManifest(files []ReleaseInputFile, vars map[string]ReleaseVariableValue, discovery *ReleaseDiscovery, defaultNamespace string) (FrozenManifest, error) {
	return freezeReleaseManifest(files, vars, discovery, defaultNamespace, true)
}
func freezeReleaseManifest(files []ReleaseInputFile, vars map[string]ReleaseVariableValue, discovery *ReleaseDiscovery, defaultNamespace string, render bool) (FrozenManifest, error) {
	out := FrozenManifest{Files: files}
	if len(files) == 0 || len(files) > MaxReleaseResources || discovery == nil {
		return out, ErrManifest
	}
	total, rendered := 0, 0
	seen := map[string]bool{}
	for _, file := range files {
		total += len(file.Content)
		if total > MaxReleaseInputBytes || file.Digest != releaseByteDigest(file.Content) {
			return FrozenManifest{}, ErrManifest
		}
		decoder := yaml.NewDecoder(bytes.NewReader(file.Content))
		for {
			var n yaml.Node
			e := decoder.Decode(&n)
			if e == io.EOF {
				break
			}
			if e != nil {
				return FrozenManifest{}, ErrManifest
			}
			if len(n.Content) == 0 || n.Content[0].Tag == "!!null" {
				continue
			}
			count := 0
			if render {
				if releaseIdentitySecret(&n, vars, "") {
					return FrozenManifest{}, ErrVariable
				}
				e = renderReleaseNode(&n, vars, &out.Sensitive, 0, &count)
			} else {
				e = renderReleaseNodeLiteral(&n, 0, &count)
			}
			if e != nil {
				return FrozenManifest{}, e
			}
			var obj map[string]any
			if n.Decode(&obj) != nil || obj == nil {
				return FrozenManifest{}, ErrManifest
			}
			if _, ok := obj["status"]; ok {
				return FrozenManifest{}, ErrManifest
			}
			if normalizeReleaseSecret(obj) != nil {
				return FrozenManifest{}, ErrManifest
			}
			api, _ := obj["apiVersion"].(string)
			kind, _ := obj["kind"].(string)
			meta, ok := obj["metadata"].(map[string]any)
			if !ok {
				return FrozenManifest{}, ErrManifest
			}
			ann := releaseMap(meta["annotations"])
			if ann[ReleaseActionAnnotation] != nil || ann[OperationAnnotation] != nil {
				return FrozenManifest{}, ErrManifest
			}
			name, _ := meta["name"].(string)
			ns, namespaceString := meta["namespace"].(string)
			if meta["namespace"] != nil && !namespaceString {
				return FrozenManifest{}, ErrManifest
			}
			if !validDeploymentName(name) || meta["generateName"] != nil || meta["uid"] != nil || meta["resourceVersion"] != nil {
				return FrozenManifest{}, ErrManifest
			}
			resource, e := discovery.Resolve(api, kind)
			if e != nil {
				return FrozenManifest{}, e
			}
			if resource.Namespaced {
				if ns == "" {
					ns = defaultNamespace
					meta["namespace"] = ns
				}
				if !validName(ns, 63) {
					return FrozenManifest{}, ErrManifest
				}
			} else if ns != "" {
				return FrozenManifest{}, ErrManifest
			}
			id := ReleaseResourceID{Group: resource.Group, Version: resource.Version, Resource: resource.Name, Kind: kind, Namespace: ns, Name: name}
			if seen[id.Key()] {
				return FrozenManifest{}, ErrManifest
			}
			seen[id.Key()] = true
			b, e := json.Marshal(obj)
			if e != nil || len(b) > MaxReleaseObjectBytes {
				return FrozenManifest{}, ErrManifest
			}
			rendered += len(b)
			if rendered > MaxReleaseManifestBytes || len(out.Resources) >= MaxReleaseResources {
				return FrozenManifest{}, ErrManifest
			}
			if kind == "Secret" {
				out.Sensitive = true
			}
			adapter := ReleaseAdapterFor(resource.Group, kind)
			entry := ReleaseResource{ID: id, Object: obj, Digest: releaseByteDigest(b), Phase: adapter.Phase, Health: adapter.Health}
			if crd, ok := discovery.proposed[api+"/"+kind]; ok {
				copy := crd
				entry.RequiredCRD = &copy
			}
			out.Resources = append(out.Resources, entry)
		}
	}
	if len(out.Resources) == 0 {
		return FrozenManifest{}, ErrManifest
	}
	sort.SliceStable(out.Resources, func(i, j int) bool {
		if out.Resources[i].Phase != out.Resources[j].Phase {
			return out.Resources[i].Phase < out.Resources[j].Phase
		}
		return out.Resources[i].ID.Key() < out.Resources[j].ID.Key()
	})
	return out, nil
}

func renderReleaseScalar(n *yaml.Node, vars map[string]ReleaseVariableValue, sensitive *bool, depth int, count *int) error {
	text := n.Value
	matches := placeholder.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	type token struct {
		start, end int
		key        string
		escaped    bool
	}
	tokens := make([]token, 0, len(matches))
	for _, m := range matches {
		item := token{start: m[0], end: m[1]}
		if m[2] >= 0 {
			item.key = text[m[2]:m[3]]
			if m[0] > 0 && text[m[0]-1] == '$' {
				item.escaped = true
				item.start--
			}
		} else {
			item.key = text[m[4]:m[5]]
		}
		tokens = append(tokens, item)
	}
	if len(tokens) == 1 && !tokens[0].escaped && tokens[0].start == 0 && tokens[0].end == len(text) {
		v, ok := vars[tokens[0].key]
		if !ok {
			return ErrVariable
		}
		replacement, e := variableNode(v)
		if e != nil {
			return e
		}
		*sensitive = *sensitive || v.Secret
		*n = *cloneYAML(replacement)
		return renderReleaseNodeLiteral(n, depth, count)
	}
	var out strings.Builder
	offset := 0
	for _, item := range tokens {
		out.WriteString(text[offset:item.start])
		if item.escaped {
			out.WriteString(text[item.start+1 : item.end])
		} else {
			v, ok := vars[item.key]
			if !ok {
				return ErrVariable
			}
			if v.Type == "object" || v.Type == "array" || v.Type == "null" {
				return ErrVariable
			}
			literal, e := variableNode(v)
			if e != nil {
				return e
			}
			out.WriteString(literal.Value)
			*sensitive = *sensitive || v.Secret
		}
		offset = item.end
	}
	out.WriteString(text[offset:])
	n.Value = out.String()
	return nil
}
