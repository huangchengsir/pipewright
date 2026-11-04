package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func testProfile() ToolProfile {
	return ToolProfile{Protocol: ProfileProtocol, Image: "registry.invalid/tools/kubectl@sha256:" + strings.Repeat("a", 64), Architecture: "amd64", KubectlPath: "/usr/bin/kubectl", UID: 1000, Network: "bridge"}
}

type fixture struct {
	s      *Service
	db     *store.Store
	v      vault.Vault
	c      Credential
	conn   Connection
	grant  Grant
	target Target
	a      Authorization
}

func newFixture(t *testing.T, st *store.Store) *fixture {
	t.Helper()
	key := [32]byte{1, 2, 3}
	v := vault.New(st.DB, &key)
	legacy, e := v.Create(vault.CreateInput{Name: "git fixture", Type: vault.TypeGitToken, Secret: "fake-git-value"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = st.DB.Exec(`INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "project-1", "fixture", "https://git.invalid/repo", legacy.ID, "2026-11-03", "2026-11-03")
	if e != nil {
		t.Fatal(e)
	}
	s := New(st.DB, v)
	ctx := context.Background()
	c, e := s.SaveCredential(ctx, "", 0, credentialInput(t))
	if e != nil {
		t.Fatal(e)
	}
	conn, e := s.SaveConnection(ctx, "", 0, ConnectionInput{Name: "test cluster", CredentialID: c.ID, Enabled: true, Profile: testProfile(), Executors: []Executor{{Kind: "local"}}})
	if e != nil {
		t.Fatal(e)
	}
	g, e := s.SaveGrant(ctx, conn.ID, "", 0, GrantInput{ProjectID: "project-1", Environment: "production", Namespace: "app", Deployment: "backend", Containers: []string{"app"}, Executor: Executor{Kind: "local"}, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	target := Target{ProjectID: "project-1", Environment: "production", ConnectionID: conn.ID, CredentialID: c.ID, Context: c.Context, Namespace: "app", Deployment: "backend", Container: "app", Executor: Executor{Kind: "local"}}
	a, e := s.Authorize(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	return &fixture{s, st, v, c, conn, g, target, a}
}
func (f *fixture) operation(t *testing.T, run string) Operation {
	t.Helper()
	o, e := f.s.CreateOperation(context.Background(), OperationInput{RunID: run, NodeID: "deploy/job", Attempt: 1, OwnerEpoch: "owner-1", Intent: Intent{Authorization: f.a, Image: "registry.invalid/app/backend@sha256:" + strings.Repeat("b", 64), TimeoutSeconds: 300, Source: ImageSource{RunID: run, StageID: "build", JobID: "push", ArtifactID: "image-artifact", Commit: strings.Repeat("c", 40)}}})
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func (f *fixture) runtime(t *testing.T, o Operation) RuntimeProof {
	t.Helper()
	ctx := context.Background()
	l, e := f.s.RegisterRuntime(ctx, o.ID, o.OwnerEpoch, "engine-test")
	if e != nil {
		t.Fatal(e)
	}
	p := RuntimeProof{OperationID: o.ID, OwnerEpoch: o.OwnerEpoch, EngineID: l.EngineID, ContainerID: strings.Repeat("d", 64), ProfileFingerprint: f.a.ProfileFingerprint, AuthorizationFingerprint: f.a.Fingerprint, Linux: true, PrivateTmpfs: true, SwapProtected: true, CoreDisabled: true, NonRoot: true, ReadOnlyRoot: true, BootstrapVerified: true, ClientVersionVerified: true}
	if _, e = f.s.VerifyRuntime(ctx, p, l.Revision); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestCredentialIsolationAndBoundEnvelope(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		if _, e := f.v.Reveal(f.c.ID); !errors.Is(e, vault.ErrProtectedCredential) {
			t.Fatalf("generic reveal: %v", e)
		}
		views, e := f.s.ListCredentials(ctx)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := json.Marshal(views)
		if bytes.Contains(body, []byte(credentialInput(t).Token)) {
			t.Fatal("metadata leak")
		}
		var cipher []byte
		if e = st.DB.QueryRow(`SELECT secret_cipher FROM k8s_credentials WHERE id=?`, f.c.ID).Scan(&cipher); e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(cipher, []byte(credentialInput(t).Token)) {
			t.Fatal("plaintext persisted")
		}
		o := f.operation(t, "run-1")
		p := f.runtime(t, o)
		approved, _ := f.s.GetOperation(ctx, o.ID)
		if e = f.s.UseCredential(ctx, f.a, p, func(context.Context, []byte) error { t.Fatal("unapproved secret use"); return nil }); e != ErrDenied {
			t.Fatal("approval absent did not block secret", e)
		}
		if _, e = f.s.ApproveOperation(ctx, o.ID, o.OwnerEpoch, approved.Revision, approved.IntentHash); e != nil {
			t.Fatal(e)
		}
		var retained []byte
		called := false
		if e = f.s.UseCredential(ctx, f.a, p, func(_ context.Context, b []byte) error {
			called = true
			retained = b
			if !bytes.Contains(b, []byte(credentialInput(t).Token)) {
				t.Fatal("wrong secret")
			}
			return nil
		}); e != nil || !called {
			t.Fatal("authorized lease failed", e)
		}
		for _, b := range retained {
			if b != 0 {
				t.Fatal("lease buffer not wiped")
			}
		}
		other, e := f.s.SaveCredential(ctx, "", 0, credentialInput(t))
		if e != nil {
			t.Fatal(e)
		}
		var otherCipher []byte
		_ = st.DB.QueryRow(`SELECT secret_cipher FROM k8s_credentials WHERE id=?`, other.ID).Scan(&otherCipher)
		_, e = st.DB.Exec(`UPDATE k8s_credentials SET secret_cipher=? WHERE id=?`, otherCipher, f.c.ID)
		if e != nil {
			t.Fatal(e)
		}
		freshOp := f.operation(t, "cross-row-run")
		freshProof := f.runtime(t, freshOp)
		freshOp, _ = f.s.GetOperation(ctx, freshOp.ID)
		if _, e = f.s.ApproveOperation(ctx, freshOp.ID, freshOp.OwnerEpoch, freshOp.Revision, freshOp.IntentHash); e != nil {
			t.Fatal(e)
		}
		if e = f.s.UseCredential(ctx, f.a, freshProof, func(context.Context, []byte) error { t.Fatal("cross-row material consumed"); return nil }); e != ErrDecrypt {
			t.Fatalf("cross-row not rejected: %v", e)
		}
	})
}
func TestAuthorizeExactScopeAndRotation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		if !f.grant.RequireApproval || !f.a.RequireApproval {
			t.Fatal("approval not default")
		}
		changes := map[string]func(*Target){"project": func(t *Target) { t.ProjectID = "project-2" }, "environment-case": func(t *Target) { t.Environment = "Production" }, "empty-environment": func(t *Target) { t.Environment = "" }, "namespace": func(t *Target) { t.Namespace = "other" }, "deployment": func(t *Target) { t.Deployment = "other" }, "container": func(t *Target) { t.Container = "sidecar" }, "credential": func(t *Target) { t.CredentialID = "kcred_other" }, "context": func(t *Target) { t.Context = "other" }, "executor": func(t *Target) { t.Executor.Kind = "ssh" }}
		for n, change := range changes {
			t.Run(n, func(t *testing.T) {
				target := f.target
				change(&target)
				if _, e := f.s.Authorize(ctx, target); e == nil {
					t.Fatal("scope allowed")
				}
			})
		}
		in := credentialInput(t)
		in.Token = "rotated-synthetic-value"
		c, e := f.s.SaveCredential(ctx, f.c.ID, f.c.Revision, in)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.ValidateAuthorization(ctx, f.a); e != ErrConflict {
			t.Fatalf("old authorization survived rotation: %v", e)
		}
		fresh, e := f.s.Authorize(ctx, f.target)
		if e != nil || fresh.CredentialRevision != c.Revision {
			t.Fatal("new version not admitted", e)
		}
		if _, e = f.s.SaveCredential(ctx, f.c.ID, f.c.Revision, in); e != ErrConflict {
			t.Fatal("stale write allowed", e)
		}
		in.Server = "https://other.invalid"
		if _, e = f.s.SaveCredential(ctx, f.c.ID, c.Revision, in); e != ErrInUse {
			t.Fatal("trust switch by rotation allowed", e)
		}
		if e = f.s.DeleteCredential(ctx, f.c.ID, c.Revision); e != ErrInUse {
			t.Fatal("referenced credential deleted", e)
		}
	})
}
func TestUnverifiedRuntimeCannotDecrypt(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	o := f.operation(t, "run-1")
	p := f.runtime(t, o)
	ctx := context.Background()
	for name, change := range map[string]func(*RuntimeProof){"swap": func(p *RuntimeProof) { p.SwapProtected = false }, "tmpfs": func(p *RuntimeProof) { p.PrivateTmpfs = false }, "core": func(p *RuntimeProof) { p.CoreDisabled = false }, "root": func(p *RuntimeProof) { p.NonRoot = false }, "engine": func(p *RuntimeProof) { p.EngineID = "other-engine" }, "cid": func(p *RuntimeProof) { p.ContainerID = strings.Repeat("e", 64) }, "profile": func(p *RuntimeProof) { p.ProfileFingerprint = strings.Repeat("e", 64) }} {
		t.Run(name, func(t *testing.T) {
			bad := p
			change(&bad)
			if e := f.s.UseCredential(ctx, f.a, bad, func(context.Context, []byte) error { t.Fatal("unverified runtime got secret"); return nil }); e == nil {
				t.Fatal("not rejected")
			}
		})
	}
	disabled, e := f.s.SetCredentialEnabled(ctx, f.c.ID, f.c.Revision, false)
	if e != nil || disabled.Enabled {
		t.Fatal(e)
	}
	if e = f.s.UseCredential(ctx, f.a, p, func(context.Context, []byte) error { t.Fatal("disabled credential consumed"); return nil }); e != ErrDenied {
		t.Fatal(e)
	}
}
func TestGrantCASAndDeleteRevokesAdmission(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	in := GrantInput{ProjectID: f.grant.ProjectID, Environment: f.grant.Environment, Namespace: f.grant.Namespace, Deployment: f.grant.Deployment, Containers: f.grant.Containers, Executor: f.grant.Executor, Enabled: true}
	if _, e := f.s.SaveGrant(ctx, f.conn.ID, "", 0, in); e != ErrConflict {
		t.Fatal("duplicate grant allowed", e)
	}
	no := false
	in.RequireApproval = &no
	g, e := f.s.SaveGrant(ctx, f.conn.ID, f.grant.ID, f.grant.Revision, in)
	if e != nil || g.RequireApproval {
		t.Fatal("explicit automation grant failed", e)
	}
	if e = f.s.ValidateAuthorization(ctx, f.a); e != ErrConflict {
		t.Fatal("old grant remained valid", e)
	}
	if e = f.s.DeleteGrant(ctx, g.ID, g.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Authorize(ctx, f.target); e != ErrDenied {
		t.Fatal("deleted grant allowed", e)
	}
}
func TestPersistentHoldConcurrencyAndNoImplicitExpiry(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		a := f.operation(t, "run-1")
		b := f.operation(t, "run-2")
		ctx := context.Background()
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, op := range []Operation{a, b} {
			wg.Add(1)
			go func(o Operation) {
				defer wg.Done()
				_, e := f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, "deployment-uid")
				errs <- e
			}(op)
		}
		wg.Wait()
		close(errs)
		won, lost := 0, 0
		for e := range errs {
			if e == nil {
				won++
			} else if errors.Is(e, ErrHeld) {
				lost++
			} else {
				t.Fatal(e)
			}
		}
		if won != 1 || lost != 1 {
			t.Fatal("hold not exclusive")
		}
		var heldID string
		if e := st.DB.QueryRow(`SELECT operation_id FROM k8s_workload_holds`).Scan(&heldID); e != nil {
			t.Fatal(e)
		}
		_, _ = st.DB.Exec(`UPDATE k8s_workload_holds SET created_at=1`)
		fresh := f.operation(t, "run-3")
		if _, e := f.s.AcquireHold(ctx, fresh.ID, fresh.OwnerEpoch, "deployment-uid"); e != ErrHeld {
			t.Fatal("age allowed takeover", e)
		}
		persisted, e := New(st.DB, f.v).GetOperation(ctx, heldID)
		if e != nil || persisted.ID != heldID {
			t.Fatal("restart read failed", e)
		}
	})
}

func TestEnvelopePurposeRevisionAndKeyMismatch(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	var cipher []byte
	_ = f.db.DB.QueryRow(`SELECT secret_cipher FROM k8s_credentials WHERE id=?`, f.c.ID).Scan(&cipher)
	for _, test := range []struct {
		name, purpose, id string
		revision          int64
	}{{"purpose", "other", f.c.ID, f.c.Revision}, {"revision", credentialPurpose, f.c.ID, 999}, {"id", credentialPurpose, "kcred_other", f.c.Revision}} {
		t.Run(test.name, func(t *testing.T) {
			if _, e := f.s.open(test.purpose, test.id, test.revision, cipher); e != ErrDecrypt {
				t.Fatal("envelope accepted", e)
			}
		})
	}
	wrong := [32]byte{9}
	s := New(f.db.DB, vault.New(f.db.DB, &wrong))
	if _, e := s.open(credentialPurpose, f.c.ID, f.c.Revision, cipher); e != ErrDecrypt {
		t.Fatal("wrong key accepted", e)
	}
	var policy, secrets int64
	_ = f.db.DB.QueryRow(`SELECT value FROM config_control_epochs WHERE name='policy'`).Scan(&policy)
	_ = f.db.DB.QueryRow(`SELECT value FROM config_control_epochs WHERE name='secret_catalog'`).Scan(&secrets)
	if _, e := f.s.SetCredentialEnabled(ctx, f.c.ID, f.c.Revision, false); e != nil {
		t.Fatal(e)
	}
	var p2, s2 int64
	_ = f.db.DB.QueryRow(`SELECT value FROM config_control_epochs WHERE name='policy'`).Scan(&p2)
	_ = f.db.DB.QueryRow(`SELECT value FROM config_control_epochs WHERE name='secret_catalog'`).Scan(&s2)
	if p2 != policy+1 || s2 != secrets+1 {
		t.Fatal("config change did not invalidate catalogs")
	}
}

func TestConfiguredSSHExecutorUsesOnlyServerOwnedHostPin(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	pin := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	executor := Executor{Kind: "ssh", ServerID: "server-one", HostKeySHA256: pin}
	sshCred, e := f.v.Create(vault.CreateInput{Name: "ssh test", Type: vault.TypeSSHPassword, Secret: "test-only-password"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.DB.Exec(`INSERT INTO servers(id,name,host,port,user,credential_id,created_at,updated_at) VALUES('server-one','fixture','192.0.2.10',22,'fixture',?,'old','old')`, sshCred.ID); e != nil {
		t.Fatal(e)
	}

	c, e := f.s.SaveConnection(ctx, "", 0, ConnectionInput{Name: "ssh cluster", CredentialID: f.c.ID, Enabled: true, Profile: testProfile(), Executors: []Executor{executor}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.SaveGrant(ctx, c.ID, "", 0, GrantInput{ProjectID: "project-1", Environment: "production", Namespace: "app", Deployment: "backend", Containers: []string{"app"}, Executor: executor, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	target := f.target
	target.ConnectionID = c.ID
	target.Executor = Executor{Kind: "ssh", ServerID: "server-one"}
	a, e := f.s.AuthorizeConfigured(ctx, target)
	if e != nil || a.Target.Executor.HostKeySHA256 != pin {
		t.Fatal(a, e)
	}
	target.Executor.HostKeySHA256 = pin
	if _, e = f.s.AuthorizeConfigured(ctx, target); e != ErrInvalid {
		t.Fatal("job-supplied pin accepted", e)
	}
}
