package proxy

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/target"
)

// Caddy 编排常量(每主机一个 Caddy 容器,跑在共享 docker 网络上,按上游容器名路由)。
const (
	// proxyNetwork 是 Caddy 与上游容器共享的 docker 网络名(Caddy 据此按容器名解析上游)。
	proxyNetwork = "pipewright-proxy"
	// caddyContainer 是托管的反代容器名(每主机一个,幂等创建)。
	caddyContainer = "pipewright-caddy"
	// caddyImage 是反代镜像(Caddy v2,自带 LE HTTP-01 自动签发 + 续期)。
	caddyImage = "caddy:2"
	// caddyDataVol 是证书/ACME 账户持久卷(删路由不删卷,避免重签触发 LE 限速)。
	caddyDataVol = "pipewright_caddy_data"
	// caddyConfigVol 是 Caddy 自动持久化配置卷。
	caddyConfigVol = "pipewright_caddy_config"
	// caddyfilePath 是容器内 Caddyfile 路径。
	caddyfilePath = "/etc/caddy/Caddyfile"
	// caddyfileTmpPath 是 apply 时在目标主机上的临时落盘路径(再 docker cp 进容器)。
	caddyfileTmpPath = "/tmp/pipewright-caddyfile"
)

// renderCaddyfile 据一组 enabled 路由生成 Caddyfile 文本(纯函数,可直接单测)。
// 每条路由产一个站点块:`domain {\n    reverse_proxy container:port\n}`。Caddy 见到带域名的
// 站点块即自动经 Let's Encrypt(HTTP-01)签发并续期证书,无需任何额外指令。
// 无 enabled 路由时返回带说明注释的空配置(reload 一份合法空配置,不破坏既有 Caddy 进程)。
func renderCaddyfile(routes []Route) string {
	// 稳定排序:按 domain 升序,保证渲染结果确定(便于测试 + reload 幂等)。
	sorted := make([]Route, len(routes))
	copy(sorted, routes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Domain < sorted[j].Domain })

	var b strings.Builder
	b.WriteString("# 由 Pipewright 自动生成,请勿手改。\n")
	if len(sorted) == 0 {
		b.WriteString("# 当前没有启用的反代路由。\n")
		return b.String()
	}
	for _, r := range sorted {
		renderSite(&b, r)
	}
	return b.String()
}

// renderSite 渲染单条路由的站点块(R2:多域名别名 + 访问控制 + 安全头 + 压缩 + 重定向)。
// 站点块头 = 主域名 + 别名,以 ", " 连接;块内指令按确定顺序输出(IP 黑名单 → 白名单 →
// basic auth → header → encode → redir → reverse_proxy),便于 golden 测试与 reload 幂等。
// 所有进入文本的值已在领域层(validateConfig)严格校验过,渲染处不再二次防注入。
func renderSite(b *strings.Builder, r Route) {
	cfg := r.Config

	// 站点块头:主域名 + 别名。
	header := r.Domain
	for _, a := range cfg.Aliases {
		header += ", " + a
	}
	b.WriteString(header)
	b.WriteString(" {\n")

	// IP 黑名单:命中即 403(优先于白名单,显式拒绝先行)。
	if len(cfg.IPDeny) > 0 {
		b.WriteString("    @denied remote_ip " + strings.Join(cfg.IPDeny, " ") + "\n")
		b.WriteString("    respond @denied 403\n")
	}
	// IP 白名单:不在白名单内即 403(只有列出的网段可访问)。
	if len(cfg.IPAllow) > 0 {
		b.WriteString("    @notallowed not remote_ip " + strings.Join(cfg.IPAllow, " ") + "\n")
		b.WriteString("    respond @notallowed 403\n")
	}
	// Basic Auth:用户名 + bcrypt 哈希(两者都在才渲染)。
	if cfg.BasicAuthUser != "" && cfg.BasicAuthHash != "" {
		b.WriteString("    basic_auth {\n")
		b.WriteString("        " + cfg.BasicAuthUser + " " + cfg.BasicAuthHash + "\n")
		b.WriteString("    }\n")
	}
	// 安全头 + HSTS(任一开启即渲染 header 块)。
	if cfg.HSTS || cfg.SecurityHeaders {
		b.WriteString("    header {\n")
		if cfg.HSTS {
			b.WriteString("        Strict-Transport-Security \"max-age=31536000; includeSubDomains\"\n")
		}
		if cfg.SecurityHeaders {
			b.WriteString("        X-Frame-Options \"DENY\"\n")
			b.WriteString("        X-Content-Type-Options \"nosniff\"\n")
			b.WriteString("        Referrer-Policy \"strict-origin-when-cross-origin\"\n")
		}
		b.WriteString("    }\n")
	}
	// 压缩。
	if cfg.Compression {
		b.WriteString("    encode gzip zstd\n")
	}
	// 自定义重定向(逐条 redir from to status)。
	for _, rd := range cfg.Redirects {
		b.WriteString("    redir " + rd.From + " " + rd.To + " " + strconv.Itoa(rd.Status) + "\n")
	}

	// 反代到上游(始终最后)。
	b.WriteString("    reverse_proxy " + r.UpstreamContainer + ":" + strconv.Itoa(r.UpstreamPort) + "\n")
	b.WriteString("}\n")
}

// ensureCaddy 幂等地在目标主机上保证 Caddy 反代就绪:建共享网络 + 起 Caddy 容器(若缺)。
// 起容器前探测 80/443 占用,冲突即返回人话错误(不强起)。
func ensureCaddy(ctx context.Context, tg target.Service, serverID string) error {
	// 1) 共享网络(存在即跳过)。inspect 非零退出(网络不存在)→ create。
	netInsp, err := tg.Exec(ctx, serverID, []string{"docker", "network", "inspect", proxyNetwork})
	if err != nil {
		return mapExecErr(err)
	}
	if netInsp.ExitCode != 0 {
		res, cerr := tg.Exec(ctx, serverID, []string{"docker", "network", "create", proxyNetwork})
		if cerr != nil {
			return mapExecErr(cerr)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%w:%s", ErrCaddyStart, strings.TrimSpace(firstNonEmpty(res.Stderr, res.Stdout)))
		}
	}

	// 2) Caddy 容器存在则直接返回(幂等)。inspect 成功(exit 0)= 已在。
	insp, err := tg.Exec(ctx, serverID, []string{"docker", "inspect", caddyContainer})
	if err != nil {
		return mapExecErr(err)
	}
	if insp.ExitCode == 0 {
		return nil
	}

	// 3) 起前探测 80/443 占用(避免与既有进程抢端口,LE 校验需 80)。
	if busy, detail := portsBusy(ctx, tg, serverID); busy {
		return fmt.Errorf("%w%s", ErrPortConflict, detail)
	}

	// 4) 起 Caddy 容器(array 不拼 shell)。
	runCmd := []string{
		"docker", "run", "-d",
		"--name", caddyContainer,
		"--restart", "unless-stopped",
		"--network", proxyNetwork,
		"-p", "80:80", "-p", "443:443",
		"-v", caddyDataVol + ":/data",
		"-v", caddyConfigVol + ":/config",
		caddyImage,
		"caddy", "run", "--config", caddyfilePath, "--adapter", "caddyfile",
	}
	res, err := tg.Exec(ctx, serverID, runCmd)
	if err != nil {
		return mapExecErr(err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%w:%s", ErrCaddyStart, strings.TrimSpace(firstNonEmpty(res.Stderr, res.Stdout)))
	}
	return nil
}

// portsBusy 探测目标主机 80/443 是否被(非 Caddy 容器的)进程占用。best-effort:探测命令本身
// 失败(无 ss / 无权限)视为「无法确认占用」→ 不阻断起容器(返回 false)。
func portsBusy(ctx context.Context, tg target.Service, serverID string) (bool, string) {
	res, err := tg.Exec(ctx, serverID, []string{"ss", "-ltn"})
	if err != nil || res == nil || res.ExitCode != 0 {
		return false, ""
	}
	out := res.Stdout
	var hit []string
	for _, p := range []string{":80 ", ":443 "} {
		if strings.Contains(out, p) {
			hit = append(hit, strings.TrimSpace(strings.TrimSuffix(p, " ")))
		}
	}
	if len(hit) == 0 {
		return false, ""
	}
	return true, "(" + strings.Join(hit, "、") + " 已被占用)"
}

// connectUpstream 把上游容器接入共享网络,使 Caddy 能按容器名访问它。
// 已连接(docker 报 "already exists")视为成功(幂等);其余非零退出 → 人话错误。
func connectUpstream(ctx context.Context, tg target.Service, serverID, container string) error {
	res, err := tg.Exec(ctx, serverID, []string{"docker", "network", "connect", proxyNetwork, container})
	if err != nil {
		return mapExecErr(err)
	}
	if res.ExitCode != 0 {
		msg := strings.ToLower(firstNonEmpty(res.Stderr, res.Stdout))
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "already") {
			return nil
		}
		return fmt.Errorf("%w:%s", ErrUpstreamConnect, strings.TrimSpace(firstNonEmpty(res.Stderr, res.Stdout)))
	}
	return nil
}

// applyCaddyfile 把渲染好的 Caddyfile 落到目标主机临时路径 → docker cp 进 Caddy 容器 → reload
// (优雅热加载,零停机)。reload 前先 validate;validate 失败不 reload(不破坏既有可用配置)。
func applyCaddyfile(ctx context.Context, tg target.Service, serverID, content string) error {
	// 1) Upload 到主机临时路径(流式;remotePath 作位置参数,不拼 shell)。
	if err := tg.Upload(ctx, serverID, bytes.NewReader([]byte(content)), caddyfileTmpPath); err != nil {
		return mapExecErr(err)
	}
	// 2) docker cp 进容器。
	cpRes, err := tg.Exec(ctx, serverID, []string{"docker", "cp", caddyfileTmpPath, caddyContainer + ":" + caddyfilePath})
	if err != nil {
		return mapExecErr(err)
	}
	if cpRes.ExitCode != 0 {
		return fmt.Errorf("%w:%s", ErrApply, strings.TrimSpace(firstNonEmpty(cpRes.Stderr, cpRes.Stdout)))
	}
	// 3) validate(best-effort:旧 Caddy 无 validate 子命令时跳过,不阻断 reload)。
	val, err := tg.Exec(ctx, serverID, []string{"docker", "exec", caddyContainer, "caddy", "validate", "--config", caddyfilePath, "--adapter", "caddyfile"})
	if err != nil {
		return mapExecErr(err)
	}
	if val.ExitCode != 0 && !looksUnsupported(val.Stderr) {
		return fmt.Errorf("%w:%s", ErrApply, strings.TrimSpace(firstNonEmpty(val.Stderr, val.Stdout)))
	}
	// 4) reload(优雅热加载)。
	rl, err := tg.Exec(ctx, serverID, []string{"docker", "exec", caddyContainer, "caddy", "reload", "--config", caddyfilePath, "--adapter", "caddyfile"})
	if err != nil {
		return mapExecErr(err)
	}
	if rl.ExitCode != 0 {
		return fmt.Errorf("%w:%s", ErrApply, strings.TrimSpace(firstNonEmpty(rl.Stderr, rl.Stdout)))
	}
	return nil
}

// looksUnsupported 判定一段 stderr 是否为「子命令/flag 不被该版本支持」(用于 validate 兜底跳过)。
func looksUnsupported(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "unknown command") ||
		strings.Contains(s, "unknown flag") ||
		strings.Contains(s, "unknown shorthand")
}

// firstNonEmpty 返回首个非空白字符串。
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// mapExecErr 把 target.Exec/Upload 的传输层错误透传(领域错误已是人话;由上层 humanize)。
func mapExecErr(err error) error {
	if err == nil {
		return nil
	}
	return err
}
