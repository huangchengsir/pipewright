package notify

import (
	"reflect"
	"strings"
	"testing"
)

func TestAnomalyAnalysisMergedIntoOnePayload(t *testing.T) {
	for _, template := range []string{"", "Custom notice", "{{status}}\n{{aiAnalysis}}", "{{ status }}\n{{ aiAnalysis }}"} {
		t.Run(template, func(t *testing.T) {
			s, _, _ := newSvc(t, nil)
			if template != "" {
				if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{Event: EventAnomalyDetected, TitleTemplate: "Alert", BodyTemplate: template}); err != nil {
					t.Fatal(err)
				}
			}
			vars := TemplateVars{Project: "server", Status: "Disk usage 94% exceeds 80%", Event: EventAnomalyDetected, AIAnalysis: "AI analysis: inspect disk owners before cleanup"}
			p := s.RenderPayload(ctx(), EventAnomalyDetected, "", vars)
			if !strings.Contains(p.Body, vars.Status) || strings.Count(p.Body, vars.AIAnalysis) != 1 || p.Fields["aiAnalysis"] != vars.AIAnalysis {
				t.Fatalf("incomplete or duplicate alert: %+v", p)
			}
		})
	}
}

func TestAnalysisDoesNotChangeOtherEventsOrDisabledAlerts(t *testing.T) {
	for _, event := range []string{EventBuildFailed, EventAnomalyDetected} {
		s, _, _ := newSvc(t, nil)
		vars := TemplateVars{Project: "server", Status: "original", Event: event}
		without := s.RenderPayload(ctx(), event, "", vars)
		if event != EventAnomalyDetected {
			vars.AIAnalysis = "not monitoring"
		}
		with := s.RenderPayload(ctx(), event, "", vars)
		if !reflect.DeepEqual(without, with) {
			t.Fatalf("changed old event: %s", event)
		}
	}
}
