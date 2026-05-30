package deploy

// healthcheck.go 实现「部署后健康门控」(FR-12 健康检查侧;Story 4.3)。
//
// 部署命令在某台目标机执行成功后,可选地经 **同一条 target.Exec 链路** 在该机做健康探测,
// 只有探测通过才算该机部署成功。探测有两种:
//   - http   : 构造 `curl -fsS --max-time <T> <url>` array,看 curl 退出码(-f 保证 4xx/5xx 即非零)。
//   - command: 直接跑调用方给定的 cmd array(看退出码)。
//
// 全程 **array 化**([]string)经 target.Exec,绝不在平台侧拼接 shell(AC-SEC-02);
// url / 命令各参数作为独立 array 元素传入。探测支持重试 N 次 + 间隔 + 单次 ctx 超时,
// 重试耗尽仍失败 → 该机 failed + 人读 message(绝无明文密钥)。
//
// 向后兼容:HealthCheck 为 nil 或 type=none → 完全跳过(等同 4-2 行为)。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 健康检查类型枚举(冻结契约)。
const (
	// HealthCheckNone 表示不做健康检查(缺省 / 向后兼容 4-2)。
	HealthCheckNone = "none"
	// HealthCheckHTTP 表示经 curl 做 HTTP 探测。
	HealthCheckHTTP = "http"
	// HealthCheckCommand 表示直接跑给定命令探测。
	HealthCheckCommand = "command"
)

// 健康检查默认值与上限(防滥用 / DoS 目标机)。
const (
	defaultHealthRetries  = 3
	defaultHealthInterval = 3 * time.Second
	defaultHealthTimeout  = 5 * time.Second

	maxHealthRetries  = 20
	maxHealthTimeout  = 60 * time.Second
	maxHealthInterval = 60 * time.Second // code-review P7:间隔上限,防 sleep 炸弹
)

// HealthCheck 是一次部署后健康探测的配置(对齐 deploy 请求 deployConfig.healthCheck 子结构,冻结)。
//
// 形状定死:type/url/command/retries/intervalSeconds/timeoutSeconds。4-4 零停机切换在健康门控
// 通过后才切流量、失败触发回滚,消费本结果不改形状。
type HealthCheck struct {
	// Type 是探测类型:none | http | command。空 / none → 跳过。
	Type string
	// URL 是 HTTP 探测目标(type=http 时用)。
	URL string
	// Command 是命令探测的 array(type=command 时用;AC-SEC-02 不拼 shell)。
	Command []string
	// Retries 是探测最大尝试次数(<=0 → 默认 3;上限 20)。
	Retries int
	// IntervalSeconds 是两次尝试之间的间隔秒数(<0 → 默认 3s)。
	IntervalSeconds int
	// TimeoutSeconds 是单次探测的超时秒数(<=0 → 默认 5s;上限 60s)。
	TimeoutSeconds int
}

// enabled 判定该健康检查是否需要执行(非 nil 且 type 为 http/command)。
func (hc *HealthCheck) enabled() bool {
	if hc == nil {
		return false
	}
	switch hc.Type {
	case HealthCheckHTTP, HealthCheckCommand:
		return true
	default:
		return false
	}
}

// retries 归一并夹紧最大尝试次数(<=0 → 默认;> 上限 → 上限)。
func (hc *HealthCheck) retries() int {
	n := hc.Retries
	if n <= 0 {
		n = defaultHealthRetries
	}
	if n > maxHealthRetries {
		n = maxHealthRetries
	}
	return n
}

// interval 归一并夹紧重试间隔(<0 → 默认;0 合法表示无间隔;> 上限 → 上限)。
// code-review P7:retries/timeout 都夹了上限,interval 之前漏夹 → 可传 86400 当 sleep 炸弹;
// 补 maxHealthInterval 上限,三参数夹紧自洽。
func (hc *HealthCheck) interval() time.Duration {
	if hc.IntervalSeconds < 0 {
		return defaultHealthInterval
	}
	d := time.Duration(hc.IntervalSeconds) * time.Second
	if d > maxHealthInterval {
		d = maxHealthInterval
	}
	return d
}

// timeout 归一并夹紧单次探测超时(<=0 → 默认;> 上限 → 上限)。
func (hc *HealthCheck) timeout() time.Duration {
	if hc.TimeoutSeconds <= 0 {
		return defaultHealthTimeout
	}
	d := time.Duration(hc.TimeoutSeconds) * time.Second
	if d > maxHealthTimeout {
		d = maxHealthTimeout
	}
	return d
}

// probeCommand 构造单次探测要跑的 array 命令(AC-SEC-02:各参数独立元素,不拼 shell)。
//   - http   : curl -fsS --max-time <T> <url>(-f:HTTP 错误码即非零退出;-sS:静默但保留错误)。
//   - command: 原样返回调用方给定的 cmd array。
func (hc *HealthCheck) probeCommand() ([]string, error) {
	switch hc.Type {
	case HealthCheckHTTP:
		url := strings.TrimSpace(hc.URL)
		if url == "" {
			return nil, errors.New("健康检查 http 类型缺少 url")
		}
		maxTime := strconv.Itoa(int(hc.timeout().Seconds()))
		return []string{"curl", "-fsS", "--max-time", maxTime, url}, nil
	case HealthCheckCommand:
		if len(hc.Command) == 0 {
			return nil, errors.New("健康检查 command 类型缺少命令")
		}
		// 复制一份,避免外部切片被本层意外共享 / 篡改。
		cmd := make([]string, len(hc.Command))
		copy(cmd, hc.Command)
		return cmd, nil
	default:
		return nil, fmt.Errorf("不支持的健康检查类型:%s", hc.Type)
	}
}

// runHealthCheck 在指定目标机上做健康探测:重试 N 次 + 间隔 + 单次 ctx 超时。
//
// 经注入的 s.targets.Exec(同部署链路)执行 array 命令。任一次尝试「无执行错误且退出码为 0」
// 即判通过,返回 nil;否则间隔后重试;重试耗尽仍失败 → 返回人读错误(绝无明文密钥)。
// 外层 ctx 取消 / 超时 → 立即停止并返回。
func (s *service) runHealthCheck(ctx context.Context, serverID string, hc *HealthCheck) error {
	cmd, berr := hc.probeCommand()
	if berr != nil {
		return berr
	}
	attempts := hc.retries()
	interval := hc.interval()
	timeout := hc.timeout()

	var lastMsg string
	for i := 0; i < attempts; i++ {
		// 外层 ctx 先于本次尝试取消 → 立即返回(不再无谓探测)。
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("健康检查中止:%s", healthCtxReason(err))
		}

		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		out, eerr := s.targets.Exec(attemptCtx, serverID, cmd)
		cancel()

		switch {
		case eerr != nil:
			// 执行 / 连接 / 超时类错误:人读化(target 层错误体已无凭据明文)。
			lastMsg = humanExecError(eerr)
		case out != nil && out.ExitCode != 0:
			// 探测命令非零退出(curl -f 命中 4xx/5xx,或 command 自身失败):回显 stderr 摘要。
			detail := truncate(strings.TrimSpace(out.Stderr))
			if detail != "" {
				lastMsg = fmt.Sprintf("退出码 %d:%s", out.ExitCode, detail)
			} else {
				lastMsg = fmt.Sprintf("退出码 %d", out.ExitCode)
			}
		default:
			// 通过。
			return nil
		}

		// 还有后续尝试 → 间隔后重试(间隔期间也响应 ctx 取消)。
		if i < attempts-1 && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("健康检查中止:%s", healthCtxReason(ctx.Err()))
			case <-timer.C:
			}
		}
	}

	return fmt.Errorf("健康检查失败(已重试 %d 次):%s", attempts, lastMsg)
}

// healthCtxReason 把 ctx 错误人读化(绝不泄漏内部细节)。
func healthCtxReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "整体超时"
	}
	return "已取消"
}
