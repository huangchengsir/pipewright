package kubeagent

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHelmSSACreatePermitUsesAtomicNamedPOSTAndCannotAdoptRace(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "concurrent-object"}[race], func(t *testing.T) {
			var calls atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/api/v1/namespaces/app/configmaps" {
					t.Error("SSA creation was not narrowed to atomic POST", r.Method, r.URL.Path)
				}
				if race {
					w.WriteHeader(409)
					_, _ = w.Write([]byte(`{"kind":"Status","reason":"AlreadyExists"}`))
					return
				}
				w.WriteHeader(201)
				_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"owned","resourceVersion":"1"}}`))
			}), []kubeprotocol.Resource{configScope()})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			body := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"value":"private-fixture"}}`)
			done := make(chan kubeprotocol.APIResult, 1)
			go func() {
				r, e := a.Do(ctx, kubeprotocol.Request{Method: "PATCH", Path: "/api/v1/namespaces/app/configmaps/config", ContentType: "application/apply-patch+yaml", Body: body})
				if e != nil {
					t.Error(e)
				}
				done <- r
			}()
			var pending *kubeprotocol.PendingWrite
			for pending == nil {
				if ctx.Err() != nil {
					t.Fatal("pending missing")
				}
				pending = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
				if pending == nil {
					time.Sleep(time.Millisecond)
				}
			}
			if calls.Load() != 0 {
				t.Fatal("creation reached cluster before permission")
			}
			if r := a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "upload", StreamID: "approved", Index: 0, Count: 1, Size: len(body), Digest: privateDigest(body), Data: body}); r.Code != "ok" {
				t.Fatal(r.Code)
			}
			permit := kubeprotocol.Permit{RequestID: pending.ID, ActionID: "approved-action", Digest: pending.Digest, Allow: true, CreateOnly: true, BodyStream: "approved", Expires: time.Now().Add(5 * time.Second).UnixMilli()}
			if r := a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &permit}); r.Code != "ok" {
				t.Fatal(r.Code)
			}
			result := <-done
			want := 201
			if race {
				want = 409
			}
			if result.Status != want || calls.Load() != 1 {
				t.Fatal("creation result or request count incorrect", result.Status, calls.Load())
			}
			if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &permit}).Code == "ok" {
				t.Fatal("create permit replayed")
			}
		})
	}
}
func TestCreateOnlyPermitRequiresBothActionsExactBodyAndNoExistingIdentity(t *testing.T) {
	scope := configScope()
	a := New()
	a.resources = []kubeprotocol.Resource{scope}
	body := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"}}`)
	in := kubeprotocol.Request{Method: "PATCH", Path: resourcePath(scope, false), ContentType: "application/apply-patch+yaml", Body: body}
	permit := kubeprotocol.Permit{Allow: true, BodyStream: "approved", CreateOnly: true}
	if _, e := a.createOnlyRequest(in, permit); e != nil {
		t.Fatal(e)
	}
	for _, foreign := range []string{strings.ReplaceAll(string(body), "config\"", "other\""), strings.ReplaceAll(string(body), "app", "other"), `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"existing","resourceVersion":"4"}}`} {
		next := in
		next.Body = []byte(foreign)
		if _, e := a.createOnlyRequest(next, permit); e == nil {
			t.Fatal("foreign existing identity accepted")
		}
	}
	a.resources[0].Actions = []string{"get", "patch"}
	if _, e := a.createOnlyRequest(in, permit); e == nil {
		t.Fatal("create right inferred from patch")
	}
}
func TestHelperSerializesPendingWritesUntilPreviousResultIsPublished(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			close(entered)
			<-release
		}
		var object map[string]any
		_ = json.NewDecoder(r.Body).Decode(&object)
		meta := mapValue(object["metadata"])
		meta["uid"] = "owned"
		meta["resourceVersion"] = "1"
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(object)
	}), []kubeprotocol.Resource{configScope()})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"}}`)
	request := kubeprotocol.Request{Method: "POST", Path: "/api/v1/namespaces/app/configmaps", Body: body}
	done := make(chan error, 2)
	go func() { _, e := a.Do(ctx, request); done <- e }()
	var p *kubeprotocol.PendingWrite
	for p == nil {
		if ctx.Err() != nil {
			t.Fatal("pending missing")
		}
		p = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
		time.Sleep(time.Millisecond)
	}
	permit := kubeprotocol.Permit{RequestID: p.ID, ActionID: "first", Digest: p.Digest, Allow: true, Expires: time.Now().Add(5 * time.Second).UnixMilli()}
	a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &permit})
	<-entered
	go func() { _, e := a.Do(ctx, request); done <- e }()
	time.Sleep(10 * time.Millisecond)
	if next := a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}); next.Pending != nil || calls.Load() != 1 {
		t.Fatal("second write bypassed first durable receipt")
	}
	close(release)
	var next kubeprotocol.Reply
	seenFirst := false
	for next.Pending == nil {
		if ctx.Err() != nil {
			t.Fatal("second pending missing")
		}
		next = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"})
		if next.Result != nil {
			seenFirst = seenFirst || next.Result.ActionID == "first"
		}
		time.Sleep(time.Millisecond)
	}
	if !seenFirst {
		t.Fatal("next permission preceded first published result")
	}
	permit.RequestID = next.Pending.ID
	permit.Digest = next.Pending.Digest
	permit.ActionID = "second"
	a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &permit})
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
