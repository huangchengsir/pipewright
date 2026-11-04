package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

const leaseLabel = "io.pipewright.k8s.lease"
const ownerLabel = "io.pipewright.k8s.owner"
const bootstrap = `trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done`
const credentialWriter = `umask 077; cat > /run/pipewright/kubeconfig && chmod 600 /run/pipewright/kubeconfig`
const capabilityProbe = `set -eu
umask 077
printf 'probe' > /run/pipewright/.probe
chmod 600 /run/pipewright/.probe
printf 'PWCORE\n'
ulimit -c
printf 'PWCOREPATTERN\n'
cat /proc/sys/kernel/core_pattern
printf 'PWSTATUS\n'
cat /proc/self/status
printf 'PWMOUNTS\n'
cat /proc/self/mountinfo
printf 'PWSWAPS\n'
cat /proc/swaps
printf 'PWEND\n'
`

type Runtime struct {
	store    *Service
	factory  TransportFactory
	registry RegistryAuthSource
}

func NewRuntime(s *Service, factory TransportFactory, registry RegistryAuthSource) *Runtime {
	return &Runtime{store: s, factory: factory, registry: registry}
}

type runtimeSession struct {
	runtime             *Runtime
	transport           PrivateTransport
	lease               RuntimeLease
	auth                Authorization
	minor               int
	mu                  sync.Mutex
	closed, patched     bool
	patchAccessVerified bool
}
type WriterSession struct{ s *runtimeSession }
type ObserverSession struct{ s *runtimeSession }

func (r *Runtime) OpenWriter(ctx context.Context, id, owner string) (*WriterSession, error) {
	if r == nil || r.store == nil || r.factory == nil {
		return nil, ErrTool
	}
	op, e := r.store.GetOperation(ctx, id)
	if e != nil {
		return nil, e
	}
	if op.OwnerEpoch != owner || op.OwnerFenced || op.Retired || op.Business != NotDispatched || op.DispatchFenced {
		return nil, ErrOwner
	}
	if op.Intent.Authorization.RequireApproval && op.ApprovedHash != op.IntentHash {
		return nil, ErrDenied
	}
	s, e := r.open(ctx, op.Intent.Authorization, "writer", id, owner)
	if e != nil {
		return nil, e
	}
	return &WriterSession{s}, nil
}
func (r *Runtime) OpenObserver(ctx context.Context, purpose string, t Target, sourceID, owner string) (*ObserverSession, error) {
	if r == nil || r.store == nil || r.factory == nil {
		return nil, ErrTool
	}
	a, e := r.store.Authorize(ctx, t)
	if e != nil {
		return nil, e
	}
	s, e := r.open(ctx, a, purpose, sourceID, owner)
	if e != nil {
		return nil, e
	}
	return &ObserverSession{s}, nil
}
func (r *Runtime) open(ctx context.Context, a Authorization, purpose, sourceID, owner string) (_ *runtimeSession, resultErr error) {
	if e := r.store.ValidateAuthorization(ctx, a); e != nil {
		return nil, e
	}
	transport, e := r.factory(ctx, a.Target.Executor, nil)
	if e != nil {
		return nil, e
	}
	engine, e := readEngine(ctx, transport, a.Profile.Architecture)
	if e != nil {
		return nil, e
	}
	// Registry auth is never consumed from a project config or inherited Docker HOME.
	if e = r.pull(ctx, transport, a.Profile); e != nil {
		return nil, e
	}
	image, e := inspectTool(ctx, transport, a.Profile)
	if e != nil {
		return nil, e
	}
	var lease RuntimeLease
	if purpose == "writer" {
		lease, e = r.store.RegisterRuntime(ctx, sourceID, owner, engine)
	} else {
		lease, e = r.store.registerObserver(ctx, purpose, sourceID, owner, engine, a)
	}
	if e != nil {
		return nil, e
	}
	s := &runtimeSession{runtime: r, transport: transport, lease: lease, auth: a}
	defer func() {
		if resultErr != nil {
			_, _ = s.close(context.WithoutCancel(ctx))
		}
	}()
	lease, e = r.store.PrepareRuntimeCreate(ctx, lease.OperationID, owner, transport.Snapshot(), image)
	if e != nil {
		return nil, e
	}
	s.lease = lease
	created, createErr := transport.Docker(ctx, createToolArgs(lease, a.Profile), nil)
	clearResult(created)
	// Never repeat create. Even on a lost reply, only this exact intent is inspected.
	info, missing, e := inspectContainer(ctx, transport, lease)
	if e != nil || missing {
		if createErr != nil {
			return nil, ErrTransportUnknown
		}
		return nil, ErrRuntimeIdentity
	}
	lease, e = r.store.BindRuntimeContainer(ctx, lease.OperationID, owner, engine, info.ID)
	if e != nil {
		return nil, e
	}
	s.lease = lease
	if !safeContainer(info, lease, a.Profile, false) {
		return nil, ErrRuntimeUnverified
	}
	started, e := transport.Docker(ctx, []string{"start", lease.ContainerID}, nil)
	defer clearResult(started)
	if e != nil || started == nil || started.Truncated || started.ExitCode != 0 {
		return nil, ErrTransportUnknown
	}
	info, missing, e = inspectContainer(ctx, transport, lease)
	if e != nil || missing || !safeContainer(info, lease, a.Profile, true) {
		return nil, ErrRuntimeUnverified
	}
	probe, e := s.execPrivate(ctx, []string{"/bin/sh", "-c", capabilityProbe}, nil)
	if e != nil {
		return nil, e
	}
	ok := validCapabilityProbe(probe, a.Profile.UID)
	clear(probe)
	if !ok {
		return nil, ErrRuntimeUnverified
	}
	client, e := s.execPrivate(ctx, []string{a.Profile.KubectlPath, "version", "--client=true", "--output=json"}, nil)
	if e != nil {
		return nil, e
	}
	s.minor, e = clientMinor(client)
	clear(client)
	if e != nil {
		return nil, e
	}
	proof := RuntimeProof{OperationID: lease.OperationID, OwnerEpoch: owner, EngineID: engine, ContainerID: lease.ContainerID, ProfileFingerprint: a.ProfileFingerprint, AuthorizationFingerprint: a.Fingerprint, Linux: true, PrivateTmpfs: true, SwapProtected: true, CoreDisabled: true, NonRoot: true, ReadOnlyRoot: true, BootstrapVerified: true, ClientVersionVerified: true, HostIdentityVerified: true}
	lease, e = r.store.VerifyRuntime(ctx, proof, lease.Revision)
	if e != nil {
		return nil, e
	}
	s.lease = lease
	// Re-probe immediately before decrypting; no caller-supplied capability booleans.
	if e = s.checkIdentity(ctx, true); e != nil {
		return nil, e
	}
	consume := func(ctx context.Context, config []byte) error {
		out, e := s.execPrivate(ctx, []string{"/bin/sh", "-c", credentialWriter}, config)
		clear(out)
		return e
	}
	if purpose == "writer" {
		e = r.store.UseCredential(ctx, a, proof, consume)
	} else {
		e = r.store.useObserverCredential(ctx, lease, proof, consume)
	}
	if e != nil {
		return nil, e
	}
	// The actual server version is only knowable after the authorized credential lease.
	if e = s.checkServerVersion(ctx); e != nil {
		return nil, e
	}
	return s, nil
}
func (r *Runtime) pull(ctx context.Context, t PrivateTransport, p ToolProfile) error {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if p.RegistryCredentialID == "" {
		return t.Pull(callCtx, p.Image, nil)
	}
	if r.registry == nil {
		return ErrTool
	}
	return r.registry(callCtx, p.RegistryCredentialID, func(user, password string) error {
		if user == "" || password == "" || len(user) > 1024 || len(password) > 8192 {
			return ErrTool
		}
		payload, e := json.Marshal(map[string]string{"username": user, "password": password})
		if e != nil {
			return ErrTool
		}
		defer clear(payload)
		if e = t.Pull(callCtx, p.Image, payload); e != nil {
			return ErrTool
		}
		return nil
	})
}
func createToolArgs(l RuntimeLease, p ToolProfile) []string {
	uid := strconv.FormatInt(p.UID, 10)
	return []string{"create", "--name", l.ContainerName, "--label", leaseLabel + "=" + l.OperationID, "--label", ownerLabel + "=" + l.OwnerEpoch, "--read-only", "--user", uid + ":" + uid, "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--ulimit", "core=0:0", "--restart", "no", "--network", p.Network, "--cpus", "1", "--memory", "256m", "--memory-swap", "256m", "--pids-limit", "64", "--log-driver", "none", "--tmpfs", "/run/pipewright:rw,noexec,nosuid,nodev,size=8m,mode=0700,uid=" + uid + ",gid=" + uid, "--entrypoint", "/bin/sh", p.Image, "-c", bootstrap}
}
func readEngine(ctx context.Context, t PrivateTransport, arch string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, e := t.Docker(c, []string{"info", "--format", "{{json .}}"}, nil)
	defer clearResult(out)
	if e != nil || out == nil || out.ExitCode != 0 || out.Truncated {
		return "", ErrTool
	}
	var info struct{ ID, OSType, Architecture string }
	if privateJSON(out.Stdout, &info) != nil {
		return "", ErrTool
	}
	actual := info.Architecture
	if actual == "x86_64" {
		actual = "amd64"
	}
	if actual == "aarch64" {
		actual = "arm64"
	}
	if info.OSType != "linux" || actual != arch || !cleanText(info.ID, 256) {
		return "", ErrRuntimeUnverified
	}
	return info.ID, nil
}
func inspectTool(ctx context.Context, t PrivateTransport, p ToolProfile) (string, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, e := t.Docker(c, []string{"image", "inspect", "--format", "{{json .}}", p.Image}, nil)
	defer clearResult(out)
	if e != nil || out == nil || out.ExitCode != 0 || out.Truncated {
		return "", ErrTool
	}
	var info struct {
		ID, Os, Architecture string
		RepoDigests          []string
		Config               struct {
			Volumes map[string]json.RawMessage
			Env     []string
		}
	}
	if privateJSON(out.Stdout, &info) != nil || info.Os != "linux" || info.Architecture != p.Architecture || !strings.HasPrefix(info.ID, "sha256:") || !fullHash.MatchString(strings.TrimPrefix(info.ID, "sha256:")) || len(info.Config.Volumes) != 0 {
		return "", ErrRuntimeUnverified
	}
	trusted := false
	for _, ref := range info.RepoDigests {
		trusted = trusted || ref == p.Image
	}
	if !trusted {
		return "", ErrRuntimeIdentity
	}
	for _, env := range info.Config.Env {
		key := strings.ToUpper(strings.SplitN(env, "=", 2)[0])
		switch key {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "KUBECONFIG", "KUBERNETES_SERVICE_HOST", "LD_PRELOAD", "LD_LIBRARY_PATH":
			return "", ErrRuntimeUnverified
		}
	}
	return info.ID, nil
}

type containerInfo struct {
	ID          string `json:"Id"`
	Name, Image string
	Config      struct {
		User            string
		Labels          map[string]string
		Entrypoint, Cmd []string
		Volumes         map[string]json.RawMessage
	}
	State struct {
		Running bool
		Status  string
		Pid     int
		Dead    bool
	}
	HostConfig struct {
		ReadonlyRootfs, Privileged, PublishAllPorts bool
		Binds, CapAdd, CapDrop, SecurityOpt         []string
		NetworkMode, PidMode, IpcMode               string
		Tmpfs                                       map[string]string
		NanoCpus, Memory, MemorySwap, PidsLimit     int64
		PortBindings                                map[string]json.RawMessage
		RestartPolicy                               struct{ Name string }
		LogConfig                                   struct{ Type string }
		Ulimits                                     []struct {
			Name       string
			Soft, Hard int64
		}
	}
	Mounts []struct {
		Type, Destination string
		RW                bool
	}
}

func inspectContainer(ctx context.Context, t PrivateTransport, l RuntimeLease) (containerInfo, bool, error) {
	var info containerInfo
	lookup := l.ContainerID
	if lookup == "" {
		lookup = l.ContainerName
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, e := t.Docker(c, []string{"inspect", "--type", "container", "--format", "{{json .}}", lookup}, nil)
	defer clearResult(out)
	if e != nil || out == nil || out.Truncated {
		return info, false, ErrTransportUnknown
	}
	if out.ExitCode == 1 && len(bytes.TrimSpace(out.Stdout)) == 0 {
		// The deliberately inert /dev/null config prevents ambient Docker auth.
		// Docker 29 emits this exact warning before an otherwise authoritative
		// missing-container result. No other warning or trailing error is ignored.
		msg := strings.TrimSpace(string(out.Stderr))
		msg = strings.TrimPrefix(msg, "WARNING: Error loading config file: open /dev/null/config.json: not a directory\n")
		if msg == "Error: No such object: "+lookup || msg == "Error: No such container: "+lookup || msg == "Error response from daemon: No such container: "+lookup {
			return info, true, nil
		}
	}
	if out.ExitCode != 0 || privateJSON(out.Stdout, &info) != nil {
		return info, false, ErrRuntimeIdentity
	}
	if !imageID.MatchString(info.ID) || info.Name != "/"+l.ContainerName || info.Config.Labels[leaseLabel] != l.OperationID || info.Config.Labels[ownerLabel] != l.OwnerEpoch || (l.ContainerID != "" && info.ID != l.ContainerID) {
		return info, false, ErrRuntimeIdentity
	}
	return info, false, nil
}
func safeContainer(c containerInfo, l RuntimeLease, p ToolProfile, running bool) bool {
	h := c.HostConfig
	uid := strconv.FormatInt(p.UID, 10)
	if c.Image != l.ToolImageID || c.Config.User != uid+":"+uid || len(c.Config.Volumes) != 0 || !h.ReadonlyRootfs || h.Privileged || len(h.Binds) != 0 || len(h.CapAdd) != 0 || len(h.CapDrop) != 1 || strings.ToUpper(h.CapDrop[0]) != "ALL" || h.NetworkMode != p.Network || h.PidMode != "" || h.IpcMode == "host" || h.PublishAllPorts || len(h.PortBindings) > 0 || h.RestartPolicy.Name != "no" || h.LogConfig.Type != "none" || h.NanoCpus != 1000000000 || h.Memory != 268435456 || h.MemorySwap != 268435456 || h.PidsLimit != 64 || len(h.Tmpfs) != 1 || c.State.Dead || running && !c.State.Running {
		return false
	}
	if len(c.Config.Entrypoint) != 1 || c.Config.Entrypoint[0] != "/bin/sh" || len(c.Config.Cmd) != 2 || c.Config.Cmd[0] != "-c" || c.Config.Cmd[1] != bootstrap {
		return false
	}
	nnp := false
	for _, v := range h.SecurityOpt {
		nnp = nnp || v == "no-new-privileges:true" || v == "no-new-privileges"
	}
	if !nnp {
		return false
	}
	core := false
	for _, v := range h.Ulimits {
		if v.Name == "core" && v.Soft == 0 && v.Hard == 0 {
			core = true
		}
	}
	if !core {
		return false
	}
	options, ok := h.Tmpfs["/run/pipewright"]
	if !ok {
		return false
	}
	for _, v := range []string{"rw", "noexec", "nosuid", "nodev", "size=8m", "mode=0700", "uid=" + uid, "gid=" + uid} {
		if !containsOption(options, v) {
			return false
		}
	}
	for _, m := range c.Mounts {
		if m.Type != "tmpfs" || m.Destination != "/run/pipewright" || !m.RW {
			return false
		}
	}
	return true
}
func containsOption(options, want string) bool {
	for _, v := range strings.Split(options, ",") {
		if v == want {
			return true
		}
	}
	return false
}
func validCapabilityProbe(raw []byte, uid int64) bool {
	text := string(raw)
	parts := strings.Split(text, "PWSTATUS\n")
	if len(parts) != 2 {
		return false
	}
	core := strings.Split(parts[0], "PWCOREPATTERN\n")
	if len(core) != 2 || core[0] != "PWCORE\n0\n" {
		return false
	}
	pattern := strings.TrimSpace(core[1])
	if pattern == "" || strings.HasPrefix(pattern, "|") || strings.Contains(pattern, "\n") {
		return false
	}
	parts = strings.Split(parts[1], "PWMOUNTS\n")
	if len(parts) != 2 {
		return false
	}
	userOK := false
	for _, line := range strings.Split(parts[0], "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			if len(fields) != 4 {
				return false
			}
			userOK = true
			for _, v := range fields {
				if v != strconv.FormatInt(uid, 10) {
					return false
				}
			}
		}
	}
	tail := strings.Split(parts[1], "PWSWAPS\n")
	if len(tail) != 2 {
		return false
	}
	mountOK := false
	for _, line := range strings.Split(tail[0], "\n") {
		fields := strings.Fields(line)
		if len(fields) > 6 && fields[4] == "/run/pipewright" && strings.Contains(line, " - tmpfs ") {
			mountOK = containsOption(fields[5], "rw") && containsOption(fields[5], "nosuid") && containsOption(fields[5], "nodev") && containsOption(fields[5], "noexec")
		}
	}
	swaps := strings.TrimSuffix(tail[1], "PWEND\n")
	if swaps == tail[1] {
		return false
	}
	lines := strings.Split(strings.TrimSpace(swaps), "\n")
	swapOK := len(lines) == 1 && strings.HasPrefix(lines[0], "Filename")
	return userOK && mountOK && swapOK
}
func clientMinor(raw []byte) (int, error) {
	var v struct{ ClientVersion struct{ Major, Minor string } }
	if privateJSON(raw, &v) != nil || v.ClientVersion.Major != "1" {
		return 0, ErrVersion
	}
	n, e := strconv.Atoi(strings.TrimRight(v.ClientVersion.Minor, "+"))
	if e != nil || n < 1 {
		return 0, ErrVersion
	}
	return n, nil
}
func (s *runtimeSession) execPrivate(ctx context.Context, cmd []string, in []byte) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"exec", "-i", "--env", "HOME=/run/pipewright", "--env", "LANG=C", "--env", "LC_ALL=C", s.lease.ContainerID}
	args = append(args, cmd...)
	out, e := s.transport.Docker(c, args, in)
	defer clearResult(out)
	if e != nil {
		return nil, ErrTransportUnknown
	}
	if out == nil {
		return nil, ErrTool
	}
	if out.Truncated {
		return nil, ErrPrivateLimit
	}
	if out.ExitCode != 0 {
		return nil, ErrTool
	}
	return append([]byte(nil), out.Stdout...), nil
}
func (s *runtimeSession) kubectl(args ...string) []string {
	out := []string{s.auth.Profile.KubectlPath, "--kubeconfig=/run/pipewright/kubeconfig", "--cache-dir=/run/pipewright/cache", "--request-timeout=10s", "--context", s.auth.Target.Context, "--namespace", s.auth.Target.Namespace}
	return append(out, args...)
}
func (s *runtimeSession) checkServerVersion(ctx context.Context) error {
	raw, e := s.execPrivate(ctx, s.kubectl("version", "--output=json"), nil)
	if e != nil {
		return e
	}
	defer clear(raw)
	var v struct{ ServerVersion struct{ Major, Minor string } }
	if privateJSON(raw, &v) != nil || v.ServerVersion.Major != "1" {
		return ErrVersion
	}
	n, e := strconv.Atoi(strings.TrimRight(v.ServerVersion.Minor, "+"))
	if e != nil || n < s.minor-1 || n > s.minor+1 {
		return ErrVersion
	}
	return nil
}
func (s *runtimeSession) checkIdentity(ctx context.Context, capabilities bool) error {
	engine, e := readEngine(ctx, s.transport, s.auth.Profile.Architecture)
	if e != nil || engine != s.lease.EngineID {
		return ErrRuntimeIdentity
	}
	c, missing, e := inspectContainer(ctx, s.transport, s.lease)
	if e != nil || missing || !safeContainer(c, s.lease, s.auth.Profile, true) {
		return ErrRuntimeIdentity
	}
	if capabilities {
		raw, e := s.execPrivate(ctx, []string{"/bin/sh", "-c", capabilityProbe}, nil)
		if e != nil {
			return e
		}
		valid := validCapabilityProbe(raw, s.auth.Profile.UID)
		clear(raw)
		if !valid {
			return ErrRuntimeUnverified
		}
	}
	return nil
}
func (s *runtimeSession) getDeployment(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrOwner
	}
	if e := s.runtime.store.ValidateAuthorization(ctx, s.auth); e != nil {
		return nil, e
	}
	if e := s.checkIdentity(ctx, false); e != nil {
		return nil, e
	}
	return s.execPrivate(ctx, s.kubectl("get", "deployment", s.auth.Target.Deployment, "--output=json"), nil)
}
func (w *WriterSession) GetDeployment(ctx context.Context) ([]byte, error) {
	return w.s.getDeployment(ctx)
}
func (o *ObserverSession) GetDeployment(ctx context.Context) ([]byte, error) {
	return o.s.getDeployment(ctx)
}
func (w *WriterSession) LeaseID() string                { return w.s.lease.OperationID }
func (o *ObserverSession) LeaseID() string              { return o.s.lease.OperationID }
func (w *WriterSession) Authorization() Authorization   { return w.s.auth }
func (o *ObserverSession) Authorization() Authorization { return o.s.auth }
func (w *WriterSession) Patch(ctx context.Context, prepared Operation, patch []byte) ([]byte, error) {
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.patched || prepared.ID != s.lease.OperationID || prepared.OwnerEpoch != s.lease.OwnerEpoch {
		return nil, ErrOwner
	}
	current, e := s.runtime.store.GetOperation(ctx, prepared.ID)
	if e != nil {
		return nil, e
	}
	if current.Revision != prepared.Revision || current.OwnerFenced || current.Retired || !current.DispatchFenced || current.Business != OutcomeUnknown || current.Receipt == nil {
		return nil, ErrConflict
	}
	if !s.patchAccessVerified {
		return nil, ErrAccessReview
	}
	if e = validateFixedPatch(current, patch); e != nil {
		return nil, e
	}
	if e = s.runtime.store.ValidateAuthorization(ctx, s.auth); e != nil {
		return nil, e
	}
	if e = s.checkIdentity(ctx, true); e != nil {
		return nil, e
	}
	if e = s.checkServerVersion(ctx); e != nil {
		return nil, e
	}
	s.patched = true
	return s.execPrivate(ctx, s.kubectl("patch", "deployment", s.auth.Target.Deployment, "--type=json", "--patch-file=/dev/stdin", "--output=json"), patch)
}
func validateFixedPatch(o Operation, patch []byte) error {
	var changes []struct {
		Op, Path string
		Value    json.RawMessage
	}
	if len(patch) > 16384 || privateJSON(patch, &changes) != nil || len(changes) != 6 {
		return ErrInvalid
	}
	readString := func(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
	if changes[0].Op != "test" || changes[0].Path != "/metadata/uid" || readString(changes[0].Value) != o.Receipt.PreparedUID || changes[1].Op != "test" || changes[1].Path != "/metadata/resourceVersion" || readString(changes[1].Value) != o.Receipt.PreparedResourceVersion {
		return ErrInvalid
	}
	prefix := "/spec/template/spec/containers/"
	namePath := changes[2].Path
	if !strings.HasPrefix(namePath, prefix) || !strings.HasSuffix(namePath, "/name") {
		return ErrInvalid
	}
	index := strings.TrimSuffix(strings.TrimPrefix(namePath, prefix), "/name")
	n, e := strconv.Atoi(index)
	if e != nil || n < 0 || n > 1024 || strconv.Itoa(n) != index {
		return ErrInvalid
	}
	imagePath := prefix + index + "/image"
	if changes[2].Op != "test" || readString(changes[2].Value) != o.Intent.Authorization.Target.Container || changes[3].Op != "test" || changes[3].Path != imagePath || readString(changes[3].Value) != o.Receipt.Image || changes[4].Op != "replace" || changes[4].Path != imagePath || readString(changes[4].Value) != o.Intent.Image {
		return ErrInvalid
	}
	marker := changes[5]
	if marker.Op != "add" {
		return ErrInvalid
	}
	if marker.Path == "/metadata/annotations/pipewright.io~1deployment-operation" {
		if readString(marker.Value) != o.ID {
			return ErrInvalid
		}
	} else if marker.Path == "/metadata/annotations" {
		var v map[string]string
		if json.Unmarshal(marker.Value, &v) != nil || len(v) != 1 || v["pipewright.io/deployment-operation"] != o.ID {
			return ErrInvalid
		}
	} else {
		return ErrInvalid
	}
	return nil
}
func (w *WriterSession) Close(ctx context.Context) (RuntimeLease, error)   { return w.s.close(ctx) }
func (o *ObserverSession) Close(ctx context.Context) (RuntimeLease, error) { return o.s.close(ctx) }
func (s *runtimeSession) close(ctx context.Context) (RuntimeLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return s.runtime.cleanup(clean, s.transport, s.lease.OperationID, s.auth)
}

// Cleanup recovers only the exact registered runtime. It never starts kubectl,
// reinjects a credential or releases a workload hold.
func (r *Runtime) Cleanup(ctx context.Context, id string) (RuntimeLease, error) {
	lease, e := r.store.GetRuntimeLease(ctx, id)
	if e != nil {
		return lease, e
	}
	if lease.Cleanup == CleanupVerified {
		return lease, nil
	}
	var a Authorization
	if lease.Purpose == "writer" {
		op, e := r.store.GetOperation(ctx, id)
		if e != nil {
			return lease, e
		}
		if !op.OwnerFenced {
			return lease, ErrOwner
		}
		a = op.Intent.Authorization
	} else {
		if lease.Authorization == nil {
			return lease, ErrDenied
		}
		a = *lease.Authorization
	}
	t, e := r.factory(ctx, a.Target.Executor, lease.SSHSnapshot)
	if e != nil {
		return lease, e
	}
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return r.cleanup(clean, t, id, a)
}
func (r *Runtime) cleanup(ctx context.Context, t PrivateTransport, id string, a Authorization) (RuntimeLease, error) {
	l, e := r.store.GetRuntimeLease(ctx, id)
	if e != nil {
		return l, e
	}
	if l.Cleanup == CleanupVerified {
		return l, nil
	}
	blocked := func() (RuntimeLease, error) {
		fresh, e := r.store.GetRuntimeLease(ctx, id)
		if e != nil {
			return l, e
		}
		out, e := r.store.RecordCleanupBlocked(ctx, id, fresh.Revision)
		if e != nil {
			return out, e
		}
		return out, ErrRuntimeUnverified
	}
	if !l.CreateRequested {
		// The durable creation fence has not been acquired; under the same store
		// locks RecordCleanup rejects a racing transition to CreateRequested.
		evidence := CleanupEvidence{ContainerName: l.ContainerName, EngineID: l.EngineID, OwnerEpoch: l.OwnerEpoch, CreationNotRequested: true, WriterExited: true, ContainerAbsent: true}
		return r.store.RecordCleanup(ctx, id, l.Revision, evidence)
	}
	engine, e := readEngine(ctx, t, a.Profile.Architecture)
	if e != nil || engine != l.EngineID {
		return blocked()
	}
	info, missing, e := inspectContainer(ctx, t, l)
	if e != nil {
		return blocked()
	}
	if l.ContainerID == "" {
		// A missing name after an ambiguous create is not evidence the request cannot
		// arrive later. Keep the slot/cleanup blocked until an exact CID is found.
		if missing {
			return blocked()
		}
		// Bind an exact, discovered owned CID even for a previously blocked lease.
		l, e = r.store.bindRecoveredContainer(ctx, l, info.ID)
		if e != nil {
			return blocked()
		}
	}
	if !missing {
		stopped, e := t.Docker(ctx, []string{"stop", "--time", "2", l.ContainerID}, nil)
		defer clearResult(stopped)
		if e != nil || stopped == nil || stopped.Truncated || stopped.ExitCode != 0 {
			return blocked()
		}
		info, missing, e = inspectContainer(ctx, t, l)
		if e != nil || missing || info.State.Running || info.State.Pid != 0 {
			return blocked()
		}
		removed, e := t.Docker(ctx, []string{"rm", l.ContainerID}, nil)
		defer clearResult(removed)
		if e != nil || removed == nil || removed.Truncated || removed.ExitCode != 0 {
			return blocked()
		}
		_, missing, e = inspectContainer(ctx, t, l)
		if e != nil || !missing {
			return blocked()
		}
	}
	fresh, e := r.store.GetRuntimeLease(ctx, id)
	if e != nil {
		return l, e
	}
	evidence := CleanupEvidence{ContainerName: l.ContainerName, ContainerID: l.ContainerID, EngineID: l.EngineID, OwnerEpoch: l.OwnerEpoch, LabelsMatched: true, WriterExited: true, ContainerAbsent: true}
	return r.store.RecordCleanup(ctx, id, fresh.Revision, evidence)
}
func (s *Service) bindRecoveredContainer(ctx context.Context, l RuntimeLease, cid string) (RuntimeLease, error) {
	if !imageID.MatchString(cid) {
		return l, ErrInvalid
	}
	var out RuntimeLease
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		old, e := s.runtimeLease(ctx, q, l.OperationID)
		if e != nil {
			return e
		}
		if old.Revision != l.Revision || old.ContainerID != "" || !old.CreateRequested {
			return ErrConflict
		}
		old.ContainerID = cid
		e = s.saveRuntime(ctx, q, &old, old.Revision)
		out = old
		return e
	})
	return out, e
}
