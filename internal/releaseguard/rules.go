package releaseguard

import (
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
)

var repositoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,511}$`)

func normalizedRepository(repo string) string {
	if !repositoryName.MatchString(repo) || strings.Contains(repo, "://") || strings.Contains(repo, "//") || strings.HasSuffix(repo, "/") {
		return ""
	}
	parts := strings.Split(repo, "/")
	if len(parts) == 1 || (!strings.ContainsAny(parts[0], ".:") && parts[0] != "localhost") {
		repo = "docker.io/" + repo
	}
	if strings.HasPrefix(repo, "index.docker.io/") {
		repo = "docker.io/" + strings.TrimPrefix(repo, "index.docker.io/")
	}
	if strings.HasPrefix(repo, "docker.io/") && strings.Count(repo, "/") == 1 {
		repo = "docker.io/library/" + strings.TrimPrefix(repo, "docker.io/")
	}
	return repo
}

func repositoryDigest(ref string) (string, string, bool) {
	if len(ref) > 1024 {
		return "", "", false
	}
	repo, digest, ok := strings.Cut(ref, "@")
	repo = normalizedRepository(repo)
	return repo, digest, ok && repo != "" && configDigest.MatchString(digest)
}

// Config IDs, manifest digests and index digests are different identities.
// Exact RepoDigests are usable even when their media type is unavailable;
// different digests require independently verified, same-type evidence.
func VersionMatch(t PlanTarget, v RuntimeSnapshot) string {
	e := t.ExpectedImage
	if v.Outcome != "ok" || !v.ImageAvailable || v.Truncated || !configDigest.MatchString(e.Digest) {
		return "insufficient"
	}
	switch e.DigestKind {
	case deployattempt.DigestManifest, deployattempt.DigestIndex, deployattempt.DigestRepositoryUnknown:
	default:
		return "insufficient"
	}
	repo := normalizedRepository(e.Repository)
	if repo == "" {
		return "insufficient"
	}
	if (e.Platform.OS != "" && e.Platform.OS != v.Platform.OS) || (e.Platform.Architecture != "" && e.Platform.Architecture != v.Platform.Architecture) || (e.Platform.Variant != "" && e.Platform.Variant != v.Platform.Variant) {
		return "insufficient"
	}
	for _, ref := range v.RepoDigests {
		r, d, ok := repositoryDigest(ref)
		if ok && r == repo && d == e.Digest {
			return "passed"
		}
	}
	r, d, ok := repositoryDigest(v.DeclaredImage)
	if !ok || r != repo || d == e.Digest || v.RepositoryDigestKind != e.DigestKind || (e.DigestKind != deployattempt.DigestManifest && e.DigestKind != deployattempt.DigestIndex) {
		return "insufficient"
	}
	vr, vd, verified := repositoryDigest(v.VerifiedRepositoryDigest)
	if !verified || vr != r || vd != d || (v.RepositoryDigestSource != "descriptor_verified" && v.RepositoryDigestSource != "mapping_verified") {
		return "insufficient"
	}
	for _, ref := range v.RepoDigests {
		ar, ad, valid := repositoryDigest(ref)
		if valid && ar == r && ad == d {
			return "failed"
		}
	}
	return "insufficient"
}

func condition(t *TargetObservation, name, result, reason, id string) {
	for i := range t.Conditions {
		c := &t.Conditions[i]
		if c.Name != name {
			continue
		}
		if c.Result == "failed" {
			return
		}
		c.Result = result
		c.Reason = reason
		if id != "" && len(c.EvidenceIDs) < 32 {
			for _, prior := range c.EvidenceIDs {
				if prior == id {
					return
				}
			}
			c.EvidenceIDs = append(c.EvidenceIDs, id)
		}
		return
	}
	ids := []string{}
	if id != "" {
		ids = append(ids, id)
	}
	t.Conditions = append(t.Conditions, Condition{Name: name, Result: result, Reason: reason, EvidenceIDs: ids})
}

func applySample(t *TargetObservation, v RuntimeSnapshot, evidenceID string, at time.Time, duration, interval time.Duration) {
	if t.Terminated || t.Completed {
		return
	}
	if duration <= 0 || interval <= 0 {
		t.Limitations = appendUnique(t.Limitations, "sampling_parameters_invalid")
		return
	}
	// Out-of-window and out-of-order replies cannot change the current result.
	if !t.Deadline.IsZero() && at.After(t.Deadline.Add(5*time.Second)) {
		t.Limitations = appendUnique(t.Limitations, "sample_outside_window")
		return
	}
	if t.Actual != nil && at.Before(t.Actual.CollectedAt) {
		t.Limitations = appendUnique(t.Limitations, "sample_out_of_order")
		return
	}
	if v.Outcome == "target_changed" {
		t.Terminated = true
		t.Limitations = appendUnique(t.Limitations, "target_changed")
		return
	}
	if v.Outcome == "not_found" {
		condition(t, "liveness", "failed", "container_not_found", evidenceID)
		t.Terminated = true
		return
	}
	if v.Outcome != "ok" {
		t.Limitations = appendUnique(t.Limitations, safeReason(v.Reason))
		return
	}
	if !deployattempt.ValidContainerID(v.ContainerID) || v.ContainerID != t.ContainerID {
		t.Terminated = true
		t.Limitations = appendUnique(t.Limitations, "container_identity_changed")
		return
	}
	if v.CreatedAt == "" || v.StartedAt == "" || !configDigest.MatchString(v.ConfigID) || v.RestartCount < 0 {
		t.Limitations = appendUnique(t.Limitations, "container_identity_incomplete")
		return
	}
	previous := t.Actual
	if previous != nil && previous.Outcome == "ok" {
		if previous.CreatedAt != v.CreatedAt || previous.ConfigID != v.ConfigID {
			t.Terminated = true
			t.Limitations = appendUnique(t.Limitations, "container_generation_changed")
			return
		}
		if previous.StartedAt != v.StartedAt {
			condition(t, "restarts", "failed", "container_restarted", evidenceID)
			t.Terminated = true
			return
		}
	}
	v.CollectedAt = at
	t.Actual = &v // Retain last trusted identity across collection errors.
	if !v.Running || v.OOMKilled {
		condition(t, "liveness", "failed", "container_exited_or_oom", evidenceID)
	}
	if v.TargetChanged {
		t.Terminated = true
		t.Limitations = appendUnique(t.Limitations, "target_changed")
		return
	}
	if t.InitialRestartCount == nil {
		n := v.RestartCount
		t.InitialRestartCount = &n
	}
	if t.HealthRequired == nil {
		required := v.Health != ""
		t.HealthRequired = &required
	}
	matched := VersionMatch(t.PlanTarget, v)
	if matched == "passed" {
		condition(t, "version", "passed", "repository_digest_matched", evidenceID)
	}
	if matched == "failed" && (t.ReadyAt != nil || !at.Before(t.Deadline)) {
		condition(t, "version", "failed", "confirmed_previous_version", evidenceID)
	}
	if t.ReadyAt == nil && v.Running && matched == "passed" && !at.After(t.Deadline) {
		ready := at
		t.ReadyAt = &ready
		t.Deadline = ready.Add(duration)
		t.Coverage.ExpectedSamples = int(math.Ceil(float64(duration)/float64(interval))) + 1
	}
	if t.ReadyAt == nil {
		return
	}
	if matched != "passed" {
		condition(t, "version", "insufficient", "version_identity_unavailable", evidenceID)
	}
	if v.RestartCount > *t.InitialRestartCount {
		condition(t, "restarts", "failed", "restart_count_increased", evidenceID)
	} else {
		condition(t, "restarts", "passed", "restart_count_unchanged", evidenceID)
	}
	if v.Running && !v.OOMKilled {
		condition(t, "liveness", "passed", "container_running", evidenceID)
	}
	healthKnown := true
	if *t.HealthRequired {
		if at.Before(t.ReadyAt.Add(time.Minute)) {
			condition(t, "health", "pending", "health_startup_grace", evidenceID)
		} else {
			switch v.Health {
			case "healthy":
				condition(t, "health", "passed", "docker_healthy", evidenceID)
			case "unhealthy":
				condition(t, "health", "failed", "docker_unhealthy", evidenceID)
			default:
				condition(t, "health", "insufficient", "docker_health_unknown", evidenceID)
				healthKnown = false
			}
		}
	} else {
		condition(t, "health", "not_applicable", "docker_health_not_configured", "")
	}
	if matched != "passed" || !v.Running || v.OOMKilled || v.Truncated || !healthKnown {
		return
	}
	if at.Before(t.ReadyAt.Add(-5*time.Second)) || at.After(t.Deadline.Add(5*time.Second)) {
		return
	}
	c := &t.Coverage
	if c.LastSample != nil && at.Before(*c.LastSample) {
		return
	}
	slot := int(math.Round(float64(at.Sub(*t.ReadyAt)) / float64(interval)))
	if slot < 0 || slot >= c.ExpectedSamples {
		return
	}
	seen := false
	for _, prior := range c.SampleSlots {
		if prior == slot {
			seen = true
			break
		}
	}
	if !seen {
		c.SampleSlots = append(c.SampleSlots, slot)
		c.SuccessfulSamples = len(c.SampleSlots)
	}
	if c.LastSample != nil {
		gap := int(math.Ceil(at.Sub(*c.LastSample).Seconds()))
		c.MaxGapSeconds = max(c.MaxGapSeconds, gap)
	}
	if c.FirstSample == nil {
		ts := at
		c.FirstSample = &ts
	}
	ts := at
	c.LastSample = &ts
}

func settle(t *TargetObservation, at time.Time, final bool) {
	failed, uncertain, missing := t.VerificationResult != nil && *t.VerificationResult == "failed", false, false
	required := map[string]bool{"version": false, "liveness": false, "restarts": false, "health": false}
	for _, c := range t.Conditions {
		switch c.Result {
		case "failed":
			failed = true
		case "uncertain":
			uncertain = true
		case "insufficient", "pending":
			missing = true
		}
		if _, ok := required[c.Name]; ok {
			required[c.Name] = c.Result == "passed" || (c.Name == "health" && c.Result == "not_applicable" && t.HealthRequired != nil && !*t.HealthRequired)
		}
	}
	for _, ok := range required {
		if !ok {
			missing = true
		}
	}
	c := &t.Coverage
	validSlots := len(c.SampleSlots) == c.SuccessfulSamples
	seen := map[int]bool{}
	for _, slot := range c.SampleSlots {
		if slot < 0 || slot >= c.ExpectedSamples || seen[slot] {
			validSlots = false
		}
		seen[slot] = true
	}
	c.Complete = validSlots && c.ExpectedSamples > 1 && seen[0] && seen[c.ExpectedSamples-1] && t.ReadyAt != nil && c.FirstSample != nil && c.LastSample != nil && c.SuccessfulSamples <= c.ExpectedSamples &&
		c.SuccessfulSamples*10 >= c.ExpectedSamples*9 && c.MaxGapSeconds >= 0 && c.MaxGapSeconds <= 60 &&
		!c.FirstSample.Before(t.ReadyAt.Add(-5*time.Second)) && !c.FirstSample.After(t.ReadyAt.Add(5*time.Second)) &&
		!c.LastSample.Before(t.Deadline.Add(-5*time.Second)) && !c.LastSample.After(t.Deadline.Add(5*time.Second)) && !c.LastSample.Before(*c.FirstSample) &&
		!at.Before(t.Deadline.Add(-5*time.Second))
	if failed {
		t.VerificationResult = strptr("failed")
		return
	}
	if !final && !t.Terminated {
		t.VerificationResult = nil
		return
	}
	if uncertain {
		t.VerificationResult = strptr("uncertain")
	} else if missing || !c.Complete || t.Terminated {
		t.VerificationResult = strptr("insufficient")
	} else {
		t.VerificationResult = strptr("passed")
	}
}

func aggregate(r *Report, final bool) {
	at := time.Now()
	if r.FinishedAt != nil {
		at = *r.FinishedAt
	}
	failed, uncertain, missing, pending := r.VerificationResult != nil && *r.VerificationResult == "failed", false, false, false
	for i := range r.Targets {
		settle(&r.Targets[i], at, final)
		v := r.Targets[i].VerificationResult
		if v == nil {
			pending = true
			continue
		}
		switch *v {
		case "failed":
			failed = true
		case "uncertain":
			uncertain = true
		case "insufficient":
			missing = true
		case "passed":
		default:
			missing = true
		}
	}
	if failed {
		r.VerificationResult = strptr("failed")
	} else if !final && pending {
		r.VerificationResult = nil
	} else if uncertain {
		r.VerificationResult = strptr("uncertain")
	} else if missing || pending || len(r.Targets) == 0 {
		r.VerificationResult = strptr("insufficient")
	} else {
		r.VerificationResult = strptr("passed")
	}
}

func appendUnique(in []string, s string) []string {
	if s == "" {
		return in
	}
	for _, v := range in {
		if v == s {
			return in
		}
	}
	return append(in, s)
}
func safeReason(s string) string {
	if len(s) > 128 || strings.ContainsAny(s, "\r\n\x00") {
		return "unavailable"
	}
	return s
}
