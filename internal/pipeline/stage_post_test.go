package pipeline

import (
	"errors"
	"testing"
)

func TestPostConditionMatches(t *testing.T) {
	cases := []struct {
		cond   string
		failed bool
		want   bool
	}{
		{PostAlways, false, true}, {PostAlways, true, true},
		{PostOnSuccess, false, true}, {PostOnSuccess, true, false},
		{PostOnFailure, false, false}, {PostOnFailure, true, true},
		{"", false, true}, {"", true, true}, // 空 → always
		{"ALWAYS", true, true}, // 大小写容错
	}
	for _, c := range cases {
		if got := PostConditionMatches(c.cond, c.failed); got != c.want {
			t.Errorf("PostConditionMatches(%q, failed=%v) = %v, want %v", c.cond, c.failed, got, c.want)
		}
	}
}

func TestNormalizePost(t *testing.T) {
	t.Run("valid + condition 默认 always + trim", func(t *testing.T) {
		got, err := normalizePost([]PostStep{
			{Condition: " On_Failure ", Image: " busybox ", Commands: []string{"echo cleanup"}, WorkDir: " sub "},
			{Image: "alpine", Commands: []string{"echo always"}}, // 空 condition → always
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got[0].Condition != PostOnFailure || got[0].Image != "busybox" || got[0].WorkDir != "sub" {
			t.Errorf("step0 = %+v", got[0])
		}
		if got[1].Condition != PostAlways {
			t.Errorf("empty condition should default always, got %q", got[1].Condition)
		}
	})

	t.Run("empty → nil", func(t *testing.T) {
		if got, _ := normalizePost(nil); got != nil {
			t.Errorf("nil → nil, got %v", got)
		}
	})

	bad := []struct {
		name  string
		steps []PostStep
	}{
		{"bad condition", []PostStep{{Condition: "weird", Image: "x", Commands: []string{"c"}}}},
		{"no image", []PostStep{{Condition: "always", Commands: []string{"c"}}}},
		{"no commands", []PostStep{{Condition: "always", Image: "x", Commands: []string{"  ", ""}}}},
	}
	for _, b := range bad {
		t.Run(b.name, func(t *testing.T) {
			if _, err := normalizePost(b.steps); !errors.Is(err, ErrInvalidStage) {
				t.Fatalf("want ErrInvalidStage, got %v", err)
			}
		})
	}
}
