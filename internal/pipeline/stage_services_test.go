package pipeline

import (
	"errors"
	"testing"
)

func TestNormalizeServices(t *testing.T) {
	t.Run("valid + trim", func(t *testing.T) {
		got, err := normalizeServices([]ServiceSpec{
			{Name: " testdb ", Image: " postgres:16 ", Env: []string{"POSTGRES_PASSWORD=x", "  "}, Ports: []string{"5432:5432"}},
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got[0].Name != "testdb" || got[0].Image != "postgres:16" {
			t.Errorf("svc = %+v", got[0])
		}
		if len(got[0].Env) != 1 { // 空 env 行被剔除
			t.Errorf("env = %v", got[0].Env)
		}
	})

	t.Run("empty → nil", func(t *testing.T) {
		if got, _ := normalizeServices(nil); got != nil {
			t.Errorf("nil → nil, got %v", got)
		}
	})

	bad := []struct {
		name  string
		specs []ServiceSpec
	}{
		{"bad name", []ServiceSpec{{Name: "1bad", Image: "x"}}},
		{"empty name", []ServiceSpec{{Name: "  ", Image: "x"}}},
		{"dup name", []ServiceSpec{{Name: "db", Image: "x"}, {Name: "db", Image: "y"}}},
		{"no image", []ServiceSpec{{Name: "db"}}},
	}
	for _, b := range bad {
		t.Run(b.name, func(t *testing.T) {
			if _, err := normalizeServices(b.specs); !errors.Is(err, ErrInvalidStage) {
				t.Fatalf("want ErrInvalidStage, got %v", err)
			}
		})
	}
}
