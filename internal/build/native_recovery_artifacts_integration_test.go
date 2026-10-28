package build

// All physical effects below are gated by nativeRetryReady. Fixtures use only
// pre-existing exact cached images, unique identities and loopback listeners.
// They do not provision a business registry or pull/build dependencies online.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

func nativeFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
func nativeDocker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, stderr, code, err := (execCommander{}).Run(ctx, "docker", args, "")
	if err != nil || code != 0 {
		t.Fatalf("actual Docker fixture operation %s failed: exit=%d err=%v stderr=%s", args[0], code, err, stderr)
	}
	return strings.TrimSpace(out)
}
func nativeWaitHTTP(t *testing.T, url, body string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		r, err := client.Get(url)
		if err == nil {
			var text strings.Builder
			_, copyErr := io.Copy(&text, io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			if copyErr == nil && r.StatusCode == 200 && (body == "" || text.String() == body) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("actual loopback fixture did not become ready")
}

// The registry is created while stopped. The first actual push is refused by
// its not-yet-listening endpoint; starting that same unique fixture repairs only
// the environment, leaving source/manifest/target config unchanged.
func TestNativeRecoveryActualPushKeepsBuiltImage(t *testing.T) {
	image := nativeRetryReady(t)
	registryImage := os.Getenv("PIPEWRIGHT_TEST_NODE_RETRY_REGISTRY_IMAGE")
	nativeRequireImage(t, registryImage)
	name := "pw-native-registry-" + uuid.NewString()
	port := nativeFreePort(t)
	cid := nativeDocker(t, "create", "--pull=never", "--name", name, "--label", "io.pipewright.acceptance.fixture="+name, "--publish", fmt.Sprintf("127.0.0.1:%d:5000", port), registryImage)
	if !deployattempt.ValidContainerID(cid) {
		t.Fatal("registry create did not return exact CID")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, stderr, code, err := (execCommander{}).Run(ctx, "docker", []string{"container", "inspect", "--format", `{{.Id}} {{.Image}} {{index .Config.Labels "io.pipewright.acceptance.fixture"}}`, cid}, "")
		if code == 1 && (strings.Contains(stderr, "No such container: "+cid) || strings.Contains(stderr, "No such object: "+cid)) {
			return
		}
		if err != nil || code != 0 || strings.TrimSpace(out) != cid+" "+registryImage+" "+name {
			t.Error("registry owner/identity drift; fixture preserved")
			return
		}
		_, _, _, _ = (execCommander{}).Run(ctx, "docker", []string{"container", "stop", "--time", "2", cid}, "")
		_, _, code, err = (execCommander{}).Run(ctx, "docker", []string{"container", "rm", "-v", cid}, "")
		if err != nil || code != 0 {
			t.Error("exact registry fixture lifecycle removal failed")
		}
	})
	remote := fmt.Sprintf("127.0.0.1:%d", port)
	repository := "native-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	build := pipeline.Job{ID: "image", Name: "image", Type: "build_image", Config: map[string]any{"artifactType": "image", "dockerfilePath": "Dockerfile"}}
	push := pipeline.Job{ID: "push", Name: "push", Type: "push_image", Needs: []string{"image"}, Config: map[string]any{"registry": remote, "imageName": repository, "tag": "acceptance"}}
	h, d, _ := nativeHarness(t, retrySpec(build, push, nativeJob(image, "after", "true", "push")))
	nativeExecute(t, h, "root", true)
	d.mu.Lock()
	builds := d.builds
	local := append([]string(nil), d.images...)
	d.mu.Unlock()
	if builds != 1 || len(local) != 1 {
		t.Fatal("actual producer did not build exactly once")
	}
	identity, err := d.ImageIdentity(context.Background(), local[0])
	if err != nil {
		t.Fatal(err)
	}
	ref := remote + "/" + repository + ":acceptance"
	t.Cleanup(func() { nativeRemoveImageTag(t, ref, identity.ImageID) })
	nativeDocker(t, "container", "start", cid)
	nativeWaitHTTP(t, "http://"+remote+"/v2/", "")
	child := h.submit(t, "root", []string{h.node(t, "root", "stage", "push").Key}, nil)
	nativeExecute(t, h, child, false)
	d.mu.Lock()
	builds, pushes := d.builds, d.pushes
	d.mu.Unlock()
	if builds != 1 || pushes != 2 {
		t.Fatalf("actual commands build=%d push=%d", builds, pushes)
	}
	after, err := d.ImageIdentity(context.Background(), ref)
	if err != nil || after.ImageID != identity.ImageID || after.Digest == "" {
		t.Fatal("pushed identity/digest not independently observed", err)
	}
	nativeAssertCounts(t, d, map[string]int{"after": 1})
	t.Log("actual refused push then cached-only loopback registry; original local image build count1, push count2, downstream continues")
}

func nativeFileBuild(image, kind string) pipeline.Job {
	return pipeline.Job{ID: "filebuild", Name: "filebuild", Type: "build_image", Config: map[string]any{"artifactType": kind, "buildModel": "toolchain", "toolchainLanguage": "sha256", "toolchainVersion": strings.TrimPrefix(image, "sha256:")}}
}
func TestNativeRecoveryActualBuildImageDist(t *testing.T) {
	image := nativeRetryReady(t)
	for _, failProducer := range []bool{false, true} {
		name := "reuse_successful_dist"
		if failProducer {
			name = "retry_dist_adapter"
		}
		t.Run(name, func(t *testing.T) {
			consumer := nativeJob(image, "consume", `test "$(cat dist/index.html)" = native-build-image-dist`, "filebuild")
			h, d, _ := nativeHarness(t, retrySpec(nativeFileBuild(image, "dist"), consumer, nativeJob(image, "after", "true", "consume")))
			target := "consume"
			if failProducer {
				target = "filebuild"
			}
			d.failing(target, true)
			nativeExecute(t, h, "root", true)
			d.failing(target, false)
			child := h.submit(t, "root", []string{h.node(t, "root", "stage", target).Key}, nil)
			nativeExecute(t, h, child, false)
			producerCount, consumerCount := 1, 2
			if failProducer {
				producerCount, consumerCount = 2, 1
			}
			nativeAssertCounts(t, d, map[string]int{"filebuild": producerCount, "consume": consumerCount, "after": 1})
			var artifacts int
			if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_artifacts WHERE type='dist'").Scan(&artifacts); err != nil || artifacts != 1 {
				t.Fatal("real dist archive not persistently registered once", artifacts, err)
			}
		})
	}
}

func TestNativeRecoveryActualBuildImageJAR(t *testing.T) {
	image := nativeRetryReady(t)
	if os.Getenv("PIPEWRIGHT_TEST_NODE_RETRY_JAR") != "1" {
		t.Skip("JAR physical gate pending explicit exact Maven image/offline-plugin-cache facts; not accepted")
	}
	maven := os.Getenv("PIPEWRIGHT_TEST_NODE_RETRY_MAVEN_IMAGE")
	nativeRequireImage(t, maven)
	// This is the real mvn package adapter, offline and with networking disabled.
	// Missing cached Maven plugins fail; no fake JAR or dependency download.
	consumer := nativeJob(image, "consume", `node -e 'const b=require("fs").readFileSync("target/native-retry-1.0.jar");if(b[0]!==80||b[1]!==75)process.exit(5)'`, "filebuild")
	h, d, _ := nativeHarness(t, retrySpec(nativeFileBuild(maven, "jar"), consumer))
	d.failing("consume", true)
	nativeExecute(t, h, "root", true)
	producer := h.node(t, "root", "stage", "filebuild")
	var producerStatus, reason string
	if err := h.store.DB.QueryRow("SELECT status,reason_code FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", producer.Key).Scan(&producerStatus, &reason); err != nil {
		t.Fatal(err)
	}
	var storedJARs int
	if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_artifacts WHERE run_id='root' AND type='jar'").Scan(&storedJARs); err != nil {
		t.Fatal(err)
	}
	if producerStatus != "success" || d.count("jarbuild") != 1 || storedJARs != 1 {
		lines := 0
		for _, line := range h.sink.logs {
			if strings.Contains(line, "[ERROR]") || strings.Contains(line, "BUILD FAILURE") || strings.Contains(line, "offline") && strings.Contains(line, "maven") {
				if len(line) > 400 {
					line = line[:400] + "…"
				}
				t.Log("native Maven fixture diagnostic: " + line)
				lines++
				if lines >= 8 {
					break
				}
			}
		}
		t.Fatalf("JAR producer not verified: status=%s reason=%s actualBuildMarkers=%d storedJARs=%d; no consumer retry attempted", producerStatus, reason, d.count("jarbuild"), storedJARs)
	}
	d.failing("consume", false)
	child := h.submit(t, "root", []string{h.node(t, "root", "stage", "consume").Key}, nil)
	nativeExecute(t, h, child, false)
	nativeAssertCounts(t, d, map[string]int{"jarbuild": 1, "consume": 2})
}

func TestNativeRecoveryActualLocalDockerFileReceipt(t *testing.T) {
	image := nativeRetryReady(t)
	port, password, dir := nativeSSHFixture(t)
	ready := filepath.Join(dir, "build-ready")
	name := "pw-native-local-" + uuid.NewString()
	httpPort := nativeFreePort(t)
	producer := nativeJob(image, "package", "tar -czf deployctx.tar Dockerfile.local source.txt")
	producer.Config["artifacts"] = `[{"path":"deployctx.tar","name":"deploy-context"}]`
	job := pipeline.Job{ID: "deploy", Name: "deploy", Type: "deploy_ssh", Needs: []string{"package"}, Config: map[string]any{"serverId": "native-a", "artifactType": "archive", "deployPath": filepath.Join(dir, "application"), "localDocker": "true", "containerName": name, "localDockerPrepareCommand": "test -f " + nativeShellQuote(ready) + "\ntar -xzf deployctx.tar", "localDockerDockerfile": "Dockerfile.local", "ports": fmt.Sprintf("127.0.0.1:%d:8080", httpPort)}}
	h, d, source := nativeHarness(t, retrySpec(producer, job, nativeJob(image, "after", "true", "deploy")))
	targets, _ := nativeInstallDeployer(t, h)
	nativeAddServer(t, h, targets, "native-a", port, password)
	dockerfile, err := os.ReadFile(filepath.Join(source.repo, "Dockerfile.local"))
	if err != nil {
		t.Fatal(err)
	}
	_, fixtureLabel, found := strings.Cut(string(dockerfile), "LABEL io.pipewright.acceptance.fixture=")
	if !found {
		t.Fatal("local image ownership label missing from committed fixture")
	}
	fixtureLabel = strings.SplitN(fixtureLabel, "\n", 2)[0]
	nativeOwnLocalContainer(t, h, name, fixtureLabel)
	nativeExecute(t, h, "root", true)
	if err := os.WriteFile(ready, []byte("current-test-condition"), 0600); err != nil {
		t.Fatal(err)
	}
	child := h.submit(t, "root", []string{h.node(t, "root", "stage", "deploy").Key}, nil)
	nativeExecute(t, h, child, false)
	nativeWaitHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/", httpPort), "original-committed-source\n")
	attempts, err := deployattempt.New(h.store.DB).ListByRun(context.Background(), child)
	if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 {
		t.Fatal("real local image receipt missing", err)
	}
	target := attempts[0].Targets[0]
	if !deployattempt.ValidLocalExpected(target.ExpectedImage) || target.Outcome != deployattempt.Succeeded {
		t.Fatal("no pre-activation local config identity/success receipt")
	}
	var actual deployattempt.ActualImage
	for _, receipt := range target.Receipts {
		if receipt.Kind == deployattempt.Succeeded {
			actual = receipt.ActualImage
		}
	}
	if !deployattempt.ValidContainerID(actual.ContainerID) || actual.ConfigID != target.ExpectedImage.Digest {
		t.Fatal("actual container/config receipt does not prove local no-registry activation")
	}
	observed := nativeDocker(t, "container", "inspect", "--format", "{{.Id}} {{.Image}}", actual.ContainerID)
	if observed != actual.ContainerID+" "+actual.ConfigID {
		t.Fatal("physical container drifted from persisted receipt")
	}
	nativeAssertCounts(t, d, map[string]int{"package": 1, "after": 1})
	if d.pushes != 0 {
		t.Fatal("local file deployment unexpectedly required a registry push")
	}
	t.Log("real file archive→SSH upload→local docker build/run; independent pre-activation config and full CID receipts without repository digest")
}

// This exact planned name is UUID-scoped. Cleanup additionally checks durable
// product ownership and exact image/container identity, then stops and removes
// only that instance. A mismatch is left for the explicit final cleanup review.
func nativeOwnLocalContainer(t *testing.T, h *retryHarness, name, fixtureLabel string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, stderr, code, err := (execCommander{}).Run(ctx, "docker", []string{"container", "inspect", "--format", "{{json .}}", name}, "")
		if code == 1 && (strings.Contains(stderr, "No such container: "+name) || strings.Contains(stderr, "No such object: "+name)) {
			return
		}
		var container struct {
			ID     string `json:"Id"`
			Image  string
			Config struct{ Labels map[string]string }
		}
		if err != nil || code != 0 || json.Unmarshal([]byte(out), &container) != nil || !deployattempt.ValidContainerID(container.ID) {
			t.Error("owned local container cannot be inspected; preserved")
			return
		}
		proof, proofErr := deployattempt.New(h.store.DB).GetTarget(ctx, container.Config.Labels["pipewright.deployment-target"])
		if proofErr != nil || proof == nil || proof.ContainerName != name || proof.Source.ProjectID != "p1" || !deployattempt.ValidLocalExpected(proof.ExpectedImage) || proof.ExpectedImage.Digest != container.Image || proof.Source.RunID != container.Config.Labels["pipewright.run"] {
			if proofErr != nil || proof == nil {
				t.Errorf("local container owner not proven; preserved (receiptUnavailable=%t notFound=%t databaseClosed=%t)", proofErr != nil, errors.Is(proofErr, deployattempt.ErrNotFound), proofErr != nil && strings.Contains(proofErr.Error(), "database is closed"))
			} else {
				var producerRun string
				artifactErr := h.store.DB.QueryRow("SELECT run_id FROM run_artifacts WHERE id=?", proof.ArtifactID).Scan(&producerRun)
				t.Errorf("local container owner not proven; preserved (nameMatches=%t projectMatches=%t validLocalExpected=%t configMatches=%t runLabelMatchesAttempt=%t artifactProducerKnown=%t runLabelMatchesProducer=%t sourceAttempt=%s labelRun=%s artifactID=%s)", proof.ContainerName == name, proof.Source.ProjectID == "p1", deployattempt.ValidLocalExpected(proof.ExpectedImage), proof.ExpectedImage.Digest == container.Image, proof.Source.RunID == container.Config.Labels["pipewright.run"], artifactErr == nil, artifactErr == nil && producerRun == container.Config.Labels["pipewright.run"], proof.Source.RunID, container.Config.Labels["pipewright.run"], proof.ArtifactID)
			}
			return
		}
		_, _, _, _ = (execCommander{}).Run(ctx, "docker", []string{"container", "stop", "--time", "2", container.ID}, "")
		_, _, code, err = (execCommander{}).Run(ctx, "docker", []string{"container", "rm", container.ID}, "")
		if err != nil || code != 0 {
			t.Error("exact local container lifecycle cleanup failed")
			return
		}
		label, _, code, err := (execCommander{}).Run(ctx, "docker", []string{"image", "inspect", "--format", `{{index .Config.Labels "io.pipewright.acceptance.fixture"}}`, container.Image}, "")
		if err != nil || code != 0 || strings.TrimSpace(label) != fixtureLabel {
			t.Error("private built image label unavailable; preserved")
			return
		}
		_, _, code, err = (execCommander{}).Run(ctx, "docker", []string{"image", "rm", container.Image}, "")
		if err != nil || code != 0 {
			t.Error("exact private local image lifecycle cleanup failed")
		}
	})
}
