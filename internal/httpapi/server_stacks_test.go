package httpapi

import (
	"strings"
	"testing"
)

func TestParseStacks_FromLabels(t *testing.T) {
	// 格式:project|||configFiles|||state(每容器一行);空 project = 裸 docker run,跳过。
	out := strings.Join([]string{
		"dockercompose|||/opt/app/docker-compose.yml|||Up 2 months",
		"dockercompose||||||Up 3 weeks (healthy)", // 同项目第二个容器,无 cfg 段
		"dockercompose||||||Exited (0) 1 day ago", // 同项目第三个,停止
		"nacos||||||Up 5 hours",                   // 另一项目
		"||||||Up 1 hour",                         // 无 project(裸 run)→ 跳过
		"",                                        // 空行
	}, "\n")
	got := parseStacks(out)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	byName := map[string]stackDTO{}
	for _, s := range got {
		byName[s.Name] = s
	}
	dc := byName["dockercompose"]
	if dc.Status != "2/3 运行" {
		t.Fatalf("dockercompose status = %q, want 2/3 运行", dc.Status)
	}
	if dc.ConfigFiles != "/opt/app/docker-compose.yml" {
		t.Fatalf("dockercompose cfg = %q", dc.ConfigFiles)
	}
	if byName["nacos"].Status != "running(1)" {
		t.Fatalf("nacos status = %q, want running(1)", byName["nacos"].Status)
	}
}

func TestParseStacks_Empty(t *testing.T) {
	if got := parseStacks("  \n"); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
	// 全是裸 docker run(无 project)→ 空。
	if got := parseStacks("||||||running\n||||||exited"); len(got) != 0 {
		t.Fatalf("want empty for no-project lines, got %+v", got)
	}
}

func TestStackActionWhitelist(t *testing.T) {
	for _, a := range []string{"start", "stop", "restart", "down", "update"} {
		if !stackActions[a] {
			t.Fatalf("%q should be allowed", a)
		}
	}
	for _, a := range []string{"up", "rm", "kill", "exec", ""} {
		if stackActions[a] {
			t.Fatalf("%q should NOT be allowed", a)
		}
	}
}

func TestComposeFileArgs(t *testing.T) {
	// 单文件。
	got, err := composeFileArgs("/opt/pipewright/stacks/x/docker-compose.yml")
	if err != nil || len(got) != 2 || got[0] != "-f" || got[1] != "/opt/pipewright/stacks/x/docker-compose.yml" {
		t.Fatalf("single = %v, err=%v", got, err)
	}
	// 多文件(逗号分隔)→ 多个 -f。
	got, err = composeFileArgs("/a/docker-compose.yml,/a/override.yml")
	if err != nil || len(got) != 4 {
		t.Fatalf("multi = %v, err=%v", got, err)
	}
	// 非法:相对路径 / flag 注入 / 空。
	for _, bad := range []string{"", "relative/path.yml", "-rf", "/a\nb.yml"} {
		if _, err := composeFileArgs(bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}
