package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

type fakePrivateDocker struct {
	serverMinor                                                                    string
	info                                                                           containerInfo
	created, removed                                                               bool
	createCount, patchCount, secretCount                                           int
	lostCreate, lateCreate, foreign, swap, core, volumes, wrongEngine, failCleanup bool
	receivedCredential                                                             bool
	calls                                                                          [][]string
	reviewCount                                                                    int
	reviewRequest                                                                  []byte
	reviewResult                                                                   *target.SecretResult
	reviewErr                                                                      error
	afterReview                                                                    func()
	deployment                                                                     []byte
	patchResult                                                                    *target.SecretResult
	patchErr                                                                       error
}

func (f *fakePrivateDocker) Snapshot() *target.ConnectionSnapshot       { return nil }
func (f *fakePrivateDocker) Pull(context.Context, string, []byte) error { return nil }
func fakeResult(v any) *target.SecretResult {
	b, _ := json.Marshal(v)
	return &target.SecretResult{Stdout: b}
}
func fixtureProbe() string {
	return "PWCORE\n0\nPWCOREPATTERN\ncore\nPWSTATUS\nUid:\t1000\t1000\t1000\t1000\nPWMOUNTS\n1 2 0:1 / /run/pipewright rw,nosuid,nodev,noexec - tmpfs tmpfs rw,size=8192k\nPWSWAPS\nFilename\tType\tSize\tUsed\tPriority\nPWEND\n"
}
func (f *fakePrivateDocker) Docker(_ context.Context, args []string, in []byte) (*target.SecretResult, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if bytes.Contains(in, []byte("fixture-cluster-token")) {
		f.receivedCredential = true
	}
	switch args[0] {
	case "info":
		id := "engine-test"
		if f.wrongEngine {
			id = "different-engine"
		}
		return fakeResult(map[string]any{"ID": id, "OSType": "linux", "Architecture": "x86_64"}), nil
	case "image":
		cfg := map[string]any{"Volumes": map[string]any{}, "Env": []string{"PATH=/bin:/usr/bin"}}
		if f.volumes {
			cfg["Volumes"] = map[string]any{"/tmp": map[string]any{}}
		}
		return fakeResult(map[string]any{"Id": "sha256:" + strings.Repeat("f", 64), "Os": "linux", "Architecture": "amd64", "RepoDigests": []string{testProfile().Image}, "Config": cfg}), nil
	case "create":
		f.createCount++
		get := func(key string) string {
			for i, v := range args {
				if v == key && i+1 < len(args) {
					return args[i+1]
				}
			}
			return ""
		}
		f.info.ID = strings.Repeat("d", 64)
		f.info.Name = "/" + get("--name")
		f.info.Image = "sha256:" + strings.Repeat("f", 64)
		f.info.Config.User = "1000:1000"
		f.info.Config.Entrypoint = []string{"/bin/sh"}
		f.info.Config.Cmd = []string{"-c", bootstrap}
		f.info.Config.Labels = map[string]string{}
		for i, v := range args {
			if v == "--label" {
				kv := strings.SplitN(args[i+1], "=", 2)
				f.info.Config.Labels[kv[0]] = kv[1]
			}
		}
		h := &f.info.HostConfig
		h.ReadonlyRootfs = true
		h.CapDrop = []string{"ALL"}
		h.SecurityOpt = []string{"no-new-privileges:true"}
		h.NetworkMode = "bridge"
		h.NanoCpus = 1000000000
		h.Memory = 268435456
		h.MemorySwap = 268435456
		h.PidsLimit = 64
		h.RestartPolicy.Name = "no"
		h.LogConfig.Type = "none"
		h.Tmpfs = map[string]string{"/run/pipewright": "rw,noexec,nosuid,nodev,size=8m,mode=0700,uid=1000,gid=1000"}
		h.Ulimits = []struct {
			Name       string
			Soft, Hard int64
		}{{"core", 0, 0}}
		f.created = !f.lateCreate
		if f.lostCreate {
			return nil, ErrTransportUnknown
		}
		return &target.SecretResult{Stdout: []byte(f.info.ID)}, nil
	case "inspect":
		if !f.created || f.removed {
			return &target.SecretResult{ExitCode: 1, Stderr: []byte("WARNING: Error loading config file: open /dev/null/config.json: not a directory\nError: No such object: " + args[len(args)-1])}, nil
		}
		v := f.info
		if f.foreign {
			v.Config.Labels = map[string]string{ownerLabel: "foreign"}
		}
		return fakeResult(v), nil
	case "start":
		f.info.State.Running = true
		f.info.State.Pid = 91
		return &target.SecretResult{}, nil
	case "stop":
		if f.failCleanup {
			return nil, ErrTransportUnknown
		}
		f.info.State.Running = false
		f.info.State.Pid = 0
		return &target.SecretResult{}, nil
	case "rm":
		f.removed = true
		return &target.SecretResult{}, nil
	case "exec":
		joined := strings.Join(args, " ")
		if strings.Contains(joined, capabilityProbe) {
			p := fixtureProbe()
			if f.swap {
				p = strings.Replace(p, "PWEND", "/swap file 1024 0 -2\nPWEND", 1)
			}
			if f.core {
				p = strings.Replace(p, "PWCORE\n0", "PWCORE\nunlimited", 1)
			}
			return &target.SecretResult{Stdout: []byte(p)}, nil
		}
		if strings.Contains(joined, credentialWriter) {
			f.secretCount++
			return &target.SecretResult{}, nil
		}
		if strings.Contains(joined, "version") {
			minor := f.serverMinor
			if minor == "" {
				minor = "34"
			}
			return fakeResult(map[string]any{"clientVersion": map[string]string{"major": "1", "minor": "34"}, "serverVersion": map[string]string{"major": "1", "minor": minor}}), nil
		}
		if strings.Contains(joined, " create --raw=") {
			f.reviewCount++
			f.reviewRequest = append([]byte(nil), in...)
			if f.afterReview != nil {
				f.afterReview()
			}
			if f.reviewResult != nil || f.reviewErr != nil {
				return f.reviewResult, f.reviewErr
			}
			var review map[string]any
			if json.Unmarshal(in, &review) != nil {
				return nil, ErrTool
			}
			review["status"] = map[string]any{"allowed": true}
			return fakeResult(review), nil
		}
		if strings.Contains(joined, " get deployment ") {
			if f.deployment != nil {
				return &target.SecretResult{Stdout: append([]byte(nil), f.deployment...)}, nil
			}
			return &target.SecretResult{Stdout: []byte(`{"kind":"Deployment","metadata":{"uid":"workload"}}`)}, nil
		}
		if strings.Contains(joined, " patch deployment ") {
			f.patchCount++
			if f.patchResult != nil || f.patchErr != nil {
				return f.patchResult, f.patchErr
			}
			return &target.SecretResult{Stdout: []byte(`{"kind":"Deployment"}`)}, nil
		}
	}
	return nil, ErrTool
}
func testRuntime(t *testing.T) (*fixture, *Runtime, *fakePrivateDocker, Operation) {
	t.Helper()
	f := newFixture(t, storetest.Open(t))
	fake := &fakePrivateDocker{}
	rt := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) {
		return fake, nil
	}, nil)
	o := f.operation(t, "run-one")
	var e error
	o, e = f.s.ApproveOperation(context.Background(), o.ID, o.OwnerEpoch, o.Revision, o.IntentHash)
	if e != nil {
		t.Fatal(e)
	}
	return f, rt, fake, o
}
func TestPrivateRuntimeHappyPathAndSecretOnlyStdin(t *testing.T) {
	f, r, d, o := testRuntime(t)
	ctx := context.Background()
	w, e := r.OpenWriter(ctx, o.ID, o.OwnerEpoch)
	if e != nil {
		t.Fatal(e)
	}
	if d.createCount != 1 || d.secretCount != 1 {
		t.Fatal("unexpected dispatch", d.createCount, d.secretCount)
	}
	for _, args := range d.calls {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, credentialInput(t).Token) {
			t.Fatal("secret in argv")
		}
	}
	l, e := f.s.GetRuntimeLease(ctx, o.ID)
	if e != nil || !l.CredentialInjected || !l.CreateRequested || l.Proof == nil {
		t.Fatal(l, e)
	}
	raw, e := w.GetDeployment(ctx)
	if e != nil || !bytes.Contains(raw, []byte("Deployment")) {
		t.Fatal(e)
	}
	clear(raw)
	if _, e = r.OpenWriter(ctx, o.ID, o.OwnerEpoch); e != ErrConflict {
		t.Fatal("second runtime accepted", e)
	}
	l, e = w.Close(ctx)
	if e != nil || l.Cleanup != CleanupVerified || !d.removed {
		t.Fatal(l, e)
	}
}
func TestPrivateRuntimeRejectsUnsafeCapabilitiesBeforeSecret(t *testing.T) {
	for _, kind := range []string{"swap", "core", "volume"} {
		t.Run(kind, func(t *testing.T) {
			_, r, d, o := testRuntime(t)
			switch kind {
			case "swap":
				d.swap = true
			case "core":
				d.core = true
			case "volume":
				d.volumes = true
			}
			if _, e := r.OpenWriter(context.Background(), o.ID, o.OwnerEpoch); e != ErrRuntimeUnverified {
				t.Fatal(e)
			}
			if d.secretCount != 0 {
				t.Fatal("secret exposed before safety")
			}
		})
	}
}
func TestPrivateRuntimeLostCreateUsesExactNameOnce(t *testing.T) {
	_, r, d, o := testRuntime(t)
	d.lostCreate = true
	w, e := r.OpenWriter(context.Background(), o.ID, o.OwnerEpoch)
	if e != nil {
		t.Fatal(e)
	}
	if d.createCount != 1 {
		t.Fatal("create replay")
	}
	if _, e = w.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestPrivateRuntimeMissingAfterAmbiguousCreateStaysBlocked(t *testing.T) {
	f, r, d, o := testRuntime(t)
	d.lostCreate = true
	d.lateCreate = true
	ctx := context.Background()
	if _, e := r.OpenWriter(ctx, o.ID, o.OwnerEpoch); e != ErrTransportUnknown {
		t.Fatal(e)
	}
	l, e := f.s.GetRuntimeLease(ctx, o.ID)
	if e != nil || l.Cleanup != CleanupBlocked || l.ContainerID != "" {
		t.Fatal(l, e)
	}
	current, _ := f.s.GetOperation(ctx, o.ID)
	if _, e = f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, current.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Cleanup(ctx, o.ID); e != ErrRuntimeUnverified {
		t.Fatal("404 cleared ambiguity", e)
	}
	d.created = true
	l, e = r.Cleanup(ctx, o.ID)
	if e != nil || l.Cleanup != CleanupVerified || d.createCount != 1 {
		t.Fatal(l, e)
	}
}
func TestPrivateRuntimeForeignAndChangedEngineNeverDeleted(t *testing.T) {
	for _, kind := range []string{"foreign", "engine"} {
		t.Run(kind, func(t *testing.T) {
			_, r, d, o := testRuntime(t)
			w, e := r.OpenWriter(context.Background(), o.ID, o.OwnerEpoch)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "foreign" {
				d.foreign = true
			} else {
				d.wrongEngine = true
			}
			l, e := w.Close(context.Background())
			if e != ErrRuntimeUnverified || l.Cleanup != CleanupBlocked || d.removed {
				t.Fatal("foreign cleanup", l, e)
			}
		})
	}
}
func TestObserverUsesSeparateLeaseAndCannotChangeOriginal(t *testing.T) {
	f, r, d, o := testRuntime(t)
	ctx := context.Background()
	w, e := r.OpenWriter(ctx, o.ID, o.OwnerEpoch)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Close(ctx); e != nil {
		t.Fatal(e)
	}
	original, _ := f.s.GetOperation(ctx, o.ID)
	original, e = f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, original.Revision)
	if e != nil {
		t.Fatal(e)
	}
	oldLease, _ := f.s.GetRuntimeLease(ctx, o.ID)
	d.created = false
	d.removed = false
	obs, e := r.OpenObserver(ctx, "reconcile", f.target, o.ID, "observer-owner")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(obs.LeaseID(), "kobs_") || obs.LeaseID() == o.ID {
		t.Fatal("observer reused writer")
	}
	raw, e := obs.GetDeployment(ctx)
	clear(raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = obs.Close(ctx); e != nil {
		t.Fatal(e)
	}
	current, _ := f.s.GetOperation(ctx, o.ID)
	lease, _ := f.s.GetRuntimeLease(ctx, o.ID)
	if current.Revision != original.Revision || lease.Revision != oldLease.Revision || d.patchCount != 0 {
		t.Fatal("observer modified source")
	}
}
func TestObserverRequiresCurrentGrantAndDoesNotReviveApproval(t *testing.T) {
	f, r, _, o := testRuntime(t)
	ctx := context.Background()
	if _, e := r.OpenObserver(ctx, "reconcile", f.target, o.ID, "observer"); e != ErrOwner {
		t.Fatal("active writer reconciled", e)
	}
	if e := f.s.DeleteGrant(ctx, f.grant.ID, f.grant.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e := r.OpenObserver(ctx, "probe", f.target, "", "observer"); e != ErrDenied {
		t.Fatal("revoked observer", e)
	}
}
func TestFixedPatchRejectsAlternateMutation(t *testing.T) {
	_, _, _, o := testRuntime(t)
	receipt := beforeReceipt()
	receipt.PreparedUID = receipt.UID
	receipt.PreparedResourceVersion = receipt.ResourceVersion
	o.Receipt = &receipt
	patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": receipt.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": receipt.ResourceVersion}, {"op": "test", "path": "/spec/template/spec/containers/0/name", "value": "app"}, {"op": "test", "path": "/spec/template/spec/containers/0/image", "value": receipt.Image}, {"op": "replace", "path": "/spec/template/spec/containers/0/image", "value": o.Intent.Image}, {"op": "add", "path": "/metadata/annotations/pipewright.io~1deployment-operation", "value": o.ID}}
	raw, _ := json.Marshal(patch)
	if e := validateFixedPatch(o, raw); e != nil {
		t.Fatal(e)
	}
	patch[4]["path"] = "/spec/replicas"
	raw, _ = json.Marshal(patch)
	if e := validateFixedPatch(o, raw); e == nil {
		t.Fatal("arbitrary mutation")
	}
}
func TestPrivateProcessOutputIsBoundedAndArgumentsLiteral(t *testing.T) {
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	marker := "literal-$(not-executed)-`x`;echo"
	out, e := privateCommand(context.Background(), binary, []string{"-test.run=TestPrivateProcessHelper", "--", marker}, []byte("stdin-only-secret"))
	defer clearResult(out)
	if e != nil || out == nil || out.ExitCode != 0 || !out.Truncated || len(out.Stdout)+len(out.Stderr) > MaxPrivateResponseBytes || !bytes.Contains(out.Stdout, []byte(marker)) {
		t.Fatal("private process boundary", e)
	}
}
func TestPrivateProcessHelper(t *testing.T) {
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			_, _ = os.Stdout.WriteString(os.Args[i+1])
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 2*MaxPrivateResponseBytes))
			os.Exit(0)
		}
	}
}

func TestCapabilityRejectsPipedCoreHandlerAndUnknownPattern(t *testing.T) {
	for _, pattern := range []string{"|/usr/lib/systemd/systemd-coredump %P", "|/usr/share/apport/apport %p", ""} {
		raw := strings.Replace(fixtureProbe(), "PWCOREPATTERN\ncore\n", "PWCOREPATTERN\n"+pattern+"\n", 1)
		if validCapabilityProbe([]byte(raw), 1000) {
			t.Fatal("unsafe core pattern accepted")
		}
	}
	if !validCapabilityProbe([]byte(fixtureProbe()), 1000) {
		t.Fatal("safe fixture denied")
	}
}

func TestRuntimeUncreatedLeaseSafelyReleasesSlot(t *testing.T) {
	f, r, d, o := testRuntime(t)
	ctx := context.Background()
	l, e := f.s.RegisterRuntime(ctx, o.ID, o.OwnerEpoch, "engine-test")
	if e != nil {
		t.Fatal(e)
	}
	if l.CreateRequested {
		t.Fatal("unexpected create fence")
	}
	actual, _ := f.s.GetOperation(ctx, o.ID)
	if _, e = f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, actual.Revision); e != nil {
		t.Fatal(e)
	}
	l, e = r.Cleanup(ctx, o.ID)
	if e != nil || l.Cleanup != CleanupVerified || l.CleanupEvidence == nil || !l.CleanupEvidence.CreationNotRequested || d.createCount != 0 {
		t.Fatal("uncreated lease stuck", l, e)
	}
}
func TestCreateFenceRejectsUncreatedCleanupClaim(t *testing.T) {
	f, _, _, o := testRuntime(t)
	ctx := context.Background()
	l, e := f.s.RegisterRuntime(ctx, o.ID, o.OwnerEpoch, "engine-test")
	if e != nil {
		t.Fatal(e)
	}
	l, e = f.s.PrepareRuntimeCreate(ctx, l.OperationID, o.OwnerEpoch, nil, "sha256:"+strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.RecordCleanup(ctx, o.ID, l.Revision, CleanupEvidence{ContainerName: l.ContainerName, EngineID: l.EngineID, OwnerEpoch: l.OwnerEpoch, CreationNotRequested: true, WriterExited: true, ContainerAbsent: true}); e != ErrRuntimeUnverified {
		t.Fatal("lost create falsely cleared", e)
	}
}
func TestWriterPatchRequiresDurableFenceAndCannotRepeat(t *testing.T) {
	f, r, d, o := testRuntime(t)
	ctx := context.Background()
	w, e := r.OpenWriter(ctx, o.ID, o.OwnerEpoch)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close(ctx)
	before := beforeReceipt()
	if _, e = f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, before.UID); e != nil {
		t.Fatal(e)
	}
	o, _ = f.s.GetOperation(ctx, o.ID)
	if _, e = w.Patch(ctx, o, nil); e != ErrConflict {
		t.Fatal("write without durable fence", e)
	}
	if e = w.CheckPatchAccess(ctx); e != nil {
		t.Fatal(e)
	}
	o, e = f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, before)
	if e != nil {
		t.Fatal(e)
	}
	patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": before.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": before.ResourceVersion}, {"op": "test", "path": "/spec/template/spec/containers/0/name", "value": "app"}, {"op": "test", "path": "/spec/template/spec/containers/0/image", "value": before.Image}, {"op": "replace", "path": "/spec/template/spec/containers/0/image", "value": o.Intent.Image}, {"op": "add", "path": "/metadata/annotations/pipewright.io~1deployment-operation", "value": o.ID}}
	raw, _ := json.Marshal(patch)
	out, e := w.Patch(ctx, o, raw)
	clear(out)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Patch(ctx, o, raw); e != ErrOwner || d.patchCount != 1 {
		t.Fatal("write replay", e, d.patchCount)
	}
}
func TestObserverRotationUsesCurrentAuthorization(t *testing.T) {
	f, r, d, o := testRuntime(t)
	ctx := context.Background()
	o, e := f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, o.Revision)
	if e != nil {
		t.Fatal(e)
	}
	in := credentialInput(t)
	in.Token = "different-test-token"
	rotated, e := f.s.SaveCredential(ctx, f.c.ID, f.c.Revision, in)
	if e != nil {
		t.Fatal(e)
	}
	obs, e := r.OpenObserver(ctx, "reconcile", f.target, o.ID, "observer-rotation")
	if e != nil {
		t.Fatal(e)
	}
	defer obs.Close(ctx)
	if obs.Authorization().CredentialRevision != rotated.Revision || d.secretCount != 1 {
		t.Fatal("old auth revived")
	}
	old, e := f.s.GetOperation(ctx, o.ID)
	if e != nil || old.Revision != o.Revision || old.Intent.Authorization.CredentialRevision == rotated.Revision {
		t.Fatal("old intent rewritten")
	}
}

func TestRuntimeRechecksActualServerCompatibilityBeforeDeployment(t *testing.T) {
	for _, minor := range []string{"32", "33", "35+", "36", "invalid"} {
		t.Run(minor, func(t *testing.T) {
			f, r, d, o := testRuntime(t)
			d.serverMinor = minor
			w, e := r.OpenWriter(context.Background(), o.ID, o.OwnerEpoch)
			compatible := minor == "33" || minor == "35+"
			if compatible {
				if e != nil {
					t.Fatal(e)
				}
				if _, e = w.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
			} else if e != ErrVersion {
				t.Fatal("unsupported actual server accepted", e)
			}
			l, e := f.s.GetRuntimeLease(context.Background(), o.ID)
			if e != nil || l.Cleanup != CleanupVerified || d.patchCount != 0 || d.secretCount != 1 {
				t.Fatal("compatibility failure leaked runtime or dispatched", l, e, d.patchCount, d.secretCount)
			}
		})
	}
}
