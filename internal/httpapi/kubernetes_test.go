package httpapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type k8sAudit struct{ entries []audit.Entry }

func (a *k8sAudit) Record(_ context.Context, e audit.Entry) error {
	a.entries = append(a.entries, e)
	return nil
}

// Embed the remaining recorder methods: these tests exercise only Record.
type k8sRecorder struct {
	audit.Recorder
	*k8sAudit
}

func (a k8sRecorder) Record(ctx context.Context, e audit.Entry) error {
	return a.k8sAudit.Record(ctx, e)
}

type k8sHTTPFixture struct {
	db       *store.Store
	s        *kubernetes.Service
	session  *auth.Session
	handler  http.Handler
	input    kubernetes.CredentialInput
	cred     kubernetes.Credential
	conn     kubernetes.Connection
	grant    kubernetes.Grant
	op       kubernetes.Operation
	aud      *k8sAudit
	observer *k8sTestObserver
}
type k8sTestObserver struct {
	calls        int
	beforeReturn func()
	receipt      kubernetes.Receipt
}

func (o *k8sTestObserver) Probe(context.Context, kubernetes.Target) (kubernetes.Receipt, error) {
	o.calls++
	if o.beforeReturn != nil {
		o.beforeReturn()
	}
	return o.receipt, nil
}
func (o *k8sTestObserver) Inspect(context.Context, string, string) (kubernetes.Receipt, error) {
	o.calls++
	if o.beforeReturn != nil {
		o.beforeReturn()
	}
	return o.receipt, nil
}
func (o *k8sTestObserver) PrepareRetire(ctx context.Context, _ string, _ int64) (kubernetes.Receipt, error) {
	return o.Inspect(ctx, "", "")
}
func newK8sHTTP(t *testing.T) *k8sHTTPFixture {
	t.Helper()
	st := storetest.Open(t)
	_, e := st.DB.Exec(`INSERT INTO admin_user(id,username,password_hash,created_at,updated_at) VALUES(1,'admin','test-only','now','now')`)
	if e != nil {
		t.Fatal(e)
	}
	sess, e := auth.NewSessionStore(st.DB).Create()
	if e != nil {
		t.Fatal(e)
	}
	key := [32]byte{1}
	v := vault.New(st.DB, &key)
	s := kubernetes.New(st.DB, v)
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	in := kubernetes.CredentialInput{Name: "cluster", Mode: "server_token", Context: "test", Server: "https://cluster.invalid", CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Token: "private-test-token-must-not-escape", Enabled: true}
	c, e := s.SaveCredential(context.Background(), "", 0, in)
	if e != nil {
		t.Fatal(e)
	}
	profile := kubernetes.ToolProfile{Protocol: kubernetes.ProfileProtocol, Image: "registry.invalid/kubectl@sha256:" + strings.Repeat("a", 64), Architecture: "amd64", KubectlPath: "/usr/bin/kubectl", UID: 1000, Network: "bridge"}
	conn, e := s.SaveConnection(context.Background(), "", 0, kubernetes.ConnectionInput{Name: "cluster", CredentialID: c.ID, Enabled: true, Profile: profile, Executors: []kubernetes.Executor{{Kind: "local"}}})
	if e != nil {
		t.Fatal(e)
	}
	legacy, e := v.Create(vault.CreateInput{Name: "git fixture", Type: vault.TypeGitToken, Secret: "test-git-token"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = st.DB.Exec(`INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES('project-1','fixture','https://git.invalid/repo',?,'now','now')`, legacy.ID)
	if e != nil {
		t.Fatal(e)
	}
	grant, e := s.SaveGrant(context.Background(), conn.ID, "", 0, kubernetes.GrantInput{ProjectID: "project-1", Environment: "test", Namespace: "app", Deployment: "backend", Containers: []string{"app"}, Executor: kubernetes.Executor{Kind: "local"}, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	target := kubernetes.Target{ProjectID: "project-1", Environment: "test", ConnectionID: conn.ID, CredentialID: c.ID, Context: c.Context, Namespace: "app", Deployment: "backend", Container: "app", Executor: kubernetes.Executor{Kind: "local"}}
	authorization, e := s.Authorize(context.Background(), target)
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.CreateOperation(context.Background(), kubernetes.OperationInput{RunID: "run-1", NodeID: "deploy/job", Attempt: 1, OwnerEpoch: "owner-test", Intent: kubernetes.Intent{Authorization: authorization, Image: "registry.invalid/backend@sha256:" + strings.Repeat("b", 64), TimeoutSeconds: 60, Source: kubernetes.ImageSource{RunID: "run-1", StageID: "build", JobID: "push", ArtifactID: "artifact-1", Commit: strings.Repeat("c", 40)}}})
	if e != nil {
		t.Fatal(e)
	}
	observer := &k8sTestObserver{}
	aud := &k8sAudit{}
	handler := New(testWebFSAuth(), auth.NewService(st.DB, nil), WithKubernetes(s, observer), WithVault(v), WithAudit(k8sRecorder{k8sAudit: aud}))
	return &k8sHTTPFixture{st, s, sess, handler, in, c, conn, grant, op, aud, observer}
}
func (f *k8sHTTPFixture) request(method, path string, body any, login, csrf bool) *httptest.ResponseRecorder {
	var raw []byte
	switch x := body.(type) {
	case string:
		raw = []byte(x)
	case nil:
	default:
		raw, _ = json.Marshal(x)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if login {
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: f.session.Token})
	}
	if csrf {
		r.Header.Set(headerCsrf, f.session.CSRFToken)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func TestKubernetesHTTPAuthStrictJSONAndSecretIsolation(t *testing.T) {
	f := newK8sHTTP(t)
	base := "/api/kubernetes/credentials"
	if w := f.request("GET", base, nil, false, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := f.request("POST", base, f.input, true, false); w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	for _, body := range []string{`{"expectedRevision":1,"expectedRevision":1}`, `{"enabled":true,"Enabled":false}`, `{"Enabled":true}`, `{"enabled":true,"unknown":1}`, `{"enabled":true} {}`, `[]`, `{"enabled":true,"x":` + strings.Repeat(`[`, 20) + `0` + strings.Repeat(`]`, 20) + `}`, `{"token":"` + strings.Repeat("s", 384<<10) + `"}`} {
		if w := f.request("POST", base, body, true, true); w.Code != 400 {
			t.Fatalf("strict status %d", w.Code)
		}
	}
	w := f.request("POST", base, f.input, true, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), f.input.Token) || strings.Contains(w.Body.String(), "certificate-authority-data") {
		t.Fatal("secret response")
	}
	var created kubernetes.Credential
	if json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Revision != 1 {
		t.Fatal("create DTO")
	}
	w = f.request("POST", base+"/preview", f.input, true, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	all, e := f.s.ListCredentials(context.Background())
	if e != nil || len(all) != 2 {
		t.Fatal("preview persisted", e, len(all))
	}
	for _, entry := range f.aud.entries {
		raw, _ := json.Marshal(entry)
		if bytes.Contains(raw, []byte(f.input.Token)) || bytes.Contains(raw, []byte("certificate")) {
			t.Fatal("secret in audit")
		}
	}
	if w = f.request("GET", base, nil, true, false); w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable credential metadata")
	}
	if w = f.request("POST", "/api/credentials/"+f.cred.ID+"/reveal", nil, true, true); w.Code != 403 || !strings.Contains(w.Body.String(), "protected_credential") {
		t.Fatal("generic reveal", w.Code, w.Body.String())
	}
}
func TestKubernetesHTTPCASRecentPasswordAndGrantScope(t *testing.T) {
	f := newK8sHTTP(t)
	path := "/api/kubernetes/credentials/" + f.cred.ID + "/enabled"
	if w := f.request("POST", path, `{"expectedRevision":99,"enabled":false}`, true, true); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	f.db.DB.Exec(`UPDATE sessions SET password_verified_at='2000-01-01T00:00:00Z'`)
	if w := f.request("POST", path, `{"expectedRevision":1,"enabled":false}`, true, true); w.Code != 403 || !strings.Contains(w.Body.String(), "recent_password_required") {
		t.Fatal(w.Code, w.Body.String())
	}
	f.db.DB.Exec(`UPDATE sessions SET password_verified_at=?`, time.Now().UTC().Format(time.RFC3339))
	if w := f.request("POST", path, `{"expectedRevision":1,"enabled":false}`, true, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.request("DELETE", "/api/kubernetes/connections/kconn_other/grants/"+f.grant.ID, `{"expectedRevision":1}`, true, true); w.Code != 404 {
		t.Fatal("cross connection grant", w.Code, w.Body.String())
	}
	if g, e := f.s.GetGrant(context.Background(), f.grant.ID); e != nil || !g.RequireApproval {
		t.Fatal("default approval lost", e)
	}
}
func TestKubernetesHTTPBoundOperationsAndRecheckAfterObservation(t *testing.T) {
	f := newK8sHTTP(t)
	w := f.request("GET", "/api/runs/run-1/kubernetes/operations", nil, true, false)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, field := range []string{"ownerEpoch", "authorization", "credentialId", "profileFingerprint", "approvedHash", "server", "hostKeySHA256"} {
		if strings.Contains(w.Body.String(), `"`+field+`"`) {
			t.Fatal("private field escaped", field)
		}
	}
	if w = f.request("POST", "/api/runs/run-other/kubernetes/operations/"+f.op.ID+"/reconcile", `{"expectedRevision":1}`, true, true); w.Code != 404 || f.observer.calls != 0 {
		t.Fatal("cross run observer", w.Code)
	}
	if w = f.request("POST", "/api/kubernetes/deploy", `{}`, true, true); w.Code != 404 {
		t.Fatal("arbitrary deploy route", w.Code)
	}
	o, e := f.s.FenceOwner(context.Background(), f.op.ID, f.op.OwnerEpoch, f.op.Revision)
	if e != nil {
		t.Fatal(e)
	}
	f.observer.beforeReturn = func() { f.db.DB.Exec(`DELETE FROM sessions WHERE token=?`, f.session.Token) }
	w = f.request("POST", "/api/runs/run-1/kubernetes/operations/"+o.ID+"/reconcile", map[string]int64{"expectedRevision": o.Revision}, true, true)
	if w.Code != 401 || f.observer.calls != 1 {
		t.Fatal("revocation after network was not checked", w.Code, w.Body.String())
	}
	current, e := f.s.GetOperation(context.Background(), o.ID)
	if e != nil || current.Revision != o.Revision {
		t.Fatal("revoked actor mutated operation", e)
	}
}
func TestKubernetesHTTPProbeCannotOverrideTrust(t *testing.T) {
	f := newK8sHTTP(t)
	path := "/api/kubernetes/connections/" + f.conn.ID + "/probe"
	w := f.request("POST", path, map[string]string{"grantId": f.grant.ID, "container": "app", "server": "https://evil.invalid"}, true, true)
	if w.Code != 400 || f.observer.calls != 0 {
		t.Fatal("trust override accepted")
	}
	w = f.request("POST", path, map[string]string{"grantId": f.grant.ID, "container": "other"}, true, true)
	if w.Code != 403 || f.observer.calls != 0 {
		t.Fatal("ungranted container", w.Code)
	}
	w = f.request("POST", path, map[string]string{"grantId": f.grant.ID, "container": "app"}, true, true)
	if w.Code != 200 || f.observer.calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestKubernetesJSONKeepsOpsLimit(t *testing.T) {
	type input struct {
		Text string `json:"text"`
	}
	raw := []byte(`{"text":"` + strings.Repeat("a", 70<<10) + `"}`)
	var x input
	if opsJSON(raw, &x) == nil {
		t.Fatal("ops limit widened")
	}
	if strictObjectJSON(raw, &x, 384<<10) != nil {
		t.Fatal("separate envelope limit rejected")
	}
	if code, _ := k8sErrorCode(errors.New("secret not echo")); code != 500 {
		t.Fatal(code)
	}
}

func TestKubernetesMaintenanceErrorIsActionable(t *testing.T) {
	if code, reason := k8sErrorCode(configstate.ErrMaintenance); code != 409 || reason != "kubernetes_in_use" {
		t.Fatal(code, reason)
	}
}
