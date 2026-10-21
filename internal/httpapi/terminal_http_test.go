package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

func terminalRequest(t *testing.T, c *http.Client, method, url, csrf string, body any) *http.Response {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, _ := http.NewRequest(method, url, &b)
	if csrf != "" {
		req.Header.Set(headerCsrf, csrf)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func TestTerminalHTTPHostAndContainer(t *testing.T) {
	for _, container := range []bool{false, true} {
		t.Run(map[bool]string{false: "host", true: "container"}[container], func(t *testing.T) {
			dialer := &fakeInteractiveDialer{}
			srv, client, csrf, _ := setupTerminalAPI(t, dialer)
			id := newServerAPI(t, client, srv.URL, csrf)
			base := srv.URL + "/api/servers/" + id
			if container {
				base += "/containers/app"
			}
			base += "/terminal"
			body := map[string]string{"requestId": "0123456789abcdef", "shell": "/bin/sh"}
			res := terminalRequest(t, client, "POST", base+"/sessions", "", body)
			res.Body.Close()
			if res.StatusCode != 403 {
				t.Fatal(res.StatusCode)
			}
			req, _ := http.NewRequest("POST", base+"/sessions", bytes.NewBufferString(`{"requestId":"0123456789abcdef"}`))
			req.Header.Set(headerCsrf, csrf)
			req.Header.Set("Origin", "https://evil.test")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 403 {
				t.Fatal("cross origin", res.StatusCode)
			}
			create := func() string {
				res := terminalRequest(t, client, "POST", base+"/sessions", csrf, body)
				defer res.Body.Close()
				if res.StatusCode != 200 {
					data, _ := io.ReadAll(res.Body)
					t.Fatalf("create %d %s", res.StatusCode, data)
				}
				var result struct {
					ID string `json:"id"`
				}
				if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
					t.Fatal(err)
				}
				if res.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("cache")
				}
				return result.ID
			}
			sid := create()
			if create() != sid {
				t.Fatal("not idempotent")
			}
			path := base + "/sessions/" + sid
			res = terminalRequest(t, client, "POST", path+"/input", csrf, map[string]any{"seq": 1, "data": base64.StdEncoding.EncodeToString([]byte("echo 中\n"))})
			res.Body.Close()
			if res.StatusCode != 204 {
				t.Fatal(res.StatusCode)
			}
			res = terminalRequest(t, client, "GET", path+"?after=0", "", nil)
			var out struct {
				Data string `json:"data"`
				Next int64  `json:"next"`
			}
			if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			data, _ := base64.StdEncoding.DecodeString(out.Data)
			if string(data) != "echo 中\n" || out.Next != int64(len(data)) {
				t.Fatal(out)
			}
			res = terminalRequest(t, client, "POST", path+"/input", csrf, map[string]any{"seq": 2, "cols": 120, "rows": 40})
			res.Body.Close()
			if res.StatusCode != 204 {
				t.Fatal(res.StatusCode)
			}
			cols, rows := dialer.sess.resizeDims()
			if cols != 120 || rows != 40 {
				t.Fatal(cols, rows)
			}
			res = terminalRequest(t, client, "DELETE", path, csrf, nil)
			res.Body.Close()
			if res.StatusCode != 204 {
				t.Fatal(res.StatusCode)
			}
		})
	}
}

type exitedTerminalSession struct {
	output          *bytes.Reader
	done            chan struct{}
	once            sync.Once
	writes, resizes atomic.Int32
}

func (s *exitedTerminalSession) Read(p []byte) (int, error) { return s.output.Read(p) }
func (s *exitedTerminalSession) Write([]byte) (int, error) {
	s.writes.Add(1)
	return 0, io.ErrClosedPipe
}
func (s *exitedTerminalSession) Resize(int, int) error { s.resizes.Add(1); return io.EOF }
func (s *exitedTerminalSession) Close() error          { s.once.Do(func() { close(s.done) }); return nil }

type exitedTerminalDialer struct {
	fakeInteractiveDialer
	session target.Session
}

func (d *exitedTerminalDialer) RunInteractive(context.Context, string, target.SSHConfig, []string) (target.Session, error) {
	return d.session, nil
}

func TestTerminalHTTPEndedInputPreservesFinalOutputDrain(t *testing.T) {
	for _, container := range []bool{false, true} {
		t.Run(map[bool]string{false: "host", true: "container"}[container], func(t *testing.T) {
			output := bytes.Repeat([]byte("/bin/zsh: not found 中\r\n"), 6000)
			s := &exitedTerminalSession{output: bytes.NewReader(output), done: make(chan struct{})}
			srv, client, csrf, _ := setupTerminalAPI(t, &exitedTerminalDialer{session: s})
			id := newServerAPI(t, client, srv.URL, csrf)
			base := srv.URL + "/api/servers/" + id
			if container {
				base += "/containers/app"
			}
			base += "/terminal/sessions"
			res := terminalRequest(t, client, "POST", base, csrf, map[string]string{"requestId": "0123456789abcdef", "shell": "/bin/zsh"})
			var created struct {
				ID string `json:"id"`
			}
			if res.StatusCode != 200 {
				t.Fatal("create", res.StatusCode)
			}
			if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			path := base + "/" + created.ID
			select {
			case <-s.done:
			case <-time.After(time.Second):
				t.Fatal("output EOF not observed before input")
			}
			assertInputError := func(input map[string]any, code string) {
				t.Helper()
				res := terminalRequest(t, client, "POST", path+"/input", csrf, input)
				defer res.Body.Close()
				var result struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
					t.Fatal(err)
				}
				if res.StatusCode != 410 || result.Error.Code != code {
					t.Fatalf("input after close = %d %s, want 410 %s", res.StatusCode, result.Error.Code, code)
				}
			}
			assertInputError(map[string]any{"seq": 1, "cols": 120, "rows": 40}, "terminal_ended")
			assertInputError(map[string]any{"seq": 1, "data": base64.StdEncoding.EncodeToString([]byte("new command\n"))}, "terminal_ended")
			var got []byte
			var after int64
			for {
				res := terminalRequest(t, client, "GET", path+"?after="+strconv.FormatInt(after, 10), "", nil)
				var out struct {
					Data   string `json:"data"`
					Next   int64  `json:"next"`
					Closed bool   `json:"closed"`
				}
				if res.StatusCode != 200 {
					t.Fatal("poll", res.StatusCode)
				}
				if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				data, err := base64.StdEncoding.DecodeString(out.Data)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, data...)
				after = out.Next
				if out.Closed {
					break
				}
			}
			if !bytes.Equal(got, output) {
				t.Fatalf("final HTTP output lost: %d != %d", len(got), len(output))
			}
			if s.writes.Load() != 0 || s.resizes.Load() != 0 {
				t.Fatal("ended input reached the remote session")
			}
			res = terminalRequest(t, client, "DELETE", path, csrf, nil)
			res.Body.Close()
			if res.StatusCode != 204 {
				t.Fatal("delete", res.StatusCode)
			}
			assertInputError(map[string]any{"seq": 1, "cols": 80, "rows": 24}, "terminal_closed")
		})
	}
}

func TestTerminalV2StartBarrierAndReady(t *testing.T) {
	dialer := &fakeInteractiveDialer{}
	srv, client, csrf, _ := setupTerminalAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)
	hdr := http.Header{}
	for _, cookie := range client.Jar.Cookies(mustParseURL(t, srv.URL)) {
		hdr.Add("Cookie", cookie.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	url := wsURL(srv.URL) + "/api/servers/" + id + "/terminal/ws-v2"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	if len(dialer.cmd()) != 0 {
		t.Fatal("SSH before start")
	}
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"start","extra":true}`)); err != nil {
		t.Fatal(err)
	}
	_, _, _ = conn.Read(ctx)
	_ = conn.CloseNow()
	if len(dialer.cmd()) != 0 {
		t.Fatal("invalid start created SSH")
	}
	conn, _, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"start"}`)); err != nil {
		t.Fatal(err)
	}
	typ, ready, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageText || string(ready) != `{"type":"ready"}` {
		t.Fatalf("ready %s %v", ready, err)
	}
	if len(dialer.cmd()) != 1 {
		t.Fatal("shell argv", dialer.cmd())
	}
	if err = conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, output, err := conn.Read(ctx)
	if err != nil || string(output) != "hello" {
		t.Fatalf("echo %s %v", output, err)
	}
}

func TestTerminalV2UpgradeMissingHeadersDoesNotOpenSSH(t *testing.T) {
	dialer := &fakeInteractiveDialer{}
	srv, client, csrf, _ := setupTerminalAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)
	res := terminalRequest(t, client, "GET", srv.URL+"/api/servers/"+id+"/terminal/ws-v2", "", nil)
	res.Body.Close()
	if res.StatusCode != 426 || len(dialer.cmd()) != 0 {
		t.Fatal(res.StatusCode, dialer.cmd())
	}
}

func TestTerminalV2UpstreamOnly101AndHTTPFallbackSinglePTY(t *testing.T) {
	dialer := &fakeInteractiveDialer{}
	srv, client, csrf, rec := setupTerminalAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)
	hdr := http.Header{}
	for _, cookie := range client.Jar.Cookies(mustParseURL(t, srv.URL)) {
		hdr.Add("Cookie", cookie.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// This is the upstream side of a proxy that obtained 101 but never delivered
	// it to the browser. No start can come from that browser.
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL)+"/api/servers/"+id+"/terminal/ws-v2", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if len(dialer.cmd()) != 0 {
		t.Fatal("upstream upgrade opened PTY")
	}
	base := srv.URL + "/api/servers/" + id + "/terminal/sessions"
	res := terminalRequest(t, client, "POST", base, csrf, map[string]string{"requestId": "0123456789abcdef"})
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	// The unstarted upgraded socket expires without ever creating another SSH.
	_, _, _ = conn.Read(ctx)
	entries, err := rec.List(context.Background(), audit.ListFilter{Action: audit.ActionServerTerminal})
	if err != nil || len(entries.Entries) != 1 {
		t.Fatalf("audit %v %v", entries, err)
	}
	if !equalStrSlice(dialer.cmd(), []string{"/bin/sh"}) {
		t.Fatal(dialer.cmd())
	}
}
