package deploy

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/target"
)

func TestDisplayCmd(t *testing.T) {
	cases := []struct {
		name string
		cmd  []string
		want string
	}{
		{"sh -c shows script", []string{"sh", "-c", "docker build -t app .\ndocker run app", "/cur"}, "docker build -t app .\ndocker run app"},
		{"mask -e KV", []string{"docker", "run", "-e", "DB_PASSWORD=s3cr3t", "-p", "8080:8080", "app"}, "docker run -e DB_PASSWORD=*** -p 8080:8080 app"},
		{"mask --env KV", []string{"docker", "run", "--env", "TOKEN=abc", "app"}, "docker run --env TOKEN=*** app"},
		{"mask --password value", []string{"docker", "login", "--password", "hunter2", "reg.io"}, "docker login --password *** reg.io"},
		{"port not masked", []string{"docker", "run", "-p", "443:443", "app"}, "docker run -p 443:443 app"},
		{"plain join", []string{"tar", "xf", "deployctx.tar"}, "tar xf deployctx.tar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := displayCmd(c.cmd); got != c.want {
				t.Fatalf("displayCmd(%v) = %q, want %q", c.cmd, got, c.want)
			}
		})
	}
}

// TestExecStreamsToCmdLog 验证:挂了 WithCmdLog → s.exec 把命令 + stdout/stderr 回流;未挂 → 不 panic、纯透传。
func TestExecStreamsToCmdLog(t *testing.T) {
	tgt := &stubTarget{
		servers: map[string]*target.Server{},
		execFn: func(_ string, _ []string) (*target.ExecResult, error) {
			return &target.ExecResult{Stdout: "BUILD SUCCESS\n", Stderr: "", ExitCode: 0}, nil
		},
	}
	svc := New(tgt, nil).(*service)

	var mu sync.Mutex
	var lines []string
	ctx := WithCmdLog(context.Background(), func(stream, text string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, stream+"|"+text)
	})

	if _, err := svc.exec(ctx, "srv1", []string{"sh", "-c", "docker build -t app .", "/cur"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "stdout|$ docker build -t app .") {
		t.Fatalf("command not echoed: %q", joined)
	}
	if !strings.Contains(joined, "stdout|BUILD SUCCESS") {
		t.Fatalf("stdout not streamed: %q", joined)
	}

	// 未挂 cmdlog:纯透传,不 panic、不记录。
	if _, err := svc.exec(context.Background(), "srv1", []string{"echo", "hi"}); err != nil {
		t.Fatalf("exec without sink: %v", err)
	}
}
