package kubeagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

const testOpenAPIProtobufType = "application/com.github.proto-openapi.spec.v2@v1.0+protobuf"
const testOpenAPIResponseType = "application/com.github.proto-openapi.spec.v2.v1.0+protobuf"

func TestNativeHelmOpenAPIProxyFixedTLSNegotiationAndJSONRPC(t *testing.T) {
	native := []byte{0x0a, 0x03, '2', '.', '0'}
	var nativeCalls, jsonCalls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/openapi/v2" || r.Header.Get("Authorization") != "Bearer fixture-private-token" || r.Header.Get("X-Untrusted") != "" {
			t.Error("private transport endpoint/header scope changed")
		}
		if r.Header.Get("Accept") == testOpenAPIProtobufType {
			nativeCalls.Add(1)
			if r.URL.RawQuery != "timeout=32s" {
				t.Error("validated transport timeout changed")
			}
			w.Header().Set("Content-Type", testOpenAPIResponseType)
			_, _ = w.Write(native)
			return
		}
		jsonCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"swagger":"2.0"}`))
	}), []kubeprotocol.Resource{configScope()})
	server := httptest.NewServer(http.HandlerFunc(a.proxy))
	defer server.Close()
	a.gateway = server.URL
	request, _ := http.NewRequest("GET", server.URL+"/openapi/v2?timeout=32s", nil)
	request.Header.Set("Accept", testOpenAPIProtobufType)
	request.Header.Set("X-Untrusted", "private-header-canary")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal("proxy fixture failed")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != testOpenAPIResponseType || !bytes.Equal(body, native) || nativeCalls.Load() != 1 {
		t.Fatal("native schema negotiation unavailable or representation changed")
	}
	result, err := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/openapi/v2"})
	if err != nil || result.Status != 200 || !bytes.Equal(result.Body, []byte(`{"swagger":"2.0"}`)) || jsonCalls.Load() != 1 {
		t.Fatal("RPC schema no longer uses JSON")
	}
	if len(a.PendingIDs()) != 0 {
		t.Fatal("schema read generated write permit")
	}
}

func TestNativeHelmOpenAPIRejectsScopeRepresentationAndResponseFailures(t *testing.T) {
	for _, scenario := range []string{"wrong-query", "force-false", "bad-timeout", "duplicate-timeout", "request-body", "expired", "uninitialized", "wrong-type", "deprecated-response-type", "type-parameters", "api-status", "redirect", "too-large", "empty", "truncated", "canceled", "wrong-TLS-name"} {
		t.Run(scenario, func(t *testing.T) {
			var calls, redirectCalls atomic.Int32
			redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectCalls.Add(1) }))
			defer redirect.Close()
			a, upstream := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", testOpenAPIResponseType)
				switch scenario {
				case "wrong-type":
					w.Header().Set("Content-Type", "application/json")
				case "deprecated-response-type":
					w.Header().Set("Content-Type", testOpenAPIProtobufType)
				case "type-parameters":
					w.Header().Set("Content-Type", testOpenAPIResponseType+"; charset=utf-8")
				case "api-status":
					w.WriteHeader(403)
				case "redirect":
					http.Redirect(w, r, redirect.URL, 302)
					return
				case "too-large":
					w.Header().Set("Content-Length", "4194305")
					_, _ = w.Write(bytes.Repeat([]byte("x"), kubeprotocol.MaxResponseBytes+1))
					return
				case "empty":
					return
				case "truncated":
					w.Header().Set("Content-Length", "50")
				case "canceled":
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte("private-upstream-error-canary"))
			}), []kubeprotocol.Resource{configScope()})
			upstream.Config.ErrorLog = log.New(io.Discard, "", 0)
			proxy := httptest.NewServer(http.HandlerFunc(a.proxy))
			defer proxy.Close()
			a.gateway = proxy.URL
			path := "/openapi/v2"
			body := ""
			switch scenario {
			case "wrong-query":
				path += "?arbitrary=true"
			case "force-false":
				path += "?force=false"
			case "bad-timeout":
				path += "?timeout=0s"
			case "duplicate-timeout":
				path += "?timeout=32s&timeout=32s"
			case "request-body":
				body = "{}"
			case "expired":
				a.deadline = time.Now().Add(-time.Second)
			case "uninitialized":
				a.initialized = false
			case "wrong-TLS-name":
				original := a.client.Transport.(*http.Transport)
				transport := original.Clone()
				transport.TLSClientConfig = original.TLSClientConfig.Clone()
				transport.TLSClientConfig.ServerName = "wrong.invalid"
				transport.TLSClientConfig.MinVersion = tls.VersionTLS12
				a.client.Transport = transport
				defer transport.CloseIdleConnections()
			}
			ctx := context.Background()
			if scenario == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			}
			request := httptest.NewRequest("GET", path, strings.NewReader(body)).WithContext(ctx)
			request.Host = strings.TrimPrefix(proxy.URL, "http://")
			request.Header.Set("Accept", testOpenAPIProtobufType)
			response := httptest.NewRecorder()
			a.proxy(response, request)
			if response.Code != 502 || strings.Contains(response.Body.String(), "private-upstream") || redirectCalls.Load() != 0 || len(a.PendingIDs()) != 0 {
				t.Fatalf("schema failure escaped closed boundary: status=%d calls=%d redirectCalls=%d", response.Code, calls.Load(), redirectCalls.Load())
			}
			switch scenario {
			case "wrong-query", "force-false", "bad-timeout", "duplicate-timeout", "request-body", "expired", "uninitialized", "wrong-TLS-name":
				if calls.Load() != 0 {
					t.Fatal("invalid schema request reached API")
				}
			}
		})
	}
}
func TestNativeHelmOpenAPIBudgetAndOnlyExactCLIReadNegotiates(t *testing.T) {
	for _, size := range []int{kubeprotocol.MaxResponseBytes, kubeprotocol.MaxResponseBytes + 1} {
		t.Run(fmtSize(size), func(t *testing.T) {
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", testOpenAPIResponseType)
				_, _ = w.Write(bytes.Repeat([]byte("x"), size))
			}), []kubeprotocol.Resource{configScope()})
			a.gateway = "http://gateway.invalid"
			req := httptest.NewRequest("GET", "/openapi/v2", nil)
			req.Host = strings.TrimPrefix(a.gateway, "http://")
			req.Header.Set("Accept", testOpenAPIProtobufType)
			out := httptest.NewRecorder()
			a.proxy(out, req)
			if size == kubeprotocol.MaxResponseBytes {
				if out.Code != 200 || out.Body.Len() != size {
					t.Fatal("existing schema bound changed")
				}
			} else if out.Code != 502 || strings.Contains(out.Body.String(), strings.Repeat("x", 10)) {
				t.Fatal("oversized native schema leaked")
			}
		})
	}
	var nativeCalls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == testOpenAPIProtobufType {
			nativeCalls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"APIResourceList","resources":[]}`))
	}), []kubeprotocol.Resource{configScope()})
	a.gateway = "http://gateway.invalid"
	for _, scenario := range []string{"JSON-Accept", "parameter-Accept", "multiple-Accept", "other-path", "other-method", "incoming-auth"} {
		req := httptest.NewRequest("GET", "/openapi/v2", nil)
		req.Host = strings.TrimPrefix(a.gateway, "http://")
		req.Header.Set("Accept", testOpenAPIProtobufType)
		switch scenario {
		case "JSON-Accept":
			req.Header.Set("Accept", "application/json")
		case "parameter-Accept":
			req.Header.Set("Accept", testOpenAPIProtobufType+";q=1")
		case "multiple-Accept":
			req.Header.Add("Accept", testOpenAPIProtobufType)
		case "other-path":
			req.URL.Path = "/api/v1"
		case "other-method":
			req.Method = "POST"
		case "incoming-auth":
			req.Header.Set("Authorization", "Bearer private-incoming-canary")
		}
		out := httptest.NewRecorder()
		a.proxy(out, req)
		if nativeCalls.Load() != 0 || out.Header().Get("Content-Type") == testOpenAPIResponseType || len(a.PendingIDs()) != 0 {
			t.Fatal("non-exact request gained native schema transport")
		}
	}
}
func fmtSize(size int) string {
	if size == kubeprotocol.MaxResponseBytes {
		return "exact-bound"
	}
	return "over-bound"
}
