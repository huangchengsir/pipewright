package build

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

var errReleaseImage = errors.New("kubernetes_image_resolution_failed")

func releaseRegistryReference(reference string) (origin, repository, selector string, err error) {
	raw := reference
	scheme := "https"
	if strings.HasPrefix(raw, "https://") {
		raw = strings.TrimPrefix(raw, "https://")
	} else if strings.HasPrefix(raw, "http://") {
		raw = strings.TrimPrefix(raw, "http://")
		scheme = "http"
	}
	if strings.ContainsAny(raw, "\\?#%\x00\r\n\t ") {
		return "", "", "", errReleaseImage
	}
	parts := strings.SplitN(raw, "/", 2)
	host := "registry-1.docker.io"
	repository = raw
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		host = parts[0]
		repository = parts[1]
	}
	if strings.EqualFold(host, "docker.io") {
		host = "registry-1.docker.io"
	}
	if host == "registry-1.docker.io" && !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	selector = "latest"
	if pos := strings.LastIndex(repository, "@"); pos >= 0 {
		selector = repository[pos+1:]
		repository = repository[:pos]
		if !imageManifestDigest.MatchString(selector) {
			return "", "", "", errReleaseImage
		}
	} else if pos := strings.LastIndex(repository, ":"); pos > strings.LastIndex(repository, "/") {
		selector = repository[pos+1:]
		repository = repository[:pos]
		if !imageTagPattern.MatchString(selector) {
			return "", "", "", errReleaseImage
		}
	}
	if !validImageRepository(repository) {
		return "", "", "", errReleaseImage
	}
	parsed, e := url.Parse(scheme + "://" + host)
	if e != nil || parsed.User != nil || parsed.Host == "" {
		return "", "", "", errReleaseImage
	}
	if _, _, e = parseImageRegistry(host); e != nil {
		return "", "", "", errReleaseImage
	}
	if scheme == "http" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", "", "", errReleaseImage
		}
	}
	return scheme + "://" + host, repository, selector, nil
}
func resolveReleaseRegistryImage(ctx context.Context, reference, user, password string) (string, error) {
	if kubernetes.ValidImageReference(reference) {
		return reference, nil
	}
	origin, repo, selector, e := releaseRegistryReference(reference)
	if e != nil {
		return "", e
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := origin + "/v2/" + repo + "/manifests/" + url.PathEscape(selector)
	fetch := func(token string) (*http.Response, error) {
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if e != nil {
			return nil, e
		}
		request.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		} else if user != "" || password != "" {
			request.SetBasicAuth(user, password)
		}
		return client.Do(request)
	}
	response, e := fetch("")
	if e != nil {
		return "", errReleaseImage
	}
	if response.StatusCode == http.StatusUnauthorized {
		challenge := response.Header.Get("WWW-Authenticate")
		response.Body.Close()
		if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
			return "", errReleaseImage
		}
		_, params, e := mime.ParseMediaType("Bearer; " + strings.ReplaceAll(strings.TrimSpace(challenge[7:]), ",", ";"))
		if e != nil {
			return "", errReleaseImage
		}
		realm, e := url.Parse(params["realm"])
		base, _ := url.Parse(origin)
		if e != nil || realm.Scheme != "https" || realm.User != nil || realm.Fragment != "" || (realm.Host != base.Host && !(base.Host == "registry-1.docker.io" && realm.Host == "auth.docker.io")) {
			return "", errReleaseImage
		}
		query := realm.Query()
		query.Set("service", params["service"])
		query.Set("scope", "repository:"+repo+":pull")
		realm.RawQuery = query.Encode()
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
		if e != nil {
			return "", errReleaseImage
		}
		if user != "" || password != "" {
			request.SetBasicAuth(user, password)
		}
		tokenResponse, e := client.Do(request)
		if e != nil {
			return "", errReleaseImage
		}
		body, e := io.ReadAll(io.LimitReader(tokenResponse.Body, 64<<10))
		tokenResponse.Body.Close()
		defer clear(body)
		if e != nil || len(body) >= 64<<10 || tokenResponse.StatusCode != 200 {
			return "", errReleaseImage
		}
		var token struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal(body, &token) != nil {
			return "", errReleaseImage
		}
		if token.Token == "" {
			token.Token = token.AccessToken
		}
		if token.Token == "" || strings.ContainsAny(token.Token, "\r\n") {
			return "", errReleaseImage
		}
		response, e = fetch(token.Token)
		if e != nil {
			return "", errReleaseImage
		}
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	defer clear(body)
	if e != nil || response.StatusCode != 200 || len(body) > 4<<20 {
		return "", errReleaseImage
	}
	var manifest struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
	}
	if json.Unmarshal(body, &manifest) != nil || manifest.SchemaVersion != 2 {
		return "", errReleaseImage
	}
	switch manifest.MediaType {
	case "application/vnd.oci.image.index.v1+json", "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.docker.distribution.manifest.v2+json":
	default:
		return "", errReleaseImage
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if stated := response.Header.Get("Docker-Content-Digest"); stated != "" && stated != digest {
		return "", errReleaseImage
	}
	if strings.HasPrefix(selector, "sha256:") && selector != digest {
		return "", errReleaseImage
	}
	base, _ := url.Parse(origin)
	return base.Host + "/" + repo + "@" + digest, nil
}
func (b *Builder) releaseImageUpdates(ctx context.Context, cfg pipeline.KubernetesReleaseConfig, ex runretry.Execution, node runretry.NodeRef) ([]kubernetes.ReleaseImageUpdate, error) {
	inputs, e := ex.Inputs(ctx, node)
	if e != nil {
		return nil, e
	}
	updates := []kubernetes.ReleaseImageUpdate{}
	for _, binding := range cfg.Images {
		image := ""
		switch binding.Source.Kind {
		case "external":
			user, password := "", ""
			if binding.Source.CredentialID != "" && !kubernetes.ValidImageReference(binding.Source.Reference) {
				if b.vault == nil || kubernetes.IsProtectedCredentialID(binding.Source.CredentialID) {
					return nil, errReleaseImage
				}
				secret, e := b.vault.Reveal(binding.Source.CredentialID)
				if e != nil || secret == "" {
					return nil, errReleaseImage
				}
				user, password, _ = strings.Cut(secret, ":")
				if password == "" {
					user = ""
					password = secret
				}
			}
			image, e = resolveReleaseRegistryImage(ctx, binding.Source.Reference, user, password)
			if e != nil {
				return nil, e
			}
		case "artifact":
			if binding.Source.Artifact == nil || b.artifactLister == nil {
				return nil, errReleaseImage
			}
			src := binding.Source.Artifact
			producer, ok := ex.Node(src.StageID, src.JobID)
			if !ok {
				return nil, errReleaseImage
			}
			matches := 0
			for _, candidate := range inputs.Images {
				index, valid := sourceDeclarationIndex(run.Artifact{Metadata: candidate.Metadata})
				if !valid || index != *src.DeclarationIndex || candidate.ProducerNodeKey != producer.Key {
					continue
				}
				artifacts, e := b.artifactLister(ctx, candidate.ProducerAttemptID)
				if e != nil {
					return nil, e
				}
				for _, a := range artifacts {
					pushed, _ := a.Metadata["pushed"].(bool)
					if a.ID == candidate.ArtifactID && a.RunID == candidate.ProducerAttemptID && a.Type == run.ArtifactImage && pushed && metadataString(a, "logicalNodeKey") == producer.Key && metadataString(a, "sourceStageId") == src.StageID && metadataString(a, "sourceJobId") == src.JobID {
						image = metadataString(a, "digestRepository") + "@" + metadataString(a, "digest")
						if image != metadataString(run.Artifact{Metadata: candidate.Metadata}, "digestRepository")+"@"+metadataString(run.Artifact{Metadata: candidate.Metadata}, "digest") {
							return nil, errReleaseImage
						}
						matches++
					}
				}
			}
			if matches != 1 {
				return nil, errReleaseImage
			}
		default:
			return nil, errReleaseImage
		}
		if !kubernetes.ValidImageReference(image) {
			return nil, errReleaseImage
		}
		updates = append(updates, kubernetes.ReleaseImageUpdate{Container: binding.Container, Image: image})
	}
	return updates, nil
}
