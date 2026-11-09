package kubeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func storageCodecScope() kubeprotocol.Resource {
	return kubeprotocol.Resource{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app", Actions: []string{"get", "create", "update", "helm-storage"}}
}
func storageCodecBody(t *testing.T, namespace, name string, kind schema.GroupVersionKind, size int) []byte {
	t.Helper()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Type: "helm.sh/release.v1", Data: map[string][]byte{"release": bytes.Repeat([]byte("x"), size)}}
	secret.SetGroupVersionKind(kind)
	body, err := runtime.Encode(helmStorageSecretCodec, secret)
	if err != nil {
		t.Fatal("official fixture encoder failed")
	}
	return body
}
func TestHelmStorageCodecOnlyExplicitProxyStorageIdentity(t *testing.T) {
	for _, scenario := range []string{"valid-namespace", "typed-url-namespace", "other-name", "prefix-collision", "suffix-collision", "noncanonical-revision", "wrong-namespace", "wrong-path", "wrong-resource", "subresource", "wrong-gvk", "wrong-version", "missing-kind", "no-storage-grant", "read-only", "wrong-content-type", "wrong-secret-type", "bad-frame", "raw-limit", "json-expansion-limit", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			a := New()
			a.initialized = true
			a.deadline = time.Now().Add(time.Minute)
			a.resources = []kubeprotocol.Resource{storageCodecScope()}
			namespace, name, gvk := "app", "sh.helm.release.v1.app.v1", helmStorageSecretGVK
			if scenario == "typed-url-namespace" {
				namespace = ""
			}
			switch scenario {
			case "other-name":
				name = "sh.helm.release.v1.other.v1"
			case "prefix-collision":
				name = "sh.helm.release.v1.appx.v1"
			case "suffix-collision":
				name += ".evil"
			case "noncanonical-revision":
				name = "sh.helm.release.v1.app.v01"
			case "wrong-namespace":
				namespace = "other"
			case "wrong-gvk":
				gvk.Kind = "ConfigMap"
			case "wrong-version":
				gvk.Version = "v2"
			case "missing-kind":
				gvk.Kind = ""
			}
			body := storageCodecBody(t, namespace, name, gvk, 32)
			in := kubeprotocol.Request{Method: "POST", Path: "/api/v1/namespaces/app/secrets", ContentType: helmStorageProtobufType, Body: body}
			switch scenario {
			case "wrong-path":
				in.Path = "/api/v1/namespaces/other/secrets"
			case "wrong-resource":
				in.Path = "/api/v1/namespaces/app/configmaps"
			case "subresource":
				in.Path += "/status"
			case "no-storage-grant":
				a.resources[0].Actions = []string{"create", "update"}
				a.resources[0].Name = name
			case "read-only":
				a.resources[0].Actions = []string{"get", "helm-storage"}
			case "wrong-content-type":
				in.ContentType = "application/json"
			case "wrong-secret-type":
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Type: "Opaque", Data: map[string][]byte{"release": []byte("private-codec-canary")}}
				secret.SetGroupVersionKind(gvk)
				in.Body, _ = runtime.Encode(helmStorageSecretCodec, secret)
			case "bad-frame":
				in.Body = []byte("k8s\x00\xff")
			case "raw-limit":
				in.Body = bytes.Repeat([]byte("x"), (1<<20)+1)
			case "json-expansion-limit":
				in.Body = storageCodecBody(t, namespace, name, gvk, 820000)
				if len(in.Body) > 1<<20 {
					t.Fatal("fixture raw body exceeded input budget")
				}
			case "expired":
				a.deadline = time.Now().Add(-time.Second)
			}
			out, err := a.normalizeHelmStorageRequest(in)
			if scenario == "valid-namespace" || scenario == "typed-url-namespace" {
				if err != nil || out.ContentType != "application/json" || len(out.Body) > 1<<20 {
					t.Fatal("authorized typed storage body rejected")
				}
				var object map[string]any
				if json.Unmarshal(out.Body, &object) != nil || mapValue(object["metadata"])["namespace"] != "app" || object["kind"] != "Secret" || object["apiVersion"] != "v1" {
					t.Fatal("decoded identity not bound to exact URL")
				}
				clear(out.Body)
			} else if err == nil || err.Error() != errDenied.Error() {
				t.Fatal("invalid storage transport accepted or raw error leaked")
			}
		})
	}
}
func TestHelmStorageProxyCanonicalJSONStillRequiresSingleBoundPermit(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		t.Run(method, func(t *testing.T) {
			var writes atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writes.Add(1)
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("opaque protobuf forwarded upstream")
				}
				body, _ := io.ReadAll(r.Body)
				var object map[string]any
				if json.Unmarshal(body, &object) != nil || mapValue(object["metadata"])["namespace"] != "app" {
					t.Error("canonical exact identity missing")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(201)
				_, _ = w.Write(body)
			}), []kubeprotocol.Resource{storageCodecScope()})
			server := httptest.NewServer(http.HandlerFunc(a.proxy))
			defer server.Close()
			a.gateway = server.URL
			path := "/api/v1/namespaces/app/secrets"
			if method == "PUT" {
				path += "/sh.helm.release.v1.app.v1"
			}
			raw := storageCodecBody(t, "", "sh.helm.release.v1.app.v1", helmStorageSecretGVK, 32)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan int, 1)
			go func() {
				req, _ := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(raw))
				req.Header.Set("Content-Type", helmStorageProtobufType)
				resp, err := server.Client().Do(req)
				if err != nil {
					done <- 0
					return
				}
				defer resp.Body.Close()
				io.Copy(io.Discard, resp.Body)
				done <- resp.StatusCode
			}()
			var pending *kubeprotocol.PendingWrite
			for pending == nil && ctx.Err() == nil {
				pending = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
				if pending == nil {
					time.Sleep(time.Millisecond)
				}
			}
			if pending == nil || writes.Load() != 0 {
				t.Fatal("storage decode bypassed approval fence")
			}
			encoded, _ := json.Marshal(pending.Request)
			if pending.Digest != privateDigest(encoded) || pending.Request.ContentType != "application/json" || !json.Valid(pending.Request.Body) || bytes.HasPrefix(pending.Request.Body, []byte("k8s\x00")) {
				t.Fatal("pending identity did not bind canonical JSON")
			}
			permit := &kubeprotocol.Permit{RequestID: pending.ID, ActionID: "fixture-action", Digest: pending.Digest, Allow: true, Expires: time.Now().Add(time.Second).UnixMilli()}
			if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit}).Code != "ok" {
				t.Fatal("bound permit rejected")
			}
			if <-done != 201 || writes.Load() != 1 {
				t.Fatal("exact one storage write not observed")
			}
			if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit}).Code == "ok" {
				t.Fatal("canonical permit replayed")
			}
			// RPC does not gain an opaque protobuf decoder.
			if _, err := a.Do(ctx, kubeprotocol.Request{Method: method, Path: path, Body: raw, ContentType: helmStorageProtobufType}); err == nil || writes.Load() != 1 {
				t.Fatal("RPC protobuf write accepted")
			}
		})
	}
}
func TestHelmStorageProxyRejectsWrongMethodWithoutPendingOrAPICall(t *testing.T) {
	var calls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }), []kubeprotocol.Resource{storageCodecScope()})
	server := httptest.NewServer(http.HandlerFunc(a.proxy))
	defer server.Close()
	a.gateway = server.URL
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		body := storageCodecBody(t, "app", "sh.helm.release.v1.app.v1", helmStorageSecretGVK, 32)
		req, _ := http.NewRequest(method, server.URL+"/api/v1/namespaces/app/secrets/sh.helm.release.v1.app.v1", bytes.NewReader(body))
		req.Header.Set("Content-Type", helmStorageProtobufType)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("fixture HTTP failed")
		}
		safe, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 415 || calls.Load() != 0 || len(a.PendingIDs()) != 0 || strings.Contains(string(safe), "private-codec-canary") {
			t.Fatal("wrong-method storage request escaped closed boundary")
		}
	}
}
