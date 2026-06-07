package httpapi

import (
	"reflect"
	"testing"
)

func TestValidateCreateSpec_Valid(t *testing.T) {
	req := &createContainerRequest{
		Image:   "nginx:latest",
		Name:    "web-1",
		Ports:   []string{"8080:80", "127.0.0.1:9090:90/tcp", "443"},
		Env:     []string{"FOO=bar", "TZ=Asia/Shanghai"},
		Volumes: []string{"/data/web:/usr/share/nginx/html:ro", "myvol:/cache"},
		Restart: "unless-stopped",
		Command: "nginx -g daemon off;",
	}
	if err := validateCreateSpec(req); err == nil {
		// command 含 ';' 应被拒
		t.Fatalf("command with ';' should be rejected")
	}
	req.Command = "nginx"
	if err := validateCreateSpec(req); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
}

func TestValidateCreateSpec_RejectsInjection(t *testing.T) {
	cases := []struct {
		name string
		req  createContainerRequest
	}{
		{"image shell metachar", createContainerRequest{Image: "nginx; rm -rf /"}},
		{"image flag injection", createContainerRequest{Image: "-v/etc:/etc"}},
		{"empty image", createContainerRequest{Image: ""}},
		{"name leading dash", createContainerRequest{Image: "nginx", Name: "-rf"}},
		{"name shell char", createContainerRequest{Image: "nginx", Name: "a;b"}},
		{"bad restart", createContainerRequest{Image: "nginx", Restart: "sometimes"}},
		{"port out of range", createContainerRequest{Image: "nginx", Ports: []string{"99999:80"}}},
		{"port non-numeric", createContainerRequest{Image: "nginx", Ports: []string{"abc:80"}}},
		{"env bad key", createContainerRequest{Image: "nginx", Env: []string{"1FOO=bar"}}},
		{"env no equals", createContainerRequest{Image: "nginx", Env: []string{"FOOBAR"}}},
		{"env newline value", createContainerRequest{Image: "nginx", Env: []string{"FOO=a\nb"}}},
		{"volume one part", createContainerRequest{Image: "nginx", Volumes: []string{"/onlyone"}}},
		{"volume bad mode", createContainerRequest{Image: "nginx", Volumes: []string{"/a:/b:xx"}}},
		{"volume shell char", createContainerRequest{Image: "nginx", Volumes: []string{"/a:/b;c"}}},
	}
	for _, tc := range cases {
		req := tc.req
		if err := validateCreateSpec(&req); err == nil {
			t.Fatalf("%s: should be rejected but passed", tc.name)
		}
	}
}

func TestBuildDockerRunCmd(t *testing.T) {
	req := &createContainerRequest{
		Image:   "redis:latest",
		Name:    "cache",
		Ports:   []string{"6379:6379"},
		Env:     []string{"X=1"},
		Volumes: []string{"data:/data"},
		Restart: "always",
		Command: "redis-server --appendonly yes",
	}
	got := buildDockerRunCmd(req)
	want := []string{
		"docker", "run", "-d",
		"--name", "cache",
		"--restart", "always",
		"-p", "6379:6379",
		"-e", "X=1",
		"-v", "data:/data",
		"redis:latest",
		"redis-server", "--appendonly", "yes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildDockerRunCmd =\n%v\nwant\n%v", got, want)
	}
}

func TestBuildDockerRunCmd_Minimal(t *testing.T) {
	got := buildDockerRunCmd(&createContainerRequest{Image: "nginx"})
	want := []string{"docker", "run", "-d", "nginx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("minimal = %v, want %v", got, want)
	}
}
