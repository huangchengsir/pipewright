// Command builddm builds a private Pipewright binary with a user-supplied official
// DM driver. It never copies that module into the public repository or modifies
// the repository's go.mod/go.sum. Go's overlay and a temporary modfile provide the
// optional integration; ordinary go mod tidy does not discover a dm import.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed dm_adapter.go.template
var templates embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	driverDir := flag.String("driver-dir", "", "private official dm module directory")
	output := flag.String("o", "", "private output path outside this checkout (required)")
	goPath := flag.String("go", "", "Go executable (defaults to this toolchain)")
	targetOS := flag.String("goos", runtime.GOOS, "target OS")
	targetArch := flag.String("goarch", runtime.GOARCH, "target architecture")
	testPackage := flag.String("test-package", "", "compile an internal test package instead of the app")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !moduleNamed(module, "github.com/huangchengsir/pipewright") {
		return errors.New("run builddm from the Pipewright repository root")
	}
	vendor, err := filepath.Abs(*driverDir)
	if err != nil || *driverDir == "" {
		return errors.New("-driver-dir must name your privately supplied official dm module")
	}
	vendor, err = filepath.EvalSymlinks(vendor)
	if err != nil {
		return errors.New("official dm module directory is unavailable")
	}
	vendorMod, err := os.ReadFile(filepath.Join(vendor, "go.mod"))
	if err != nil || !moduleNamed(vendorMod, "dm") {
		return errors.New("-driver-dir must contain module dm")
	}
	dest, err := filepath.Abs(*output)
	if err != nil || *output == "" {
		return errors.New("-o must name a private build output outside the checkout")
	}
	dest, err = privateOutput(root, dest)
	if err != nil {
		return errors.New("DM build outputs must stay outside the public checkout")
	}
	if *testPackage != "" && (!strings.HasPrefix(*testPackage, "./internal/") || strings.Contains(*testPackage, "..") || strings.ContainsAny(*testPackage, "\\ ;\t\r\n")) {
		return errors.New("invalid internal test package")
	}
	work, err := os.MkdirTemp("", "pipewright-dm-build-")
	if err != nil {
		return err
	}
	tempRoot, err := filepath.Rel(os.TempDir(), work)
	if err != nil || strings.Contains(tempRoot, string(os.PathSeparator)) || !strings.HasPrefix(tempRoot, "pipewright-dm-build-") {
		return errors.New("temporary build path check failed")
	}
	defer os.RemoveAll(work)
	modfile := filepath.Join(work, "private.mod")
	if err = os.WriteFile(modfile, module, 0600); err != nil {
		return err
	}
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(work, "private.sum"), sums, 0600); err != nil {
		return err
	}
	adapter, err := templates.ReadFile("dm_adapter.go.template")
	if err != nil {
		return err
	}
	adapterPath := filepath.Join(work, "dm_vendor_generated.go")
	if err = os.WriteFile(adapterPath, adapter, 0600); err != nil {
		return err
	}
	overlay := map[string]any{"Replace": map[string]string{filepath.Join(root, "internal", "store", "dm_vendor_generated.go"): adapterPath}}
	data, _ := json.Marshal(overlay)
	overlayPath := filepath.Join(work, "overlay.json")
	if err = os.WriteFile(overlayPath, data, 0600); err != nil {
		return err
	}
	executable := *goPath
	if executable == "" {
		executable = filepath.Join(runtime.GOROOT(), "bin", "go")
		if runtime.GOOS == "windows" {
			executable += ".exe"
		}
	}
	invoke := func(args ...string) error {
		cmd := exec.Command(executable, args...)
		cmd.Dir = root
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), "GOOS="+*targetOS, "GOARCH="+*targetArch, "CGO_ENABLED=0")
		return cmd.Run()
	}
	if err = invoke("mod", "edit", "-modfile="+modfile, "-require=dm@v0.0.0", "-replace=dm="+vendor); err != nil {
		return errors.New("could not prepare the private DM module overlay")
	}
	args := []string{"build"}
	pkg := "./cmd/pipewright"
	if *testPackage != "" {
		args = []string{"test", "-c"}
		pkg = *testPackage
	}
	args = append(args, "-mod=mod", "-modfile="+modfile, "-overlay="+overlayPath, "-tags=dm", "-o", dest, pkg)
	if err = invoke(args...); err != nil {
		return errors.New("private DM build failed; public module files were not changed")
	}
	fmt.Println("Private DM build created. Keep this binary and the official driver out of public release artifacts.")
	return nil
}

func moduleNamed(data []byte, name string) bool {
	return strings.HasPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "module "+name+"\n")
}

// Resolve existing parent links before checking the output boundary. Requiring
// an existing parent also avoids a later build silently traversing a new link.
func privateOutput(root, output string) (string, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil {
		return "", err
	}
	dest := filepath.Join(parent, filepath.Base(output))
	if info, e := os.Lstat(dest); e == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("output must be a regular file")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	rel, err := filepath.Rel(root, dest)
	if err != nil || !(rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return "", errors.New("output is inside checkout")
	}
	return dest, nil
}
