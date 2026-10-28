package notify

import (
	"github.com/huangchengsir/pipewright/internal/i18n"
	"strings"
)

func withRecoveryNotice(p Payload, v TemplateVars) Payload {
	if v.ExecutionKind != "recovery" {
		return p
	}
	if p.Fields == nil {
		p.Fields = map[string]string{}
	}
	details := []string{i18n.T(p.Lang, "节点恢复尝试") + " #" + v.AttemptNumber}
	for _, field := range []struct{ key, label, value string }{
		{"logicalRunId", "原始运行", v.LogicalRunID}, {"sourceRunId", "来源执行", v.SourceRunID}, {"retryInitiator", "恢复发起人", v.RetryInitiator},
	} {
		if field.value != "" {
			p.Fields[field.key] = field.value
			details = append(details, i18n.T(p.Lang, field.label)+": "+field.value)
		}
	}
	p.Fields["executionKind"] = v.ExecutionKind
	p.Fields["attemptNumber"] = v.AttemptNumber
	p.Body = strings.TrimSpace(p.Body) + "\n\n" + strings.Join(details, "\n")
	return p
}
