// Package releasesource defines the explicitly chosen, trusted release channels.
// It performs no network I/O until a caller explicitly checks or downloads a release.
package releasesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ID string

const (
	GitHub      ID    = "github"
	Gitee       ID    = "gitee"
	MarkerName        = "pipewright-release.json"
	MaxMetadata int64 = 1 << 20
)

var (
	ErrInvalid      = errors.New("release source: invalid request")
	ErrUnavailable  = errors.New("release source: unavailable")
	ErrConflict     = errors.New("release source: settings conflict")
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	assetTagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)
	tagPattern      = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	commitPattern   = regexp.MustCompile(`^[a-f0-9]{40}$`)
)

func ValidID(id ID) bool { return id == GitHub || id == Gitee }
func Normalize(id ID) ID {
	if id == "" {
		return GitHub
	}
	return id
}
func ValidRepository(repo string) bool {
	if !repoPattern.MatchString(repo) {
		return false
	}
	for _, segment := range strings.Split(repo, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
func StableTag(tag string) bool { return tagPattern.MatchString(tag) }

type Config struct {
	GitHubRepository string
	GiteeRepository  string
	// TestBaseURL is code-injected, loopback-only, never accepted from an API/env URL.
	TestBaseURL string
}
type Catalog struct{ config Config }

func New() *Catalog {
	github := strings.TrimSpace(os.Getenv("PIPEWRIGHT_RELEASE_REPO"))
	if github == "" {
		github = "huangchengsir/pipewright"
	}
	return NewCatalog(Config{GitHubRepository: github, GiteeRepository: strings.TrimSpace(os.Getenv("PIPEWRIGHT_GITEE_RELEASE_REPO"))})
}
func NewCatalog(config Config) *Catalog { return &Catalog{config: config} }

type Descriptor struct {
	ID         ID     `json:"id"`
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
}

func (c *Catalog) Sources() []Descriptor {
	out := []Descriptor{}
	for _, id := range []ID{GitHub, Gitee} {
		repo := c.Repository(id)
		name := "GitHub"
		if id == Gitee {
			name = "Gitee"
		}
		d := Descriptor{ID: id, Name: name, Repository: repo, Available: ValidRepository(repo)}
		if !d.Available {
			d.Repository = ""
			d.Reason = "not_configured"
		}
		out = append(out, d)
	}
	return out
}
func (c *Catalog) Repository(id ID) string {
	if c == nil {
		return ""
	}
	if id == GitHub {
		return c.config.GitHubRepository
	}
	if id == Gitee {
		return c.config.GiteeRepository
	}
	return ""
}
func (c *Catalog) Resolve(id ID) (string, error) {
	if !ValidID(id) {
		return "", ErrInvalid
	}
	repo := c.Repository(id)
	if !ValidRepository(repo) || c.config.TestBaseURL != "" && !LoopbackBase(c.config.TestBaseURL) {
		return "", ErrUnavailable
	}
	return repo, nil
}
func LoopbackBase(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}
func WebBase(id ID) string {
	if id == Gitee {
		return "https://gitee.com"
	}
	return "https://github.com"
}
func AssetURL(id ID, repo, tag, name, testBase string) (string, error) {
	if !ValidID(id) || !ValidRepository(repo) || !assetTagPattern.MatchString(tag) || strings.ContainsAny(name, "/\\?#") || name == "" || name == "." || name == ".." {
		return "", ErrInvalid
	}
	base := WebBase(id)
	if testBase != "" {
		if !LoopbackBase(testBase) {
			return "", ErrInvalid
		}
		base = strings.TrimRight(testBase, "/")
	}
	return base + "/" + repo + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name), nil
}
func trustedHost(id ID, host string) bool {
	host = strings.ToLower(host)
	if id == Gitee {
		return host == "gitee.com" || host == "foruda.gitee.com"
	}
	return host == "github.com" || host == "api.github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}
func Do(client *http.Client, req *http.Request, testBase string, id ID) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if !ValidID(id) {
		return nil, ErrInvalid
	}
	allowed := func(u *url.URL) bool {
		if u.User != nil || u.Fragment != "" {
			return false
		}
		if testBase != "" {
			return LoopbackBase(u.Scheme + "://" + u.Host)
		}
		return u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && trustedHost(id, u.Hostname())
	}
	if !allowed(req.URL) {
		return nil, ErrInvalid
	}
	cl := *client
	old := cl.CheckRedirect
	cl.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !allowed(next.URL) || len(via) > 0 && via[0].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return ErrInvalid
		}
		if old != nil {
			return old(next, via)
		}
		return nil
	}
	return cl.Do(req)
}

type Marker struct {
	SchemaVersion   int               `json:"schemaVersion"`
	Tag             string            `json:"tag"`
	Commit          string            `json:"commit"`
	ChecksumsSHA256 string            `json:"checksumsSha256"`
	Assets          map[string]string `json:"assets"`
}

func AssetNames(tag string) []string {
	v := strings.TrimPrefix(tag, "v")
	out := []string{}
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"} {
		ext := ".tar.gz"
		if strings.HasPrefix(platform, "windows_") {
			ext = ".zip"
		}
		out = append(out, "pipewright_"+v+"_"+platform+ext)
	}
	return out
}
func ParseMarker(body []byte, tag, commit string) (*Marker, error) {
	if validateJSON(body) != nil {
		return nil, ErrInvalid
	}
	// encoding/json otherwise accepts case-insensitive aliases for struct fields.
	// The portable marker contract has exactly these five, case-sensitive keys.
	var keys map[string]json.RawMessage
	if json.Unmarshal(body, &keys) != nil || len(keys) != 5 {
		return nil, ErrInvalid
	}
	for _, key := range []string{"schemaVersion", "tag", "commit", "checksumsSha256", "assets"} {
		if _, ok := keys[key]; !ok {
			return nil, ErrInvalid
		}
	}

	if len(body) > int(MaxMetadata) {
		return nil, ErrInvalid
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	var m Marker
	if dec.Decode(&m) != nil || dec.Decode(new(any)) != io.EOF || m.SchemaVersion != 1 || !StableTag(tag) || m.Tag != tag || !commitPattern.MatchString(m.Commit) || m.Commit != commit || !digestPattern.MatchString(m.ChecksumsSHA256) || len(m.Assets) != 6 {
		return nil, ErrInvalid
	}
	for _, name := range AssetNames(tag) {
		if !digestPattern.MatchString(m.Assets[name]) {
			return nil, ErrInvalid
		}
	}
	return &m, nil
}

// VerifyChecksums binds all six original archive entries, not merely the running platform.
func (m *Marker) VerifyChecksums(body []byte) error {
	if len(body) > int(MaxMetadata) {
		return ErrInvalid
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != m.ChecksumsSHA256 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || !digestPattern.MatchString(strings.ToLower(fields[0])) {
			return ErrInvalid
		}
		name := strings.TrimPrefix(fields[1], "*")
		if seen[name] || strings.ContainsAny(name, "/\\") || m.Assets[name] != strings.ToLower(fields[0]) {
			return ErrInvalid
		}
		seen[name] = true
	}
	if len(seen) != 6 {
		return ErrInvalid
	}
	return nil
}

type GiteeRelease struct {
	Tag        string `json:"tag_name"`
	Commit     string `json:"target_commitish"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	Notes      string `json:"body"`
	CreatedAt  string `json:"created_at"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}
type VerifiedRelease struct {
	Release   GiteeRelease
	Marker    *Marker
	Checksums []byte
}

func (c *Catalog) get(ctx context.Context, client *http.Client, u string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("User-Agent", "pipewright-release-source")
	req.Header.Set("Accept", "application/json")
	res, e := Do(client, req, c.config.TestBaseURL, Gitee)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, ErrUnavailable
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, MaxMetadata+1))
	if e != nil || int64(len(body)) > MaxMetadata {
		return nil, ErrInvalid
	}
	return body, nil
}
func (c *Catalog) VerifyGitee(ctx context.Context, client *http.Client, tag string) (*VerifiedRelease, error) {
	repo, e := c.Resolve(Gitee)
	if e != nil {
		return nil, e
	}
	base := "https://gitee.com"
	if c.config.TestBaseURL != "" {
		base = strings.TrimRight(c.config.TestBaseURL, "/")
	}
	var rel GiteeRelease
	if tag != "" {
		if !StableTag(tag) {
			return nil, ErrInvalid
		}
		raw, e := c.get(ctx, client, base+"/api/v5/repos/"+repo+"/releases/tags/"+url.PathEscape(tag))
		if e != nil {
			return nil, e
		}
		if validateJSON(raw) != nil || json.Unmarshal(raw, &rel) != nil {
			return nil, ErrInvalid
		}
	} else {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ctx = bounded
		found, complete := false, false
		for page := 1; page <= 100; page++ {
			raw, e := c.get(ctx, client, base+"/api/v5/repos/"+repo+"/releases?page="+strconv.Itoa(page)+"&per_page=20")
			if e != nil {
				return nil, e
			}
			var releases []GiteeRelease
			if validateJSON(raw) != nil || json.Unmarshal(raw, &releases) != nil || len(releases) > 20 {
				return nil, ErrInvalid
			}
			for _, candidate := range releases {
				if candidate.Draft || candidate.Prerelease || !StableTag(candidate.Tag) {
					continue
				}
				if !found || compareStableTags(candidate.Tag, rel.Tag) > 0 {
					rel = candidate
					found = true
				}
			}
			if len(releases) < 20 {
				complete = true
				break
			}
		}
		if !complete || !found {
			return nil, ErrUnavailable
		}
	}
	if rel.Draft || rel.Prerelease || !StableTag(rel.Tag) || tag != "" && tag != rel.Tag || !commitPattern.MatchString(rel.Commit) {
		return nil, ErrInvalid
	}
	names := map[string]bool{}
	for _, a := range rel.Assets {
		if names[a.Name] {
			return nil, ErrInvalid
		}
		names[a.Name] = true
	}
	for _, name := range append(AssetNames(rel.Tag), "checksums.txt", MarkerName) {
		if !names[name] {
			return nil, ErrInvalid
		}
	}
	markerURL, _ := AssetURL(Gitee, repo, rel.Tag, MarkerName, c.config.TestBaseURL)
	raw, e := c.get(ctx, client, markerURL)
	if e != nil {
		return nil, e
	}
	marker, e := ParseMarker(raw, rel.Tag, rel.Commit)
	if e != nil {
		return nil, e
	}
	sumsURL, _ := AssetURL(Gitee, repo, rel.Tag, "checksums.txt", c.config.TestBaseURL)
	sums, e := c.get(ctx, client, sumsURL)
	if e != nil {
		return nil, e
	}
	if e = marker.VerifyChecksums(sums); e != nil {
		return nil, e
	}
	return &VerifiedRelease{Release: rel, Marker: marker, Checksums: sums}, nil
}
func (c *Catalog) ForTestBase(base string) (*Catalog, error) {
	if !LoopbackBase(base) {
		return nil, ErrInvalid
	}
	cfg := c.config
	cfg.TestBaseURL = base
	return NewCatalog(cfg), nil
}
func (c *Catalog) ReleaseURL(id ID, tag string) string {
	return fmt.Sprintf("%s/%s/releases/tag/%s", WebBase(id), c.Repository(id), url.PathEscape(tag))
}

// validateJSON rejects ambiguous duplicate object keys before typed decoding.
// Metadata is untrusted: both byte size and nesting/token budgets are bounded.
func validateJSON(body []byte) error {
	if int64(len(body)) > MaxMetadata {
		return ErrInvalid
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	tokens := 0
	var value func(int) error
	value = func(depth int) error {
		if depth > 16 {
			return ErrInvalid
		}
		token, e := dec.Token()
		tokens++
		if e != nil || tokens > 65536 {
			return ErrInvalid
		}
		delimiter, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				rawKey, e := dec.Token()
				tokens++
				if e != nil || tokens > 65536 {
					return ErrInvalid
				}
				key, ok := rawKey.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			close, e := dec.Token()
			tokens++
			if e != nil || close != json.Delim('}') {
				return ErrInvalid
			}
		case '[':
			for dec.More() {
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			close, e := dec.Token()
			tokens++
			if e != nil || close != json.Delim(']') {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		return nil
	}
	if value(0) != nil {
		return ErrInvalid
	}
	if _, e := dec.Token(); e != io.EOF {
		return ErrInvalid
	}
	return nil
}

// compareStableTags orders numeric cores without fixed-width integer overflow.
// Equivalent numeric versions use a deterministic lexical tag tie-breaker.
func compareStableTags(a, b string) int {
	ap := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bp := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < 3; i++ {
		an := strings.TrimLeft(ap[i], "0")
		bn := strings.TrimLeft(bp[i], "0")
		if len(an) < len(bn) {
			return -1
		}
		if len(an) > len(bn) {
			return 1
		}
		if c := strings.Compare(an, bn); c != 0 {
			return c
		}
	}
	return strings.Compare(a, b)
}
