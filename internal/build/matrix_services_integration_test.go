package build

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

type matrixRealObject struct {
	ID, Name string
	Service  bool
}
type matrixRealCommander struct {
	Commander
	mu                   sync.Mutex
	caseID               string
	containers, networks map[string]matrixRealObject
}

func (c *matrixRealCommander) args(args []string) []string {
	index := 0
	if len(args) > 0 && args[0] == "create" {
		index = 1
	}
	if len(args) > 1 && args[0] == "network" && args[1] == "create" {
		index = 2
	}
	if index == 0 {
		return args
	}
	labels := []string{"--label", "io.pipewright.acceptance.run=platform-reliability-20261027", "--label", "io.pipewright.acceptance.case=" + c.caseID}
	out := append([]string(nil), args[:index]...)
	out = append(out, labels...)
	return append(out, args[index:]...)
}
func (c *matrixRealCommander) record(args []string, stdout string, code int, err error) {
	if err != nil || code != 0 {
		return
	}
	id := strings.TrimSpace(stdout)
	if !serviceObjectID.MatchString(id) {
		return
	}
	name := ""
	service := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--name" && i+1 < len(args) {
			name = args[i+1]
		}
		if args[i] == "--network-alias" {
			service = true
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(args) > 0 && args[0] == "create" {
		c.containers[id] = matrixRealObject{ID: id, Name: name, Service: service}
	}
	if len(args) > 1 && args[0] == "network" && args[1] == "create" {
		c.networks[id] = matrixRealObject{ID: id, Name: args[len(args)-1]}
	}
}
func (c *matrixRealCommander) Run(ctx context.Context, name string, args []string, stdin string) (string, string, int, error) {
	args = c.args(args)
	out, stderr, code, err := c.Commander.Run(ctx, name, args, stdin)
	c.record(args, out, code, err)
	return out, stderr, code, err
}
func (c *matrixRealCommander) Stream(ctx context.Context, name string, args []string, stdin string, onLine func(string, string)) (int, error) {
	args = c.args(args)
	var stdout strings.Builder
	code, err := c.Commander.Stream(ctx, name, args, stdin, func(stream, line string) {
		if stream == "stdout" {
			stdout.WriteString(line)
			stdout.WriteByte('\n')
		}
		if onLine != nil {
			onLine(stream, line)
		}
	})
	c.record(args, stdout.String(), code, err)
	return code, err
}
func (c *matrixRealCommander) snapshot() (map[string]matrixRealObject, map[string]matrixRealObject) {
	c.mu.Lock()
	defer c.mu.Unlock()
	containers := map[string]matrixRealObject{}
	networks := map[string]matrixRealObject{}
	for k, v := range c.containers {
		containers[k] = v
	}
	for k, v := range c.networks {
		networks[k] = v
	}
	return containers, networks
}
func matrixObjectExists(ctx context.Context, kind, id string) (bool, error) {
	args := []string{"ps", "-a", "--no-trunc", "--filter", "id=" + id, "--format", "{{.ID}}"}
	if kind == "network" {
		args = []string{"network", "ls", "--no-trunc", "--filter", "id=" + id, "--format", "{{.ID}}"}
	}
	out, stderr, code, err := (execCommander{}).Run(ctx, "docker", args, "")
	if err != nil || code != 0 {
		return false, fmt.Errorf("query %s %s: %d %v %s", kind, id, code, err, stderr)
	}
	return strings.TrimSpace(out) != "", nil
}
func (c *matrixRealCommander) assertClean(t *testing.T, allowed map[string]bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	containers, networks := c.snapshot()
	for _, kind := range []string{"container", "network"} {
		objects := containers
		if kind == "network" {
			objects = networks
		}
		for id := range objects {
			if allowed[id] {
				continue
			}
			exists, err := matrixObjectExists(ctx, kind, id)
			if err != nil || exists {
				t.Fatalf("runtime cleanup incomplete: %s %s exists=%t err=%v", kind, id, exists, err)
			}
		}
	}
}
func (c *matrixRealCommander) fallbackCleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	containers, networks := c.snapshot()
	for _, kind := range []string{"container", "network"} {
		objects := containers
		if kind == "network" {
			objects = networks
		}
		for id := range objects {
			exists, err := matrixObjectExists(ctx, kind, id)
			if err != nil {
				t.Errorf("owned cleanup query: %v", err)
				continue
			}
			if !exists {
				continue
			}
			t.Logf("MATRIX_ACCEPTANCE_FALLBACK_CLEANUP %s %s", kind, id)
			args := []string{"rm", "-f", "-v", id}
			if kind == "network" {
				args = []string{"network", "rm", id}
			}
			_, stderr, code, err := (execCommander{}).Run(ctx, "docker", args, "")
			if code != 0 || err != nil {
				t.Errorf("owned cleanup failed %s: %v %d %s", id, err, code, stderr)
			}
		}
	}
}

type matrixRealReporter struct {
	mu      sync.Mutex
	lines   []string
	onReady func()
	ready   sync.Once
}

func (r *matrixRealReporter) Log(_ context.Context, _ string, line string) error {
	r.mu.Lock()
	r.lines = append(r.lines, line)
	r.mu.Unlock()
	if line == "MATRIX_BODY_READY" && r.onReady != nil {
		r.ready.Do(r.onReady)
	}
	return nil
}
func (r *matrixRealReporter) EmitArtifact(context.Context, run.Artifact) error { return nil }
func (r *matrixRealReporter) JobRunning(context.Context, string) error         { return nil }
func (r *matrixRealReporter) JobDone(context.Context, string, string) error    { return nil }
func (r *matrixRealReporter) JobReporter(string) dagrun.StageReporter          { return r }
func (r *matrixRealReporter) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}
func matrixNodeCommand(js string) string {
	return "node -e '" + strings.ReplaceAll(js, "'", "'\\''") + "'"
}

const matrixRequestJS = `
async function getFixture() {
 const until=Date.now()+15000;
 let last;
 while(Date.now()<until){
  try{
   const response=await fetch("http://fixture:8080/health",{signal:AbortSignal.timeout(1000)});
   const data=await response.json();
   if(response.status!==200||data.fixture!=="pipewright-matrix-service-v1"||data.marker!==process.env.MATRIX_EXPECTED_MARKER)throw new Error("wrong fixture response");
   return data;
  }catch(error){last=error;await new Promise(resolve=>setTimeout(resolve,100));}
 }
 throw last||new Error("service did not become ready");
}
`

func matrixRealStage(nodeImage, serviceImage, marker string) pipeline.Stage {
	jsA := matrixRequestJS + `
(async()=>{
 const data=await getFixture();
 const proof={runtime:process.env.MATRIX_RUNTIME,group:process.env.MATRIX_GROUP,...data};
 const token=proof.runtime+"|"+proof.group+"|"+data.hostname;
 require("node:fs").writeFileSync(process.env.PIPEWRIGHT_ENV,"CELL_TOKEN="+token+"\n");
 console.log("MATRIX_A:"+JSON.stringify(proof));
})().catch(error=>{console.error(error);process.exit(1)});
`
	jsB := matrixRequestJS + `
(async()=>{
 const data=await getFixture();
 const proof={runtime:process.env.MATRIX_RUNTIME,group:process.env.MATRIX_GROUP,...data};
 if(process.env.CELL_TOKEN!==proof.runtime+"|"+proof.group+"|"+data.hostname)throw new Error("missing or cross-cell DAG output");
 console.log("MATRIX_B:"+JSON.stringify(proof));
})().catch(error=>{console.error(error);process.exit(1)});
`
	job := func(id, js string, needs ...string) pipeline.Job {
		return pipeline.Job{ID: id, Name: id, Type: "script", Needs: needs, Config: map[string]any{"image": nodeImage, "commands": matrixNodeCommand(js), "memory": "128m", "cpu": "0.5", "timeoutSeconds": 60}}
	}
	post := func(condition, tag string) pipeline.PostStep {
		return pipeline.PostStep{Condition: condition, Image: nodeImage, Commands: []string{matrixNodeCommand(`console.log("MATRIX_POST:` + tag + `")`)}}
	}
	return pipeline.Stage{ID: "matrix", Name: "Matrix", Kind: pipeline.KindBuild, Matrix: map[string][]string{"runtime": {"1.21", "1-21"}, "group": {"a", "b"}}, Jobs: []pipeline.Job{job("a", jsA), job("b", jsB, "a")}, Services: []pipeline.ServiceSpec{{Name: "fixture", Image: serviceImage, Env: []string{"FIXTURE_MARKER=" + marker}}}, Post: []pipeline.PostStep{post("always", "always"), post("on_success", "success"), post("on_failure", "failure")}}
}
func matrixRealSetup(t *testing.T) (*shellDriver, *matrixRealCommander, string, string, string) {
	t.Helper()
	nodeImage, serviceImage := os.Getenv("PIPEWRIGHT_TEST_NODE_IMAGE"), os.Getenv("PIPEWRIGHT_TEST_MATRIX_SERVICE_IMAGE")
	for _, image := range []string{nodeImage, serviceImage} {
		if !strings.HasPrefix(image, "sha256:") || !serviceObjectID.MatchString(strings.TrimPrefix(image, "sha256:")) {
			t.Fatal("matrix acceptance requires fixed cached image IDs")
		}
	}
	caseID := "matrix-" + uuid.NewString()
	command := &matrixRealCommander{Commander: execCommander{}, caseID: caseID, containers: map[string]matrixRealObject{}, networks: map[string]matrixRealObject{}}
	driver := &shellDriver{bin: "docker", cmdr: command, copyWorkspace: true, workspaceReconcileAfter: time.Now().Add(24 * time.Hour)}
	t.Cleanup(func() { command.fallbackCleanup(t) })
	return driver, command, nodeImage, serviceImage, caseID
}
func TestRealMatrixStageServices(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_TEST_MATRIX_SERVICES") != "1" {
		t.Skip("set PIPEWRIGHT_TEST_MATRIX_SERVICES=1 on an authorized isolated Docker node")
	}
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux Docker and copy-workspace acceptance")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"four_cells_dag_post", "post_only", "failure", "cancel", "post_failure", "fixed_port_conflict"} {
		t.Run(mode, func(t *testing.T) {
			driver, command, nodeImage, serviceImage, caseID := matrixRealSetup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			stage := matrixRealStage(nodeImage, serviceImage, caseID)
			rn := &run.Run{ID: caseID, ProjectID: "p1", Trigger: run.Trigger{Params: map[string]string{"MATRIX_EXPECTED_MARKER": caseID}}}
			if mode == "fixed_port_conflict" {
				matrixRealPortConflict(t, ctx, driver, command, stage, rn, caseID)
				return
			}
			if mode == "post_only" {
				stage.Jobs = nil
			}
			cells := dagrun.ExpandMatrix([]pipeline.Stage{stage})
			if len(cells) != 4 {
				t.Fatalf("expected actual 2x2 cells, got %d", len(cells))
			}
			if mode == "failure" {
				cells = cells[:1]
				cells[0].Jobs[0].Config["commands"] = matrixNodeCommand(`console.log("MATRIX_BODY_FAILED");process.exit(23)`)
			}
			if mode == "cancel" {
				cells = cells[:1]
				cells[0].Jobs[0].Config["commands"] = matrixNodeCommand(`console.log("MATRIX_BODY_READY");setInterval(()=>{},1000)`)
			}
			if mode == "post_failure" {
				cells = cells[:1]
				cells[0].Post[0].Commands = []string{matrixNodeCommand(`console.log("MATRIX_POST_FAIL");process.exit(17)`)}
			}
			reports := make([]*matrixRealReporter, len(cells))
			errs := make([]error, len(cells))
			var wg sync.WaitGroup
			for i, cell := range cells {
				reports[i] = &matrixRealReporter{}
				if mode == "cancel" {
					reports[i].onReady = cancel
				}
				wg.Add(1)
				go func(i int, cell pipeline.Stage) {
					defer wg.Done()
					errs[i] = NewStageExecutor(newDAGTestBuilder(driver, &markerCloner{}), nil)(ctx, rn, cell, reports[i])
				}(i, cell)
			}
			wg.Wait()
			services, _ := command.snapshot()
			byName := map[string]string{}
			for _, object := range services {
				if object.Service {
					byName[object.Name] = object.ID
				}
			}
			hostnames := map[string]bool{}
			tokens := map[string]bool{}
			for i, report := range reports {
				failed := mode == "failure" || mode == "cancel"
				if (errs[i] != nil) != failed {
					t.Fatalf("unexpected body result %s: %v logs=%v", mode, errs[i], report.snapshot())
				}
				lines := report.snapshot()
				always, success, failure := 0, 0, 0
				for _, line := range lines {
					switch line {
					case "MATRIX_POST:always":
						always++
					case "MATRIX_POST:success":
						success++
					case "MATRIX_POST:failure":
						failure++
					}
				}
				if mode == "post_failure" {
					if success != 1 || failure != 0 || !strings.Contains(strings.Join(lines, "\n"), "不影响阶段结果") {
						t.Fatalf("post failure changed result or continuation: %v", lines)
					}
				} else if always != 1 || ((success == 1) == failed) || ((failure == 1) != failed) {
					t.Fatalf("post conditions changed: %v", lines)
				}
				if mode == "cancel" && ctx.Err() == nil {
					t.Fatal("body was not actually canceled")
				}
				if mode == "four_cells_dag_post" || mode == "post_failure" {
					var a, b map[string]string
					countA, countB := 0, 0
					for _, line := range lines {
						if strings.HasPrefix(line, "MATRIX_A:") {
							countA++
							if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "MATRIX_A:")), &a); err != nil {
								t.Fatal(err)
							}
						}
						if strings.HasPrefix(line, "MATRIX_B:") {
							countB++
							if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "MATRIX_B:")), &b); err != nil {
								t.Fatal(err)
							}
						}
					}
					cid := byName[stageNetworkName(caseID, cells[i].ID)+"-fixture"]
					if len(cid) != 64 || countA != 1 || countB != 1 || a["hostname"] != cid[:12] || a["hostname"] != b["hostname"] || a["marker"] != caseID || a["runtime"] != b["runtime"] || a["group"] != b["group"] {
						t.Fatalf("unverified or cross-cell service/DAG evidence: cid=%s A=%v B=%v", cid, a, b)
					}
					hostnames[a["hostname"]] = true
					tokens[a["runtime"]+"|"+a["group"]] = true
					t.Logf("MATRIX_CELL_PROOF stage=%s CID=%s runtime=%s group=%s hostname=%s post=true", cells[i].ID, cid, a["runtime"], a["group"], a["hostname"])
				}
			}
			if mode == "four_cells_dag_post" && (len(hostnames) != 4 || len(tokens) != 4) {
				t.Fatalf("cell service identity not isolated: hosts=%v tokens=%v", hostnames, tokens)
			}
			command.assertClean(t, nil)
			t.Logf("MATRIX_REAL_CASE mode=%s cells=%d owned-created=%d runtime-cleanup=complete", mode, len(cells), len(services))
		})
	}
}
func matrixRealPortConflict(t *testing.T, ctx context.Context, driver *shellDriver, command *matrixRealCommander, stage pipeline.Stage, rn *run.Run, caseID string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	mapping := fmt.Sprintf("127.0.0.1:%d:8080", port)
	blockerNetwork := "pw-matrix-block-" + uuid.NewString()
	blockerName := blockerNetwork + "-service"
	if code, err := driver.CreateNetwork(ctx, blockerNetwork, nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if code, err := driver.RunService(ctx, blockerName, "blocker", stage.Services[0].Image, blockerNetwork, []string{"FIXTURE_MARKER=blocker"}, []string{mapping}, nil); code != 0 || err != nil {
		t.Fatalf("fixed port could not be reserved once: %d %v", code, err)
	}
	blockerCID, _ := driver.serviceContainers.Load(blockerName)
	blockerNID, _ := driver.serviceNetworks.Load(blockerNetwork)
	stage.Services[0].Ports = []string{mapping}
	cell := dagrun.ExpandMatrix([]pipeline.Stage{stage})[0]
	report := &matrixRealReporter{}
	err = NewStageExecutor(newDAGTestBuilder(driver, &markerCloner{}), nil)(ctx, rn, cell, report)
	if err == nil {
		t.Fatalf("occupied fixed mapping silently changed or succeeded: %s", mapping)
	}
	logs := strings.Join(report.snapshot(), "\n")
	if !strings.Contains(logs, "port is already allocated") && !strings.Contains(logs, "address already in use") {
		t.Fatalf("missing real fixed-port failure evidence: %s", logs)
	}
	if cell.Services[0].Ports[0] != mapping {
		t.Fatal("fixture mapping was rewritten")
	}
	command.assertClean(t, map[string]bool{blockerCID.(string): true, blockerNID.(string): true})
	exists, err := matrixObjectExists(ctx, "container", blockerCID.(string))
	if err != nil || !exists {
		t.Fatal("unrelated blocker removed", err)
	}
	if code, err := driver.StopService(ctx, blockerName, nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if code, err := driver.RemoveNetwork(ctx, blockerNetwork, nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	command.assertClean(t, nil)
	t.Logf("MATRIX_FIXED_PORT_PROOF mapping=%s failed-container=removed blocker=%s preserved-until-explicit-cleanup", mapping, blockerCID)
}
