package run

import (
	"context"
	"errors"
	"testing"
)

func TestParameterServiceSaveAndGet(t *testing.T) {
	db := testDB(t)
	svc := NewParameterService(db)
	projID := seedProject(t, db)

	defs := []ParamDef{
		{Key: "env", Label: "环境", Type: ParamTypeChoice, Default: "prod", Options: []string{"prod", "staging"}},
		{Key: "ver", Type: ParamTypeString, Default: "1.0"},
		{Key: "force", Type: ParamTypeBoolean, Default: "false"},
		{Key: "count", Type: ParamTypeNumber, Default: "3"},
	}
	cfg, err := svc.Save(context.Background(), projID, defs)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(cfg.Defs) != 4 {
		t.Fatalf("want 4 defs, got %d", len(cfg.Defs))
	}
	// label 缺省回退 key。
	if cfg.Defs[1].Label != "ver" {
		t.Errorf("label fallback = %q, want ver", cfg.Defs[1].Label)
	}
	// 非枚举类型不带 options。
	if cfg.Defs[2].Options != nil {
		t.Errorf("boolean should drop options, got %v", cfg.Defs[2].Options)
	}

	got, err := svc.Get(context.Background(), projID)
	if err != nil || len(got.Defs) != 4 {
		t.Fatalf("Get round-trip: %v / %d", err, len(got.Defs))
	}
}

func TestParameterServiceGetEmpty(t *testing.T) {
	db := testDB(t)
	svc := NewParameterService(db)
	projID := seedProject(t, db)
	got, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Defs == nil || len(got.Defs) != 0 {
		t.Fatalf("unconfigured project should yield empty defs, got %v", got.Defs)
	}
}

func TestParameterServiceSaveValidation(t *testing.T) {
	db := testDB(t)
	svc := NewParameterService(db)
	projID := seedProject(t, db)

	cases := []struct {
		name string
		defs []ParamDef
	}{
		{"bad key", []ParamDef{{Key: "1bad", Type: ParamTypeString}}},
		{"empty key", []ParamDef{{Key: "  ", Type: ParamTypeString}}},
		{"dup key", []ParamDef{{Key: "a", Type: ParamTypeString}, {Key: "a", Type: ParamTypeString}}},
		{"unknown type", []ParamDef{{Key: "a", Type: "weird"}}},
		{"choice no options", []ParamDef{{Key: "a", Type: ParamTypeChoice}}},
		{"choice default not in options", []ParamDef{{Key: "a", Type: ParamTypeChoice, Default: "z", Options: []string{"x", "y"}}}},
		{"number bad default", []ParamDef{{Key: "a", Type: ParamTypeNumber, Default: "NaNxx"}}},
		{"boolean bad default", []ParamDef{{Key: "a", Type: ParamTypeBoolean, Default: "yes"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.Save(context.Background(), projID, c.defs); !errors.Is(err, ErrInvalidParamDef) {
				t.Fatalf("want ErrInvalidParamDef, got %v", err)
			}
		})
	}
}

func TestParameterServiceSaveProjectNotFound(t *testing.T) {
	db := testDB(t)
	svc := NewParameterService(db)
	if _, err := svc.Save(context.Background(), "nope", []ParamDef{{Key: "a", Type: ParamTypeString}}); !errors.Is(err, ErrParamProjectNotFound) {
		t.Fatalf("want ErrParamProjectNotFound, got %v", err)
	}
}

func TestResolveParams(t *testing.T) {
	defs := []ParamDef{
		{Key: "env", Type: ParamTypeChoice, Default: "prod", Options: []string{"prod", "staging"}},
		{Key: "ver", Type: ParamTypeString, Default: "1.0"},
		{Key: "force", Type: ParamTypeBoolean, Default: "false"},
		{Key: "count", Type: ParamTypeNumber, Default: "3"},
		{Key: "token", Type: ParamTypeString, Required: true},
	}

	t.Run("fills defaults + keeps provided, drops undefined", func(t *testing.T) {
		got, err := ResolveParams(defs, map[string]string{"env": "staging", "token": "abc", "GHOST": "x"})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		want := map[string]string{"env": "staging", "ver": "1.0", "force": "false", "count": "3", "token": "abc"}
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %q want %q", k, got[k], v)
			}
		}
		if _, ok := got["GHOST"]; ok {
			t.Error("undefined key should be dropped")
		}
	})

	t.Run("required missing → error", func(t *testing.T) {
		if _, err := ResolveParams(defs, map[string]string{"env": "prod"}); !errors.Is(err, ErrInvalidParamValue) {
			t.Fatalf("want ErrInvalidParamValue, got %v", err)
		}
	})

	t.Run("choice out of range → error", func(t *testing.T) {
		if _, err := ResolveParams(defs, map[string]string{"env": "dev", "token": "x"}); !errors.Is(err, ErrInvalidParamValue) {
			t.Fatalf("want ErrInvalidParamValue, got %v", err)
		}
	})

	t.Run("number/boolean type mismatch → error", func(t *testing.T) {
		if _, err := ResolveParams(defs, map[string]string{"token": "x", "count": "abc"}); !errors.Is(err, ErrInvalidParamValue) {
			t.Fatalf("want number err, got %v", err)
		}
		if _, err := ResolveParams(defs, map[string]string{"token": "x", "force": "maybe"}); !errors.Is(err, ErrInvalidParamValue) {
			t.Fatalf("want boolean err, got %v", err)
		}
	})

	t.Run("no defs → passthrough (backward compat)", func(t *testing.T) {
		in := map[string]string{"anything": "goes", "x": "y"}
		got, err := ResolveParams(nil, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(got) != 2 || got["anything"] != "goes" || got["x"] != "y" {
			t.Fatalf("passthrough failed: %v", got)
		}
	})

	t.Run("provided empty falls back to default", func(t *testing.T) {
		got, err := ResolveParams(defs, map[string]string{"env": "  ", "token": "x"})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got["env"] != "prod" {
			t.Errorf("empty provided should fall back to default, got %q", got["env"])
		}
	})
}
