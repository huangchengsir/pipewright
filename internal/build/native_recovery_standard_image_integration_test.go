package build

// Physical standard-image recovery with one real target. This does not claim
// two independent Docker hosts/fanout coverage. Native effects are opt-in.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

type nativeStandardRegistry struct{ cid, image, owner, address string }

func nativeStandardRegistryFixture(t *testing.T) nativeStandardRegistry {
	t.Helper()
	nativeRetryReady(t)
	registryImage := nativeStandardRegistryImage(t)
	owner := "pw-native-standard-registry-" + uuid.NewString()
	port := nativeFreePort(t)
	cid := nativeDocker(t, "create", "--pull=never", "--name", owner, "--label", "io.pipewright.acceptance.fixture="+owner, "--publish", fmt.Sprintf("127.0.0.1:%d:5000", port), registryImage)
	if !deployattempt.ValidContainerID(cid) {
		t.Fatal("registry CID missing")
	}
	f := nativeStandardRegistry{cid: cid, image: registryImage, owner: owner, address: fmt.Sprintf("127.0.0.1:%d", port)}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, stderr, code, err := (execCommander{}).Run(ctx, "docker", []string{"container", "inspect", "--format", `{{.Id}} {{.Image}} {{index .Config.Labels "io.pipewright.acceptance.fixture"}}`, cid}, "")
		if ctx.Err() == nil && nativeStandardExactMissing(err, code, stderr, cid) {
			return
		}
		if err != nil || code != 0 || strings.TrimSpace(out) != cid+" "+registryImage+" "+owner {
			t.Error("standard registry identity changed; preserved")
			return
		}
		_, _, _, _ = (execCommander{}).Run(ctx, "docker", []string{"stop", "--time", "2", cid}, "")
		_, _, code, err = (execCommander{}).Run(ctx, "docker", []string{"rm", "-v", cid}, "")
		if err != nil || code != 0 {
			t.Error("exact standard registry cleanup failed")
		}
	})
	nativeDocker(t, "start", cid)
	nativeWaitHTTP(t, "http://"+f.address+"/v2/", "")
	return f
}

// The only changed environment is a dedicated receiver's real running state.
// Push always returns its actual result; no CLI result or receipt is fabricated.
type nativeStandardRegistryGateDriver struct {
	*nativeRetryDriver
	registry nativeStandardRegistry
	gate     atomic.Bool
	gateMu   sync.Mutex
	gateErr  error
}

func (d *nativeStandardRegistryGateDriver) Push(ctx context.Context, ref string, line func(string, string)) (int, error) {
	code, err := d.nativeRetryDriver.Push(ctx, ref, line)
	if err == nil && code == 0 && d.gate.CompareAndSwap(false, true) {
		probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		_, _, stopCode, stopErr := (execCommander{}).Run(probe, "docker", []string{"stop", "--time", "2", d.registry.cid}, "")
		cancel()
		if stopErr != nil || stopCode != 0 {
			d.gateMu.Lock()
			d.gateErr = fmt.Errorf("owned receiver stop failed: code=%d", stopCode)
			d.gateMu.Unlock()
		}
	}
	return code, err
}

func TestNativeRecoveryActualStandardPublishedImageTarget(t *testing.T) {
	nodeImage := nativeRetryReady(t)
	// Cached full-ID FROM needs the proven legacy backend; do not permit a
	// BuildKit frontend/metadata fetch to an external registry.
	t.Setenv("DOCKER_BUILDKIT", "0")
	registry := nativeStandardRegistryFixture(t)
	owner := "pw-native-standard-app-" + uuid.NewString()
	role := owner
	port := nativeFreePort(t)
	repository := "native-standard-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	remote := registry.address + "/" + repository + ":acceptance"
	build := pipeline.Job{ID: "image", Name: "image", Type: "build_image", Config: map[string]any{"artifactType": "image", "dockerfilePath": "Dockerfile.local"}}
	push := pipeline.Job{ID: "push", Name: "push", Type: "push_image", Needs: []string{"image"}, Config: map[string]any{"registry": registry.address, "imageName": repository, "tag": "acceptance"}}
	deployJob := pipeline.Job{ID: "deploy", Name: "deploy", Type: "deploy_ssh", Needs: []string{"push"}, Config: map[string]any{"serverId": "native-standard", "artifactType": "image", "artifactSource": `{"stageId":"stage","jobId":"push","declarationIndex":0}`, "containerName": role, "ports": fmt.Sprintf("127.0.0.1:%d:8080", port), "runArgs": "--pull=never --label io.pipewright.acceptance.fixture=" + owner}}
	h, d, _ := nativeHarness(t, retrySpec(build, push, deployJob, nativeJob(nodeImage, "after", "true", "deploy")))
	gated := &nativeStandardRegistryGateDriver{nativeRetryDriver: d, registry: registry}
	h.b.driver = gated
	sshPort, password, _ := nativeSSHFixture(t)
	targets, runs := nativeInstallDeployer(t, h)
	nativeAddServer(t, h, targets, "native-standard", sshPort, password)
	// Standard deployment unconditionally removes its configured role before
	// run. Seed an independently owned placeholder so this unrelated first-rm
	// behavior cannot mask the intended known failed pull.
	placeholder := nativeDocker(t, "run", "-d", "--pull=never", "--network", "none", "--name", role, "--label", "io.pipewright.acceptance.fixture="+owner, nodeImage, "node", "-e", "setInterval(()=>{},1000)")
	if !deployattempt.ValidContainerID(placeholder) {
		t.Fatal("placeholder full CID missing")
	}
	child := ""
	deployNodeKey := ""
	remoteImageID := ""
	defer nativeLogLedger(t, h, "root")
	// Registered after the DB fixture, so durable evidence is still available.
	t.Cleanup(func() {
		if child != "" {
			nativeLogLedger(t, h, child)
		}
		if remoteImageID == "" {
			d.mu.Lock()
			if len(d.images) == 1 {
				remoteImageID = d.builtIDs[d.images[0]]
			}
			d.mu.Unlock()
		}
		if nativeStandardCleanupTarget(t, h, role, owner, placeholder, nodeImage, child, deployNodeKey) && remoteImageID != "" {
			nativeRemoveImageTag(t, remote, remoteImageID)
		}
	})
	businessErr := h.execute(t, "root")
	gated.gateMu.Lock()
	gateErr := gated.gateErr
	gated.gateMu.Unlock()
	if gateErr != nil {
		t.Fatal("fixture failed to establish refused-pull condition", gateErr)
	}
	if businessErr == nil {
		t.Fatal("actual registry-refused image pull did not fail")
	}
	if h.settleErr != nil {
		t.Fatal("known failed pull did not settle", h.settleErr)
	}
	if drained, err := h.s.ResourcesDrained(context.Background(), "root"); err != nil || !drained {
		t.Fatal("root image failure resources not drained", err)
	}
	if d.builds != 1 || d.pushes != 1 || d.count("after") != 0 {
		t.Fatal("unexpected root physical counters", d.builds, d.pushes, d.count("after"))
	}
	d.mu.Lock()
	builtTags := append([]string(nil), d.images...)
	builtIDs := map[string]string{}
	for tag, id := range d.builtIDs {
		builtIDs[tag] = id
	}
	d.mu.Unlock()
	if len(builtTags) != 1 || builtIDs[builtTags[0]] == "" {
		t.Fatal("original local build identity unavailable")
	}
	remoteImageID = builtIDs[builtTags[0]]
	if nativeDocker(t, "container", "inspect", "--format", "{{.Id}}", role) != placeholder {
		t.Fatal("failed pull replaced the owned placeholder")
	}
	artifacts, err := runs.ListArtifacts(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	var published run.Artifact
	for _, artifact := range artifacts {
		if artifact.Type == run.ArtifactImage && artifact.Metadata["pushed"] == true && metadataString(artifact, "sourceJobId") == "push" {
			if published.ID != "" {
				t.Fatal("ambiguous published image")
			}
			published = artifact
		}
	}
	if published.ID == "" || published.RunID != "root" || published.Reference != remote {
		t.Fatal("original pushed producer artifact missing")
	}
	digest := metadataString(published, "digest")
	repo := metadataString(published, "digestRepository")
	if repo != registry.address+"/"+repository || len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatal("published repository proof missing")
	}
	pinned := repo + "@" + digest
	nativeDocker(t, "start", registry.cid)
	nativeWaitHTTP(t, "http://"+registry.address+"/v2/", "")
	request, err := http.NewRequest(http.MethodHead, "http://"+registry.address+"/v2/"+repository+"/manifests/"+digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Docker-Content-Digest") != digest {
		t.Fatal("dedicated receiver did not independently confirm published digest")
	}
	node := h.node(t, "root", "stage", "deploy")
	deployNodeKey = node.Key
	var parentTargetCipher []byte
	if err = h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_external_targets WHERE attempt_id='root' AND node_key=? AND server_id='native-standard'", node.Key).Scan(&parentTargetCipher); err != nil {
		t.Fatal(err)
	}
	child = h.submit(t, "root", []string{node.Key}, nil)
	nativeExecute(t, h, child, false)
	nativeWaitHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/", port), "original-committed-source\n")
	if d.builds != 1 || d.pushes != 1 || d.count("after") != 1 {
		t.Fatal("retry repeated successful standard producer", d.builds, d.pushes, d.count("after"))
	}
	attempts, err := deployattempt.New(h.store.DB).ListByRun(context.Background(), child)
	if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 {
		t.Fatal("current standard-image receipt missing", err)
	}
	targetProof := attempts[0].Targets[0]
	if targetProof.Source.RunID != child || targetProof.Source.ProjectID != "p1" || targetProof.ServerID != "native-standard" || targetProof.ArtifactID != published.ID || targetProof.Outcome != deployattempt.Succeeded || targetProof.ExpectedImage.Reference != pinned || targetProof.ExpectedImage.Repository != repo || targetProof.ExpectedImage.Digest != digest || targetProof.ExpectedImage.DigestKind == deployattempt.DigestConfig {
		t.Fatal("typed current-attempt repository receipt mismatch")
	}
	var cid string
	for _, receipt := range targetProof.Receipts {
		if receipt.Kind == deployattempt.Succeeded {
			cid = receipt.ActualImage.ContainerID
		}
	}
	if !deployattempt.ValidContainerID(cid) || cid == placeholder {
		t.Fatal("full new activated CID missing")
	}
	var opID string
	if err = h.store.DB.QueryRow("SELECT id FROM run_retry_external_targets WHERE attempt_id=? AND node_key=? AND server_id='native-standard'", child, node.Key).Scan(&opID); err != nil {
		t.Fatal(err)
	}
	op, err := h.s.TargetOperation(context.Background(), child, node.Key, opID)
	if err != nil || op.State != "success" || op.Proof.DeploymentTargetID != targetProof.ID || op.Proof.ContainerID != cid || op.Proof.ImageID != remoteImageID || op.Proof.RuntimeID == "" || op.Proof.ArtifactID != published.ID {
		t.Fatal("typed current standard target operation incomplete", err)
	}
	actual := nativeDocker(t, "container", "inspect", "--format", "{{.Id}} {{.Image}} {{.Config.Image}}", cid)
	if actual != cid+" "+op.Proof.ImageID+" "+pinned {
		t.Fatal("actual CID/config/pinned repository differs")
	}
	var repoDigests []string
	if json.Unmarshal([]byte(nativeDocker(t, "image", "inspect", "--format", "{{json .RepoDigests}}", op.Proof.ImageID)), &repoDigests) != nil {
		t.Fatal("actual image digest metadata unavailable")
	}
	matched := false
	for _, ref := range repoDigests {
		matched = matched || ref == pinned
	}
	if !matched {
		t.Fatal("actual config does not retain the expected repository digest")
	}
	var parentSource string
	if err = h.store.DB.QueryRow("SELECT run_id FROM run_artifacts WHERE id=?", published.ID).Scan(&parentSource); err != nil || parentSource != "root" {
		t.Fatal("original image producer row changed")
	}
	var parentTargetAfter []byte
	if err = h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_external_targets WHERE attempt_id='root' AND node_key=? AND server_id='native-standard'", node.Key).Scan(&parentTargetAfter); err != nil || string(parentTargetAfter) != string(parentTargetCipher) {
		t.Fatal("original failed target proof changed")
	}
	var freshArtifacts int
	if err = h.store.DB.QueryRow("SELECT COUNT(*) FROM run_artifacts WHERE run_id=?", child).Scan(&freshArtifacts); err != nil || freshArtifacts != 0 {
		t.Fatal("standard deployment fabricated a new build artifact")
	}
}

// Ordinary image activation has no pipewright.run/deployment-target labels.
// Ownership is proven using the persisted current receipt and typed operation,
// not inferred from its mutable name or an invented label convention.
func nativeStandardCleanupTarget(t *testing.T, h *retryHarness, role, owner, placeholder, nodeImage, child, nodeKey string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, stderr, code, err := (execCommander{}).Run(ctx, "docker", []string{"container", "inspect", "--format", "{{json .}}", role}, "")
	if ctx.Err() == nil && nativeStandardExactMissing(err, code, stderr, role) {
		return true
	}
	var actual struct {
		ID     string `json:"Id"`
		Image  string
		Config struct {
			Image  string
			Labels map[string]string
		}
	}
	if err != nil || code != 0 || json.Unmarshal([]byte(out), &actual) != nil || !deployattempt.ValidContainerID(actual.ID) || actual.Config.Labels["io.pipewright.acceptance.fixture"] != owner {
		t.Error("standard fixture owner not proven; preserved")
		return false
	}
	if actual.ID == placeholder {
		if actual.Image != nodeImage {
			t.Error("placeholder config drift; preserved")
			return false
		}
	} else {
		if child == "" || nodeKey == "" {
			t.Error("current child receipt unavailable; preserved")
			return false
		}
		attempts, err := deployattempt.New(h.store.DB).ListByRun(ctx, child)
		if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 {
			t.Error("current standard receipt unavailable; preserved")
			return false
		}
		proof := attempts[0].Targets[0]
		matched := false
		for _, receipt := range proof.Receipts {
			if receipt.Kind == deployattempt.Succeeded && receipt.TargetID == proof.ID && receipt.ActualImage.ContainerID == actual.ID {
				matched = true
			}
		}
		var opID string
		if h.store.DB.QueryRow("SELECT id FROM run_retry_external_targets WHERE attempt_id=? AND node_key=? AND server_id=?", child, nodeKey, proof.ServerID).Scan(&opID) != nil {
			t.Error("typed standard operation unavailable; preserved")
			return false
		}
		op, err := h.s.TargetOperation(ctx, child, nodeKey, opID)
		if err != nil || !matched || proof.Source.RunID != child || proof.Source.ProjectID != "p1" || proof.ServerID != "native-standard" || proof.ContainerName != role || proof.Outcome != deployattempt.Succeeded || proof.ExpectedImage.Repository == "" || proof.ExpectedImage.Digest == "" || actual.Config.Image != proof.ExpectedImage.Repository+"@"+proof.ExpectedImage.Digest || op.State != "success" || op.Proof.DeploymentTargetID != proof.ID || op.Proof.ContainerID != actual.ID || op.Proof.ImageID != actual.Image || op.Proof.RuntimeID == "" || op.Proof.ArtifactID != proof.ArtifactID {
			t.Error("standard exact operation/receipt/config proof mismatch; preserved")
			return false
		}
		var repoDigests []string
		repoOut, _, repoCode, repoErr := (execCommander{}).Run(ctx, "docker", []string{"image", "inspect", "--format", "{{json .RepoDigests}}", actual.Image}, "")
		if repoErr != nil || repoCode != 0 || json.Unmarshal([]byte(repoOut), &repoDigests) != nil {
			t.Error("standard current digest proof unavailable; preserved")
			return false
		}
		found := false
		for _, ref := range repoDigests {
			found = found || ref == actual.Config.Image
		}
		if !found {
			t.Error("standard current digest identity drift; preserved")
			return false
		}
	}
	_, _, _, _ = (execCommander{}).Run(ctx, "docker", []string{"stop", "--time", "2", actual.ID}, "")
	_, _, code, err = (execCommander{}).Run(ctx, "docker", []string{"rm", actual.ID}, "")
	if err != nil || code != 0 {
		t.Error("standard exact CID cleanup failed")
		return false
	}
	return true
}

// Kept separate to make the exact same native registry image contract obvious.
func nativeStandardRegistryImage(t *testing.T) string {
	t.Helper()
	image := os.Getenv("PIPEWRIGHT_TEST_NODE_RETRY_REGISTRY_IMAGE")
	nativeRequireImage(t, image)
	return image
}

func nativeStandardExactMissing(err error, code int, stderr, identity string) bool {
	var exited *exec.ExitError
	return code == 1 && errors.As(err, &exited) && exited.ExitCode() == 1 && (strings.Contains(stderr, "No such container: "+identity) || strings.Contains(stderr, "No such object: "+identity))
}
