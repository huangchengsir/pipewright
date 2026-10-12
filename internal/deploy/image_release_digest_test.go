package deploy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
)

func TestPublishedImageDeploymentDigestReference(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, ref, repo, digest string
		pushed, fail            bool
	}{
		{"stable", "reg.invalid:5000/ns/app:stable", "reg.invalid:5000/ns/app", digest, true, false},
		{"latest", "localhost/app:latest", "localhost/app", digest, true, false},
		{"ipv6", "[::1]:5000/ns/app:stable", "[::1]:5000/ns/app", digest, true, false},
		{"ambiguous_host", "registry/app:stable", "registry/app", digest, true, true},
		{"already_pinned", "reg.invalid/app@" + digest, "reg.invalid/app", digest, true, false},
		{"wrong_repo", "reg.invalid/app:stable", "reg.invalid/other", digest, true, true},
		{"missing_repo", "reg.invalid/app:stable", "", digest, true, true},
		{"missing_digest", "reg.invalid/app:stable", "reg.invalid/app", "", true, true},
		{"short_digest", "reg.invalid/app:stable", "reg.invalid/app", "sha256:abc", true, true},
		{"wrong_pinned_digest", "reg.invalid/app@sha256:" + strings.Repeat("b", 64), "reg.invalid/app", digest, true, true},
		{"bad_tag", "reg.invalid/app:--option", "reg.invalid/app", digest, true, true},
		{"injection", "reg.invalid/app:stable", "--option", digest, true, true},
		{"repository_traversal", "reg.invalid/../app:stable", "reg.invalid/../app", digest, true, true},
		{"legacy", "registry/app:old", "", "sha256:config-id", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]any{"digest": tc.digest, "pushed": tc.pushed}
			if tc.repo != "" {
				meta["digestRepository"] = tc.repo
			}
			a := run.Artifact{Reference: tc.ref, Metadata: meta}
			got, err := imageDeploymentReference(a)
			want := tc.ref
			if tc.pushed {
				want = tc.repo + "@" + tc.digest
			}
			if (err != nil) != tc.fail || !tc.fail && got != want || a.Reference != tc.ref {
				t.Fatal("invalid digest pin or display tag changed", got, err, a.Reference)
			}
		})
	}
}

func TestPublishedImageDeploymentPullAndRunPinned(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, strategy := range []string{"rolling", "canary", "blue_green"} {
		t.Run(strategy, func(t *testing.T) {
			tgt := &stubTarget{}
			srv := seedServer(t, tgt, "web-1")
			s := &service{targets: tgt}
			a := run.Artifact{Name: "app", Reference: "reg.invalid/ns/app:stable", Metadata: map[string]any{"pushed": true, "digestRepository": "reg.invalid/ns/app", "digest": digest}}
			st, message, ok := s.stageImageOne(context.Background(), srv, a, nil)
			if !ok {
				t.Fatal(message)
			}
			res := s.activateImageOne(context.Background(), srv, a, nil, st, time.Now(), strategy)
			if res.Status != run.TargetSuccess {
				t.Fatal(res)
			}
			pin := "reg.invalid/ns/app@" + digest
			cmd := runCmd(tgt.calls)
			if !hasCmd(tgt.calls, "docker", "pull", pin) || len(cmd) == 0 || cmd[len(cmd)-1] != pin || a.Reference != "reg.invalid/ns/app:stable" {
				t.Fatal("mutable tag deployed", tgt.calls, a)
			}
		})
	}
}

func TestPublishedImageInvalidDigestDoesNotTouchTarget(t *testing.T) {
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	s := &service{targets: tgt}
	a := run.Artifact{Reference: "reg.invalid/app:stable", Metadata: map[string]any{"pushed": true, "digestRepository": "reg.invalid/other", "digest": "sha256:" + strings.Repeat("a", 64)}}
	if _, _, ok := s.stageImageOne(context.Background(), srv, a, nil); ok || len(tgt.calls) != 0 {
		t.Fatal("bad digest reached remote commands", tgt.calls)
	}
}
