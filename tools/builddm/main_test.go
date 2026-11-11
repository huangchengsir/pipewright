package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModuleLineEndings(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		if !moduleNamed([]byte("module dm"+ending+"go 1.18"+ending), "dm") {
			t.Fatal("line ending rejected")
		}
	}
	if moduleNamed([]byte("module dm-fork\n"), "dm") {
		t.Fatal("other module accepted")
	}
}
func TestPrivateOutputBoundary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "checkout")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := privateOutput(root, filepath.Join(root, "private.exe")); e == nil {
		t.Fatal("checkout output accepted")
	}
	if _, e := privateOutput(root, filepath.Join(base, "private.exe")); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(base, "back-to-checkout")
	if e := os.Symlink(root, link); e != nil {
		t.Skip("OS does not permit symbolic links")
	}
	if _, e := privateOutput(root, filepath.Join(link, "private.exe")); e == nil {
		t.Fatal("linked checkout output accepted")
	}
}
