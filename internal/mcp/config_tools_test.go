package mcp

import (
	"strings"
	"testing"
)

func TestConfigurationJSONRejectsAmbiguousNestedKeys(t *testing.T) {
	for _, raw := range []string{`{"projectId":"a","projectId":"b"}`, `{"operations":[{"limits":{"cpu":"1","cpu":"2"}}]}`, `{"x":1} {"x":2}`, `{"x":`, strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18)} {
		if uniqueJSONKeys([]byte(raw)) {
			t.Fatalf("ambiguous input accepted: %q", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"x":[{"cpu":"1"},{"cpu":"2"}]}`, `{"value":null}`} {
		if !uniqueJSONKeys([]byte(raw)) {
			t.Fatalf("valid JSON rejected: %q", raw)
		}
	}
}
