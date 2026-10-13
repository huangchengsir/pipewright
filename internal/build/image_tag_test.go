package build

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImageDateTag(t *testing.T) {
	at := time.Date(2026, 10, 13, 9, 5, 7, 0, time.FixedZone("Shanghai", 8*3600))
	for _, tc := range []struct{ tag, want string }{
		{"${DATE}", "2026-10-13-01-05"},
		{"${DATE:yyyy-MM-dd-HH-mm}", "2026-10-13-01-05"},
		{"${DATE:yyyyMMddHHmmss}", "20261013010507"},
		{"release-${DATE}-${COMMIT_SHA}", "release-2026-10-13-01-05-abc1234"},
		{"${DATE:yyyy.MM.dd}_${DATE:HH_mm_ss}", "2026.10.13_01_05_07"},
		{"stable", "stable"}, {"", ""},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			got, err := expandImageTag(tc.tag, "abc1234", at)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	beforeMidnight := time.Date(2026, 1, 1, 1, 2, 0, 0, time.FixedZone("Shanghai", 8*3600))
	if got, err := expandImageTag("${DATE}", "", beforeMidnight); err != nil || got != "2025-12-31-17-02" {
		t.Fatalf("UTC date boundary: %q, %v", got, err)
	}
}

func TestImageDateTagRejectsInvalidInput(t *testing.T) {
	for _, tag := range []string{
		"${DATE:}", "${DATE:YYYY-MM-DD}", "${DATE:yyy}", "${DATE:%Y-%m-%d}",
		"${DATE:yyyy/MM/dd}", "${DATE:yyyy:MM}", "${DATE:--}", "${DATE:yyyy SECRET}",
		"${DATE:yyyy$(cmd)}", "${DATE", "${UNKNOWN}", strings.Repeat("${DATE}", 10),
	} {
		t.Run(tag, func(t *testing.T) {
			d := &pushTestDriver{}
			b, r := pushFixture(d, "")
			r.CreatedAt = time.Date(2026, 10, 13, 1, 5, 0, 0, time.UTC)
			rep := newPushReport()
			err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(map[string]any{"registry": "reg.invalid", "tag": tag}), rep)
			if !errors.Is(err, ErrBuildFailed) || len(d.operations("login"))+len(d.operations("tag"))+len(d.operations("push")) != 0 || len(rep.arts) != 0 {
				t.Fatalf("invalid date reached publishing: %v, %v", err, d.calls)
			}
			if strings.Contains(strings.Join(rep.logs, "\n"), "SECRET") {
				t.Fatal("invalid date format leaked input")
			}
		})
	}
	if _, err := expandImageTag("${DATE}", "abc1234", time.Time{}); err == nil {
		t.Fatal("missing run date must not use current time")
	}
	if got, err := expandImageTag("${COMMIT_SHA}", "abc1234", time.Time{}); err != nil || got != "abc1234" {
		t.Fatal("legacy commit tag requires no timestamp", got, err)
	}
}

func TestImageDateTagExecutionUsesPersistedRunTime(t *testing.T) {
	d := &pushTestDriver{}
	b, r := pushFixture(d, "")
	r.CreatedAt = time.Date(2026, 10, 13, 1, 5, 7, 0, time.UTC)
	cfg := map[string]any{"registry": "reg.invalid", "imageName": "team/app", "tag": "${DATE}-${COMMIT_SHA}"}
	for i := 0; i < 2; i++ {
		rep := newPushReport()
		if err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(cfg), rep); err != nil {
			t.Fatal(err, rep.logs)
		}
		if len(rep.arts) != 1 || rep.arts[0].Reference != "reg.invalid/team/app:2026-10-13-01-05-abc1234" {
			t.Fatal("date tag must persist expanded reference", rep.arts)
		}
	}
}
