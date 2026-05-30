package httpapi

// server_ops_e2e_test.go 是 Epic 6 运维的**真 e2e**:对一台真 alpine+sshd 容器(真 Linux,有真
// /proc)真跑指标采集 / 服务日志 tail / 服务操作命令 —— macOS localhost 证不到的 happy path
//(本机无 /proc/loadavg、free 等;容器里真有,能验解析出真实值而非回退桩)。
//
// 默认 SKIP(PIPEWRIGHT_E2E_DEPLOY=1 启用)。跑法:
//
//	PIPEWRIGHT_E2E_DEPLOY=1 go test ./internal/httpapi/ -run E2E -v
//
// 容器内无 systemd(非 PID1)、无 docker → 服务操作(systemctl/docker)不验真生效;改验其依赖的
// target.Exec array 化执行 + 命令 array 防注入在真 SSH 上成立(server_ops 的安全基座)。

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestE2EServerMetricsRealProc 验指标采集对真 Linux 容器解析出真实 CPU/内存/磁盘(非回退桩)。
func TestE2EServerMetricsRealProc(t *testing.T) {
	c := startSSHContainer(t)
	svc, id := newE2ETargetService(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dto, locErr := collectServerMetrics(ctx, svc, id)
	if locErr != nil {
		t.Fatalf("collectServerMetrics 定位类错误: %v", locErr)
	}
	if !dto.Reachable {
		t.Fatalf("真容器应可达: error=%q", dto.Error)
	}

	// CPU:真 Linux 有 /proc/loadavg + nproc → loadavg1/cores 非 nil 且合理。
	if dto.CPU == nil || dto.CPU.Loadavg1 == nil || dto.CPU.Cores == nil {
		t.Fatalf("CPU 指标应从真 /proc 解析出非 nil 值: %+v", dto.CPU)
	}
	if *dto.CPU.Loadavg1 < 0 {
		t.Fatalf("loadavg1 不合理: %v", *dto.CPU.Loadavg1)
	}
	if *dto.CPU.Cores < 1 {
		t.Fatalf("cores 应 >=1: %v", *dto.CPU.Cores)
	}

	// 内存:alpine `free -b` 真有 Mem 行 → total>0 且 used 合理(0<=used<=total)。
	if dto.Memory == nil {
		t.Fatalf("内存指标应从真 free -b 解析出非 nil(macOS 无 free 才该 nil)")
	}
	if dto.Memory.TotalBytes <= 0 || dto.Memory.UsedBytes < 0 || dto.Memory.UsedBytes > dto.Memory.TotalBytes {
		t.Fatalf("内存值不合理: used=%d total=%d", dto.Memory.UsedBytes, dto.Memory.TotalBytes)
	}

	// 磁盘:`df -B1 /` 真有根分区 → total>0 且 used 合理。
	if dto.Disk == nil {
		t.Fatalf("磁盘指标应从真 df 解析出非 nil")
	}
	if dto.Disk.TotalBytes <= 0 || dto.Disk.UsedBytes < 0 || dto.Disk.UsedBytes > dto.Disk.TotalBytes {
		t.Fatalf("磁盘值不合理: used=%d total=%d", dto.Disk.UsedBytes, dto.Disk.TotalBytes)
	}

	t.Logf("真指标 OK: load=%.2f cores=%d mem=%d/%d disk=%d/%d",
		*dto.CPU.Loadavg1, *dto.CPU.Cores,
		dto.Memory.UsedBytes, dto.Memory.TotalBytes,
		dto.Disk.UsedBytes, dto.Disk.TotalBytes)
}

// TestE2EServerLogsTailFile 验服务日志 tail:容器内常驻进程真写日志,经 ExecStream(tail -f)读到真实行。
func TestE2EServerLogsTailFile(t *testing.T) {
	c := startSSHContainer(t)
	svc, id := newE2ETargetService(t, c)

	// 容器内起一个常驻进程,每 0.3s 往 /var/log/app.log 追加一行带递增计数的日志。
	logPath := "/var/log/app.log"
	c.ExecDetached(t, "i=0; while true; do i=$((i+1)); echo \"PWLOG line-$i\" >> "+logPath+"; sleep 0.3; done")

	// 等日志文件先有几行(确保 follow 前已有历史)。
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := c.Exec(t, "sh", "-c", "wc -l < "+logPath+" 2>/dev/null || echo 0")
		if n := strings.TrimSpace(out); n != "" && n != "0" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	// follow=true → tail -f;经 ExecStream 真流式读。校验 source/target 先过白名单(file 绝对路径)。
	if err := validateLogTarget("file", logPath); err != nil {
		t.Fatalf("validateLogTarget: %v", err)
	}
	cmd := buildLogCmd("file", logPath, 5, true)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rc, err := svc.ExecStream(ctx, id, cmd)
	if err != nil {
		t.Fatalf("ExecStream tail -f: %v", err)
	}
	defer func() { _ = rc.Close() }()

	// 读取直到见到至少一行 PWLOG(真实行经真 SSH 流回)。
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var acc strings.Builder
		for {
			n, rerr := rc.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if strings.Contains(acc.String(), "PWLOG line-") {
					got <- acc.String()
					return
				}
			}
			if rerr != nil {
				got <- acc.String()
				return
			}
		}
	}()

	select {
	case s := <-got:
		if !strings.Contains(s, "PWLOG line-") {
			t.Fatalf("tail -f 未读到真实日志行: %q", s)
		}
		t.Logf("日志 tail OK: 读到真实行 %q", firstMatch(s, "PWLOG line-"))
	case <-ctx.Done():
		t.Fatal("等待日志行超时(tail -f 未流回真实行)")
	}
}

// TestE2EServiceOpExecMechanism 验服务操作所依赖的 target.Exec array 化执行 + 防注入在真 SSH 上成立。
//
// systemctl/docker 在本容器无法真生效(无 systemd PID1 / 无 docker),故不验「服务真重启」;
// 但 server_ops 的安全基座 = 命令 array 经 target.Exec 各参数 shell 转义后执行,绝不二次解释。
// 这里用 buildServiceCmd 同款 array 经真 SSH 跑,验:(a) array 命令真执行;(b) 注入探针被当字面参数。
func TestE2EServiceOpExecMechanism(t *testing.T) {
	c := startSSHContainer(t)
	svc, id := newE2ETargetService(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// buildServiceCmd 产出 array(此处用 docker 型只为取同款 array 形状;真跑换成容器内可用命令验机制)。
	if got := buildServiceCmd("docker", "restart", "nginx"); len(got) != 3 || got[0] != "docker" {
		t.Fatalf("buildServiceCmd array 形状异常: %v", got)
	}

	// 用 array 经真 SSH 跑一个容器内真存在的命令(echo),验 array 化执行链路真通。
	res, err := svc.Exec(ctx, id, []string{"echo", "service-op-array-ok"})
	if err != nil {
		t.Fatalf("Exec array: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "service-op-array-ok") {
		t.Fatalf("array 化命令未真执行: exit=%d out=%q", res.ExitCode, res.Stdout)
	}

	// 防注入:把 shell 元字符当**字面参数**传给 echo,远端绝不二次解释执行 $(id)。
	probe := "INJ_$(id)"
	res2, err := svc.Exec(ctx, id, []string{"echo", probe})
	if err != nil {
		t.Fatalf("Exec 注入探针: %v", err)
	}
	if strings.Contains(res2.Stdout, "uid=") {
		t.Fatalf("命令注入未防住!`$(id)` 被远端解释执行\n输出: %q", res2.Stdout)
	}
	if !strings.Contains(res2.Stdout, "INJ_$(id)") {
		t.Fatalf("未见字面回显 `$(id)`,无法确认防注入语义\n输出: %q", res2.Stdout)
	}
	t.Logf("服务操作执行机制 OK: array 真执行 + `$(id)` 当字面参数未执行")
}

// firstMatch 取首个含 sub 的行(日志友好)。
func firstMatch(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}
