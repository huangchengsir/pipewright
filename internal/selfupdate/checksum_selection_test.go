package selfupdate

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestChecksumSelectsExactAssetAmongValidEntries(t *testing.T) {
	const first = "pipewright_2.0.0_linux_amd64.tar.gz"
	const selected = "pipewright_2.0.0_darwin_arm64.tar.gz"
	const last = "pipewright_2.0.0_linux_arm64.tar.gz"
	firstSHA, selectedSHA, lastSHA := strings.Repeat("1", 64), strings.Repeat("aB", 32), strings.Repeat("3", 64)
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			t.Error("unexpected asset request")
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(firstSHA + "  " + first + "\n" + selectedSHA + " *" + selected + "\n" + lastSHA + "  " + last + "\n"))
	})
	sum, e := h.s.checksum(context.Background(), Task{Repository: h.opts.Repository, Target: "2.0.0", Asset: selected})
	if e != nil || sum != strings.ToLower(selectedSHA) {
		t.Fatalf("selected checksum = %q, error = %v; want %q", sum, e, strings.ToLower(selectedSHA))
	}
	if sum == firstSHA || sum == lastSHA {
		t.Fatal("selected another valid release asset")
	}
	if _, e = h.s.checksum(context.Background(), Task{Repository: h.opts.Repository, Target: "2.0.0", Asset: "missing.tar.gz"}); e == nil {
		t.Fatal("missing selected asset accepted")
	}
}
