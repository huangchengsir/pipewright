package httpapi

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/huangjiawei/devopstool/internal/target"
)

// --- 纯函数:target 校验(AC-SEC-02 要害) ---

func TestValidateLogTarget(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		target  string
		wantErr bool
	}{
		{"file 合法绝对路径", "file", "/var/log/app.log", false},
		{"file 相对路径拒", "file", "var/log/app.log", true},
		{"file 含 .. 拒", "file", "/var/log/../../etc/passwd", true},
		{"file 含分号拒", "file", "/var/log/app.log;rm -rf /", true},
		{"file 含管道拒", "file", "/var/log/a|b", true},
		{"file 含命令替换拒", "file", "/var/log/$(whoami)", true},
		{"file 含反引号拒", "file", "/var/log/`id`", true},
		{"file 含空格拒", "file", "/var/log/a b", true},
		{"file 含换行拒", "file", "/var/log/a\nrm", true},
		{"file 空拒", "file", "", true},
		{"journald 合法 unit", "journald", "nginx.service", false},
		{"journald 合法 @ unit", "journald", "ssh@1.service", false},
		{"journald 含分号拒", "journald", "nginx;rm", true},
		{"journald 含空格拒", "journald", "nginx svc", true},
		{"journald 含斜杠拒", "journald", "../etc", true},
		{"docker 合法名", "docker", "my-app_1", false},
		{"docker 含分号拒", "docker", "app;reboot", true},
		{"docker 含斜杠拒", "docker", "a/b", true},
		{"未知 source 拒", "syslog", "x", true},
		{"空 source 拒", "", "/var/log/a", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateLogTarget(c.source, c.target)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateLogTarget(%q,%q) err=%v, wantErr=%v", c.source, c.target, err, c.wantErr)
			}
		})
	}
}

// --- 纯函数:命令 array 构造(不拼 shell) ---

func TestBuildLogCmd(t *testing.T) {
	cases := []struct {
		name   string
		source string
		target string
		lines  int
		follow bool
		want   []string
	}{
		{"file 历史", "file", "/var/log/a.log", 50, false, []string{"tail", "-n", "50", "/var/log/a.log"}},
		{"file 实时", "file", "/var/log/a.log", 50, true, []string{"tail", "-n", "50", "-f", "/var/log/a.log"}},
		{"journald 历史", "journald", "nginx.service", 100, false, []string{"journalctl", "-u", "nginx.service", "-n", "100", "--no-pager"}},
		{"journald 实时", "journald", "nginx.service", 100, true, []string{"journalctl", "-u", "nginx.service", "-n", "100", "--no-pager", "-f"}},
		{"docker 历史", "docker", "web", 30, false, []string{"docker", "logs", "--tail", "30", "web"}},
		{"docker 实时", "docker", "web", 30, true, []string{"docker", "logs", "--tail", "30", "-f", "web"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildLogCmd(c.source, c.target, c.lines, c.follow)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("buildLogCmd = %v, want %v", got, c.want)
			}
		})
	}
}

func TestClampLines(t *testing.T) {
	cases := map[string]int{"": logLinesDefault, "0": logLinesDefault, "-5": logLinesDefault, "abc": logLinesDefault, "50": 50, "5000": logLinesMax}
	for in, want := range cases {
		if got := clampLines(in); got != want {
			t.Fatalf("clampLines(%q) = %d, want %d", in, got, want)
		}
	}
}

// --- HTTP 集成:历史日志真回显 ---

func TestServerLogsHistory(t *testing.T) {
	dialer := stubDialer{res: &target.ExecResult{Stdout: "line1\nline2\nline3\n", ExitCode: 0}}
	srv, client, csrf := setupServerAPI(t, dialer)
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	resp, _ := client.Get(srv.URL + "/api/servers/" + id + "/logs?source=file&target=/var/log/app.log&lines=50")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var out serverLogsDTO
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, raw)
	}
	if out.Source != "file" || out.Target != "/var/log/app.log" {
		t.Fatalf("dto source/target wrong: %+v", out)
	}
	if len(out.Lines) != 3 || out.Lines[0].Text != "line1" || out.Lines[2].Text != "line3" {
		t.Fatalf("lines wrong: %+v", out.Lines)
	}
	if out.Lines[0].TS != nil {
		t.Fatalf("ts should be null")
	}
}

// --- HTTP 集成:非法 target → 400 invalid_log_target ---

func TestServerLogsInvalidTarget(t *testing.T) {
	srv, client, csrf := setupServerAPI(t, stubDialer{res: &target.ExecResult{}})
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	bad := []string{
		"/api/servers/" + id + "/logs?source=file&target=/var/log/../etc/passwd",
		"/api/servers/" + id + "/logs?source=file&target=/var/log/a.log;rm",
		"/api/servers/" + id + "/logs?source=journald&target=nginx;rm",
		"/api/servers/" + id + "/logs?source=evil&target=x",
		"/api/servers/" + id + "/logs/stream?source=file&target=/x;rm",
	}
	for _, u := range bad {
		resp, _ := client.Get(srv.URL + u)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400: %s", u, resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), "invalid_log_target") {
			t.Fatalf("%s body missing invalid_log_target: %s", u, raw)
		}
	}
}

// --- HTTP 集成:SSH 失败 → 200 + error,不 500 ---

func TestServerLogsSSHFailNo500(t *testing.T) {
	dialer := stubDialer{err: target.ErrUnreachable}
	srv, client, csrf := setupServerAPI(t, dialer)
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	resp, _ := client.Get(srv.URL + "/api/servers/" + id + "/logs?source=file&target=/var/log/app.log")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (人读不 500): %s", resp.StatusCode, raw)
	}
	var out serverLogsDTO
	_ = json.Unmarshal(raw, &out)
	if out.Error == "" {
		t.Fatalf("want human error field, got: %s", raw)
	}
	if len(out.Lines) != 0 {
		t.Fatalf("want empty lines on failure")
	}
}

// --- HTTP 集成:命令非零退出(文件不存在)→ 200 + error ---

func TestServerLogsNonZeroExit(t *testing.T) {
	dialer := stubDialer{res: &target.ExecResult{Stderr: "tail: cannot open '/no/such': No such file or directory", ExitCode: 1}}
	srv, client, csrf := setupServerAPI(t, dialer)
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	resp, _ := client.Get(srv.URL + "/api/servers/" + id + "/logs?source=file&target=/no/such")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var out serverLogsDTO
	_ = json.Unmarshal(raw, &out)
	if !strings.Contains(out.Error, "No such file") {
		t.Fatalf("want stderr in error, got: %s", raw)
	}
}

// --- HTTP 集成:服务器不存在 → 404 ---

func TestServerLogsServerNotFound(t *testing.T) {
	srv, client, _ := setupServerAPI(t, stubDialer{res: &target.ExecResult{}})
	resp, _ := client.Get(srv.URL + "/api/servers/no-such-id/logs?source=file&target=/var/log/a.log")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode, raw)
	}
}

// --- HTTP 集成:未认证 → 401 ---

func TestServerLogsUnauthorized(t *testing.T) {
	srv, _, _ := setupServerAPI(t, stubDialer{res: &target.ExecResult{}})
	noAuth := newTestClient(t)
	resp, _ := noAuth.Get(srv.URL + "/api/servers/x/logs?source=file&target=/var/log/a.log")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// --- HTTP 集成:实时 SSE 真流式(logline 事件) ---

func TestServerLogsStreamSSE(t *testing.T) {
	dialer := stubDialer{res: &target.ExecResult{Stdout: "rt-1\nrt-2\nrt-3\n"}}
	srv, client, csrf := setupServerAPI(t, dialer)
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	resp, err := client.Get(srv.URL + "/api/servers/" + id + "/logs/stream?source=file&target=/var/log/app.log")
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// 读取直到拿到 3 条 logline(流在远端"进程退出"后结束)。
	var got []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var m map[string]string
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m) == nil {
				if txt, ok := m["text"]; ok {
					got = append(got, txt)
				}
			}
		}
		if len(got) == 3 {
			break
		}
	}
	if len(got) != 3 || got[0] != "rt-1" || got[2] != "rt-3" {
		t.Fatalf("stream lines = %v, want [rt-1 rt-2 rt-3]", got)
	}
}

// createServerAPI 经 API 建一台服务器,返回 id。
func createServerAPI(t *testing.T, client *http.Client, srvURL, csrf, credID string) string {
	t.Helper()
	body := `{"name":"s1","host":"127.0.0.1","port":22,"user":"deploy","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srvURL+"/api/servers", csrf, body)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var s map[string]any
	_ = json.Unmarshal(raw, &s)
	id, _ := s["id"].(string)
	if id == "" {
		t.Fatalf("create server failed: %s", raw)
	}
	return id
}
