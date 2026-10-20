package mcp

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/run"
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var repositoryPartPattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// ParseHostname accepts an authority, not a URL, wildcard, userinfo or path.
// Matching uses its exact lowercase hostname; suffix matching is never used.
func ParseHostname(authority string) (string, error) {
	if authority == "" || len(authority) > 260 || strings.ContainsAny(authority, "/\\@?#%, \t\n\r") {
		return "", ErrInvalidArgument
	}
	u, err := url.Parse("http://" + authority)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host != authority {
		return "", ErrInvalidArgument
	}
	host := u.Hostname()
	if strings.HasSuffix(authority, ":") {
		return "", ErrInvalidArgument
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrInvalidArgument
		}
	}
	if strings.Contains(host, ":") {
		if !strings.HasPrefix(authority, "[") || net.ParseIP(host) == nil {
			return "", ErrInvalidArgument
		}
	} else {
		if strings.ContainsAny(authority, "[]") || len(host) > 253 {
			return "", ErrInvalidArgument
		}
		for _, label := range strings.Split(host, ".") {
			if !hostnamePattern.MatchString(label) {
				return "", ErrInvalidArgument
			}
		}
	}
	return strings.ToLower(host), nil
}

func safeImageVersion(a run.Artifact) (any, any) {
	if a.Type != run.ArtifactImage || len(a.Reference) > 255 {
		return nil, nil
	}
	ref := a.Reference
	if strings.TrimSpace(ref) != ref || strings.ContainsAny(ref, "?#%\\ \t\r\n") || strings.Contains(ref, "://") {
		return nil, nil
	}
	repo, digest, pinned := strings.Cut(ref, "@")
	if pinned && !digestPattern.MatchString(digest) {
		return nil, nil
	}
	if !pinned {
		i := strings.LastIndex(repo, ":")
		if i <= strings.LastIndex(repo, "/") || !tagPattern.MatchString(repo[i+1:]) {
			return nil, nil
		}
		repo = repo[:i]
	}
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return nil, nil
	}
	if _, err := ParseHostname(parts[0]); err != nil {
		return nil, nil
	}
	for _, part := range parts[1:] {
		if !repositoryPartPattern.MatchString(part) {
			return nil, nil
		}
	}
	if pinned {
		return ref, digest
	}
	// A build's local config digest is not proof of a pushed manifest.
	if a.Metadata["pushed"] == true && a.Metadata["digestRepository"] == repo {
		if digest, ok := a.Metadata["digest"].(string); ok && digestPattern.MatchString(digest) {
			return ref, digest
		}
	}
	return ref, nil
}
