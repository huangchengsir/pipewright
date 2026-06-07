package httpapi

import "testing"

func TestParseStacks_JSONArray(t *testing.T) {
	out := `[{"Name":"web","Status":"running(2)","ConfigFiles":"/opt/pipewright/stacks/web/docker-compose.yml"},{"Name":"db","Status":"exited(1)","ConfigFiles":"/x/docker-compose.yml"}]`
	got := parseStacks(out)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "web" || got[0].Status != "running(2)" {
		t.Fatalf("stack0 wrong: %+v", got[0])
	}
}

func TestParseStacks_LineDelimitedFallback(t *testing.T) {
	out := "{\"Name\":\"a\",\"Status\":\"running(1)\"}\n{\"Name\":\"b\",\"Status\":\"exited(0)\"}\n"
	got := parseStacks(out)
	if len(got) != 2 || got[1].Name != "b" {
		t.Fatalf("fallback parse wrong: %+v", got)
	}
}

func TestParseStacks_Empty(t *testing.T) {
	if got := parseStacks("  \n"); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
	if got := parseStacks("[]"); len(got) != 0 {
		t.Fatalf("want empty for [], got %+v", got)
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
