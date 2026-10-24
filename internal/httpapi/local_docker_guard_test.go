package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
)

func TestLocalDockerConfigHTTPRejectsBeforeAttempt(t *testing.T) {
	f := newGuardHTTPFixture(t)
	before, err := f.ledger.ListByRun(context.Background(), f.runID)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"artifactId":"` + f.artifactID + `","serverIds":["` + f.serverID + `"],"deployConfig":{"localDocker":"true","containerName":"app","restartCommand":"private-input-do-not-echo"}}`
	path := "/api/runs/" + f.runID + "/deploy"
	for _, input := range []struct {
		token, csrf string
		status      int
	}{{"", "", 401}, {"valid", "", 403}, {"valid", "csrf", 422}} {
		w := guardHTTPRequest(f.http, "POST", path, body, input.token, input.csrf)
		if w.Code != input.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-input-do-not-echo") {
			t.Fatal("configuration leaked")
		}
		if input.status == 422 && !strings.Contains(w.Body.String(), "invalid_local_docker_config") {
			t.Fatal(w.Body.String())
		}
	}
	after, err := f.ledger.ListByRun(context.Background(), f.runID)
	if err != nil || len(after) != len(before) || f.observer.reads.Load() != 0 {
		t.Fatal("invalid request had side effects", err)
	}
}

func TestLocalDockerErrorHTTPDoesNotExposeWrappedData(t *testing.T) {
	w := httptest.NewRecorder()
	writeDeployError(w, errors.Join(deploy.ErrInvalidLocalDockerConfig, errors.New("private-build-token")))
	if w.Code != 422 || strings.Contains(w.Body.String(), "private-build-token") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLocalDockerFromNowHTTPRequiresTypedPreActivationIdentity(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "pre-activation"}[typed], func(t *testing.T) {
			f := newGuardHTTPFixture(t)
			ctx := context.Background()
			original, err := f.ledger.Get(ctx, f.attemptID)
			if err != nil {
				t.Fatal(err)
			}
			a, err := f.ledger.Begin(ctx, deployattempt.BeginInput{RunID: f.runID, Initiator: "stage"})
			if err != nil {
				t.Fatal(err)
			}
			expected := deployattempt.ExpectedImage{Reference: "sha256:" + strings.Repeat("b", 64), Digest: "sha256:" + strings.Repeat("b", 64), DigestKind: deployattempt.DigestConfig, Platform: deployattempt.Platform{OS: "linux", Architecture: "amd64"}}
			if typed {
				expected.IdentitySource = deployattempt.IdentityScriptPreActivation
			}
			tr, err := f.ledger.StartTarget(ctx, a.ID, deployattempt.TargetInput{ArtifactID: f.artifactID, ArtifactName: "archive", ServerID: f.serverID, ServerName: "fixture-server", ContainerName: "app", ConnectionFingerprint: original.Targets[0].ConnectionFingerprint, ExpectedImage: expected})
			if err != nil {
				t.Fatal(err)
			}
			actual := deployattempt.ActualImage{ContainerID: strings.Repeat("2", 64), ConfigID: expected.Digest, Platform: expected.Platform}
			for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
				if _, err = f.ledger.AppendReceipt(ctx, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: actual}); err != nil {
					t.Fatal(err)
				}
			}
			body := `{"mode":"from_now","attemptId":"` + a.ID + `","durationSeconds":300}`
			w := guardHTTPRequest(f.http, "POST", "/api/projects/"+f.projectID+"/release-guard-plans", body, "valid", "csrf")
			var plan releaseguard.Plan
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || len(plan.Targets) != 1 {
				t.Fatal(w.Code, w.Body.String())
			}
			want := "unsupported"
			if typed {
				want = "available"
			}
			if plan.Targets[0].Availability != want || f.observer.reads.Load() != 0 {
				t.Fatal(plan.Targets[0].Availability, want)
			}
			if strings.Contains(w.Body.String(), "fixture-private") {
				t.Fatal("credential leaked")
			}
		})
	}
}
