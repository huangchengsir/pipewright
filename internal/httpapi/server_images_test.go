package httpapi

import "testing"

func TestParseImages(t *testing.T) {
	// 逐字段 Tab 分隔(真实 docker images --format "{{.ID}}\t...");Size 含内部空格不应被拆。
	out := "ad5fe409de38\tcalciumion/new-api\tlatest\t232 MB\t7 weeks ago\n" +
		"1f073813b641\tredis\tlatest\t202MB\t2 months ago\n" +
		"deadbeef0001\t<none>\t<none>\t100MB\t1 day ago\n"
	got := parseImages(out)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Repository != "calciumion/new-api" || got[0].Tag != "latest" || got[0].Size != "232 MB" {
		t.Fatalf("img0 wrong: %+v", got[0])
	}
	if got[2].Repository != "<none>" {
		t.Fatalf("dangling repo wrong: %+v", got[2])
	}
}

func TestParseImages_SkipsBlankAndShortLines(t *testing.T) {
	// 空行 / 首列 ID 为空(老 docker json 模板异常会输出空)应被跳过;字段不足补空不崩。
	out := "\n\t\t\t\t\nx\ta\tb\n"
	got := parseImages(out)
	if len(got) != 1 || got[0].ID != "x" || got[0].Repository != "a" || got[0].Tag != "b" {
		t.Fatalf("want 1 valid image, got %+v", got)
	}
	if got[0].Size != "" || got[0].CreatedSince != "" {
		t.Fatalf("missing trailing fields should be empty: %+v", got[0])
	}
}

func TestValidateImageActionRef(t *testing.T) {
	ok := []string{"nginx", "nginx:latest", "calciumion/new-api:latest", "ad5fe409de38", "registry.io/repo:tag@sha256:abc"}
	for _, r := range ok {
		if err := validateImageActionRef(r); err != nil {
			t.Fatalf("%q should be valid: %v", r, err)
		}
	}
	bad := []string{"", "-rf", "nginx; rm -rf /", "a|b", "$(whoami)", "a b"}
	for _, r := range bad {
		if err := validateImageActionRef(r); err == nil {
			t.Fatalf("%q should be rejected", r)
		}
	}
}
