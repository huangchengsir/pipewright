package notify

import (
	"github.com/huangchengsir/pipewright/internal/i18n"
	"strings"
	"testing"
)

func TestRecoveryNoticeLabelsEveryLanguageAndCustomBody(t *testing.T) {
	v := TemplateVars{ExecutionKind: "recovery", LogicalRunID: "original-id", SourceRunID: "source-id", AttemptNumber: "3", RetryInitiator: "current-operator"}
	for _, lang := range i18n.Supported {
		t.Run(lang, func(t *testing.T) {
			p := withRecoveryNotice(Payload{Title: "Custom title", Body: "Custom body", Lang: lang}, v)
			for _, part := range []string{"Custom body", "original-id", "source-id", "current-operator", i18n.T(lang, "节点恢复尝试")} {
				if !strings.Contains(p.Body, part) {
					t.Fatalf("missing recovery label %q in %q", part, p.Body)
				}
			}
			if lang != "zh-CN" && strings.Contains(p.Body, "恢复发起人") {
				t.Fatal("source locale leaked")
			}
			if p.Title != "Custom title" || p.Fields["executionKind"] != "recovery" || p.Fields["attemptNumber"] != "3" {
				t.Fatal(p)
			}
		})
	}
	normal := Payload{Title: "unchanged", Body: "original"}
	got := withRecoveryNotice(normal, TemplateVars{})
	if got.Title != normal.Title || got.Body != normal.Body || got.Fields != nil {
		t.Fatal(got)
	}
	if got := RenderText("{{executionKind}} {{sourceRunId}} {{retryInitiator}}", v); got != "recovery source-id current-operator" {
		t.Fatal(got)
	}
}
