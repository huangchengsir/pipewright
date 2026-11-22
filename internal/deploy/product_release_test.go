package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

func TestProductReleaseImageProjectionAllowsMissingVariant(t *testing.T) {
	projection := template.Must(template.New("inspect").Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) { data, err := json.Marshal(value); return string(data), err },
	}).Parse(productImageProjection))
	id := "sha256:" + strings.Repeat("a", 64)
	digest := "registry.example/app@sha256:" + strings.Repeat("b", 64)
	for _, variant := range []string{"", "v8"} {
		fields := map[string]any{"Id": id, "Os": "linux", "Architecture": "amd64", "RepoDigests": []string{digest}}
		if variant != "" {
			fields["Variant"] = variant
		}
		var output strings.Builder
		if err := projection.Execute(&output, fields); err != nil {
			t.Fatal(err)
		}
		var image productImageIdentity
		if !decodeLocalProjection(output.String(), &image) || image.Variant != variant || image.ID != id ||
			len(image.RepoDigests) != 1 || image.RepoDigests[0] != digest {
			t.Fatalf("incorrect projected image identity: %+v", image)
		}
	}
}

func TestProductReleaseRequiresSameRunPushedRepositoryDigest(t *testing.T) {
	repo := "registry.example.test/apps/demo"
	digest := "sha256:" + strings.Repeat("a", 64)
	a := run.Artifact{RunID: "release-run", Type: run.ArtifactImage, Reference: repo + ":mutable", Metadata: map[string]any{"pushed": true, "digestRepository": repo, "digest": digest}}
	if !pinnedProductArtifact(a, "release-run") {
		t.Fatal("trusted actual push rejected")
	}
	if ref, err := imageDeploymentReference(a); err != nil || ref != repo+"@"+digest {
		t.Fatal("tag reached deployment", ref, err)
	}
	if pinnedProductArtifact(a, "another-run") {
		t.Fatal("other run artifact accepted")
	}
	a.Metadata["pushed"] = false
	if pinnedProductArtifact(a, "release-run") {
		t.Fatal("untested local tag accepted")
	}
	a.Metadata["pushed"] = true
	delete(a.Metadata, "digest")
	if pinnedProductArtifact(a, "release-run") {
		t.Fatal("mutable reference with no trusted digest accepted")
	}
}

type productFixtureLedger struct {
	deployattempt.Writer
	receipts []deployattempt.Receipt
}

func (f *productFixtureLedger) AppendReceipt(_ context.Context, _ string, r deployattempt.Receipt) (*deployattempt.Receipt, error) {
	f.receipts = append(f.receipts, r)
	return &r, nil
}

func TestProductReleaseChecksHealthAndRollsBackKnownContainerOnly(t *testing.T) {
	fake := &stubTarget{}
	srv := seedServer(t, fake, "fixture")
	newID := strings.Repeat("a", 64)
	oldID := strings.Repeat("b", 64)
	oldImage := "sha256:" + strings.Repeat("c", 64)
	expectedImage := "sha256:" + strings.Repeat("d", 64)
	registryRef := "registry.example.test/org/app@sha256:" + strings.Repeat("e", 64)
	ledger := &productFixtureLedger{}
	svc := &service{targets: localDockerTestTarget{stubTarget: fake}, attempts: ledger}
	fake.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
		switch {
		case len(cmd) > 1 && cmd[0] == "docker" && cmd[1] == "run":
			return &target.ExecResult{Stdout: newID}, nil
		case len(cmd) > 2 && cmd[0] == "docker" && cmd[1] == "container" && cmd[2] == "inspect":
			return &target.ExecResult{Stdout: `{"id":"` + newID + `","image":"` + oldImage + `","running":true}`}, nil
		case cmd[0] == "fixture-health":
			return &target.ExecResult{ExitCode: 1}, nil
		default:
			return &target.ExecResult{}, nil
		}
	}
	st := imageState{name: "named", ref: registryRef, prevImage: oldImage, productPreviousID: oldID,
		productImage: &productImageIdentity{ID: expectedImage},
		receipt:      &imageReceipt{target: &deployattempt.Target{ID: "target"}},
	}
	res := svc.activateImageOne(t.Context(), srv, run.Artifact{}, &HealthCheck{Type: HealthCheckCommand, Command: []string{"fixture-health"}, Retries: 1}, st, time.Now(), "")
	if res.Status != run.TargetRolledBack || !hasCmd(fake.calls, "fixture-health") ||
		!hasCmd(fake.calls, "docker", "rm", "-f", oldID) || !hasCmd(fake.calls, "docker", "rm", "-f", newID) ||
		!hasCmd(fake.calls, "docker", "run", "--pull=never", "-d", "--name", "named", oldImage) ||
		hasCmd(fake.calls, "docker", "rm", "-f", "named") {
		t.Fatalf("unsafe rollback or bypassed health probe: %#v %#v", res, fake.calls)
	}
	for _, receipt := range ledger.receipts {
		if receipt.Kind == deployattempt.Succeeded {
			t.Fatal("unverified deployment reported as successful")
		}
	}
}
