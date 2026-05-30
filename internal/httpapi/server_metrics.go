package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/target"
)

// Story 6.1(FR-15):多机状态总览 —— 服务器层资源指标(CPU 负载/核数、内存 used/total、
// 磁盘 used/total),经 SSH 跑**固定白名单只读命令**采集,解析为结构化指标。
//
// AC-SEC-02 核心:采集命令是**纯静态命令 array**,绝不接受任何用户输入拼接 —— 无注入面。
// 指标无敏感信息。
//
// 容错纪律:
//   - 某台不可达 / 认证失败 → 该台 reachable:false + 人读 error,**不 500**,不连累其它台。
//   - 单个指标命令缺失 / 输出格式异常 → 该指标 null(指针为 nil),不报错、不影响其它指标
//     (跨平台 best-effort:Linux 优先,macOS/不支持 → 该指标 null)。
//   - 批量端点逐台并行采集,有界并发(信号量防 N 台同时 SSH 打爆)。

const (
	// metricsConcurrency 是批量采集的最大并发 SSH 数(有界,防打爆)。
	metricsConcurrency = 6
	// metricsCmdTimeout 是单台采集的整体超时(三条只读命令串行跑,够宽松)。
	metricsCmdTimeout = 15 * time.Second
	// metricsOutMax 是单条命令 stdout 解析前的截断上限(防超大输出撑爆内存;指标输出本就极小)。
	metricsOutMax = 64 * 1024
)

// 采集命令(AC-SEC-02:固定静态 array,绝不含任何用户输入)。
//   - loadavg:`cat /proc/loadavg`(Linux);macOS 无 /proc → 回退 `uptime` 解析。
//   - cores:`nproc`(Linux);缺失则回退 `getconf _NPROCESSORS_ONLN`(跨平台,含 macOS)。
//   - memory:`free -b`(Linux);macOS 无 free → 该指标 null(契约允许)。
//   - disk:`df -B1 /`(Linux);macOS 不识别 -B1 → 回退 `df -k /`(KiB)再换算字节。跨平台可真回显。
var (
	cmdLoadavg    = []string{"cat", "/proc/loadavg"}
	cmdUptime     = []string{"uptime"}
	cmdNproc      = []string{"nproc"}
	cmdGetconfCPU = []string{"getconf", "_NPROCESSORS_ONLN"}
	cmdFreeBytes  = []string{"free", "-b"}
	cmdDfBytes    = []string{"df", "-B1", "/"}
	cmdDfKiB      = []string{"df", "-k", "/"}
)

// cpuMetric / memoryMetric / diskMetric 是各维度指标 DTO(冻结契约字段形状)。
// 任一维度采集/解析失败 → 整段为 null(指针 nil),不影响其它维度。
type cpuMetric struct {
	Loadavg1 *float64 `json:"loadavg1"`
	Cores    *int     `json:"cores"`
}

type memoryMetric struct {
	UsedBytes  int64 `json:"usedBytes"`
	TotalBytes int64 `json:"totalBytes"`
}

type diskMetric struct {
	Path       string `json:"path"`
	UsedBytes  int64  `json:"usedBytes"`
	TotalBytes int64  `json:"totalBytes"`
}

// serverMetricsDTO 是单台服务器指标响应体(冻结契约)。
//   - reachable:false 时 cpu/memory/disk 为 null,error 人读非空。
//   - reachable:true 时各指标独立:解析失败的维度为 null,其余正常。
type serverMetricsDTO struct {
	ServerID    string        `json:"serverId"`
	Reachable   bool          `json:"reachable"`
	Error       string        `json:"error"`
	CPU         *cpuMetric    `json:"cpu"`
	Memory      *memoryMetric `json:"memory"`
	Disk        *diskMetric   `json:"disk"`
	CollectedAt string        `json:"collectedAt"`
}

// collectServerMetrics 对单台服务器采集指标。永不返回 error:不可达/失败均落到
// DTO.reachable=false + 人读 error(批量端点据此让单台失败不连累全局)。
// 第二个返回值是「定位类」错误(服务器/凭据不存在、保险库未配),仅供单台端点映射 422/503;
// 批量端点忽略它(逐台独立,定位类对某台亦只表现为该台 reachable:false)。
func collectServerMetrics(ctx context.Context, svc target.Service, id string) (serverMetricsDTO, error) {
	out := serverMetricsDTO{ServerID: id, CollectedAt: time.Now().UTC().Format(time.RFC3339)}

	cctx, cancel := context.WithTimeout(ctx, metricsCmdTimeout)
	defer cancel()

	// 先探一条命令确认可达(用 loadavg/uptime 的探测当连通性判断)。任何定位/连接/认证类失败
	// → reachable:false。后续各指标命令独立,失败仅该指标 null。
	cpu, reachErr := collectCPU(cctx, svc, id)
	if reachErr != nil {
		out.Reachable = false
		out.Error = humanMetricsError(reachErr)
		if isLocateError(reachErr) {
			return out, reachErr
		}
		return out, nil
	}
	out.Reachable = true
	out.CPU = cpu
	out.Memory = collectMemory(cctx, svc, id)
	out.Disk = collectDisk(cctx, svc, id)
	return out, nil
}

// isLocateError 判定是否为「定位类」错误(服务器/凭据不存在、保险库未配)——这类该映射
// 422/503 而非 reachable:false。
func isLocateError(err error) bool {
	return errors.Is(err, target.ErrNotFound) ||
		errors.Is(err, target.ErrCredentialNotFound) ||
		errors.Is(err, target.ErrVaultUnconfigured)
}

// runMetricCmd 跑一条采集命令并返回截断后的 stdout。第二个返回值是「连接/定位类」错误
// (供 reachable 判定);命令非零退出 / 命令不存在不算连接错误(返回 stdout + nil err,
// 由解析层据空/异常输出降级为 null)。
func runMetricCmd(ctx context.Context, svc target.Service, id string, cmd []string) (string, error) {
	res, err := svc.Exec(ctx, id, cmd)
	if err != nil {
		return "", err
	}
	out := res.Stdout
	if len(out) > metricsOutMax {
		out = out[:metricsOutMax]
	}
	return out, nil
}

// collectCPU 取 CPU 负载 + 核数。loadavg 兼跑连通性探测:其连接/定位类错误向上传递(决定
// reachable)。负载/核数任一解析失败 → 该子字段 nil(但 cpu 段仍返回,不影响 reachable)。
func collectCPU(ctx context.Context, svc target.Service, id string) (*cpuMetric, error) {
	m := &cpuMetric{}

	// loadavg:优先 /proc/loadavg;失败(macOS 无)再尝试 uptime。第一条命令的连接/认证类错误
	// 决定 reachable,故此处把它的 err 上抛。
	out, err := runMetricCmd(ctx, svc, id, cmdLoadavg)
	if err != nil {
		return nil, err
	}
	if v, ok := parseLoadavg(out); ok {
		m.Loadavg1 = &v
	} else {
		// /proc/loadavg 不存在(命令非零退出,out 多为空)→ 回退 uptime(macOS 等)。
		if up, upErr := runMetricCmd(ctx, svc, id, cmdUptime); upErr == nil {
			if v, ok := parseUptimeLoadavg(up); ok {
				m.Loadavg1 = &v
			}
		}
	}

	// cores:nproc 优先,失败回退 getconf。
	if np, npErr := runMetricCmd(ctx, svc, id, cmdNproc); npErr == nil {
		if c, ok := parseInt(np); ok {
			m.Cores = &c
		}
	}
	if m.Cores == nil {
		if gc, gcErr := runMetricCmd(ctx, svc, id, cmdGetconfCPU); gcErr == nil {
			if c, ok := parseInt(gc); ok {
				m.Cores = &c
			}
		}
	}
	return m, nil
}

// collectMemory 取内存 used/total(字节)。解析失败(如 macOS 无 free)→ nil。
func collectMemory(ctx context.Context, svc target.Service, id string) *memoryMetric {
	out, err := runMetricCmd(ctx, svc, id, cmdFreeBytes)
	if err != nil {
		return nil
	}
	used, total, ok := parseFreeBytes(out)
	if !ok {
		return nil
	}
	return &memoryMetric{UsedBytes: used, TotalBytes: total}
}

// collectDisk 取根分区 used/total(字节)。`df -B1 /` 优先;macOS 不识别 -B1 → 回退 `df -k /`
// 换算字节(KiB×1024)。解析失败 → nil。
func collectDisk(ctx context.Context, svc target.Service, id string) *diskMetric {
	if out, err := runMetricCmd(ctx, svc, id, cmdDfBytes); err == nil {
		if used, total, ok := parseDf(out, 1); ok {
			return &diskMetric{Path: "/", UsedBytes: used, TotalBytes: total}
		}
	}
	// 回退 df -k(KiB)。
	if out, err := runMetricCmd(ctx, svc, id, cmdDfKiB); err == nil {
		if used, total, ok := parseDf(out, 1024); ok {
			return &diskMetric{Path: "/", UsedBytes: used, TotalBytes: total}
		}
	}
	return nil
}

// --- 解析器(纯函数,可单测;空/格式异常一律 ok=false,绝不 panic) ---

// parseLoadavg 解析 `/proc/loadavg`,取第一个字段(1 分钟负载)。
// 例:`0.42 0.35 0.30 1/234 5678` → 0.42。
func parseLoadavg(s string) (float64, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// parseUptimeLoadavg 从 `uptime` 输出解析 1 分钟负载(macOS/Linux 通用)。
// 例:`... load averages: 1.23 1.10 1.05`(macOS)或 `... load average: 1.23, 1.10, 1.05`(Linux)。
func parseUptimeLoadavg(s string) (float64, bool) {
	low := strings.ToLower(s)
	idx := strings.Index(low, "load average")
	if idx < 0 {
		return 0, false
	}
	rest := s[idx:]
	// 跳到冒号后。
	if c := strings.Index(rest, ":"); c >= 0 {
		rest = rest[c+1:]
	}
	// 逗号/空白都当分隔。
	rest = strings.ReplaceAll(rest, ",", " ")
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// parseInt 解析单个整数(nproc / getconf 输出),裁剪空白。
func parseInt(s string) (int, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, false
	}
	// 取第一行第一个 token(防多余输出)。
	fields := strings.Fields(t)
	v, err := strconv.Atoi(fields[0])
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// parseFreeBytes 解析 `free -b` 输出,取 Mem 行的 total/used(字节)。
// 形如:
//
//	              total        used        free      shared  buff/cache   available
//	Mem:    17179869184  4123456789  ...
//
// 取 Mem 行的第 1 列 total、第 2 列 used。容错:列不足/非数字 → false。
func parseFreeBytes(s string) (used, total int64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(fields[0]), "mem") {
			continue
		}
		t, err1 := strconv.ParseInt(fields[1], 10, 64)
		u, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 != nil || err2 != nil || t <= 0 || u < 0 {
			return 0, 0, false
		}
		return u, t, true
	}
	return 0, 0, false
}

// parseDf 解析 `df` 输出的根分区行,取 total(第 2 列)/ used(第 3 列),乘以 unit 化为字节。
// `df -B1 /` 时 unit=1(已是字节);`df -k /` 时 unit=1024(KiB)。
// 形如:
//
//	Filesystem     1B-blocks       Used   Available Use% Mounted on
//	/dev/disk1  494384795648  ...
//
// df 可能把长设备名折行;故扫描所有非表头行,取**首个含 ≥4 个数值列**的数据行。容错 → false。
func parseDf(s string, unit int64) (used, total int64, ok bool) {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// 跳过表头(首列 Filesystem)。
		if i == 0 || strings.EqualFold(fields[0], "Filesystem") {
			continue
		}
		// df 折行时数据可能在下一行,字段数少；规整后期望:[fs] total used avail use% mounted
		// 或折行后:total used avail use% mounted(无 fs 列)。统一找连续两个可解析为大整数的列。
		t, u, found := extractDfTotalsUsed(fields)
		if found {
			if t <= 0 || u < 0 {
				return 0, 0, false
			}
			return u * unit, t * unit, true
		}
	}
	return 0, 0, false
}

// extractDfTotalsUsed 从 df 数据行字段里取 total/used。标准布局:
// Filesystem total used avail capacity ... → total=fields[1], used=fields[2]。
// 折行布局(首列已被折到上一行):total used avail ... → total=fields[0], used=fields[1]。
// 用启发式:找首个连续两列都是纯数字(且后续还有列)的位置当 total/used。
func extractDfTotalsUsed(fields []string) (total, used int64, ok bool) {
	for i := 0; i+1 < len(fields); i++ {
		t, e1 := strconv.ParseInt(fields[i], 10, 64)
		u, e2 := strconv.ParseInt(fields[i+1], 10, 64)
		if e1 == nil && e2 == nil {
			return t, u, true
		}
	}
	return 0, 0, false
}

// humanMetricsError 把领域错误映射为人读文案(绝不含凭据明文/内部栈)。
func humanMetricsError(err error) string {
	switch {
	case errors.Is(err, target.ErrAuth):
		return "SSH 认证失败:密钥或口令无效,或无登录权限"
	case errors.Is(err, target.ErrUnreachable):
		return "无法连接服务器:端口未开放、主机不可达或超时"
	case errors.Is(err, target.ErrInvalidCredential):
		return "凭据不是可用的 SSH 私钥或口令"
	case errors.Is(err, context.DeadlineExceeded):
		return "采集超时"
	default:
		return "采集指标失败:连接或命令执行错误"
	}
}

// --- HTTP handlers ---

// makeServerMetricsHandler 返回 GET /api/servers/{id}/metrics(认证,只读)。
// 服务器不存在/凭据不存在/保险库未配 → 标准状态码;连接/认证/采集失败 → 200 + reachable:false,不 500。
func makeServerMetricsHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		// 先确认服务器存在(404 在写任何 200 体之前)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}
		out, locErr := collectServerMetrics(r.Context(), svc, id)
		// 定位类错误(凭据不存在 / 保险库未配)→ 走标准映射(422/503),而非 reachable:false。
		if locErr != nil {
			writeServerError(w, locErr)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// makeAllServerMetricsHandler 返回 GET /api/servers/metrics(认证,只读;批量)。
// 逐台并行采集(有界并发),各自独立:某台失败仅该台 reachable:false,不连累其它台、不 500。
func makeAllServerMetricsHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		servers, err := svc.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		items := make([]serverMetricsDTO, len(servers))
		sem := make(chan struct{}, metricsConcurrency)
		var wg sync.WaitGroup
		for i, srv := range servers {
			wg.Add(1)
			go func(i int, id string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// 批量逐台独立:定位类错误对某台亦只表现为该台 reachable:false(忽略 locErr)。
				items[i], _ = collectServerMetrics(r.Context(), svc, id)
			}(i, srv.ID)
		}
		wg.Wait()
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
