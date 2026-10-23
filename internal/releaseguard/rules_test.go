package releaseguard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
)

var ruleStart = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
var ruleContainer = strings.Repeat("a", 64)
var ruleDigest = "sha256:" + strings.Repeat("b", 64)
var oldDigest = "sha256:" + strings.Repeat("c", 64)

func ruleTarget() TargetObservation {
	return TargetObservation{PlanTarget: PlanTarget{ID: "target", ContainerID: ruleContainer, ExpectedImage: deployattempt.ExpectedImage{Repository: "registry.invalid/app", Digest: ruleDigest, DigestKind: deployattempt.DigestManifest}}, Deadline: ruleStart.Add(120 * time.Second)}
}

func ruleSnapshot() RuntimeSnapshot {
	return RuntimeSnapshot{Outcome: "ok", ContainerID: ruleContainer, CreatedAt: ruleStart.Add(-time.Minute).Format(time.RFC3339Nano), StartedAt: ruleStart.Add(-time.Second).Format(time.RFC3339Nano), FinishedAt: time.Time{}.Format(time.RFC3339Nano), ConfigID: "sha256:" + strings.Repeat("d", 64), DeclaredImage: "registry.invalid/app@" + ruleDigest, RepoDigests: []string{"registry.invalid/app@" + ruleDigest}, Running: true, ImageAvailable: true, Platform: deployattempt.Platform{OS: "linux", Architecture: "amd64"}}
}

func sampleRule(t *TargetObservation, v RuntimeSnapshot, at time.Time) {
	applySample(t, v, fmt.Sprint(at.UnixNano()), at, 5*time.Minute, 30*time.Second)
}

func requireRuleResult(t *testing.T, target *TargetObservation, want string) {
	t.Helper()
	settle(target, ruleStart.Add(5*time.Minute), true)
	if target.VerificationResult == nil || *target.VerificationResult != want {
		t.Fatalf("result=%v want=%s coverage=%+v conditions=%+v", target.VerificationResult, want, target.Coverage, target.Conditions)
	}
}

func TestVersionIdentityMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PlanTarget, *RuntimeSnapshot)
		want   string
	}{
		{"exact manifest", func(*PlanTarget, *RuntimeSnapshot) {}, "passed"},
		{"unknown repository digest exact", func(p *PlanTarget, v *RuntimeSnapshot) {
			p.ExpectedImage.DigestKind = deployattempt.DigestRepositoryUnknown
		}, "passed"},
		{"config ID is not manifest", func(p *PlanTarget, v *RuntimeSnapshot) { v.RepoDigests = nil; v.ConfigID = p.ExpectedImage.Digest }, "insufficient"},
		{"expected config ID cannot pass", func(p *PlanTarget, v *RuntimeSnapshot) { p.ExpectedImage.DigestKind = deployattempt.DigestConfig }, "insufficient"},
		{"index child mapping unknown", func(p *PlanTarget, v *RuntimeSnapshot) {
			p.ExpectedImage.DigestKind = deployattempt.DigestIndex
			v.RepoDigests = []string{"registry.invalid/app@" + oldDigest}
			v.DeclaredImage = v.RepoDigests[0]
		}, "insufficient"},
		{"different untyped declared digest", func(p *PlanTarget, v *RuntimeSnapshot) {
			v.RepoDigests = []string{"registry.invalid/app@" + oldDigest}
			v.DeclaredImage = v.RepoDigests[0]
		}, "insufficient"},
		{"same type verified old manifest", func(p *PlanTarget, v *RuntimeSnapshot) {
			v.RepoDigests = []string{"registry.invalid/app@" + oldDigest}
			v.DeclaredImage = v.RepoDigests[0]
			v.VerifiedRepositoryDigest = v.DeclaredImage
			v.RepositoryDigestKind = deployattempt.DigestManifest
			v.RepositoryDigestSource = "descriptor_verified"
		}, "failed"},
		{"unproven typed assertion", func(p *PlanTarget, v *RuntimeSnapshot) {
			v.RepoDigests = []string{"registry.invalid/app@" + oldDigest}
			v.DeclaredImage = v.RepoDigests[0]
			v.VerifiedRepositoryDigest = v.DeclaredImage
			v.RepositoryDigestKind = deployattempt.DigestManifest
		}, "insufficient"},
		{"different repository", func(p *PlanTarget, v *RuntimeSnapshot) {
			v.RepoDigests = []string{"registry.invalid/other@" + ruleDigest}
		}, "insufficient"},
		{"invalid digest", func(p *PlanTarget, v *RuntimeSnapshot) { p.ExpectedImage.Digest = "sha256:short" }, "insufficient"},
		{"platform missing or different", func(p *PlanTarget, v *RuntimeSnapshot) { p.ExpectedImage.Platform.Architecture = "arm64" }, "insufficient"},
		{"truncated identity", func(p *PlanTarget, v *RuntimeSnapshot) { v.Truncated = true }, "insufficient"},
		{"docker hub normalization", func(p *PlanTarget, v *RuntimeSnapshot) {
			p.ExpectedImage.Repository = "alpine"
			v.RepoDigests = []string{"docker.io/library/alpine@" + ruleDigest}
		}, "passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, v := ruleTarget().PlanTarget, ruleSnapshot()
			tc.change(&p, &v)
			if got := VersionMatch(p, v); got != tc.want {
				t.Fatalf("VersionMatch=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestStartupAndMissingObjectMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*RuntimeSnapshot)
		want   string
	}{
		{"exit before ready", func(v *RuntimeSnapshot) { v.Running = false }, "failed"},
		{"OOM before ready", func(v *RuntimeSnapshot) { v.OOMKilled = true }, "failed"},
		{"exit despite missing image metadata", func(v *RuntimeSnapshot) { v.Running = false; v.ImageAvailable = false }, "failed"},
		{"Docker confirmed missing", func(v *RuntimeSnapshot) { v.Outcome = "not_found" }, "failed"},
		{"SSH error", func(v *RuntimeSnapshot) { v.Outcome = "unavailable"; v.Reason = "inspection_unavailable" }, "insufficient"},
		{"known supersession", func(v *RuntimeSnapshot) { v.Outcome = "target_changed" }, "insufficient"},
		{"different container", func(v *RuntimeSnapshot) { v.ContainerID = strings.Repeat("e", 64) }, "insufficient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, v := ruleTarget(), ruleSnapshot()
			tc.change(&v)
			sampleRule(&target, v, ruleStart)
			requireRuleResult(t, &target, tc.want)
		})
	}
}

func TestConfirmedOldVersionWaitsForReadinessDeadline(t *testing.T) {
	target, v := ruleTarget(), ruleSnapshot()
	v.RepoDigests = []string{"registry.invalid/app@" + oldDigest}
	v.DeclaredImage = v.RepoDigests[0]
	v.VerifiedRepositoryDigest = v.DeclaredImage
	v.RepositoryDigestKind = deployattempt.DigestManifest
	v.RepositoryDigestSource = "mapping_verified"
	sampleRule(&target, v, ruleStart)
	settle(&target, ruleStart, false)
	if target.VerificationResult != nil {
		t.Fatal("old version during startup grace must wait")
	}
	sampleRule(&target, v, ruleStart.Add(120*time.Second))
	requireRuleResult(t, &target, "failed")
}

func TestCoverageMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		skip   map[int]bool
		change func(int, *RuntimeSnapshot)
		want   string
	}{
		{"complete", nil, nil, "passed"},
		{"one missing slot meets ninety percent", map[int]bool{5: true}, nil, "passed"},
		{"two missing slots below ninety percent", map[int]bool{3: true, 7: true}, nil, "insufficient"},
		{"missing final sample", map[int]bool{10: true}, nil, "insufficient"},
		{"gap exceeds sixty seconds", map[int]bool{3: true, 4: true}, nil, "insufficient"},
		{"image metadata gaps do not count", nil, func(i int, v *RuntimeSnapshot) {
			if i == 3 || i == 7 {
				v.ImageAvailable = false
			}
		}, "insufficient"},
		{"truncated identity does not count", nil, func(i int, v *RuntimeSnapshot) {
			if i == 3 || i == 7 {
				v.Truncated = true
			}
		}, "insufficient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := ruleTarget()
			for i := 0; i <= 10; i++ {
				if tc.skip[i] {
					continue
				}
				v := ruleSnapshot()
				if tc.change != nil {
					tc.change(i, &v)
				}
				sampleRule(&target, v, ruleStart.Add(time.Duration(i)*30*time.Second))
			}
			requireRuleResult(t, &target, tc.want)
		})
	}
}

func TestDuplicateSamplesCannotFillMissingSlots(t *testing.T) {
	target := ruleTarget()
	for i := 0; i <= 10; i += 2 {
		at := ruleStart.Add(time.Duration(i) * 30 * time.Second)
		for j := 0; j < 10; j++ {
			sampleRule(&target, ruleSnapshot(), at.Add(time.Duration(j)*time.Millisecond))
		}
	}
	requireRuleResult(t, &target, "insufficient")
	if target.Coverage.SuccessfulSamples != 6 {
		t.Fatalf("duplicates counted as coverage: %+v", target.Coverage)
	}
}

func TestMaximumGapStillFailsWhenNinetyPercentIsSatisfied(t *testing.T) {
	target := ruleTarget()
	for i := 0; i <= 20; i++ {
		if i == 4 || i == 5 {
			continue
		}
		at := ruleStart.Add(time.Duration(i) * 30 * time.Second)
		applySample(&target, ruleSnapshot(), fmt.Sprint(i), at, 10*time.Minute, 30*time.Second)
	}
	settle(&target, ruleStart.Add(10*time.Minute), true)
	if target.Coverage.SuccessfulSamples != 19 || target.Coverage.ExpectedSamples != 21 || target.Coverage.MaxGapSeconds != 90 || *target.VerificationResult != "insufficient" {
		t.Fatalf("gap rule bypassed by sufficient percentage: %+v", target)
	}
}

func TestExplicitConflictingConditionIsSeparateFromMissingAndFailure(t *testing.T) {
	target := ruleTarget()
	condition(&target, "version", "uncertain", "trusted_sources_conflict", "conflict-evidence")
	requireRuleResult(t, &target, "uncertain")
	condition(&target, "liveness", "failed", "container_not_found", "exit-evidence")
	requireRuleResult(t, &target, "failed")
}

func TestCoverageRejectsOutOfWindowAndPrematureSettlement(t *testing.T) {
	target := ruleTarget()
	for i := 0; i <= 10; i++ {
		sampleRule(&target, ruleSnapshot(), ruleStart.Add(time.Duration(i)*30*time.Second))
	}
	settle(&target, ruleStart.Add(time.Minute), true)
	if *target.VerificationResult != "insufficient" {
		t.Fatal("future recorded samples cannot prove a completed window")
	}
	target.Coverage.FirstSample = strTime(ruleStart.Add(-6 * time.Second))
	requireRuleResult(t, &target, "insufficient")
	target.Coverage.FirstSample = strTime(ruleStart)
	target.Coverage.LastSample = strTime(ruleStart.Add(306 * time.Second))
	requireRuleResult(t, &target, "insufficient")
	sampleRule(&target, RuntimeSnapshot{Outcome: "not_found"}, ruleStart.Add(307*time.Second))
	if target.VerificationResult != nil && *target.VerificationResult == "failed" {
		t.Fatal("out-of-window missing object became a release failure")
	}
}

func strTime(at time.Time) *time.Time { return &at }

func TestHealthGraceAndFailureStickiness(t *testing.T) {
	target := ruleTarget()
	v := ruleSnapshot()
	v.Health = "unhealthy"
	sampleRule(&target, v, ruleStart)
	settle(&target, ruleStart, false)
	if target.VerificationResult != nil {
		t.Fatal("health grace incorrectly failed")
	}
	sampleRule(&target, v, ruleStart.Add(time.Minute))
	settle(&target, ruleStart.Add(time.Minute), false)
	if target.VerificationResult == nil || *target.VerificationResult != "failed" {
		t.Fatal("unhealthy after grace must fail")
	}
	v.Health = "healthy"
	sampleRule(&target, v, ruleStart.Add(90*time.Second))
	sampleRule(&target, RuntimeSnapshot{Outcome: "target_changed"}, ruleStart.Add(120*time.Second))
	requireRuleResult(t, &target, "failed")
}

func TestGenerationAndRestartSurviveCollectionGap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*RuntimeSnapshot)
		want   string
	}{
		{"Created changed", func(v *RuntimeSnapshot) { v.CreatedAt = ruleStart.Format(time.RFC3339Nano) }, "insufficient"},
		{"config changed", func(v *RuntimeSnapshot) { v.ConfigID = "sha256:" + strings.Repeat("f", 64) }, "insufficient"},
		{"Started changed despite same restart count", func(v *RuntimeSnapshot) { v.StartedAt = ruleStart.Format(time.RFC3339Nano) }, "failed"},
		{"restart count increased", func(v *RuntimeSnapshot) { v.RestartCount = 1 }, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := ruleTarget()
			sampleRule(&target, ruleSnapshot(), ruleStart)
			sampleRule(&target, RuntimeSnapshot{Outcome: "unavailable"}, ruleStart.Add(30*time.Second))
			v := ruleSnapshot()
			tc.change(&v)
			sampleRule(&target, v, ruleStart.Add(60*time.Second))
			requireRuleResult(t, &target, tc.want)
		})
	}
}

func TestAggregationKeepsFailureAndUnknownTargets(t *testing.T) {
	failed := ruleTarget()
	sampleRule(&failed, RuntimeSnapshot{Outcome: "not_found"}, ruleStart)
	r := Report{Targets: []TargetObservation{failed, ruleTarget()}}
	aggregate(&r, true)
	if r.VerificationResult == nil || *r.VerificationResult != "failed" {
		t.Fatal("another target's missing coverage erased failure")
	}
	r.Targets = nil
	aggregate(&r, true)
	if *r.VerificationResult != "failed" {
		t.Fatal("aggregate lost an already established failure")
	}
	empty := Report{}
	aggregate(&empty, true)
	if *empty.VerificationResult != "insufficient" {
		t.Fatal("empty target set passed")
	}
}
