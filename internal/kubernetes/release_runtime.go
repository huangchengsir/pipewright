package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

type ReleaseSession struct {
	runtime   *Runtime
	transport PrivateTransport
	lease     ReleaseRuntimeLease
	minor     int
	mu        sync.Mutex
	closed    bool
	closeErr  error
}

func (r *Runtime) OpenReleasePreview(ctx context.Context, a ReleaseAuthorization, owner string) (*ReleaseSession, error) {
	return r.openRelease(ctx, "", owner, a)
}
func (r *Runtime) OpenReleaseWriter(ctx context.Context, id, owner string) (*ReleaseSession, error) {
	if r == nil || r.store == nil {
		return nil, ErrTool
	}
	record, e := r.store.GetRelease(ctx, id)
	if e != nil {
		return nil, e
	}
	return r.openRelease(ctx, id, owner, record.Plan.Authorization)
}
func (r *Runtime) openRelease(ctx context.Context, parent, owner string, a ReleaseAuthorization) (_ *ReleaseSession, resultErr error) {
	phase := "runtime"
	defer func() { resultErr = WithReleasePreparationPhase(resultErr, phase) }()
	if r == nil || r.factory == nil || r.store == nil {
		return nil, ErrTool
	}
	phase = "authorization"
	if e := r.store.ValidateReleaseAuthorization(ctx, a); e != nil {
		return nil, e
	}
	p := a.Profile.Profile
	phase = "transport"
	transport, e := r.factory(ctx, a.Request.Executor, nil)
	if e != nil {
		return nil, e
	}
	phase = "engine"
	engine, e := readEngine(ctx, transport, p.Runtime.Architecture)
	if e != nil {
		return nil, e
	}
	phase = "pull"
	if e = r.pull(ctx, transport, p.Runtime); e != nil {
		return nil, e
	}
	phase = "inspect"
	image, e := inspectTool(ctx, transport, p.Runtime)
	if e != nil {
		return nil, e
	}
	phase = "runtime_register"
	lease, e := r.store.RegisterReleaseRuntime(ctx, parent, owner, engine, a)
	if e != nil {
		return nil, e
	}
	session := &ReleaseSession{runtime: r, transport: transport, lease: lease}
	defer func() {
		if resultErr != nil {
			_, _ = session.Close(context.WithoutCancel(ctx))
		}
	}()
	phase = "container_create"
	lease, e = r.store.PrepareReleaseRuntimeCreate(ctx, lease.ID, owner, lease.Runtime.Revision, transport.Snapshot(), image)
	if e != nil {
		return nil, e
	}
	session.lease = lease
	args := createToolArgs(lease.Runtime, p.Runtime)
	for i, v := range args {
		args[i] = strings.ReplaceAll(v, "size=8m", "size=64m")
	}
	created, createErr := transport.Docker(ctx, args, nil)
	clearResult(created)
	info, missing, e := inspectContainer(ctx, transport, lease.Runtime)
	if e != nil || missing {
		if createErr != nil {
			return nil, ErrTransportUnknown
		}
		return nil, ErrRuntimeIdentity
	}
	lease, e = r.store.BindReleaseRuntimeContainer(ctx, lease.ID, owner, lease.Runtime.Revision, engine, info.ID)
	if e != nil {
		return nil, e
	}
	session.lease = lease
	if !safeContainerSized(info, lease.Runtime, p.Runtime, false, "64m") {
		return nil, ErrRuntimeUnverified
	}
	phase = "container_start"
	started, e := transport.Docker(ctx, []string{"start", info.ID}, nil)
	defer clearResult(started)
	if e != nil || started == nil || started.Truncated || started.ExitCode != 0 {
		return nil, ErrTransportUnknown
	}
	phase = "capability"
	if e = session.checkIdentity(ctx, true); e != nil {
		return nil, e
	}
	phase = "version"
	client, e := session.exec(ctx, []string{p.Runtime.KubectlPath, "version", "--client=true", "--output=json"}, nil)
	if e != nil {
		return nil, e
	}
	session.minor, e = clientMinor(client)
	clear(client)
	if e != nil {
		return nil, e
	}
	version, e := session.exec(ctx, []string{p.HelperPath, "version"}, nil)
	if e != nil || string(version) != "pipewright-kube-tool/2\n" {
		clear(version)
		return nil, ErrVersion
	}
	clear(version)
	helm, e := session.exec(ctx, []string{p.HelmPath, "version", "--template={{.Version}}"}, nil)
	if e != nil || strings.TrimSpace(string(helm)) != p.HelmVersion {
		clear(helm)
		return nil, ErrVersion
	}
	clear(helm)
	proof := RuntimeProof{OperationID: lease.ID, OwnerEpoch: owner, EngineID: engine, ContainerID: info.ID, ProfileFingerprint: fingerprint(p), AuthorizationFingerprint: a.Fingerprint, Linux: true, PrivateTmpfs: true, SwapProtected: true, CoreDisabled: true, NonRoot: true, ReadOnlyRoot: true, BootstrapVerified: true, ClientVersionVerified: true, HostIdentityVerified: true}
	lease, e = r.store.VerifyReleaseRuntime(ctx, lease.ID, owner, lease.Runtime.Revision, proof)
	if e != nil {
		return nil, e
	}
	session.lease = lease
	phase = "helper_init"
	server, e := transport.Docker(ctx, []string{"exec", "--detach", lease.Runtime.ContainerID, p.HelperPath, "serve"}, nil)
	clearResult(server)
	if e != nil {
		return nil, e
	}
	ready := false
	for i := 0; i < 20; i++ {
		reply, e := session.rawRPC(ctx, kubeprotocol.Frame{Version: 2, Command: "ping"})
		if e == nil && reply.Code == "ok" {
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !ready {
		return nil, ErrTool
	}
	phase = "capability"
	if e = session.checkIdentity(ctx, true); e != nil {
		return nil, e
	}
	for _, scope := range a.Request.Resources {
		if releaseStringSetContains(scope.Actions, []string{releaseDaemonRevisionAction}) {
			if e = session.requireDaemonRevisionCapability(ctx); e != nil {
				return nil, e
			}
			break
		}
	}
	phase = "helper_init"
	e = r.store.UseReleaseCredential(ctx, lease.ID, owner, func(ctx context.Context, config []byte) error {
		resources := []kubeprotocol.Resource{}
		for _, scope := range a.Request.Resources {
			id := scope.ID
			resources = append(resources, kubeprotocol.Resource{Group: id.Group, Version: id.Version, Resource: id.Resource, Kind: id.Kind, Namespace: id.Namespace, Name: id.Name, Actions: scope.Actions})
		}
		input := kubeprotocol.Init{Kubeconfig: config, Resources: resources, HelmPath: p.HelmPath, HelmVersion: p.HelmVersion, Deadline: time.Now().Add(30 * time.Minute).UnixMilli()}
		data, _ := json.Marshal(input)
		defer clear(data)
		stream := "credential-init"
		if e := session.rawUpload(ctx, stream, data); e != nil {
			return e
		}
		reply, e := session.rawRPC(ctx, kubeprotocol.Frame{Version: 2, Command: "init", StreamID: stream})
		if e != nil || reply.Code != "ok" {
			return ErrRuntimeUnverified
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	phase = "version"
	raw, e := session.Read(ctx, "/version", "")
	if e != nil {
		return nil, e
	}
	var serverVersion struct {
		Major string `json:"major"`
		Minor string `json:"minor"`
	}
	if json.Unmarshal(raw, &serverVersion) != nil {
		clear(raw)
		return nil, ErrVersion
	}
	clear(raw)
	minorText := strings.TrimRight(serverVersion.Minor, "+")
	serverMinor, e := strconv.Atoi(minorText)
	if e != nil || serverVersion.Major != "1" || serverMinor-session.minor > 1 || session.minor-serverMinor > 1 {
		return nil, ErrVersion
	}
	return session, nil
}
func (s *ReleaseSession) exec(ctx context.Context, args []string, input []byte) ([]byte, error) {
	shell := &runtimeSession{transport: s.transport, lease: s.lease.Runtime}
	return shell.execPrivate(ctx, args, input)
}
func (s *ReleaseSession) checkIdentity(ctx context.Context, capabilities bool) (resultErr error) {
	phase := "authorization"
	defer func() { resultErr = WithReleasePreparationPhase(resultErr, phase) }()
	if s.closed {
		return ErrOwner
	}
	a := s.lease.Authorization
	if e := s.runtime.store.ValidateReleaseAuthorization(ctx, a); e != nil {
		return e
	}
	phase = "engine"
	engine, e := readEngine(ctx, s.transport, a.Profile.Profile.Runtime.Architecture)
	if e != nil || engine != s.lease.Runtime.EngineID {
		return ErrRuntimeIdentity
	}
	phase = "inspect"
	info, missing, e := inspectContainer(ctx, s.transport, s.lease.Runtime)
	if e != nil || missing || !safeContainerSized(info, s.lease.Runtime, a.Profile.Profile.Runtime, true, "64m") {
		return ErrRuntimeIdentity
	}
	phase = "capability"
	if capabilities {
		probe, e := s.exec(ctx, []string{"/bin/sh", "-c", capabilityProbe}, nil)
		if e != nil {
			return e
		}
		valid := validCapabilityProbe(probe, a.Profile.Profile.Runtime.UID)
		clear(probe)
		if !valid {
			return ErrRuntimeUnverified
		}
	}
	return nil
}
func (s *ReleaseSession) rawRPC(ctx context.Context, f kubeprotocol.Frame) (kubeprotocol.Reply, error) {
	var reply kubeprotocol.Reply
	b, e := json.Marshal(f)
	if e != nil || len(b) > 256<<10 {
		return reply, ErrInvalid
	}
	defer clear(b)
	raw, e := s.exec(ctx, []string{s.lease.Authorization.Profile.Profile.HelperPath, "rpc"}, b)
	if e != nil {
		return reply, e
	}
	defer clear(raw)
	if json.Unmarshal(raw, &reply) != nil || reply.Version != 2 {
		return reply, ErrTool
	}
	return reply, nil
}
func (s *ReleaseSession) RPC(ctx context.Context, f kubeprotocol.Frame) (kubeprotocol.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkIdentity(ctx, false); e != nil {
		return kubeprotocol.Reply{}, e
	}
	reply, e := s.rawRPC(ctx, f)
	if e != nil || reply.Code != "chunked" {
		return reply, e
	}
	if reply.Size < 0 || reply.Size > 8<<20 || reply.Count != (reply.Size+kubeprotocol.MaxChunkBytes-1)/kubeprotocol.MaxChunkBytes {
		return kubeprotocol.Reply{}, ErrPrivateLimit
	}
	var body []byte
	defer clear(body)
	for i := 0; i < reply.Count; i++ {
		part, e := s.rawRPC(ctx, kubeprotocol.Frame{Version: 2, Command: "download", StreamID: reply.StreamID, Index: i})
		if e != nil || part.Code != "ok" || len(part.Data) > kubeprotocol.MaxChunkBytes {
			return kubeprotocol.Reply{}, ErrPrivateLimit
		}
		body = append(body, part.Data...)
		clear(part.Data)
	}
	_, _ = s.rawRPC(ctx, kubeprotocol.Frame{Version: 2, Command: "forget", StreamID: reply.StreamID})
	if len(body) != reply.Size || releaseByteDigest(body) != reply.Digest {
		return kubeprotocol.Reply{}, ErrPrivateLimit
	}
	var assembled kubeprotocol.Reply
	if json.Unmarshal(body, &assembled) != nil || assembled.Version != 2 {
		return assembled, ErrTool
	}
	return assembled, nil
}
func (s *ReleaseSession) Read(ctx context.Context, path, query string) ([]byte, error) {
	r, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "read", Request: &kubeprotocol.Request{Method: "GET", Path: path, Query: query}})
	if e != nil {
		return nil, e
	}
	if r.Code != "ok" || r.Result == nil {
		return nil, ErrTool
	}
	if r.Result.Status == 404 {
		return nil, ErrNotFound
	}
	if r.Result.Status != 200 {
		clear(r.Result.Body)
		return nil, ErrDenied
	}
	return r.Result.Body, nil
}
func (s *ReleaseSession) Discovery(ctx context.Context) (*ReleaseDiscovery, error) {
	d := NewReleaseDiscovery()
	seen := map[string]bool{}
	for _, scope := range s.lease.Authorization.Request.Resources {
		id := scope.ID
		path := "/api/" + id.Version
		if id.Group != "" {
			path = "/apis/" + id.Group + "/" + id.Version
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		raw, e := s.Read(ctx, path, "")
		if e != nil {
			return nil, e
		}
		e = d.Add(raw)
		clear(raw)
		if e != nil {
			return nil, e
		}
	}
	return d, nil
}
func (s *ReleaseSession) Upload(ctx context.Context, id string, data []byte) error {
	if len(data) > MaxReleaseInputBytes {
		return ErrInvalid
	}
	count := max(1, (len(data)+kubeprotocol.MaxChunkBytes-1)/kubeprotocol.MaxChunkBytes)
	digest := releaseByteDigest(data)
	for i := 0; i < count; i++ {
		start := i * kubeprotocol.MaxChunkBytes
		end := min(len(data), start+kubeprotocol.MaxChunkBytes)
		r, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "upload", StreamID: id, Index: i, Count: count, Size: len(data), Digest: digest, Data: data[start:end]})
		if e != nil {
			return e
		}
		if r.Code != "ok" {
			return ErrPrivateLimit
		}
	}
	return nil
}
func (s *ReleaseSession) Close(ctx context.Context) (out ReleaseRuntimeLease, resultErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		s.closeErr = resultErr
		if resultErr != nil {
			_ = s.runtime.store.releaseRuntimeBlocked(context.WithoutCancel(ctx), s.lease.ID)
		}
	}()
	if s.closed {
		return s.lease, s.closeErr
	}
	s.closed = true
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	l, e := s.runtime.store.GetReleaseRuntime(clean, s.lease.ID)
	if e != nil {
		return l, e
	}
	s.lease = l
	if l.Runtime.Cleanup == CleanupVerified {
		return l, nil
	}
	r := l.Runtime
	proof := CleanupEvidence{ContainerName: r.ContainerName, ContainerID: r.ContainerID, EngineID: r.EngineID, OwnerEpoch: r.OwnerEpoch, WriterExited: true, ContainerAbsent: true}
	if !r.CreateRequested {
		proof.CreationNotRequested = true
		return s.runtime.store.RecordReleaseRuntimeCleanup(clean, l.ID, r.OwnerEpoch, r.Revision, proof)
	}
	engine, e := readEngine(clean, s.transport, l.Authorization.Profile.Profile.Runtime.Architecture)
	if e != nil || engine != r.EngineID {
		return l, ErrRuntimeUnverified
	}
	info, missing, e := inspectContainer(clean, s.transport, r)
	if e != nil {
		return l, ErrRuntimeUnverified
	}
	if r.ContainerID == "" {
		if missing {
			return l, ErrRuntimeUnverified
		}
		l, e = s.runtime.store.BindReleaseRuntimeContainer(clean, l.ID, r.OwnerEpoch, l.Runtime.Revision, r.EngineID, info.ID)
		if e != nil {
			return l, e
		}
		r = l.Runtime
		s.lease = l
	}
	if !missing {
		stopped, e := s.transport.Docker(clean, []string{"stop", "--time", "2", r.ContainerID}, nil)
		defer clearResult(stopped)
		if e != nil || stopped == nil || stopped.Truncated || stopped.ExitCode != 0 {
			return l, ErrRuntimeUnverified
		}
		info, missing, e = inspectContainer(clean, s.transport, r)
		if e != nil || missing || info.State.Running || info.State.Pid != 0 {
			return l, ErrRuntimeUnverified
		}
		removed, e := s.transport.Docker(clean, []string{"rm", r.ContainerID}, nil)
		defer clearResult(removed)
		if e != nil || removed == nil || removed.Truncated || removed.ExitCode != 0 {
			return l, ErrRuntimeUnverified
		}
		_, missing, e = inspectContainer(clean, s.transport, r)
		if e != nil || !missing {
			return l, ErrRuntimeUnverified
		}
	}
	proof.ContainerID = r.ContainerID
	proof.LabelsMatched = true
	l, e = s.runtime.store.GetReleaseRuntime(clean, l.ID)
	if e != nil {
		return l, e
	}
	out, e = s.runtime.store.RecordReleaseRuntimeCleanup(clean, l.ID, r.OwnerEpoch, l.Runtime.Revision, proof)
	s.lease = out
	return out, e
}
func (s *ReleaseSession) ExecuteAction(ctx context.Context, releaseID, owner string, revision int64, intent ReleaseActionIntent, path string) (ReleaseAction, error) {
	var out ReleaseAction
	if s.lease.ReleaseID != releaseID || s.lease.Runtime.OwnerEpoch != owner {
		return out, ErrOwner
	}
	if e := s.checkIdentity(ctx, true); e != nil {
		return out, e
	}
	out, e := s.runtime.store.PrepareReleaseAction(ctx, releaseID, owner, revision, intent)
	if e != nil {
		return out, e
	}
	method := "PATCH"
	switch intent.Verb {
	case "create":
		method = "POST"
	case "update":
		method = "PUT"
	case "delete":
		method = "DELETE"
	}
	request := kubeprotocol.Request{Method: method, Path: path, Body: out.Intent.Body, ContentType: out.Intent.ContentType}
	if method == "PATCH" || out.Intent.MutationVersion == 1 {
		request.Query = "fieldManager=pipewright-release-v2&fieldValidation=Strict"
	}
	frame := kubeprotocol.Frame{Version: 2, Command: "write", Request: &request}
	r, e := s.RPC(ctx, frame)
	if e != nil || r.Code != "accepted" {
		return out, ErrOutcomeUnknown
	}
	expectedRequest, _ := json.Marshal(request)
	digest := releaseByteDigest(expectedRequest)
	granted := false
	for {
		if e := ctx.Err(); e != nil {
			return out, errors.Join(ErrOutcomeUnknown, e)
		}
		poll, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"})
		if e != nil {
			return out, ErrOutcomeUnknown
		}
		if poll.Pending != nil {
			if granted || poll.Pending.Digest != digest {
				return out, ErrOutcomeUnknown
			}
			if e = s.checkIdentity(ctx, true); e != nil {
				return out, ErrOutcomeUnknown
			}
			if e = s.runtime.store.ValidateReleaseAuthorization(ctx, s.lease.Authorization); e != nil {
				return out, ErrOutcomeUnknown
			}
			if e = s.runtime.store.ValidateReleaseActionPermit(ctx, releaseID, out.ID, owner); e != nil {
				return out, ErrOutcomeUnknown
			}
			permit := kubeprotocol.Permit{RequestID: poll.Pending.ID, ActionID: out.ID, Digest: digest, Expires: time.Now().Add(5 * time.Second).UnixMilli(), Allow: true}
			ack, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &permit})
			if e != nil || ack.Code != "ok" {
				return out, ErrOutcomeUnknown
			}
			granted = true
		}
		if result := poll.Result; result != nil {
			if result.ActionID != out.ID || result.Code != "" {
				clear(result.Body)
				return out, ErrOutcomeUnknown
			}
			receipt := ReleaseActionReceipt{Resource: intent.Resource, Health: ReleaseHealth{State: "pending", Reason: "resource_not_observed"}}
			if result.Status >= 200 && result.Status < 300 {
				if intent.Verb == "delete" {
					receipt.UID = intent.ExpectedUID
					receipt.ResourceVersion = intent.ExpectedRV
					receipt.Health = ReleaseHealth{State: "declared", Reason: "delete_accepted"}
					clear(result.Body)
					return s.runtime.store.RecordReleaseAction(ctx, out.ID, owner, out.Revision, "verified", receipt)
				}
				if json.Unmarshal(result.Body, &receipt.Object) != nil {
					clear(result.Body)
					return out, ErrOutcomeUnknown
				}
				meta := releaseMap(receipt.Object["metadata"])
				receipt.UID = releaseString(meta["uid"])
				receipt.ResourceVersion = releaseString(meta["resourceVersion"])
				clear(result.Body)
				return s.runtime.store.RecordReleaseAction(ctx, out.ID, owner, out.Revision, "verified", receipt)
			}
			clear(result.Body)
			if releaseDefinitiveAPIRejection(result.Status) {
				recorded, e := s.runtime.store.RecordReleaseAction(ctx, out.ID, owner, out.Revision, "rejected", receipt)
				if e != nil {
					return recorded, e
				}
				return recorded, ErrDenied
			}
			return out, ErrOutcomeUnknown
		}
		select {
		case <-ctx.Done():
			return out, ErrOutcomeUnknown
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (s *ReleaseSession) DryRun(ctx context.Context, request kubeprotocol.Request) (map[string]any, error) {
	if request.Method == "GET" {
		return nil, ErrInvalid
	}
	if request.Query != "" {
		request.Query += "&"
	}
	request.Query += "dryRun=All&fieldManager=pipewright-release-v2&fieldValidation=Strict"
	r, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "dry-run", Request: &request})
	if e != nil {
		return nil, e
	}
	if r.Code != "ok" || r.Result == nil {
		return nil, ErrTool
	}
	defer clear(r.Result.Body)
	if r.Result.Status < 200 || r.Result.Status >= 300 {
		return nil, &releaseAPIStatusError{status: r.Result.Status}
	}
	var object map[string]any
	if json.Unmarshal(r.Result.Body, &object) != nil {
		return nil, ErrInvalid
	}
	return object, nil
}
func (s *ReleaseSession) rawUpload(ctx context.Context, id string, data []byte) error {
	if len(data) > MaxReleaseInputBytes {
		return ErrPrivateLimit
	}
	count := max(1, (len(data)+kubeprotocol.MaxChunkBytes-1)/kubeprotocol.MaxChunkBytes)
	digest := releaseByteDigest(data)
	for index := 0; index < count; index++ {
		start := index * kubeprotocol.MaxChunkBytes
		end := min(len(data), start+kubeprotocol.MaxChunkBytes)
		r, e := s.rawRPC(ctx, kubeprotocol.Frame{Version: 2, Command: "upload", StreamID: id, Index: index, Count: count, Size: len(data), Digest: digest, Data: data[start:end]})
		if e != nil {
			return e
		}
		if r.Code != "ok" {
			return ErrPrivateLimit
		}
	}
	return nil
}

// A complete trusted client rejection is evidence of no accepted write. Timeouts
// and ambiguous transport outcomes remain fenced; neither path auto-retries.
func releaseDefinitiveAPIRejection(status int) bool {
	return status >= 400 && status < 500 && status != 408
}
