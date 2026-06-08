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

func TestParseStackServices(t *testing.T) {
	// service\tname\timage\tstatus\tports;status 含内部空格、Ports 含逗号都不应被拆错。
	out := "web\tdc_web_1\tnginx:latest\tUp 2 hours\t0.0.0.0:8080->80/tcp\n" +
		"db\tdc_db_1\tmysql:8\tExited (0) 3 days ago\t\n" +
		"\tbare_named\tredis:7\tUp 5 minutes (Paused)\t6379/tcp\n"
	got := parseStackServices(out)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Service != "web" || got[0].Image != "nginx:latest" || got[0].State != "running" || got[0].Ports != "0.0.0.0:8080->80/tcp" {
		t.Fatalf("svc0 wrong: %+v", got[0])
	}
	if got[1].State != "exited" {
		t.Fatalf("svc1 state wrong: %+v", got[1])
	}
	if got[2].Service != "" || got[2].Name != "bare_named" || got[2].State != "paused" {
		t.Fatalf("svc2 wrong: %+v", got[2])
	}
}

func TestParseStackServices_SkipsBlankAndNameless(t *testing.T) {
	// 空行、name 为空的行跳过。
	got := parseStackServices("\n\t\t\t\t\nweb\tn1\timg\tUp\t\n")
	if len(got) != 1 || got[0].Name != "n1" {
		t.Fatalf("want 1, got %+v", got)
	}
}

func TestPrimaryComposePath(t *testing.T) {
	// 单一绝对路径 → ok。
	if p, ok := primaryComposePath("/opt/app/docker-compose.yml"); !ok || p != "/opt/app/docker-compose.yml" {
		t.Fatalf("single abs: p=%q ok=%v", p, ok)
	}
	// 空(老 v1 无标签)→ 不可编辑。
	if _, ok := primaryComposePath(""); ok {
		t.Fatalf("empty should not be editable")
	}
	// 多文件 → 不可在线编辑(写回有歧义)。
	if _, ok := primaryComposePath("/a/x.yml,/a/y.yml"); ok {
		t.Fatalf("multi should not be editable")
	}
	// 相对路径 / 含控制字符 → 拒绝。
	for _, bad := range []string{"relative.yml", "/a\nb.yml", "/a\x00b.yml"} {
		if _, ok := primaryComposePath(bad); ok {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func TestComposeProjectFromPath(t *testing.T) {
	cases := map[string]string{
		"/home/fn0S/docker-compose/nacos/docker-compose.yml": "nacos",
		"/home/fn0S/docker-compose/docker-compose.yml":       "dockercompose", // 父目录 docker-compose → 剔连字符
		"/opt/My_App/compose.yaml":                           "myapp",         // 大写+下划线被剔/小写
		"/srv/web-1/docker-compose.yml":                      "web1",
	}
	for path, want := range cases {
		if got := composeProjectFromPath(path); got != want {
			t.Fatalf("composeProjectFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}
