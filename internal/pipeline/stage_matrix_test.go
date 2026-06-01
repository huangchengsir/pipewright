package pipeline

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeMatrixEmptyReturnsNil(t *testing.T) {
	for _, in := range []map[string][]string{nil, {}, {"  ": {"  ", ""}}} {
		got, err := normalizeMatrix(in)
		if err != nil {
			t.Fatalf("空 matrix 不应报错: %v (in=%v)", err, in)
		}
		if got != nil {
			t.Errorf("空 matrix 应规范化为 nil,得 %v", got)
		}
	}
}

func TestNormalizeMatrixTrimsAndDedups(t *testing.T) {
	got, err := normalizeMatrix(map[string][]string{
		"go": {" 1.21 ", "1.22", "1.21", ""},
		"os": {"linux"},
	})
	if err != nil {
		t.Fatalf("normalizeMatrix: %v", err)
	}
	if len(got["go"]) != 2 || got["go"][0] != "1.21" || got["go"][1] != "1.22" {
		t.Errorf("go 轴应 trim+去重保序 [1.21 1.22],得 %v", got["go"])
	}
	if len(got["os"]) != 1 || got["os"][0] != "linux" {
		t.Errorf("os 轴 = %v", got["os"])
	}
}

func TestNormalizeMatrixInvalidAxisName(t *testing.T) {
	for _, bad := range []string{"1go", "go-version", "go.v", "go version"} {
		_, err := normalizeMatrix(map[string][]string{bad: {"x"}})
		if !errors.Is(err, ErrInvalidStage) {
			t.Errorf("非法轴名 %q 应报 ErrInvalidStage,得 %v", bad, err)
		}
	}
}

func TestNormalizeMatrixEmptyValuesError(t *testing.T) {
	_, err := normalizeMatrix(map[string][]string{"go": {"  ", ""}})
	if !errors.Is(err, ErrInvalidStage) {
		t.Errorf("轴值全空应报 ErrInvalidStage,得 %v", err)
	}
}

func TestNormalizeMatrixTooManyAxes(t *testing.T) {
	m := map[string][]string{}
	for i := 0; i <= MatrixMaxAxes; i++ {
		m[string(rune('a'+i))] = []string{"v"}
	}
	_, err := normalizeMatrix(m)
	if !errors.Is(err, ErrInvalidStage) {
		t.Errorf("超维度上限应报 ErrInvalidStage,得 %v", err)
	}
}

func TestNormalizeMatrixCellExplosionCapped(t *testing.T) {
	// 51 cell(单轴 51 值)> 上限 50 → 报错。
	vals := make([]string, 51)
	for i := range vals {
		vals[i] = string(rune('A')) + strings.Repeat("x", i+1)
	}
	_, err := normalizeMatrix(map[string][]string{"v": vals})
	if !errors.Is(err, ErrInvalidStage) {
		t.Errorf("cell 数超上限应报 ErrInvalidStage,得 %v", err)
	}

	// 多轴乘积爆炸:8×8 = 64 > 50。
	_, err = normalizeMatrix(map[string][]string{
		"a": {"1", "2", "3", "4", "5", "6", "7", "8"},
		"b": {"1", "2", "3", "4", "5", "6", "7", "8"},
	})
	if !errors.Is(err, ErrInvalidStage) {
		t.Errorf("乘积爆炸应报 ErrInvalidStage,得 %v", err)
	}
}

func TestNormalizeMatrixCellAtLimitOK(t *testing.T) {
	// 恰 50 cell 应放行。
	vals := make([]string, MatrixMaxCells)
	for i := range vals {
		vals[i] = "v" + strings.Repeat("a", i)
	}
	if _, err := normalizeMatrix(map[string][]string{"v": vals}); err != nil {
		t.Errorf("恰上限 %d cell 应放行,得 %v", MatrixMaxCells, err)
	}
}

func TestMatrixEnvKey(t *testing.T) {
	if got := MatrixEnvKey(" go "); got != "MATRIX_GO" {
		t.Errorf("MatrixEnvKey(go) = %q, want MATRIX_GO", got)
	}
}

func TestNormalizeSpecPropagatesMatrix(t *testing.T) {
	out, err := NormalizeSpec(Spec{Stages: []Stage{
		{Name: "源", Kind: KindSource, Jobs: []Job{{Name: "s", Type: "git_source"}}},
		{Name: "测试", Kind: KindBuild, Matrix: map[string][]string{"go": {"1.21", "1.22"}}, Jobs: []Job{}},
	}})
	if err != nil {
		t.Fatalf("NormalizeSpec: %v", err)
	}
	if len(out.Stages[1].Matrix["go"]) != 2 {
		t.Errorf("matrix 应贯穿规范化,得 %v", out.Stages[1].Matrix)
	}
}

func TestNormalizeSpecRejectsBadMatrix(t *testing.T) {
	_, err := NormalizeSpec(Spec{Stages: []Stage{
		{Name: "源", Kind: KindSource, Jobs: []Job{{Name: "s", Type: "git_source"}}},
		{Name: "测试", Kind: KindBuild, Matrix: map[string][]string{"1bad": {"x"}}, Jobs: []Job{}},
	}})
	if !errors.Is(err, ErrInvalidStage) {
		t.Errorf("非法轴名经 Save 校验应报 ErrInvalidStage,得 %v", err)
	}
}
