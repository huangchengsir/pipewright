package httpapi

import "testing"

func TestParseImages(t *testing.T) {
	out := `{"ID":"ad5fe409de38","Repository":"calciumion/new-api","Tag":"latest","Size":"232MB","CreatedSince":"7 weeks ago"}
{"ID":"1f073813b641","Repository":"redis","Tag":"latest","Size":"202MB","CreatedSince":"2 months ago"}
{"ID":"deadbeef0001","Repository":"<none>","Tag":"<none>","Size":"100MB","CreatedSince":"1 day ago"}
`
	got := parseImages(out)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Repository != "calciumion/new-api" || got[0].Tag != "latest" || got[0].Size != "232MB" {
		t.Fatalf("img0 wrong: %+v", got[0])
	}
	if got[2].Repository != "<none>" {
		t.Fatalf("dangling repo wrong: %+v", got[2])
	}
}

func TestParseImages_SkipsGarbage(t *testing.T) {
	got := parseImages("garbage\n{\"ID\":\"x\",\"Repository\":\"a\",\"Tag\":\"b\"}\n\n")
	if len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("want 1 valid image, got %+v", got)
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
