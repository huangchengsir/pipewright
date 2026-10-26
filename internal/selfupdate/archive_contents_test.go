package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"testing"
)

func TestReleaseArchiveDocumentationAndRequiredBinary(t *testing.T) {
	for _, kind := range []string{"documentation", "missing", "unsafe-path", "oversized", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			h := fixture(t, nil)
			var b bytes.Buffer
			gz := gzip.NewWriter(&b)
			tw := tar.NewWriter(gz)
			doc := []byte("release documentation")
			tw.WriteHeader(&tar.Header{Name: "README.md", Typeflag: tar.TypeReg, Size: int64(len(doc))})
			tw.Write(doc)
			if kind != "missing" {
				name := "pipewright"
				size := int64(7)
				flag := byte(tar.TypeReg)
				if kind == "unsafe-path" {
					name = "../pipewright"
				}
				if kind == "oversized" {
					size = MaxBinary + 1
				}
				if kind == "symlink" {
					flag = tar.TypeSymlink
					size = 0
				}
				_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: flag, Size: size, Mode: 0755, Linkname: "outside"})
				if flag == tar.TypeReg && size == 7 {
					tw.Write([]byte("payload"))
				}
			}
			_ = tw.Close()
			_ = gz.Close()
			h.pkg = b.Bytes()
			v := submit(t, h)
			if kind == "documentation" {
				wait(t, h.s, v.ID, "restarting")
				if h.installs.Load() != 1 {
					t.Fatal("valid release documentation rejected")
				}
				installed, e := os.ReadFile(h.opts.Executable)
				if e != nil || !bytes.Equal(installed, []byte("payload")) {
					t.Fatalf("installed binary contents = %q, error = %v; want payload", installed, e)
				}
			} else {
				wait(t, h.s, v.ID, "failed")
				if h.installs.Load() != 0 {
					t.Fatal("invalid archive installed")
				}
			}
		})
	}
}
