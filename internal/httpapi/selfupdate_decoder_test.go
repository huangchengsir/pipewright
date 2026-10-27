package httpapi

import (
	"github.com/huangchengsir/pipewright/internal/selfupdate"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSelfUpdateAuthenticatedTaskProtocolLegacyBodyDecoder(t *testing.T) {
	for _, body := range []string{"", "{}", "{\"target\":\"2.0.0\"}"} {
		r := httptest.NewRequest("POST", "/api/version/update", strings.NewReader(body))
		r.ContentLength = -1
		w := httptest.NewRecorder()
		in := selfupdate.Request{Target: "2.0.0"}
		if !decodeUpdateBody(w, r, &in, true) || in.Target != "2.0.0" || in.RequestID != "" {
			t.Fatal(body, w.Code, in)
		}
	}
	for _, body := range []string{"", "{\"url\":\"https://outside.invalid/package\"}", "{} {}"} {
		r := httptest.NewRequest("POST", "/api/version/update-tasks", strings.NewReader(body))
		w := httptest.NewRecorder()
		if decodeUpdate(w, r, new(selfupdate.Request)) || w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
}
