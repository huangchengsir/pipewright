package build

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
)

func TestRunDeployJobPassesLocalDockerContract(t *testing.T) {
	dep := &stubStageDeployer{}
	b := &Builder{deployer: dep}
	rep := &fakeReporter{}
	config := map[string]any{"serverId": "server", "artifactType": "archive", "containerName": "blogback", "localDocker": "true", "localDockerPrepareCommand": "tar xf deployctx.tar", "localDockerBuildContext": ".", "localDockerDockerfile": "Dockerfile", "localDockerBuildArgs": `["VERSION=1"]`, "localDockerRunArgs": `["-e","TZ=Asia/Shanghai","-v","/docker/Blogback:/log"]`, "ports": "8082:8082", "deployPath": "/opt/pipewright/blogback", "commandTimeoutSeconds": "600"}
	job := pipeline.Job{ID: "local", Name: "local", Type: "deploy_ssh", Config: config}
	if err := b.runDeployJob(context.Background(), rep, job, "run", nil); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"localDocker", "localDockerPrepareCommand", "localDockerBuildContext", "localDockerDockerfile", "localDockerBuildArgs", "localDockerRunArgs", "artifactType", "containerName", "ports", "commandTimeoutSeconds"} {
		if dep.gotCfg[key] != config[key] {
			t.Fatalf("contract field %s missing", key)
		}
	}
	if dep.gotCfg["releaseBase"] != "/opt/pipewright/blogback" || len(dep.gotServers) != 1 {
		t.Fatal("target/path lost")
	}
	for _, line := range rep.logs {
		if strings.Contains(line, "VERSION=1") || strings.Contains(line, "tar xf") {
			t.Fatal("raw configuration logged")
		}
	}
}

func TestRunDeployJobRejectsConflictingLocalScriptBeforeDispatch(t *testing.T) {
	dep := &stubStageDeployer{}
	b := &Builder{deployer: dep}
	rep := &fakeReporter{}
	job := pipeline.Job{ID: "local", Type: "deploy_ssh", Config: map[string]any{"serverId": "server", "artifactType": "archive", "containerName": "blogback", "localDocker": "true", "restartCommand": "private-secret-do-not-log"}}
	if err := b.runDeployJob(context.Background(), rep, job, "run", nil); !errors.Is(err, ErrBuildFailed) {
		t.Fatal(err)
	}
	if dep.gotCfg != nil {
		t.Fatal("conflicting script dispatched")
	}
	for _, line := range rep.logs {
		if strings.Contains(line, "private-secret-do-not-log") {
			t.Fatal("script leaked")
		}
	}
}
