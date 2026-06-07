package httpapi

import (
	"testing"
)

// --- 纯函数:scope 枚举白名单校验(AC-SEC-02 要害) ---

func TestValidatePruneScope(t *testing.T) {
	cases := []struct {
		name    string
		scope   string
		wantErr bool
	}{
		{"containers 合法", "containers", false},
		{"images 合法", "images", false},
		{"volumes 合法", "volumes", false},
		{"builder 合法", "builder", false},
		{"all 合法", "all", false},
		// 非白名单 / 注入一律拒。
		{"空 scope 拒", "", true},
		{"未知 scope 拒", "networks", true},
		{"大小写不匹配拒", "Containers", true},
		{"image-all 不做拒", "images-all", true},
		{"命令注入拒", "all;rm -rf /", true},
		{"flag 注入拒", "--volumes", true},
		{"带空格拒", "all ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePruneScope(c.scope)
			if (err != nil) != c.wantErr {
				t.Fatalf("validatePruneScope(%q) err=%v, wantErr=%v", c.scope, err, c.wantErr)
			}
		})
	}
}

// --- 纯函数:命令 array 构造(不拼 shell;all 不加 --volumes) ---

func TestBuildPruneCmd(t *testing.T) {
	cases := []struct {
		scope string
		want  []string
	}{
		{"containers", []string{"docker", "container", "prune", "-f"}},
		{"images", []string{"docker", "image", "prune", "-f"}},
		{"volumes", []string{"docker", "volume", "prune", "-f"}},
		{"builder", []string{"docker", "builder", "prune", "-f"}},
		{"all", []string{"docker", "system", "prune", "-f"}},
	}
	for _, c := range cases {
		got := buildPruneCmd(c.scope)
		if !equalStrSlice(got, c.want) {
			t.Fatalf("buildPruneCmd(%q) = %v, want %v", c.scope, got, c.want)
		}
	}
	// AC-SEC-02:all 绝不含 --volumes(避免误删数据卷)。
	for _, arg := range buildPruneCmd("all") {
		if arg == "--volumes" {
			t.Fatalf("buildPruneCmd(\"all\") 不应包含 --volumes(会误删数据卷)")
		}
	}
	// 非白名单返回 nil(不构造命令)。
	if got := buildPruneCmd("networks"); got != nil {
		t.Fatalf("buildPruneCmd(非法) = %v, want nil", got)
	}
}

// --- 纯函数:解析 `docker system df --format '{{json .}}'` 逐行 JSON ---

func TestParseDockerDf(t *testing.T) {
	// docker CLI 把 TotalCount/Active/Size/Reclaimable 序列化为字符串。
	stdout := `{"Active":"2","Reclaimable":"1.2GB (75%)","Size":"1.6GB","TotalCount":"5","Type":"Images"}
{"Active":"1","Reclaimable":"0B (0%)","Size":"20MB","TotalCount":"3","Type":"Containers"}
{"Active":"0","Reclaimable":"500MB","Size":"500MB","TotalCount":"4","Type":"Local Volumes"}
{"Active":"0","Reclaimable":"800MB","Size":"800MB","TotalCount":"10","Type":"Build Cache"}`

	got := parseDockerDf(stdout)
	if len(got) != 4 {
		t.Fatalf("parseDf 返回 %d 条,want 4", len(got))
	}

	images := got[0]
	if images.Type != "Images" || images.TotalCount != 5 || images.Active != 2 ||
		images.Size != "1.6GB" || images.Reclaimable != "1.2GB (75%)" {
		t.Fatalf("Images 行解析错: %+v", images)
	}
	if got[2].Type != "Local Volumes" || got[2].TotalCount != 4 {
		t.Fatalf("Local Volumes 行解析错: %+v", got[2])
	}
	if got[3].Type != "Build Cache" || got[3].TotalCount != 10 {
		t.Fatalf("Build Cache 行解析错: %+v", got[3])
	}
}

func TestParseDockerDfTolerantOfJunk(t *testing.T) {
	// 空行 / 坏 JSON / 缺 Type / "N/A" 计数:best-effort 跳过坏行、计数归 0,不 panic。
	stdout := "\n  \nnot-json\n{\"Type\":\"\"}\n{\"Type\":\"Images\",\"TotalCount\":\"N/A\",\"Active\":\"\",\"Size\":\"1GB\",\"Reclaimable\":\"1GB\"}\n"
	got := parseDockerDf(stdout)
	if len(got) != 1 {
		t.Fatalf("parseDf 应只保留 1 条有效行,得 %d 条: %+v", len(got), got)
	}
	if got[0].Type != "Images" || got[0].TotalCount != 0 || got[0].Active != 0 {
		t.Fatalf("坏计数应归 0: %+v", got[0])
	}
}

func TestParseDockerDfEmpty(t *testing.T) {
	if got := parseDockerDf(""); len(got) != 0 {
		t.Fatalf("parseDockerDf(\"\") = %v, want 空", got)
	}
}
