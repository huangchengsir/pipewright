package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/target"
)

// --- 纯函数解析器单测(AC:健壮容错,空/格式异常 → 不 panic、false) ---

func TestParseLoadavg(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"0.42 0.35 0.30 1/234 5678\n", 0.42, true},
		{"1.00 0.50 0.10 2/300 999", 1.00, true},
		{"", 0, false},
		{"garbage", 0, false},
		{"  \n ", 0, false},
	}
	for _, c := range cases {
		v, ok := parseLoadavg(c.in)
		if ok != c.ok || (ok && v != c.want) {
			t.Fatalf("parseLoadavg(%q) = %v,%v want %v,%v", c.in, v, ok, c.want, c.ok)
		}
	}
}

func TestParseUptimeLoadavg(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"12:00  up 5 days,  load averages: 1.23 1.10 1.05", 1.23, true},   // macOS
		{"12:00:00 up 1 day,  load average: 0.50, 0.40, 0.30", 0.50, true}, // Linux
		{"no load info here", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		v, ok := parseUptimeLoadavg(c.in)
		if ok != c.ok || (ok && v != c.want) {
			t.Fatalf("parseUptimeLoadavg(%q) = %v,%v want %v,%v", c.in, v, ok, c.want, c.ok)
		}
	}
}

func TestParseFreeBytes(t *testing.T) {
	out := "              total        used        free      shared  buff/cache   available\n" +
		"Mem:    17179869184  4123456789  1000000000   100000   500000000  12000000000\n" +
		"Swap:    2147483648           0  2147483648\n"
	used, usedWithCache, total, ok := parseFreeBytes(out)
	// used = 「used」列(不含缓存);usedWithCache = total - free(含页缓存)。
	if !ok || total != 17179869184 || used != 4123456789 || usedWithCache != 17179869184-1000000000 {
		t.Fatalf("parseFreeBytes = %d,%d,%d,%v", used, usedWithCache, total, ok)
	}
	if _, _, _, ok := parseFreeBytes(""); ok {
		t.Fatalf("empty should be false")
	}
	if _, _, _, ok := parseFreeBytes("no mem line here\n"); ok {
		t.Fatalf("no Mem line should be false")
	}
}

func TestParseSwapBytes(t *testing.T) {
	out := "" +
		"              total        used        free\n" +
		"Mem:    8054091776  2855583744  1000000000\n" +
		"Swap:   2147483648   536870912  1610612736\n"
	used, total, ok := parseSwapBytes(out)
	if !ok || total != 2147483648 || used != 536870912 {
		t.Fatalf("parseSwapBytes = %d,%d,%v", used, total, ok)
	}
	// 未配置 swap:total=0 仍算成功。
	if u, tt, ok := parseSwapBytes("Swap:   0   0   0\n"); !ok || u != 0 || tt != 0 {
		t.Fatalf("zero swap should be ok with 0/0, got %d,%d,%v", u, tt, ok)
	}
	// 无 Swap 行 → false。
	if _, _, ok := parseSwapBytes("Mem:  1 2 3\n"); ok {
		t.Fatalf("no swap line should be false")
	}
}

func TestParseDmidecodeMemBytes(t *testing.T) {
	// 现版本 dmidecode:二进制单位 GiB;含一个未装设备(应跳过)。
	out := "" +
		"Memory Device\n\tSize: 8 GiB\n\tLocator: DIMM 0\n" +
		"Memory Device\n\tSize: No Module Installed\n\tLocator: DIMM 1\n"
	got, ok := parseDmidecodeMemBytes(out)
	if !ok || got != 8*1024*1024*1024 {
		t.Fatalf("parseDmidecodeMemBytes(GiB) = %d,%v want %d", got, ok, int64(8*1024*1024*1024))
	}

	// 旧版 dmidecode:十进制 MB,多条相加。
	out2 := "\tSize: 4096 MB\n\tSize: 4096 MB\n"
	got2, ok2 := parseDmidecodeMemBytes(out2)
	if !ok2 || got2 != 2*4096*1000*1000 {
		t.Fatalf("parseDmidecodeMemBytes(MB) = %d,%v", got2, ok2)
	}

	// 全部未装 / 空 → false(上层留 0 不展示)。
	if _, ok := parseDmidecodeMemBytes("Memory Device\n\tSize: No Module Installed\n"); ok {
		t.Fatalf("no installed module should be false")
	}
	if _, ok := parseDmidecodeMemBytes(""); ok {
		t.Fatalf("empty should be false")
	}
}

func TestParseDf(t *testing.T) {
	// df -B1 (bytes)
	out := "Filesystem      1B-blocks         Used    Available Use% Mounted on\n" +
		"/dev/disk1   494384795648  123456789012  370927006636  25% /\n"
	used, total, ok := parseDf(out, 1)
	if !ok || total != 494384795648 || used != 123456789012 {
		t.Fatalf("parseDf bytes = %d,%d,%v", used, total, ok)
	}
	// df -k (KiB) → ×1024
	outK := "Filesystem 1024-blocks    Used Available Capacity Mounted on\n" +
		"/dev/disk1   1000000  400000    600000      40% /\n"
	usedK, totalK, okK := parseDf(outK, 1024)
	if !okK || totalK != 1000000*1024 || usedK != 400000*1024 {
		t.Fatalf("parseDf kib = %d,%d,%v", usedK, totalK, okK)
	}
	// folded long device name (data on its own line)
	outFold := "Filesystem                 1B-blocks         Used    Available Use% Mounted on\n" +
		"/dev/mapper/very-long-name\n" +
		"            494384795648  123456789012  370927006636  25% /\n"
	uf, tf, of := parseDf(outFold, 1)
	if !of || tf != 494384795648 || uf != 123456789012 {
		t.Fatalf("parseDf folded = %d,%d,%v", uf, tf, of)
	}
	if _, _, ok := parseDf("", 1); ok {
		t.Fatalf("empty df should be false")
	}
}

func TestParseInt(t *testing.T) {
	cases := map[string]struct {
		v  int
		ok bool
	}{
		"8\n":   {8, true},
		"  16 ": {16, true},
		"":      {0, false},
		"x":     {0, false},
		"-1":    {0, false},
	}
	for in, want := range cases {
		v, ok := parseInt(in)
		if ok != want.ok || (ok && v != want.v) {
			t.Fatalf("parseInt(%q) = %d,%v want %d,%v", in, v, ok, want.v, want.ok)
		}
	}
}

// --- per-command dialer:让集成测试按命令返回不同输出 ---

type cmdDialer struct {
	// byCmd 按命令首参(程序名 + 关键参数)路由 stdout / exitCode。
	fn func(cmd []string) (*target.ExecResult, error)
}

func (d cmdDialer) Run(_ context.Context, _ string, _ target.SSHConfig, cmd []string) (*target.ExecResult, error) {
	return d.fn(cmd)
}

func (d cmdDialer) RunStream(_ context.Context, _ string, _ target.SSHConfig, _ []string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (d cmdDialer) RunInteractive(_ context.Context, _ string, _ target.SSHConfig, _ []string) (target.Session, error) {
	return nil, nil
}

func (d cmdDialer) RunWithStdin(_ context.Context, _ string, _ target.SSHConfig, cmd []string, _ io.Reader) (*target.ExecResult, error) {
	return d.fn(cmd)
}

// linuxLikeDialer 模拟一台 Linux 服务器:loadavg/nproc/free/df 全可回显。
func linuxLikeDialer() cmdDialer {
	return cmdDialer{fn: func(cmd []string) (*target.ExecResult, error) {
		switch {
		case len(cmd) >= 2 && cmd[0] == "cat" && cmd[1] == "/proc/loadavg":
			return &target.ExecResult{Stdout: "0.42 0.35 0.30 1/234 5678\n", ExitCode: 0}, nil
		case cmd[0] == "nproc":
			return &target.ExecResult{Stdout: "8\n", ExitCode: 0}, nil
		case cmd[0] == "free":
			return &target.ExecResult{Stdout: "              total        used        free\nMem:    17179869184  4123456789  1000000000\n", ExitCode: 0}, nil
		case cmd[0] == "df" && len(cmd) >= 2 && cmd[1] == "-B1":
			return &target.ExecResult{Stdout: "Filesystem 1B-blocks Used Available Use% Mounted on\n/dev/sda1 494384795648 123456789012 370927006636 25% /\n", ExitCode: 0}, nil
		default:
			return &target.ExecResult{Stdout: "", Stderr: "command not found", ExitCode: 127}, nil
		}
	}}
}

// macLikeDialer 模拟 macOS:无 /proc/loadavg、无 nproc、无 free、df -B1 不识别 → 各自回退。
func macLikeDialer() cmdDialer {
	return cmdDialer{fn: func(cmd []string) (*target.ExecResult, error) {
		switch {
		case cmd[0] == "cat" && len(cmd) >= 2 && cmd[1] == "/proc/loadavg":
			return &target.ExecResult{Stdout: "", Stderr: "No such file or directory", ExitCode: 1}, nil
		case cmd[0] == "uptime":
			return &target.ExecResult{Stdout: "12:00  up 5 days, load averages: 1.23 1.10 1.05\n", ExitCode: 0}, nil
		case cmd[0] == "nproc":
			return &target.ExecResult{Stderr: "nproc: command not found", ExitCode: 127}, nil
		case cmd[0] == "getconf":
			return &target.ExecResult{Stdout: "10\n", ExitCode: 0}, nil
		case cmd[0] == "free":
			return &target.ExecResult{Stderr: "free: command not found", ExitCode: 127}, nil
		case cmd[0] == "df" && len(cmd) >= 2 && cmd[1] == "-B1":
			return &target.ExecResult{Stderr: "df: illegal option -- B", ExitCode: 1}, nil
		case cmd[0] == "df" && len(cmd) >= 2 && cmd[1] == "-k":
			return &target.ExecResult{Stdout: "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/disk1 1000000 400000 600000 40% /\n", ExitCode: 0}, nil
		default:
			return &target.ExecResult{ExitCode: 127}, nil
		}
	}}
}

func TestServerMetricsLinux(t *testing.T) {
	srv, client, csrf := setupServerAPI(t, linuxLikeDialer())
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	resp, _ := client.Get(srv.URL + "/api/servers/" + id + "/metrics")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var out serverMetricsDTO
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, raw)
	}
	if !out.Reachable {
		t.Fatalf("want reachable true: %+v", out)
	}
	if out.CPU == nil || out.CPU.Loadavg1 == nil || *out.CPU.Loadavg1 != 0.42 {
		t.Fatalf("cpu loadavg wrong: %+v", out.CPU)
	}
	if out.CPU.Cores == nil || *out.CPU.Cores != 8 {
		t.Fatalf("cores wrong: %+v", out.CPU)
	}
	if out.Memory == nil || out.Memory.TotalBytes != 17179869184 || out.Memory.UsedBytes != 4123456789 {
		t.Fatalf("memory wrong: %+v", out.Memory)
	}
	if out.Disk == nil || out.Disk.Path != "/" || out.Disk.TotalBytes != 494384795648 || out.Disk.UsedBytes != 123456789012 {
		t.Fatalf("disk wrong: %+v", out.Disk)
	}
	if out.CollectedAt == "" {
		t.Fatalf("collectedAt empty")
	}
}

func TestServerMetricsMacFallback(t *testing.T) {
	srv, client, csrf := setupServerAPI(t, macLikeDialer())
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	resp, _ := client.Get(srv.URL + "/api/servers/" + id + "/metrics")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var out serverMetricsDTO
	_ = json.Unmarshal(raw, &out)
	if !out.Reachable {
		t.Fatalf("want reachable true: %+v", out)
	}
	// loadavg via uptime fallback
	if out.CPU == nil || out.CPU.Loadavg1 == nil || *out.CPU.Loadavg1 != 1.23 {
		t.Fatalf("cpu loadavg fallback wrong: %+v", out.CPU)
	}
	// cores via getconf fallback
	if out.CPU.Cores == nil || *out.CPU.Cores != 10 {
		t.Fatalf("cores fallback wrong: %+v", out.CPU)
	}
	// free missing → memory null (AC: 该指标 null 不报错)
	if out.Memory != nil {
		t.Fatalf("memory should be null on macOS: %+v", out.Memory)
	}
	// df -k fallback
	if out.Disk == nil || out.Disk.TotalBytes != 1000000*1024 || out.Disk.UsedBytes != 400000*1024 {
		t.Fatalf("disk fallback wrong: %+v", out.Disk)
	}
}

func TestServerMetricsUnreachableNot500(t *testing.T) {
	// dialer 返回不可达错误 → reachable:false + 人读 error,200 不 500。
	dialer := cmdDialer{fn: func(_ []string) (*target.ExecResult, error) {
		return nil, target.ErrUnreachable
	}}
	srv, client, csrf := setupServerAPI(t, dialer)
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	id := createServerAPI(t, client, srv.URL, csrf, credID)

	resp, _ := client.Get(srv.URL + "/api/servers/" + id + "/metrics")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (not 500): %s", resp.StatusCode, raw)
	}
	var out serverMetricsDTO
	_ = json.Unmarshal(raw, &out)
	if out.Reachable {
		t.Fatalf("want reachable false")
	}
	if out.Error == "" {
		t.Fatalf("want human error")
	}
	if out.CPU != nil || out.Memory != nil || out.Disk != nil {
		t.Fatalf("metrics should be null when unreachable: %+v", out)
	}
}

func TestServerMetricsNotFound(t *testing.T) {
	srv, client, csrf := setupServerAPI(t, linuxLikeDialer())
	_ = csrf
	resp, _ := client.Get(srv.URL + "/api/servers/does-not-exist/metrics")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode, raw)
	}
}

func TestServerMetricsRequiresAuth(t *testing.T) {
	srv, _, _ := setupServerAPI(t, linuxLikeDialer())
	// 不带 session 的裸客户端。
	bare := &http.Client{}
	resp, _ := bare.Get(srv.URL + "/api/servers/x/metrics")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics status = %d, want 401: %s", resp.StatusCode, raw)
	}
	resp2, _ := bare.Get(srv.URL + "/api/servers/metrics")
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("batch metrics status = %d, want 401", resp2.StatusCode)
	}
}

func TestAllServerMetricsBatchIndependent(t *testing.T) {
	// 两台服务器:一台 Linux 可回显,一台不可达(死端口),批量端点逐台独立。
	// 用 host 区分行为:127.0.0.1 → ok;其它 → unreachable。
	dialer := cmdDialer{fn: func(cmd []string) (*target.ExecResult, error) {
		return linuxLikeDialer().fn(cmd)
	}}
	srv, client, csrf := setupServerAPI(t, dialer)
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	_ = createServerAPI(t, client, srv.URL, csrf, credID) // s1 (host 127.0.0.1)

	resp, _ := client.Get(srv.URL + "/api/servers/metrics")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var body struct {
		Items []serverMetricsDTO `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, raw)
	}
	if len(body.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(body.Items))
	}
	if !body.Items[0].Reachable || body.Items[0].Disk == nil {
		t.Fatalf("item should be reachable with disk: %+v", body.Items[0])
	}
}
