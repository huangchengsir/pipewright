package target

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceCleanupIsBoundedAndCannotFollowChangedTarget(t *testing.T) {
	for _, variant := range []string{"host", "credential", "expired", "ownership", "transport"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t)
			v := vault.New(db, testMasterKey())
			cred := newSSHCred(t, v, "workspace-test-secret")
			d := &capturingDialer{res: &ExecResult{}}
			svc := New(db, v, d)
			srv, e := svc.Create(ctx, CreateInput{Name: "workspace", Host: "127.0.0.1", User: "deploy", CredentialID: cred})
			if e != nil {
				t.Fatal(e)
			}
			w, e := svc.(WorkspaceCreator).CreateWorkspace(ctx, srv.ID)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.HasPrefix(w.Directory(), "/tmp/pipewright-work-") || w.Archive() != w.root+"/source.tar.gz" || len(d.gotCmd) != 6 || d.gotCmd[2] != createWorkspaceScript {
				t.Fatal("unexpected creation boundary")
			}
			d.gotAddr = ""
			switch variant {
			case "host":
				_, e = db.Exec(`UPDATE servers SET host='192.0.2.8' WHERE id=?`, srv.ID)
			case "credential":
				secret := "rotated"
				_, e = v.Update(cred, vault.UpdateInput{Secret: &secret})
			case "expired":
				w.expires = time.Now().Add(-time.Second)
			case "ownership":
				d.res = &ExecResult{ExitCode: 75, Stderr: "workspace-test-secret"}
			case "transport":
				d.err = errors.New("workspace-test-secret")
			}
			if e != nil {
				t.Fatal(e)
			}
			if variant == "host" || variant == "credential" {
				if e = w.Upload(ctx, srv.ID, strings.NewReader("private-source"), w.Archive()); !errors.Is(e, ErrTargetChanged) || d.gotAddr != "" {
					t.Fatal("changed target received source", e)
				}
				if _, e = w.Exec(ctx, srv.ID, []string{"uname"}); !errors.Is(e, ErrTargetChanged) || d.gotAddr != "" {
					t.Fatal("changed target received command", e)
				}
			}
			e = w.Cleanup(ctx)
			if e == nil || strings.Contains(e.Error(), "workspace-test-secret") {
				t.Fatal("cleanup must report safe failure", e)
			}
			if variant == "host" || variant == "credential" || variant == "expired" {
				if d.gotAddr != "" {
					t.Fatal("invalid capability dialed")
				}
			}
			d.gotAddr = ""
			if again := w.Cleanup(ctx); again != e || d.gotAddr != "" {
				t.Fatal("failed cleanup replayed")
			}
		})
	}
	if e := (&RemoteWorkspace{}).Cleanup(context.Background()); !errors.Is(e, ErrWorkspaceCleanup) {
		t.Fatal("zero-value cleanup allowed")
	}
}
