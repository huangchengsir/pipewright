package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type opsAuth struct {
	auth.Authenticator
	revoked atomic.Bool
}

func (a *opsAuth) Verify(token string) (*auth.Session, error) {
	if token != "valid" || a.revoked.Load() {
		return nil, auth.ErrSessionNotFound
	}
	return &auth.Session{CSRFToken: "csrf"}, nil
}

type opsExecutor struct {
	id      string
	calls   atomic.Int32
	fail    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (e *opsExecutor) Capture(_ context.Context, id string) (target.ConnectionSnapshot, error) {
	if id != e.id {
		return target.ConnectionSnapshot{}, target.ErrNotFound
	}
	return target.ConnectionSnapshot{Server: target.Server{ID: id, Name: "test-node", Host: "192.0.2.1", CredentialID: "reference"}, CredentialVersion: "v1"}, nil
}
func (e *opsExecutor) ExecLimited(ctx context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
	e.calls.Add(1)
	if e.fail.Load() {
		return nil, target.ErrUnreachable
	}
	if len(argv) > 1 && argv[0] == "docker" && argv[1] == "inspect" {
		for _, arg := range argv {
			if arg == "{{json .State}}" {
				return &target.LimitedResult{Stdout: `{"Running":true,"Paused":false}`}, nil
			}
		}
		return &target.LimitedResult{Stdout: strings.Repeat("a", 64) + "\n"}, nil
	}
	if e.entered != nil {
		select {
		case e.entered <- struct{}{}:
		default:
		}
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &target.LimitedResult{Stdout: "local result"}, nil
}

type opsModelStub struct{ calls atomic.Int32 }

func (m *opsModelStub) Info() opschat.ProviderInfo {
	return opschat.ProviderInfo{Provider: "test", Model: "test", ConfigHash: strings.Repeat("a", 64)}
}
func (m *opsModelStub) Plan(context.Context, opschat.PlanRequest) (opschat.Plan, error) {
	m.calls.Add(1)
	return opschat.Plan{Text: "advice", Actions: []opschat.Action{}}, nil
}
func (m *opsModelStub) Analyze(context.Context, opschat.AnalysisRequest) (string, error) {
	m.calls.Add(1)
	return "analysis", nil
}

func opsFixture(t *testing.T) (*opschat.Service, *sql.DB, *opsExecutor, *opsAuth, *opsModelStub) {
	t.Helper()
	db := storetest.OpenDB(t)
	v := vault.New(db, &[32]byte{1})
	masker := mask.NewMasker()
	e := &opsExecutor{id: uuid.NewString()}
	m := &opsModelStub{}
	s, err := opschat.New(opschat.Options{DB: db, Vault: v, Executor: e, Model: m, Masker: masker, LocalRecorder: audit.New(db, masker, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, db, e, &opsAuth{}, m
}
func opsHTTP(t *testing.T, h http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: cookieSession, Value: "valid"})
	r.Header.Set(headerCsrf, "csrf")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: status %d wanted %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}
func opsSession(t *testing.T, s *opschat.Service, ids ...string) *opschat.Session {
	t.Helper()
	c, err := s.Create(context.Background(), opschat.CreateInput{ServerIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func opsWait(t *testing.T, s *opschat.Service, id, run, status string) *opschat.Snapshot {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		snap, err := s.Snapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range snap.Runs {
			if r.ID == run && r.Status == status {
				return snap
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not reach", status)
	return nil
}

func TestOpsRoutesAuthenticationCSRFAndAvailability(t *testing.T) {
	h := New(nil, &opsAuth{})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/capabilities"}, {"GET", "/tools"}, {"GET", "/sessions"}, {"POST", "/sessions"},
		{"GET", "/sessions/x"}, {"PATCH", "/sessions/x"}, {"DELETE", "/sessions/x"}, {"POST", "/sessions/x/activate"},
		{"GET", "/sessions/x/events"}, {"GET", "/sessions/x/entries"}, {"GET", "/sessions/x/calls/y"},
		{"POST", "/sessions/x/turns"}, {"POST", "/sessions/x/retry"}, {"POST", "/sessions/x/calls/confirm"},
		{"POST", "/sessions/x/runs/y/cancel"}, {"POST", "/sessions/x/runs/y/confirmation"},
		{"GET", "/sessions/x/analysis/preview"}, {"POST", "/sessions/x/analysis"},
	} {
		r := httptest.NewRequest(tc.method, "/api/ai/ops"+tc.path, strings.NewReader("{}"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(tc, w.Code)
		}
		if tc.method != "GET" {
			r = httptest.NewRequest(tc.method, "/api/ai/ops"+tc.path, strings.NewReader("{}"))
			r.AddCookie(&http.Cookie{Name: cookieSession, Value: "valid"})
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal(tc, w.Code)
			}
		}
	}
	w := opsHTTP(t, h, "GET", "/api/ai/ops/capabilities", "", 200)
	if strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatal(w.Body.String())
	}
	opsHTTP(t, h, "GET", "/api/ai/ops/tools", "", 200)
	opsHTTP(t, h, "POST", "/api/ai/ops/sessions", "{}", 503)
	opsHTTP(t, h, "GET", "/api/ai/ops/sessions", "", 503)
}

func TestOpsCRUDCASStrictBodiesAndPureReads(t *testing.T) {
	s, _, e, a, m := opsFixture(t)
	h := New(nil, a, WithOpsChat(s))
	prefix := "/api/ai/ops"
	w := opsHTTP(t, h, "POST", prefix+"/sessions", `{"title":"first","serverIds":[]}`, 201)
	var c opschat.Session
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	base := prefix + "/sessions/" + c.ID
	opsHTTP(t, h, "GET", base, "", 200)
	opsHTTP(t, h, "GET", base+"/entries?limit=100", "", 200)
	opsHTTP(t, h, "GET", prefix+"/sessions?limit=100", "", 200)
	opsHTTP(t, h, "POST", base+"/activate", "", 200)
	opsHTTP(t, h, "PATCH", base, fmt.Sprintf(`{"revision":%d,"draft":"draft","title":"renamed"}`, c.Revision), 200)
	opsHTTP(t, h, "PATCH", base, fmt.Sprintf(`{"revision":%d,"draft":"stale"}`, c.Revision), 409)
	for _, raw := range []string{`{"revision":2,"Revision":3}`, `{"revision":2,"draft":"a","draft":"b"}`, `{"revision":2,"userId":"other"}`, `{"revision":2} {}`, `null`, `[]`, strings.Repeat(" ", 64<<10) + `{}`} {
		opsHTTP(t, h, "PATCH", base, raw, 422)
	}
	for _, raw := range []string{
		`{"runId":"x","nonce":"y","calls":[{"callId":"a","CallID":"b"}]}`,
		`{"runId":"x","nonce":"y","calls":[{"callId":"a","host":"1.2.3.4"}]}`,
	} {
		opsHTTP(t, h, "POST", base+"/calls/confirm", raw, 422)
	}
	opsHTTP(t, h, "GET", base+"/entries?limit=1&limit=2", "", 422)
	opsHTTP(t, h, "GET", base+"/entries?limit=101", "", 422)
	opsHTTP(t, h, "GET", base+"/entries?before=other:1", "", 422)
	opsHTTP(t, h, "POST", base+"/activate", `{"userId":"a"}`, 422)
	if e.calls.Load() != 0 || m.calls.Load() != 0 {
		t.Fatal("CRUD/read dispatched")
	}
	opsHTTP(t, h, "DELETE", base, "", 204)
	opsHTTP(t, h, "GET", base, "", 404)
}

func TestOpsTurnsIdempotenceCrossSessionAndAnalysisConsent(t *testing.T) {
	s, _, e, a, m := opsFixture(t)
	h := New(nil, a, WithOpsChat(s))
	c := opsSession(t, s, e.id)
	other := opsSession(t, s, e.id)
	base := "/api/ai/ops/sessions/" + c.ID
	body := fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"toolId":"host_ports","args":{}}`, uuid.NewString(), c.Revision)
	w := opsHTTP(t, h, "POST", base+"/turns", body, 202)
	var run opschat.Run
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	w = opsHTTP(t, h, "POST", base+"/turns", body, 202)
	var duplicate opschat.Run
	_ = json.Unmarshal(w.Body.Bytes(), &duplicate)
	if duplicate.ID != run.ID {
		t.Fatal("duplicate run")
	}
	opsHTTP(t, h, "POST", "/api/ai/ops/sessions/"+other.ID+"/turns", body, 409)
	opsHTTP(t, h, "POST", base+"/turns", strings.Replace(body, "host_ports", "host_processes", 1), 409)
	snap := opsWait(t, s, c.ID, run.ID, opschat.Succeeded)
	if len(snap.Calls) != 1 || e.calls.Load() != 1 {
		t.Fatal(snap.Calls, e.calls.Load())
	}
	call := snap.Calls[0]
	opsHTTP(t, h, "GET", base+"/calls/"+call.ID, "", 200)
	opsHTTP(t, h, "GET", "/api/ai/ops/sessions/"+other.ID+"/calls/"+call.ID, "", 404)
	w = opsHTTP(t, h, "GET", base+"/analysis/preview?callIds="+call.ID, "", 200)
	var preview opschat.AnalysisPreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if m.calls.Load() != 0 {
		t.Fatal("preview called provider")
	}
	input := opschat.AnalysisInput{ClientRequestID: uuid.NewString(), Revision: snap.Session.Revision, CallIDs: preview.CallIDs, PreviewHash: preview.Hash, Provider: preview.Provider}
	raw, _ := json.Marshal(input)
	opsHTTP(t, h, "POST", base+"/analysis", string(raw), 409)
	input.Consent = true
	input.PreviewHash = strings.Repeat("b", 64)
	raw, _ = json.Marshal(input)
	opsHTTP(t, h, "POST", base+"/analysis", string(raw), 409)
	input.PreviewHash = preview.Hash
	raw, _ = json.Marshal(input)
	w = opsHTTP(t, h, "POST", base+"/analysis", string(raw), 202)
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	opsWait(t, s, c.ID, run.ID, opschat.Succeeded)
	if m.calls.Load() != 1 {
		t.Fatal(m.calls.Load())
	}
	opsHTTP(t, h, "GET", base+"/analysis/preview?callIds="+call.ID+"&callIds=x", "", 422)
	opsHTTP(t, h, "POST", base+"/retry", fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"callIds":[%q]}`, uuid.NewString(), snap.Session.Revision, call.ID), 409)
}

func TestOpsConfirmationReissueCancelAndReplay(t *testing.T) {
	s, _, e, a, _ := opsFixture(t)
	h := New(nil, a, WithOpsChat(s))
	c := opsSession(t, s, e.id)
	other := opsSession(t, s, e.id)
	base := "/api/ai/ops/sessions/" + c.ID
	body := fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"toolId":"docker_action","args":{"container":"app","action":"restart"}}`, uuid.NewString(), c.Revision)
	w := opsHTTP(t, h, "POST", base+"/turns", body, 202)
	var run opschat.Run
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	snap := opsWait(t, s, c.ID, run.ID, opschat.AwaitingConfirmation)
	if e.calls.Load() != 1 {
		t.Fatal("mutation dispatched before approval")
	}
	w = opsHTTP(t, h, "POST", base+"/runs/"+run.ID+"/confirmation", "{}", 200)
	var confirmation opschat.Confirmation
	_ = json.Unmarshal(w.Body.Bytes(), &confirmation)
	input := opschat.ConfirmInput{RunID: run.ID, Nonce: confirmation.Nonce, Calls: confirmation.Calls}
	raw, _ := json.Marshal(input)
	opsHTTP(t, h, "POST", "/api/ai/ops/sessions/"+other.ID+"/calls/confirm", string(raw), 404)
	bad := input
	bad.Calls = nil
	wrong, _ := json.Marshal(bad)
	opsHTTP(t, h, "POST", base+"/calls/confirm", string(wrong), 409)
	old := snap.Confirmations[0]
	stale, _ := json.Marshal(opschat.ConfirmInput{RunID: run.ID, Nonce: old.Nonce, Calls: old.Calls})
	opsHTTP(t, h, "POST", base+"/calls/confirm", string(stale), 409)
	opsHTTP(t, h, "POST", base+"/calls/confirm", string(raw), 202)
	opsWait(t, s, c.ID, run.ID, opschat.Succeeded)
	before := e.calls.Load()
	opsHTTP(t, h, "POST", base+"/calls/confirm", string(raw), 409)
	if e.calls.Load() != before {
		t.Fatal("replayed confirmation")
	}
	opsHTTP(t, h, "POST", "/api/ai/ops/sessions/"+other.ID+"/runs/"+run.ID+"/cancel", "", 404)
	opsHTTP(t, h, "POST", base+"/runs/"+run.ID+"/cancel", "", 202)
}

func TestOpsRetryFailedWireAndUnknownRefusal(t *testing.T) {
	s, db, e, a, _ := opsFixture(t)
	h := New(nil, a, WithOpsChat(s))
	c := opsSession(t, s, e.id)
	base := "/api/ai/ops/sessions/" + c.ID
	e.fail.Store(true)
	body := fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"toolId":"host_ports","args":{}}`, uuid.NewString(), c.Revision)
	w := opsHTTP(t, h, "POST", base+"/turns", body, 202)
	var run opschat.Run
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	snap := opsWait(t, s, c.ID, run.ID, opschat.Failed)
	if len(snap.Calls) != 1 {
		t.Fatal(snap.Calls)
	}
	old := snap.Calls[0]
	e.fail.Store(false)
	body = fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"callIds":[%q]}`, uuid.NewString(), snap.Session.Revision, old.ID)
	w = opsHTTP(t, h, "POST", base+"/retry", body, 202)
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	snap = opsWait(t, s, c.ID, run.ID, opschat.Succeeded)
	if e.calls.Load() != 2 {
		t.Fatal("wrong retry dispatch count", e.calls.Load())
	}
	if _, err := db.Exec("UPDATE ops_chat_calls SET status=? WHERE id=?", opschat.Unknown, old.ID); err != nil {
		t.Fatal(err)
	}
	body = fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"callIds":[%q]}`, uuid.NewString(), snap.Session.Revision, old.ID)
	w = opsHTTP(t, h, "POST", base+"/retry", body, 409)
	if !strings.Contains(w.Body.String(), "ops_verify_first") || e.calls.Load() != 2 {
		t.Fatal(w.Body.String(), e.calls.Load())
	}
}

func TestOpsHTTPRecoveryReadsDoNotReplayAndOldApprovalExpires(t *testing.T) {
	s, db, e, a, m := opsFixture(t)
	c := opsSession(t, s, e.id)
	base := "/api/ai/ops/sessions/" + c.ID
	h := New(nil, a, WithOpsChat(s))
	body := fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"toolId":"docker_action","args":{"container":"app","action":"restart"}}`, uuid.NewString(), c.Revision)
	w := opsHTTP(t, h, "POST", base+"/turns", body, 202)
	var run opschat.Run
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	snap := opsWait(t, s, c.ID, run.ID, opschat.AwaitingConfirmation)
	old := snap.Confirmations[0]
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	masker := mask.NewMasker()
	restored, err := opschat.New(opschat.Options{DB: db, Vault: vault.New(db, &[32]byte{1}), Executor: e, Model: m, Masker: masker, LocalRecorder: audit.New(db, masker, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := restored.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	h = New(nil, a, WithOpsChat(restored))
	before := e.calls.Load()
	w = opsHTTP(t, h, "GET", base, "", 200)
	var recovered opschat.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if len(recovered.Confirmations) != 1 || recovered.Confirmations[0].Valid {
		t.Fatal(recovered.Confirmations)
	}
	opsHTTP(t, h, "GET", base+"/entries", "", 200)
	opsHTTP(t, h, "GET", base+"/calls/"+snap.Calls[0].ID, "", 200)
	raw, _ := json.Marshal(opschat.ConfirmInput{RunID: run.ID, Nonce: old.Nonce, Calls: old.Calls})
	opsHTTP(t, h, "POST", base+"/calls/confirm", string(raw), 409)
	if e.calls.Load() != before || m.calls.Load() != 0 {
		t.Fatal("recovery dispatched work")
	}
}

func opsStream(t *testing.T, server *httptest.Server, path, last string) (context.CancelFunc, *http.Response, *bufio.Scanner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
	req.AddCookie(&http.Cookie{Name: cookieSession, Value: "valid"})
	if last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		t.Fatalf("stream %d %s", resp.StatusCode, raw)
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return cancel, resp, bufio.NewScanner(resp.Body)
}
func opsEvent(t *testing.T, scanner *bufio.Scanner, want string) string {
	t.Helper()
	event, id := "", ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "id: ") {
			id = strings.TrimPrefix(line, "id: ")
		}
		if line == "" && event != "" {
			if event != want {
				t.Fatalf("event %s want %s", event, want)
			}
			return id
		}
	}
	t.Fatal("stream ended", scanner.Err())
	return ""
}

func TestOpsSSEReplayDisconnectContinuesAndShutdown(t *testing.T) {
	s, _, e, a, _ := opsFixture(t)
	c := opsSession(t, s, e.id)
	// More than one replay page exercises ordered durable catch-up, not memory hints.
	for i := 0; i < 105; i++ {
		draft := fmt.Sprint(i)
		var err error
		c, err = s.Patch(context.Background(), c.ID, opschat.PatchInput{Revision: c.Revision, Draft: &draft})
		if err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(New(nil, a, WithOpsChat(s)))
	defer server.Close()
	path := "/api/ai/ops/sessions/" + c.ID + "/events"
	preCancel, preResp, preScanner := opsStream(t, server, path+"?after="+opschat.EventCursor(c.ID, c.Watermark), "other:1")
	opsEvent(t, preScanner, "ready")
	preCancel()
	preResp.Body.Close()
	cancel, resp, scanner := opsStream(t, server, path, "")
	opsEvent(t, scanner, "ready")
	last := ""
	for i := int64(1); i <= c.Watermark; i++ {
		last = opsEvent(t, scanner, "entry")
		if last != opschat.EventCursor(c.ID, i) {
			t.Fatal(last, i)
		}
	}
	cancel()
	resp.Body.Close()
	e.entered = make(chan struct{}, 1)
	e.release = make(chan struct{})
	body := fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"toolId":"host_ports","args":{}}`, uuid.NewString(), c.Revision)
	w := opsHTTP(t, New(nil, a, WithOpsChat(s)), "POST", "/api/ai/ops/sessions/"+c.ID+"/turns", body, 202)
	var run opschat.Run
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	select {
	case <-e.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker not dispatched")
	}
	cancel, resp, scanner = opsStream(t, server, path, last)
	opsEvent(t, scanner, "ready")
	first := opsEvent(t, scanner, "entry")
	if first == last {
		t.Fatal("duplicate cursor")
	}
	cancel()
	resp.Body.Close()
	close(e.release)
	opsWait(t, s, c.ID, run.ID, opschat.Succeeded)
	_, _, scanner = opsStream(t, server, path, last)
	opsEvent(t, scanner, "ready")
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for scanner.Scan() {
	}
	if scanner.Err() != nil {
		t.Fatal(scanner.Err())
	}
}

func TestOpsSSEAuthRevokedResetAndInvalidCursor(t *testing.T) {
	s, db, _, a, _ := opsFixture(t)
	c := opsSession(t, s)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler { return requireAuth(a, next) })
	router.Get("/sessions/{id}/events", opsEventsHandler(s, a, 10*time.Millisecond))
	server := httptest.NewServer(router)
	defer server.Close()
	path := "/sessions/" + c.ID + "/events"
	_, _, scanner := opsStream(t, server, path, opschat.EventCursor(c.ID, c.Watermark))
	opsEvent(t, scanner, "ready")
	a.revoked.Store(true)
	opsEvent(t, scanner, "auth")
	if scanner.Scan() {
		t.Fatal("stream continued after auth revocation")
	}
	a.revoked.Store(false)
	opsHTTP(t, router, "GET", path+"?after=other:1", "", 422)
	if _, err := db.Exec("UPDATE ops_chat_sessions SET seq=5000 WHERE id=?", c.ID); err != nil {
		t.Fatal(err)
	}
	_, _, scanner = opsStream(t, server, path, "")
	opsEvent(t, scanner, "reset")
	if scanner.Scan() {
		t.Fatal("stream continued after reset")
	}
}

func TestOpsErrorMappingDoesNotLeakCause(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{opschat.ErrNotFound, 404, "ops_not_found"}, {opschat.ErrInvalid, 422, "ops_invalid"}, {opschat.ErrVerifyFirst, 409, "ops_verify_first"},
		{opschat.ErrQuota, 429, "ops_quota"}, {opschat.ErrDecrypt, 503, "ops_unavailable"}, {opschat.ErrLease, 503, "ops_unavailable"},
		{opschat.ErrApproval, 409, "ops_confirmation_changed"}, {opschat.ErrConsent, 409, "ops_consent_changed"},
		{errors.New("secret and remote response"), 500, "ops_storage_failed"},
	} {
		w := httptest.NewRecorder()
		opsError(w, fmt.Errorf("secret: %w", tc.err))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
