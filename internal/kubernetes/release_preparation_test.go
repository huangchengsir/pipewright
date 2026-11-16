package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"testing"
)

func TestReleasePreparationDiagnosticsAreClosedAndPreserveCause(t *testing.T) {
	private := errors.New("AdmissionSecretIdentifier")
	err := WithReleasePreparationPhase(private, "UserPrivatePhase")
	phase, reason, ok := ReleasePreparationDetails(err)
	if !ok || phase != "prepare" || reason != "preparation_failed" || !errors.Is(err, private) || strings.Contains(err.Error(), private.Error()) || strings.Contains(err.Error(), "UserPrivatePhase") {
		t.Fatal("unclosed diagnostic")
	}
	for _, phase := range []string{"inspect", "capability", "helper_init", "version", "discovery"} {
		e := WithReleasePreparationPhase(ErrRuntimeUnverified, phase)
		wrapped := WithReleasePreparationPhase(errors.Join(e, private), "manifest")
		got, reason, ok := ReleasePreparationDetails(wrapped)
		if !ok || got != phase || reason != "secret_runtime_unverified" || !errors.Is(wrapped, ErrRuntimeUnverified) || strings.Contains(wrapped.Error(), private.Error()) {
			t.Fatal("specific phase/cause lost")
		}
	}
	if WithReleasePreparationPhase(nil, "manifest") != nil {
		t.Fatal("success became failure")
	}
}

type releasePhaseTransport struct {
	*fakePrivateDocker
	helperVersion, helperPing bool
}

func (f *releasePhaseTransport) Docker(ctx context.Context, args []string, in []byte) (*target.SecretResult, error) {
	if len(args) > 0 && args[0] == "exec" {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "pipewright-kube-tool version") && f.helperVersion {
			return &target.SecretResult{Stdout: []byte("pipewright-kube-tool/2\n")}, nil
		}
		if strings.Contains(joined, "/usr/bin/helm version") && f.helperVersion {
			return &target.SecretResult{Stdout: []byte(ReleaseHelmVersion)}, nil
		}
		if strings.Contains(joined, "pipewright-kube-tool serve") {
			return &target.SecretResult{}, nil
		}
		if strings.Contains(joined, "pipewright-kube-tool rpc") && !f.helperPing {
			return &target.SecretResult{Stdout: []byte(`{"version":2,"code":"rejected"}`)}, nil
		}
	}
	out, err := f.fakePrivateDocker.Docker(ctx, args, in)
	if len(args) > 0 && args[0] == "create" {
		f.info.HostConfig.Tmpfs["/run/pipewright"] = "rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=1000,gid=1000"
	}
	return out, err
}
func TestReleaseRuntimePreparationReportsInspectCapabilityVersionAndHelperPhases(t *testing.T) {
	for _, phase := range []string{"inspect", "capability", "version", "helper_init"} {
		t.Run(phase, func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			tr := &releasePhaseTransport{fakePrivateDocker: &fakePrivateDocker{}, helperVersion: phase == "helper_init"}
			tr.volumes = phase == "inspect"
			tr.swap = phase == "capability"
			runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
			_, err := runtime.OpenReleasePreview(context.Background(), p.Authorization, "phase-owner")
			got, _, ok := ReleasePreparationDetails(err)
			if !ok || got != phase {
				t.Fatalf("wanted closed phase %s, got %s", phase, got)
			}
			if tr.receivedCredential {
				t.Fatal("precredential failure consumed cluster secret")
			}
		})
	}
}

func TestReleaseRuntimeImageIdentityUsesDockerSHA256(t *testing.T) {
	ctx := context.Background()
	f, p := releaseStoreFixture(t, storetest.Open(t))
	lease, e := f.s.RegisterReleaseRuntime(ctx, "", "image-owner", "engine-test", p.Authorization)
	if e != nil {
		t.Fatal(e)
	}
	for _, image := range []string{strings.Repeat("a", 64), "sha256:" + strings.Repeat("a", 63), "sha256:" + strings.Repeat("A", 64), "other:" + strings.Repeat("a", 64)} {
		if _, e := f.s.PrepareReleaseRuntimeCreate(ctx, lease.ID, "image-owner", lease.Runtime.Revision, nil, image); !errors.Is(e, ErrInvalid) {
			t.Fatal("invalid image identity accepted")
		}
	}
	image := "sha256:" + strings.Repeat("a", 64)
	prepared, e := f.s.PrepareReleaseRuntimeCreate(ctx, lease.ID, "image-owner", lease.Runtime.Revision, nil, image)
	if e != nil || prepared.Runtime.ToolImageID != image {
		t.Fatal("Docker image identity rejected", e)
	}
}

func TestReleasePreparationRecognizesTenantDenial(t *testing.T) {
	private := errors.New("private authorization detail")
	cause := errors.Join(tenancy.ErrForbidden, private)
	err := WithReleasePreparationPhase(cause, "authorization")
	phase, reason, ok := ReleasePreparationDetails(err)
	if !ok || phase != "authorization" || reason != "kubernetes_not_authorized" {
		t.Fatalf("got phase=%q reason=%q", phase, reason)
	}
	if !errors.Is(err, tenancy.ErrForbidden) || strings.Contains(err.Error(), private.Error()) {
		t.Fatal("authorization classification must preserve the cause without logging its text")
	}
}

type revokedIdentityTransport struct {
	*fakePrivateDocker
	revokedOn string
	commands  []string
}

func (t *revokedIdentityTransport) Docker(ctx context.Context, args []string, in []byte) (*target.SecretResult, error) {
	t.commands = append(t.commands, args[0])
	if args[0] == t.revokedOn {
		return nil, errors.Join(tenancy.ErrForbidden, errors.New("private executor detail"))
	}
	return t.fakePrivateDocker.Docker(ctx, args, in)
}

func TestReleaseRPCReportsTransportRevocationWithoutDispatch(t *testing.T) {
	for _, command := range []string{"info", "inspect"} {
		t.Run(command, func(t *testing.T) {
			ctx := context.Background()
			f, plan := releaseStoreFixture(t, storetest.Open(t))
			transport := &revokedIdentityTransport{fakePrivateDocker: &fakePrivateDocker{}, revokedOn: command}
			session := &ReleaseSession{
				runtime:   NewRuntime(f.s, nil, nil),
				transport: transport,
				lease: ReleaseRuntimeLease{
					Authorization: plan.Authorization,
					Runtime:       RuntimeLease{EngineID: "engine-test"},
				},
			}
			_, err := session.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "read"})
			_, reason, ok := ReleasePreparationDetails(err)
			if !ok || reason != "kubernetes_not_authorized" || !errors.Is(err, tenancy.ErrForbidden) {
				t.Fatalf("expected authorization denial, got %v", err)
			}
			for _, command := range transport.commands {
				if command == "exec" {
					t.Fatal("revocation dispatched a private command")
				}
			}
		})
	}
}
