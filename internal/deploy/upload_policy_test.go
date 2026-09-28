package deploy

import (
	"errors"
	"testing"
	"time"
)

func TestParseUploadPolicyDefaultsAndOverrides(t *testing.T) {
	defaults, err := parseUploadPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.command != time.Minute || defaults.connect != 15*time.Second || defaults.idle != 2*time.Minute || defaults.total != 0 {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	p, err := parseUploadPolicy(map[string]string{
		"commandTimeoutSeconds":    "90",
		"connectTimeoutSeconds":    "30",
		"uploadIdleTimeoutSeconds": "300",
		"uploadTimeoutSeconds":     "3600",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.command != 90*time.Second || p.connect != 30*time.Second || p.idle != 5*time.Minute || p.total != time.Hour {
		t.Fatalf("unexpected overrides: %+v", p)
	}
}

func TestParseUploadPolicyRejectsOutOfRangeAndMalformedValues(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"commandTimeoutSeconds", "0"},
		{"commandTimeoutSeconds", "86401"},
		{"connectTimeoutSeconds", "301"},
		{"uploadIdleTimeoutSeconds", "not-a-number"},
		{"uploadTimeoutSeconds", "-1"},
		{"uploadTimeoutSeconds", "604801"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			_, err := parseUploadPolicy(map[string]string{tc.key: tc.value})
			if !errors.Is(err, ErrInvalidUploadPolicy) {
				t.Fatalf("expected invalid upload policy, got %v", err)
			}
		})
	}
}
