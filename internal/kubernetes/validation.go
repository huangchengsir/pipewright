package kubernetes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var absoluteToolPath = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var digestReference = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[a-f0-9]{64}$`)
var fullHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var imageValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,511}$`)
var imageID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func cleanText(v string, max int) bool {
	if strings.TrimSpace(v) == "" || len(v) > max || v != strings.TrimSpace(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validDeploymentName(v string) bool {
	if len(v) > 253 {
		return false
	}
	for _, part := range strings.Split(v, ".") {
		if !validName(part, 63) {
			return false
		}
	}
	return true
}
func validID(v string) bool                  { return identifier.MatchString(v) }
func validName(v string, max int) bool       { return len(v) <= max && dnsLabel.MatchString(v) }
func IsProtectedCredentialID(id string) bool { return strings.HasPrefix(id, CredentialPrefix) }

// ValidateGenericCredentialReference rejects this namespace before generic
// consumers can resolve it. A protected credential has no legacy table row.
func ValidateGenericCredentialReference(id string) error {
	if IsProtectedCredentialID(id) {
		return ErrDenied
	}
	return nil
}
func ValidImageReference(v string) bool {
	if len(v) > 512 || !digestReference.MatchString(v) {
		return false
	}
	repo := strings.Split(v, "@")[0]
	if !strings.Contains(repo, "/") || strings.Contains(repo, "//") || strings.HasSuffix(repo, "/") {
		return false
	}
	return true
}
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validExecutor(v Executor) bool {
	if v.Kind == "local" {
		return v.ServerID == "" && v.HostKeySHA256 == ""
	}
	if v.Kind != "ssh" || !validID(v.ServerID) || !strings.HasPrefix(v.HostKeySHA256, "SHA256:") {
		return false
	}
	b, e := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(v.HostKeySHA256, "SHA256:"))
	return e == nil && len(b) == 32
}
func validateProfile(p ToolProfile) error {
	if p.Protocol != ProfileProtocol || !ValidImageReference(p.Image) || (p.Architecture != "amd64" && p.Architecture != "arm64") || p.UID <= 0 || p.UID > 2147483647 {
		return ErrInvalid
	}
	if !absoluteToolPath.MatchString(p.KubectlPath) || path.Clean(p.KubectlPath) != p.KubectlPath || len(p.KubectlPath) > 256 || strings.ContainsAny(p.KubectlPath, " \t\r\n\x00") {
		return ErrInvalid
	}
	if p.Network != "bridge" {
		return ErrInvalid
	} // No host or project service networks.
	if p.RegistryCredentialID != "" && (!validID(p.RegistryCredentialID) || IsProtectedCredentialID(p.RegistryCredentialID)) {
		return ErrInvalid
	}
	return nil
}
func validateTarget(t Target) error {
	if !validID(t.ProjectID) || !cleanText(t.Environment, 128) || !validID(t.ConnectionID) || !IsProtectedCredentialID(t.CredentialID) || !validID(t.CredentialID) || !cleanText(t.Context, 128) || !validName(t.Namespace, 63) || !validDeploymentName(t.Deployment) || !validName(t.Container, 63) || !validExecutor(t.Executor) {
		return ErrInvalid
	}
	return nil
}
func validEndpoint(raw string) (string, error) {
	if !cleanText(raw, 2048) || strings.ContainsAny(raw, "\\\t\r\n ") {
		return "", ErrInvalid
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", ErrInvalid
	}
	if u.RawPath != "" || (u.Path != "" && path.Clean(u.Path) != u.Path) {
		return "", ErrInvalid
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", ErrInvalid
		}
		if n == 443 {
			port = ""
		} else {
			port = strconv.Itoa(n)
		}
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", ErrInvalid
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), nil
}
