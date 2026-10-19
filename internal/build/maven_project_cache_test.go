package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMavenCachePreservesProjectJvmRepository(t *testing.T) {
	shell := mavenCacheTestShell(t)
	for _, tc := range []struct {
		name, rootConfig, nestedConfig string
		fixedBase, wantDefault         bool
	}{
		{name: "root_explicit", rootConfig: "-Dmaven.repo.local=/workspace/deps"},
		{name: "nested_explicit", nestedConfig: "-Xmx128m\n-Dmaven.repo.local=/workspace/deps"},
		{name: "nested_shadows_parent", rootConfig: "-Dmaven.repo.local=/parent", nestedConfig: "-Xmx128m", wantDefault: true},
		{name: "fixed_basedir", rootConfig: "-Dmaven.repo.local=/selected", fixedBase: true},
		{name: "no_explicit", rootConfig: "-Xmx128m", wantDefault: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cwd := filepath.Join(root, "backend")
			if err := os.MkdirAll(cwd, 0o700); err != nil {
				t.Fatal(err)
			}
			for rel, value := range map[string]string{".mvn/jvm.config": tc.rootConfig, "backend/.mvn/jvm.config": tc.nestedConfig} {
				if value == "" {
					continue
				}
				file := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wrapped := mavenCacheCommand([]string{shell, "-c", `printf '%s' "$MAVEN_OPTS"`}, "/workspace", jobCacheConfig{enabled: true, paths: []string{".m2"}})
			cmd := exec.Command(shell, wrapped[1:]...)
			cmd.Dir = cwd
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if key != "MAVEN_OPTS" && key != "MAVEN_ARGS" && key != "MAVEN_BASEDIR" {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "MAVEN_OPTS=-Dfeature=yes -Xmx96m", "MSYS_NO_PATHCONV=1")
			if tc.fixedBase {
				// The wrapper resolves this relative path inside the container shell.
				cmd.Env = append(cmd.Env, "MAVEN_BASEDIR=..")
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("wrapper: %v %s", err, out)
			}
			want := "-Dfeature=yes -Xmx96m"
			if tc.wantDefault {
				want = "-Dmaven.repo.local=/workspace/.m2/repository " + want
			}
			if string(out) != want {
				t.Fatalf("project JVM repository not respected: got %q want %q", out, want)
			}
		})
	}
}
