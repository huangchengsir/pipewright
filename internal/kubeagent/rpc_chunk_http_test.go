package kubeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateRPCHTTPFullDownloadChunkIsTerminal(t *testing.T) {
	payload := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"root"},"data":{"test":"` + strings.Repeat("test-private-payload-", 7000) + `"}}`)
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected write")
		}
		w.Write(payload)
	}), []kubeprotocol.Resource{configScope()})
	server := httptest.NewServer(a.rpcHandler())
	defer server.Close()
	frame := func(f kubeprotocol.Frame) kubeprotocol.Reply {
		t.Helper()
		body, e := json.Marshal(f)
		if e != nil {
			t.Fatal(e)
		}
		request, e := http.NewRequestWithContext(context.Background(), "POST", server.URL+"/rpc", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
		if e != nil || len(raw) > 1<<20 || response.StatusCode != 200 {
			t.Fatal("private physical frame limit or status")
		}
		var r kubeprotocol.Reply
		if json.Unmarshal(raw, &r) != nil {
			t.Fatal("invalid private reply")
		}
		return r
	}
	head := frame(kubeprotocol.Frame{Version: 2, Command: "read", Request: &kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/configmaps/config"}})
	if head.Code != "chunked" || head.Count < 2 {
		t.Fatal("fixture did not exercise first multi-chunk response", head.Code, head.Count)
	}
	for _, invalid := range []kubeprotocol.Frame{
		{Version: 2, Command: "download", StreamID: head.StreamID, Index: -1},
		{Version: 2, Command: "download", StreamID: head.StreamID, Index: head.Count},
		{Version: 2, Command: "download", StreamID: strings.Repeat("0", 32), Index: 0},
	} {
		if r := frame(invalid); r.Code == "ok" || len(r.Data) > 0 {
			t.Fatal("invalid/foreign chunk was readable")
		}
	}
	var joined []byte
	for i := 0; i < head.Count; i++ {
		part := frame(kubeprotocol.Frame{Version: 2, Command: "download", StreamID: head.StreamID, Index: i})
		if part.Code != "ok" || len(part.Data) == 0 || len(part.Data) > kubeprotocol.MaxChunkBytes {
			t.Fatalf("download must be terminal bounded data, got code=%s bytes=%d", part.Code, len(part.Data))
		}
		joined = append(joined, part.Data...)
	}
	if len(joined) != head.Size || privateDigest(joined) != head.Digest {
		t.Fatal("response size/digest changed")
	}
	var result kubeprotocol.Reply
	if json.Unmarshal(joined, &result) != nil || result.Result == nil || !bytes.Equal(result.Result.Body, payload) {
		t.Fatal("private body not reconstructed exactly")
	}
	if r := frame(kubeprotocol.Frame{Version: 2, Command: "forget", StreamID: head.StreamID}); r.Code != "ok" {
		t.Fatal("stream not forgotten")
	}
	if r := frame(kubeprotocol.Frame{Version: 2, Command: "download", StreamID: head.StreamID, Index: 0}); r.Code == "ok" {
		t.Fatal("forgotten stream remained readable")
	}
	if len(a.downloads) != 0 {
		t.Fatal("completed stream retained downloads")
	}
}
