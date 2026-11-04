package kubernetes

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type kubeConfig struct {
	APIVersion     string            `yaml:"apiVersion" json:"apiVersion"`
	Kind           string            `yaml:"kind" json:"kind"`
	Preferences    map[string]string `yaml:"preferences,omitempty" json:"preferences,omitempty"`
	Clusters       []namedCluster    `yaml:"clusters" json:"clusters"`
	Users          []namedUser       `yaml:"users" json:"users"`
	Contexts       []namedContext    `yaml:"contexts" json:"contexts"`
	CurrentContext string            `yaml:"current-context" json:"current-context"`
}
type namedCluster struct {
	Name    string  `yaml:"name" json:"name"`
	Cluster cluster `yaml:"cluster" json:"cluster"`
}
type cluster struct {
	Server        string `yaml:"server" json:"server"`
	CAData        string `yaml:"certificate-authority-data" json:"certificate-authority-data"`
	TLSServerName string `yaml:"tls-server-name,omitempty" json:"tls-server-name,omitempty"`
}
type namedUser struct {
	Name string `yaml:"name" json:"name"`
	User user   `yaml:"user" json:"user"`
}
type user struct {
	Token    string `yaml:"token,omitempty" json:"token,omitempty"`
	CertData string `yaml:"client-certificate-data,omitempty" json:"client-certificate-data,omitempty"`
	KeyData  string `yaml:"client-key-data,omitempty" json:"client-key-data,omitempty"`
}
type namedContext struct {
	Name    string      `yaml:"name" json:"name"`
	Context kubeContext `yaml:"context" json:"context"`
}
type kubeContext struct {
	Cluster   string `yaml:"cluster" json:"cluster"`
	User      string `yaml:"user" json:"user"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// PreviewCredential strictly parses input locally and returns only trust metadata.
// No process, file, network or secret persistence occurs here.
func PreviewCredential(in CredentialInput) (CredentialPreview, error) {
	raw, p, e := normalizeCredential(in)
	clear(raw)
	return p, e
}
func normalizeCredential(in CredentialInput) ([]byte, CredentialPreview, error) {
	var zero CredentialPreview
	if !cleanText(in.Name, 120) || len(in.Kubeconfig)+len(in.Server)+len(in.CA)+len(in.Token)+len(in.Context)+len(in.TLSServerName) > MaxImportBytes {
		return nil, zero, ErrInvalid
	}
	var c kubeConfig
	switch in.Mode {
	case "server_token":
		if in.Kubeconfig != "" || !cleanText(in.Context, 128) {
			return nil, zero, ErrInvalid
		}
		c = kubeConfig{APIVersion: "v1", Kind: "Config", CurrentContext: in.Context, Clusters: []namedCluster{{"cluster", cluster{in.Server, base64.StdEncoding.EncodeToString([]byte(in.CA)), in.TLSServerName}}}, Users: []namedUser{{"identity", user{Token: in.Token}}}, Contexts: []namedContext{{in.Context, kubeContext{Cluster: "cluster", User: "identity"}}}}
	case "kubeconfig":
		if in.Server != "" || in.CA != "" || in.Token != "" || in.TLSServerName != "" || len(in.Kubeconfig) == 0 || len(in.Kubeconfig) > MaxImportBytes {
			return nil, zero, ErrInvalid
		}
		data := []byte(in.Kubeconfig)
		defer clear(data)
		var doc yaml.Node
		if e := yaml.Unmarshal(data, &doc); e != nil {
			return nil, zero, ErrInvalid
		}
		nodes := 0
		if !safeYAML(&doc, 0, &nodes) {
			return nil, zero, ErrInvalid
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if e := dec.Decode(&c); e != nil {
			return nil, zero, ErrInvalid
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return nil, zero, ErrInvalid
		}
	default:
		return nil, zero, ErrInvalid
	}
	if c.APIVersion != "v1" || c.Kind != "Config" || len(c.Preferences) > 0 || len(c.Clusters) == 0 || len(c.Clusters) > 128 || len(c.Users) == 0 || len(c.Users) > 128 || len(c.Contexts) == 0 || len(c.Contexts) > 128 {
		return nil, zero, ErrInvalid
	}
	chosen := in.Context
	if chosen == "" {
		chosen = c.CurrentContext
	}
	if !cleanText(chosen, 128) {
		return nil, zero, ErrInvalid
	}
	contexts := map[string]kubeContext{}
	clusters := map[string]cluster{}
	users := map[string]user{}
	for _, v := range c.Contexts {
		if !cleanText(v.Name, 128) || !cleanText(v.Context.Cluster, 128) || !cleanText(v.Context.User, 128) || (v.Context.Namespace != "" && !validName(v.Context.Namespace, 63)) {
			return nil, zero, ErrInvalid
		}
		if _, ok := contexts[v.Name]; ok {
			return nil, zero, ErrInvalid
		}
		contexts[v.Name] = v.Context
	}
	for _, v := range c.Clusters {
		if !cleanText(v.Name, 128) {
			return nil, zero, ErrInvalid
		}
		if _, ok := clusters[v.Name]; ok {
			return nil, zero, ErrInvalid
		}
		if _, e := validateCluster(v.Cluster); e != nil {
			return nil, zero, ErrInvalid
		}
		clusters[v.Name] = v.Cluster
	}
	for _, v := range c.Users {
		if !cleanText(v.Name, 128) {
			return nil, zero, ErrInvalid
		}
		if _, ok := users[v.Name]; ok {
			return nil, zero, ErrInvalid
		}
		if e := validateUser(v.User); e != nil {
			return nil, zero, ErrInvalid
		}
		users[v.Name] = v.User
	}
	for _, v := range contexts {
		if _, ok := clusters[v.Cluster]; !ok {
			return nil, zero, ErrInvalid
		}
		if _, ok := users[v.User]; !ok {
			return nil, zero, ErrInvalid
		}
	}
	selected, ok := contexts[chosen]
	if !ok {
		return nil, zero, ErrInvalid
	}
	cl := clusters[selected.Cluster]
	u := users[selected.User]
	p, e := validateCluster(cl)
	if e != nil {
		return nil, zero, e
	}
	p.Mode = in.Mode
	p.Context = chosen
	cl.Server = p.Server
	normalized := kubeConfig{APIVersion: "v1", Kind: "Config", CurrentContext: chosen, Clusters: []namedCluster{{"cluster", cl}}, Users: []namedUser{{"identity", u}}, Contexts: []namedContext{{chosen, kubeContext{Cluster: "cluster", User: "identity"}}}}
	// JSON is a valid kubeconfig and ensures no aliases/implicit YAML typing survive.
	raw, e := json.Marshal(normalized)
	if e != nil || len(raw) > MaxCredentialBytes {
		clear(raw)
		return nil, zero, ErrInvalid
	}
	return raw, p, nil
}
func safeYAML(n *yaml.Node, depth int, count *int) bool {
	*count++
	if depth > 32 || *count > 8192 || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return false
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
				return false
			}
			seen[k.Value] = true
		}
	}
	for _, v := range n.Content {
		if !safeYAML(v, depth+1, count) {
			return false
		}
	}
	return true
}
func validateCluster(c cluster) (CredentialPreview, error) {
	p := CredentialPreview{}
	server, e := validEndpoint(c.Server)
	if e != nil {
		return p, e
	}
	if c.TLSServerName != "" {
		if !cleanText(c.TLSServerName, 253) || strings.ContainsAny(c.TLSServerName, "/:@\\ ") {
			return p, ErrInvalid
		}
	}
	raw, e := base64.StdEncoding.DecodeString(c.CAData)
	if e != nil || len(raw) == 0 {
		return p, ErrInvalid
	}
	rest := raw
	hashes := []string{}
	for len(bytes.TrimSpace(rest)) > 0 {
		b, r := pem.Decode(rest)
		if b == nil || b.Type != "CERTIFICATE" || len(b.Headers) > 0 {
			return p, ErrInvalid
		}
		cert, e := x509.ParseCertificate(b.Bytes)
		if e != nil || !cert.IsCA {
			return p, ErrInvalid
		}
		h := sha256.Sum256(cert.Raw)
		hashes = append(hashes, hex.EncodeToString(h[:]))
		rest = r
	}
	if len(hashes) == 0 {
		return p, ErrInvalid
	}
	sort.Strings(hashes)
	unique := hashes[:0]
	for _, h := range hashes {
		if len(unique) == 0 || unique[len(unique)-1] != h {
			unique = append(unique, h)
		}
	}
	hashes = unique
	p.Server = server
	p.TLSServerName = c.TLSServerName
	p.CAFingerprint = fingerprint(hashes)
	p.ClusterKey = fingerprint([]string{server, p.CAFingerprint, c.TLSServerName})
	return p, nil
}
func validateUser(u user) error {
	if u.Token != "" {
		if u.CertData != "" || u.KeyData != "" || len(u.Token) > 16384 {
			return ErrInvalid
		}
		for _, c := range u.Token {
			if c <= 32 || c >= 127 {
				return ErrInvalid
			}
		}
		return nil
	}
	cert, e := base64.StdEncoding.DecodeString(u.CertData)
	if e != nil || len(cert) == 0 {
		return ErrInvalid
	}
	key, e := base64.StdEncoding.DecodeString(u.KeyData)
	if e != nil || len(key) == 0 {
		return ErrInvalid
	}
	defer clear(key)
	if _, e = tls.X509KeyPair(cert, key); e != nil {
		return ErrInvalid
	}
	return nil
}
