package releaseguard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

func localExpected(id string) deployattempt.ExpectedImage {
	return deployattempt.ExpectedImage{Reference: id, Digest: id, DigestKind: deployattempt.DigestConfig, IdentitySource: deployattempt.IdentityScriptPreActivation, Platform: deployattempt.Platform{OS: "linux", Architecture: "amd64"}}
}

func localRuleTarget() TargetObservation {
	t := ruleTarget()
	t.ExpectedImage = localExpected(ruleSnapshot().ConfigID)
	return t
}

type localOnlyObserver struct{ Observer }

func (o localOnlyObserver) Inspect(ctx context.Context, cap target.ConnectionSnapshot, id string) RuntimeSnapshot {
	v := o.Observer.Inspect(ctx, cap, id)
	v.RepoDigests = nil
	v.DeclaredImage = "blogback:latest"
	return v
}

func TestLocalConfigIdentityMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PlanTarget, *RuntimeSnapshot)
		want   string
	}{
		{"complete local image without registry", func(*PlanTarget, *RuntimeSnapshot) {}, "passed"},
		{"mutable declared tag has no authority", func(_ *PlanTarget, v *RuntimeSnapshot) { v.DeclaredImage = "blogback:latest" }, "passed"},
		{"different complete config is decisive", func(_ *PlanTarget, v *RuntimeSnapshot) { v.ConfigID = oldDigest }, "failed"},
		{"different config and platform is still wrong image", func(_ *PlanTarget, v *RuntimeSnapshot) { v.ConfigID = oldDigest; v.Platform.Architecture = "arm64" }, "failed"},
		{"repository cannot override actual config", func(p *PlanTarget, v *RuntimeSnapshot) {
			v.ConfigID = oldDigest
			v.RepoDigests = []string{"reg.invalid/app@" + p.ExpectedImage.Digest}
		}, "failed"},
		{"legacy config has no capture proof", func(p *PlanTarget, _ *RuntimeSnapshot) { p.ExpectedImage.IdentitySource = "" }, "insufficient"},
		{"current container cannot be its own expectation", func(p *PlanTarget, _ *RuntimeSnapshot) { p.ExpectedImage.IdentitySource = "container_inspect" }, "insufficient"},
		{"expected short ID", func(p *PlanTarget, _ *RuntimeSnapshot) { p.ExpectedImage.Digest = "sha256:abcd" }, "insufficient"},
		{"expected mutable reference", func(p *PlanTarget, _ *RuntimeSnapshot) { p.ExpectedImage.Reference = "blogback:latest" }, "insufficient"},
		{"actual short ID", func(_ *PlanTarget, v *RuntimeSnapshot) { v.ConfigID = "sha256:abcd" }, "insufficient"},
		{"image inspect missing", func(_ *PlanTarget, v *RuntimeSnapshot) { v.ImageAvailable = false }, "insufficient"},
		{"expected platform missing", func(p *PlanTarget, _ *RuntimeSnapshot) { p.ExpectedImage.Platform = deployattempt.Platform{} }, "insufficient"},
		{"actual platform missing", func(_ *PlanTarget, v *RuntimeSnapshot) { v.Platform = deployattempt.Platform{} }, "insufficient"},
		{"architecture mismatch", func(_ *PlanTarget, v *RuntimeSnapshot) { v.Platform.Architecture = "arm64" }, "insufficient"},
		{"variant mismatch", func(_ *PlanTarget, v *RuntimeSnapshot) { v.Platform.Variant = "v8" }, "insufficient"},
		{"truncated image facts", func(_ *PlanTarget, v *RuntimeSnapshot) { v.Truncated = true }, "insufficient"},
		{"repository mixed into local identity", func(p *PlanTarget, _ *RuntimeSnapshot) { p.ExpectedImage.Repository = "reg.invalid/app" }, "insufficient"},
		{"capture source cannot relabel manifest", func(p *PlanTarget, v *RuntimeSnapshot) {
			p.ExpectedImage.DigestKind = deployattempt.DigestManifest
			p.ExpectedImage.Repository = "reg.invalid/app"
			v.RepoDigests = []string{"reg.invalid/app@" + p.ExpectedImage.Digest}
		}, "insufficient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, v := localRuleTarget().PlanTarget, ruleSnapshot()
			v.RepoDigests = nil
			tc.change(&p, &v)
			if got := VersionMatch(p, v); got != tc.want {
				t.Fatalf("VersionMatch=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestLocalImageUsesFullCoverageAndHardFailureRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(int, *RuntimeSnapshot)
		want   string
	}{
		{"complete five minute window", func(int, *RuntimeSnapshot) {}, "passed"},
		{"exit plus unavailable image still fails", func(i int, v *RuntimeSnapshot) {
			if i == 5 {
				v.Running = false
				v.ImageAvailable = false
			}
		}, "failed"},
		{"OOM plus unavailable image still fails", func(i int, v *RuntimeSnapshot) {
			if i == 5 {
				v.OOMKilled = true
				v.ImageAvailable = false
			}
		}, "failed"},
		{"restarts still fail", func(i int, v *RuntimeSnapshot) {
			if i >= 5 {
				v.RestartCount = 1
			}
		}, "failed"},
		{"unhealthy still fails", func(i int, v *RuntimeSnapshot) {
			v.Health = "healthy"
			if i == 5 {
				v.Health = "unhealthy"
			}
		}, "failed"},
		{"coverage gaps cannot pass", func(i int, v *RuntimeSnapshot) {
			if i > 2 && i < 8 {
				v.Outcome = "unavailable"
			}
		}, "insufficient"},
		{"same name replacement cannot pass", func(i int, v *RuntimeSnapshot) {
			if i == 5 {
				v.ContainerID = strings.Repeat("f", 64)
			}
		}, "insufficient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := localRuleTarget()
			for i := 0; i <= 10; i++ {
				v := ruleSnapshot()
				v.RepoDigests = nil
				tc.change(i, &v)
				sampleRule(&p, v, ruleStart.Add(time.Duration(i)*30*time.Second))
			}
			requireRuleResult(t, &p, tc.want)
		})
	}
}

func recordLocalTarget(t *testing.T, f serviceIntegrationFixture, expected deployattempt.ExpectedImage, completeActivation bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	a, err := f.ledger.Begin(ctx, deployattempt.BeginInput{RunID: "r", Initiator: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := f.ledger.GetTarget(ctx, f.targetID)
	if err != nil {
		t.Fatal(err)
	}
	in := previous.TargetInput
	in.ExpectedImage = expected
	tr, err := f.ledger.StartTarget(ctx, a.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	actual := deployattempt.ActualImage{ContainerID: strings.Repeat("2", 64)}
	if completeActivation {
		actual.ConfigID = expected.Digest
		actual.Platform = expected.Platform
	}
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		if _, err = f.ledger.AppendReceipt(ctx, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: actual}); err != nil {
			t.Fatal(err)
		}
	}
	return a.ID, tr.ID
}

func TestFromNowRequiresCapturedLocalIdentityAndActivationReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, availability string
		legacy, incomplete bool
	}{
		{"captured and activated", "available", false, false},
		{"historical config only", "unsupported", true, false},
		{"incomplete activation identity", "identity_unavailable", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceIntegrationFixture(t)
			expected := localExpected("sha256:" + strings.Repeat("b", 64))
			if tc.legacy {
				expected.IdentitySource = ""
			}
			attemptID, _ := recordLocalTarget(t, f, expected, !tc.incomplete)
			p, err := f.service.Prepare(context.Background(), "p", PrepareInput{Mode: "from_now", AttemptID: attemptID, DurationSeconds: 300})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Targets) != 1 || p.Targets[0].Availability != tc.availability || p.Targets[0].ExpectedImage != expected || f.observer.reads.Load() != 0 {
				t.Fatalf("unexpected plan or remote read: %+v", p)
			}
		})
	}
}

func TestServiceLocalImageObservesFiveMinuteWindowWithoutRepositoryDigest(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	f.service.observer = localOnlyObserver{f.observer}
	f.attemptID, f.targetID = recordLocalTarget(t, f, localExpected("sha256:"+strings.Repeat("b", 64)), true)
	r := f.start(t, "local-image-full-window")
	f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples == 1 })
	for i := 2; i <= 11; i++ {
		f.clock.add(30 * time.Second)
		want := i
		f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples >= want })
	}
	r = f.wait(t, r.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
	if r.EngineVersion != "releaseguard-v2" || r.VerificationResult == nil || *r.VerificationResult != "passed" || !r.Targets[0].Coverage.Complete {
		t.Fatal(r)
	}
}

func localAttachedFixture(t *testing.T) (serviceIntegrationFixture, context.Context, *Report, *deployattempt.Target) {
	t.Helper()
	f := newServiceIntegrationFixture(t)
	ctx := context.Background()
	art, err := f.runs.AddArtifact(ctx, run.Artifact{RunID: "r", Type: run.ArtifactArchive, Name: "app", Reference: "app.tar", Metadata: map[string]any{"stored": true, "format": "file", "filename": "app.tar"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]string{"localDocker": "true", "containerName": "app", "localDockerPrepareCommand": "tar xf app.tar"}
	p, err := f.service.Prepare(ctx, "p", PrepareInput{Mode: "attached", RunID: "r", ArtifactID: art.ID, ServerIDs: []string{"s"}, DeployConfig: cfg, DurationSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	if !pendingLocalExpected(p.Targets[0].ExpectedImage) || f.observer.reads.Load() != 0 {
		t.Fatal("plan guessed image before build", p)
	}
	changedCfg := map[string]string{"localDocker": "true", "containerName": "app", "localDockerPrepareCommand": "tar xf different.tar"}
	if _, _, err = f.service.Attached(ctx, "p", StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "changed-script"}, "r", art.ID, []string{"s"}, changedCfg); err != ErrConflict {
		t.Fatal("changed preparation script was not bound to consent", err)
	}
	deployCtx, r, err := f.service.Attached(ctx, "p", StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "local-attached"}, "r", art.ID, []string{"s"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.ledger.Begin(ctx, deployattempt.BeginInput{RunID: "r", Initiator: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	f.service.DeploymentStarted(deployCtx, a.ID)
	previous, err := f.ledger.GetTarget(ctx, f.targetID)
	if err != nil {
		t.Fatal(err)
	}
	in := previous.TargetInput
	in.ArtifactID = art.ID
	in.ExpectedImage = localExpected("sha256:" + strings.Repeat("b", 64))
	tr, err := f.ledger.StartTarget(ctx, a.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return f, deployCtx, r, tr
}

func TestAttachedLocalImageFreezesExpectationBeforeActivation(t *testing.T) {
	f, ctx, r, tr := localAttachedFixture(t)
	f.service.observer = localOnlyObserver{f.observer}
	f.service.BeforeImageActivation(ctx, tr.AttemptID, tr.ID)
	bound, err := f.service.Snapshot(context.Background(), "p", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Targets[0].ExpectedImage != tr.ExpectedImage || bound.Targets[0].ContainerID != "" || !pendingLocalExpected(bound.Plan.Targets[0].ExpectedImage) {
		t.Fatal("expected was guessed from activation or plan was overwritten", bound)
	}
	page, err := f.service.Evidence(context.Background(), "p", r.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Items {
		if e.Kind == "expected_image" && e.Reason == "local_image_fixed_before_activation" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing preactivation expected identity evidence")
	}
	actual := deployattempt.ActualImage{ContainerID: strings.Repeat("2", 64), ConfigID: tr.ExpectedImage.Digest, Platform: tr.ExpectedImage.Platform}
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		if _, err = f.ledger.AppendReceipt(context.Background(), tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: actual}); err != nil {
			t.Fatal(err)
		}
	}
	f.service.AfterImageActivation(ctx, tr.AttemptID, tr.ID, actual.ContainerID)
	f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples == 1 })
}

func TestAttachedLocalImageCannotInferExpectedFromLateOrMissingCapture(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing before callback", true: "capture after activation"}[late], func(t *testing.T) {
			f, ctx, r, tr := localAttachedFixture(t)
			actual := deployattempt.ActualImage{ContainerID: strings.Repeat("2", 64), ConfigID: tr.ExpectedImage.Digest, Platform: tr.ExpectedImage.Platform}
			for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated} {
				if _, err := f.ledger.AppendReceipt(context.Background(), tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: actual}); err != nil {
					t.Fatal(err)
				}
			}
			if late {
				f.service.BeforeImageActivation(ctx, tr.AttemptID, tr.ID)
			}
			f.service.AfterImageActivation(ctx, tr.AttemptID, tr.ID, actual.ContainerID)
			f.service.FinishAttached(ctx, r.ID)
			final := f.wait(t, r.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
			if final.VerificationResult != nil && *final.VerificationResult == "passed" || !pendingLocalExpected(final.Targets[0].ExpectedImage) || final.Targets[0].ContainerID != "" {
				t.Fatal("retroactively inferred local expectation", final)
			}
		})
	}
}

func TestAttachedLocalImageCannotBindBeforeDurableActivationReceipt(t *testing.T) {
	f, ctx, r, tr := localAttachedFixture(t)
	f.service.BeforeImageActivation(ctx, tr.AttemptID, tr.ID)
	f.service.AfterImageActivation(ctx, tr.AttemptID, tr.ID, strings.Repeat("2", 64))
	f.service.FinishAttached(ctx, r.ID)
	final := f.wait(t, r.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
	if final.Targets[0].ContainerID != "" || final.VerificationResult != nil && *final.VerificationResult == "passed" {
		t.Fatal("callback replaced durable activation evidence", final)
	}
}

func TestServiceUnexpectedLocalConfigFailsAfterReadinessDeadline(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	f.service.observer = localOnlyObserver{f.observer}
	f.attemptID, f.targetID = recordLocalTarget(t, f, localExpected("sha256:"+strings.Repeat("a", 64)), true)
	r := f.start(t, "local-wrong-image")
	f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Actual != nil })
	f.clock.add(120 * time.Second)
	r = f.wait(t, r.ID, func(r *Report) bool { return r.VerificationResult != nil && *r.VerificationResult == "failed" })
	found := false
	for _, condition := range r.Targets[0].Conditions {
		if condition.Name == "version" && condition.Result == "failed" && condition.Reason == "confirmed_unexpected_local_image" {
			found = true
		}
	}
	if !found || r.Targets[0].ReadyAt != nil {
		t.Fatal("wrong local image established readiness", r)
	}
}
