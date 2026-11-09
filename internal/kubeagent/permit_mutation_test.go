package kubeagent

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCASPermitRejectsIdentityOrWireExpansion(t *testing.T) {
	a := New()
	a.resources = []kubeprotocol.Resource{configScope()}
	request := kubeprotocol.Request{Method: "PATCH", Path: "/api/v1/namespaces/app/configmaps/config", ContentType: "application/apply-patch+yaml", Body: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"u","resourceVersion":"7"},"data":{"remove":null}}`)}
	permit := kubeprotocol.Permit{Allow: true, BodyStream: "approved", ContentType: "application/merge-patch+json", FieldManager: "pipewright-release-v2"}
	if out, e := a.permitMutationRequest(request, permit); e != nil || out.ContentType != permit.ContentType {
		t.Fatal("valid narrowing rejected", e)
	}
	for _, scenario := range []string{"uid", "rv", "kind", "name", "namespace", "version", "method", "inputType", "outputType", "manager", "stream", "createOnly", "allow", "right"} {
		t.Run(scenario, func(t *testing.T) {
			in, p := request, permit
			var object map[string]any
			json.Unmarshal(in.Body, &object)
			m := mapValue(object["metadata"])
			switch scenario {
			case "uid":
				delete(m, "uid")
			case "rv":
				delete(m, "resourceVersion")
			case "kind":
				object["kind"] = "Secret"
			case "name":
				m["name"] = "other"
			case "namespace":
				m["namespace"] = "other"
			case "version":
				object["apiVersion"] = "other/v1"
			case "method":
				in.Method = "POST"
			case "inputType":
				in.ContentType = "application/json-patch+json"
			case "outputType":
				p.ContentType = "application/strategic-merge-patch+json"
			case "manager":
				p.FieldManager = "foreign"
			case "stream":
				p.BodyStream = ""
			case "createOnly":
				p.CreateOnly = true
			case "allow":
				p.Allow = false
			case "right":
				a.resources[0].Actions = []string{"get"}
				defer func() { a.resources[0] = configScope() }()
			}
			in.Body, _ = json.Marshal(object)
			if _, e := a.permitMutationRequest(in, p); e == nil {
				t.Fatal("expanded CAS permit accepted")
			}
		})
	}
}
func TestRealGatewayCASPermitIsSingleUseAndLegacyDefaultsKeepSSA(t *testing.T) {
	for _, cas := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-ssa", true: "sealed-cas"}[cas], func(t *testing.T) {
			var calls atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				wantType, wantManager := "application/apply-patch+yaml", "helm"
				if cas {
					wantType, wantManager = "application/merge-patch+json", "pipewright-release-v2"
				}
				if r.Method != "PATCH" || r.Header.Get("Content-Type") != wantType || r.URL.Query().Get("fieldManager") != wantManager {
					t.Error("wrong approved wire semantics", r.Method, r.Header.Get("Content-Type"), r.URL.Query().Get("fieldManager"))
				}
				var obj map[string]any
				if json.NewDecoder(r.Body).Decode(&obj) != nil {
					t.Error("bad body")
				}
				m := mapValue(obj["metadata"])
				if m["uid"] != "owned" || m["resourceVersion"] != "7" {
					t.Error("lost CAS identity")
				}
				w.WriteHeader(200)
				_ = json.NewEncoder(w).Encode(obj)
			}), []kubeprotocol.Resource{configScope()})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			original := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"value":"next"}}`)
			replacement := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"owned","resourceVersion":"7"},"data":{"value":"next","remove":null}}`)
			done := make(chan error, 1)
			go func() {
				_, e := a.Do(ctx, kubeprotocol.Request{Method: "PATCH", Path: "/api/v1/namespaces/app/configmaps/config", Query: "fieldManager=helm", ContentType: "application/apply-patch+yaml", Body: original})
				done <- e
			}()
			var pending *kubeprotocol.PendingWrite
			for pending == nil {
				if ctx.Err() != nil {
					t.Fatal("pending absent")
				}
				pending = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
				if pending == nil {
					time.Sleep(time.Millisecond)
				}
			}
			if calls.Load() != 0 {
				t.Fatal("unpermitted request reached upstream")
			}
			r := a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "upload", StreamID: "approved", Count: 1, Size: len(replacement), Digest: privateDigest(replacement), Data: replacement})
			if r.Code != "ok" {
				t.Fatal(r.Code)
			}
			p := kubeprotocol.Permit{RequestID: pending.ID, ActionID: "bound-action", Digest: pending.Digest, Allow: true, BodyStream: "approved", Expires: time.Now().Add(5 * time.Second).UnixMilli()}
			if cas {
				p.ContentType = "application/merge-patch+json"
				p.FieldManager = "pipewright-release-v2"
			}
			wrong := p
			wrong.Digest = "other"
			if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &wrong}).Code == "ok" {
				t.Fatal("foreign pending digest accepted")
			}
			if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &p}).Code != "ok" {
				t.Fatal("permit rejected")
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if calls.Load() != 1 || a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &p}).Code == "ok" {
				t.Fatal("permit replayed")
			}
			ping := a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "ping"})
			if len(ping.Capabilities) != 5 || ping.Capabilities[0] != kubeprotocol.PermitCASMergeCapability || ping.Capabilities[1] != kubeprotocol.HelmStorageJSONCapability || ping.Capabilities[2] != kubeprotocol.HelmHookOpenAPICapability || ping.Capabilities[3] != kubeprotocol.HelmHookWatchResyncCapability || ping.Capabilities[4] != kubeprotocol.DaemonSetRevisionReadCapability {
				t.Fatal("CAS capability missing")
			}
		})
	}
}
