package kubeagent

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

// client-go requests the deprecated @ spelling, while the native Kubernetes
// OpenAPI handler returns the canonical .v1.0 spelling. These are fixed protocol
// values, not arbitrary headers accepted from a chart or forwarded to the API.
const helmOpenAPIProtobufAccept = "application/com.github.proto-openapi.spec.v2@v1.0+protobuf"
const helmOpenAPIProtobufContentType = "application/com.github.proto-openapi.spec.v2.v1.0+protobuf"

func nativeHelmOpenAPIRequest(r *http.Request) bool {
	values := r.Header.Values("Accept")
	return r.Method == "GET" && r.URL.Path == "/openapi/v2" && len(values) == 1 && values[0] == helmOpenAPIProtobufAccept
}

// readNativeHelmOpenAPI is CLI-proxy-only representation negotiation, never a
// general RPC/header tunnel. It reuses the pinned API transport/CA and private
// token. No writes, fallback request, schema parsing or new dependency is added.
func (a *Agent) readNativeHelmOpenAPI(ctx context.Context, in kubeprotocol.Request) (kubeprotocol.APIResult, error) {
	out := kubeprotocol.APIResult{}
	if in.Method != "GET" || in.Path != "/openapi/v2" || len(in.Body) != 0 {
		return out, errDenied
	}
	query, err := url.ParseQuery(in.Query)
	if err != nil || validateTransportTimeout(query) != nil {
		return out, errDenied
	}
	for key := range query {
		if key != "timeout" {
			return out, errDenied
		}
	}
	a.mu.Lock()
	if !a.initialized || a.endpoint == nil || a.client == nil || a.client.Transport == nil || time.Now().After(a.deadline) {
		a.mu.Unlock()
		return out, errDenied
	}
	endpoint := *a.endpoint
	client := *a.client
	token, deadline := a.token, a.deadline
	a.mu.Unlock()
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/openapi/v2"
	endpoint.RawQuery = query.Encode()
	work, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	request, err := http.NewRequestWithContext(work, "GET", endpoint.String(), nil)
	if err != nil {
		return out, errDenied
	}
	request.Header.Set("Accept", helmOpenAPIProtobufAccept)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client.Timeout = 10 * time.Second
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errDenied }
	response, err := client.Do(request)
	if err != nil {
		return out, errDenied
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != helmOpenAPIProtobufContentType || response.ContentLength > kubeprotocol.MaxResponseBytes {
		return out, errDenied
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, kubeprotocol.MaxResponseBytes+1))
	if err != nil || len(body) == 0 || len(body) > kubeprotocol.MaxResponseBytes {
		clear(body)
		return out, errDenied
	}
	out.Status = 200
	out.Body = body
	return out, nil
}
