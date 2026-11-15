package config

import "testing"

func TestParseMultiTenantMode(t *testing.T) {
	for _, tc := range []struct {
		value                            string
		present, enabled, explicit, fail bool
	}{{"", false, false, false, false}, {"true", true, true, true, false}, {"false", true, false, true, false}, {"", true, false, false, true}, {"TRUE", true, false, false, true}, {"1", true, false, false, true}, {" true ", true, false, false, true}, {"invalid", true, false, false, true}} {
		got, err := ParseMultiTenantMode(tc.value, tc.present)
		if (err != nil) != tc.fail || got.Enabled != tc.enabled || got.Explicit != tc.explicit {
			t.Fatalf("%+v got %+v %v", tc, got, err)
		}
	}
}
