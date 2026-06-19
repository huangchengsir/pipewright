package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	// defaultCaddyImage 是默认反代镜像:含 DNS-01 插件 + ratelimit + layer4 的自构建 Caddy
	// (见 deploy/caddy/Dockerfile + .github/workflows/caddy-image.yml)。可经
	// PIPEWRIGHT_CADDY_IMAGE 覆盖(如用 stock caddy:2,但那样没有 DNS-01/通配符/L4 能力)。
	defaultCaddyImage = "ghcr.io/huangchengsir/pipewright-caddy:latest"
	// caddyImageEnv 是覆盖反代镜像的环境变量名。
	caddyImageEnv = "PIPEWRIGHT_CADDY_IMAGE"
	// caddyDataVol 是证书/ACME 账户持久卷(删路由不删卷,避免重签触发 LE 限速)。
	caddyDataVol = "pipewright_caddy_data"
	// caddyConfigVol 是 Caddy 自动持久化配置卷。
	caddyConfigVol = "pipewright_caddy_config"
	// caddyfilePath 是容器内 Caddyfile 路径。
	caddyfilePath = "/etc/caddy/Caddyfile"
	// caddyfileTmpPath 是 apply 时在目标主机上的临时落盘路径(再 docker cp 进容器)。
	caddyfileTmpPath = "/tmp/pipewright-caddyfile"
)

// dnsCred 是一条 DNS 提供商的渲染材料(类型 + token 明文)。token 仅在 apply 渲染时存在于内存,
// 注入 0600 临时 Caddyfile,绝不日志/回库/回 API。
type dnsCred struct {
	Type  string // cloudflare | dnspod | alidns
	Token string // 凭据明文(进程内,用完即弃)
}

// renderCaddyfile 据一组 enabled 路由生成 Caddyfile 文本(纯函数,可直接单测)。
// 每条路由产一个站点块:`domain {\n    reverse_proxy container:port\n}`。Caddy 见到带域名的
// 站点块即自动经 Let's Encrypt(HTTP-01)签发并续期证书,无需任何额外指令。
// dnsCreds 按 DNS 提供商 id 索引:某路由绑了 DNS 提供商时渲染 `tls { dns <type> <token> }`(DNS-01,
// 通配符必需)。未提供 token(map 缺该 id)的 DNS-01 路由退回 HTTP-01 渲染(不写 token)。
// 无 enabled 路由时返回带说明注释的空配置(reload 一份合法空配置,不破坏既有 Caddy 进程)。
func renderCaddyfile(routes []Route, dnsCreds map[string]dnsCred) string {
	// 稳定排序:按 domain 升序,保证渲染结果确定(便于测试 + reload 幂等)。
	sorted := make([]Route, len(routes))
	copy(sorted, routes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Domain < sorted[j].Domain })

	var b strings.Builder
	b.WriteString("# 由 Pipewright 自动生成,请勿手改。\n")

	// R4 E4.3:TCP 透传(caddy-l4)走 Caddyfile **全局选项块**里的 layer4 段,不是 HTTP 站点块。
	// 凡有路由带 TCPPassthrough,就先发一个全局 { layer4 { :port { route { proxy c:port } } } }。
	// 必须在任何站点块之前(Caddyfile 要求全局选项块为文件首个块)。
	renderLayer4Global(&b, sorted)

	if len(sorted) == 0 {
		b.WriteString("# 当前没有启用的反代路由。\n")
		return b.String()
	}
	for _, r := range sorted {
		renderSite(&b, r, dnsCreds)
	}
	return b.String()
}

// renderLayer4Global 渲染 TCP 透传的 layer4 全局块(R4 E4.3;caddy-l4)。
// 仅当至少一条路由带 TCPPassthrough 时才输出;按监听端口升序稳定排序(渲染确定 + reload 幂等)。
// 形如:
//
//	{
//	    layer4 {
//	        :5432 {
//	            route {
//	                proxy db_container:5432
//	            }
//	        }
//	    }
//	}
//
// 所有进入文本的值已在领域层(validateConfig)严格校验过(端口 1..65535 + 容器名安全字符集)。
func renderLayer4Global(b *strings.Builder, routes []Route) {
	tcps := make([]TCPConfig, 0)
	for _, r := range routes {
		if tc := r.Config.TCPPassthrough; tc != nil {
			tcps = append(tcps, *tc)
		}
	}
	if len(tcps) == 0 {
		return
	}
	sort.Slice(tcps, func(i, j int) bool { return tcps[i].ListenPort < tcps[j].ListenPort })

	b.WriteString("{\n")
	b.WriteString("    layer4 {\n")
	for _, tc := range tcps {
		b.WriteString("        :" + strconv.Itoa(tc.ListenPort) + " {\n")
		b.WriteString("            route {\n")
		b.WriteString("                proxy " + tc.UpstreamContainer + ":" + strconv.Itoa(tc.UpstreamPort) + "\n")
		b.WriteString("            }\n")
		b.WriteString("        }\n")
	}
	b.WriteString("    }\n")
	b.WriteString("}\n")
}

// renderSite 渲染单条路由的站点块(R2:多域名别名 + 访问控制 + 安全头 + 压缩 + 重定向)。
// 站点块头 = 主域名 + 别名,以 ", " 连接;块内指令按确定顺序输出(IP 黑名单 → 白名单 →
// basic auth → header → encode → redir → reverse_proxy),便于 golden 测试与 reload 幂等。
// 所有进入文本的值已在领域层(validateConfig)严格校验过,渲染处不再二次防注入。
func renderSite(b *strings.Builder, r Route, dnsCreds map[string]dnsCred) {
	cfg := r.Config

	// 站点块头:主域名 + 别名。
	header := r.Domain
	for _, a := range cfg.Aliases {
		header += ", " + a
	}
	b.WriteString(header)
	b.WriteString(" {\n")

	// DNS-01(R3:通配符必需):该路由绑了 DNS 提供商且 apply 取到了 token → 渲染 tls { dns ... }。
	// token 注入此处(Caddy 据此经 DNS API 完成 ACME DNS-01 挑战);整份配置写 0600 临时文件,绝不日志。
	if cfg.DNSProviderID != "" {
		if cred, ok := dnsCreds[cfg.DNSProviderID]; ok && cred.Type != "" && cred.Token != "" {
			b.WriteString("    tls {\n")
			switch cred.Type {
			case "alidns":
				// 阿里云:凭据 = "AccessKeyId,AccessKeySecret";caddy-dns/alidns 要两字段块,
				// 不是单 token(单 token 会被 caddy 当成非法语法、DNS-01 签失败)。
				id, secret := splitDNSCred(cred.Token)
				b.WriteString("        dns alidns {\n")
				b.WriteString("            access_key_id " + id + "\n")
				b.WriteString("            access_key_secret " + secret + "\n")
				b.WriteString("        }\n")
			case "dnspod", "tencentcloud":
				// 腾讯云/DNSPod:凭据 = "SecretId,SecretKey";caddy-dns/tencentcloud 要两字段块。
				id, secret := splitDNSCred(cred.Token)
				b.WriteString("        dns tencentcloud {\n")
				b.WriteString("            secret_id " + id + "\n")
				b.WriteString("            secret_key " + secret + "\n")
				b.WriteString("        }\n")
			default:
				// cloudflare 等单 token 厂商:dns <type> <token> 即正确。
				b.WriteString("        dns " + cred.Type + " " + cred.Token + "\n")
			}
			b.WriteString("    }\n")
		}
	}

	// IP 黑名单:命中即 403(优先于白名单,显式拒绝先行)。
	// 用 handle 包 respond,不发裸 `respond @denied 403`:Caddy 的指令排序会把裸 respond 排到
	// reverse_proxy/handle 之后,一旦本路由用了路径规则/负载均衡(handle 块),IP 名单会被绕过、形同虚设。
	// handle 在指令顺序里早于 reverse_proxy,且 handle 块按源码顺序互斥求值,放在内容 handle 之前即真正拦截。
	if len(cfg.IPDeny) > 0 {
		b.WriteString("    @denied remote_ip " + strings.Join(cfg.IPDeny, " ") + "\n")
		b.WriteString("    handle @denied {\n")
		b.WriteString("        respond 403\n")
		b.WriteString("    }\n")
	}
	// IP 白名单:不在白名单内即 403(只有列出的网段可访问)。
	if len(cfg.IPAllow) > 0 {
		b.WriteString("    @notallowed not remote_ip " + strings.Join(cfg.IPAllow, " ") + "\n")
		b.WriteString("    handle @notallowed {\n")
		b.WriteString("        respond 403\n")
		b.WriteString("    }\n")
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
	// 主内容包进 handle 的条件:有路径规则,或有 IP 名单。有 IP 名单时必须包 handle{},
	// 才能与上面的 `handle @denied/@notallowed` 互斥——否则被拒请求 respond 403 后,裸
	// reverse_proxy 仍会执行并重复写响应。两者都无时保持裸 reverse_proxy(最简形态)。
	if len(cfg.PathRules) > 0 || len(cfg.IPAllow) > 0 || len(cfg.IPDeny) > 0 {
		// R3 E3.5 路径路由:逐条 handle <path> { reverse_proxy c:port },末尾默认 handle 落主上游。
		// 路径路由的各 handle 用裸单上游(LB/健康检查/gRPC 仅作用于默认主上游块,保持语义简单确定)。
		for _, pr := range cfg.PathRules {
			b.WriteString("    handle " + pr.Path + " {\n")
			b.WriteString("        reverse_proxy " + pr.UpstreamContainer + ":" + strconv.Itoa(pr.UpstreamPort) + "\n")
			b.WriteString("    }\n")
		}
		b.WriteString("    handle {\n")
		renderReverseProxy(b, "        ", r, cfg)
		b.WriteString("    }\n")
	} else {
		renderReverseProxy(b, "    ", r, cfg)
	}
	b.WriteString("}\n")
}

// renderReverseProxy 渲染主上游的 reverse_proxy 指令(R4 E4.2 多上游/负载均衡/健康检查 + E4.3 gRPC h2c)。
// indent 是行首缩进(站点块内 4 空格;默认 handle 内 8 空格)。
//   - 多上游:reverse_proxy primary:p up2:p up3:p(主上游始终第一)。
//   - lb_policy / health_uri / health_interval / gRPC transport 任一存在 → 渲染 { ... } 配置块;
//     否则单行(保持 R1-R3 既有 golden 不变)。
//
// 所有进入文本的值已在领域层(validateConfig)严格校验过,渲染处不再二次防注入。
func renderReverseProxy(b *strings.Builder, indent string, r Route, cfg RouteConfig) {
	// 上游列表:主上游 + 额外上游(E4.2)。
	upstreams := r.UpstreamContainer + ":" + strconv.Itoa(r.UpstreamPort)
	for _, u := range cfg.Upstreams {
		upstreams += " " + u.Container + ":" + strconv.Itoa(u.Port)
	}

	needBlock := cfg.LBPolicy != "" || cfg.HealthURI != "" || cfg.HealthInterval != "" || cfg.GRPC
	if !needBlock {
		b.WriteString(indent + "reverse_proxy " + upstreams + "\n")
		return
	}
	b.WriteString(indent + "reverse_proxy " + upstreams + " {\n")
	if cfg.LBPolicy != "" {
		b.WriteString(indent + "    lb_policy " + cfg.LBPolicy + "\n")
	}
	if cfg.HealthURI != "" {
		b.WriteString(indent + "    health_uri " + cfg.HealthURI + "\n")
	}
	if cfg.HealthInterval != "" {
		b.WriteString(indent + "    health_interval " + cfg.HealthInterval + "\n")
	}
	if cfg.GRPC {
		// gRPC 走 h2c(明文 HTTP/2)到上游容器;与上游/lb/health 合并在同一 reverse_proxy 块内。
		b.WriteString(indent + "    transport http {\n")
		b.WriteString(indent + "        versions h2c 2\n")
		b.WriteString(indent + "    }\n")
	}
	b.WriteString(indent + "}\n")
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

	// 4) 先 docker pull 配置的镜像(确保宿主机拿到含 DNS-01/L4 插件的自构建 Caddy;best-effort:
	//    pull 失败不直接拒起 —— 宿主机可能已有缓存镜像或处于离线环境,交由后续 docker run 决断)。
	image := caddyImageRef()
	if _, perr := tg.Exec(ctx, serverID, []string{"docker", "pull", image}); perr != nil {
		return mapExecErr(perr)
	}

	// 5) 起 Caddy 容器(array 不拼 shell)。
	//    --add-host host.docker.internal:host-gateway(Docker 20.10+)让 address 类上游能反代到
	//    宿主机上(非容器)的服务(host.docker.internal 解析到宿主)。已存在的旧 Caddy 容器不会
	//    追溯获得此项;仅新部署生效(MVP 可接受,移除后重新部署即可补上)。
	runCmd := []string{
		"docker", "run", "-d",
		"--name", caddyContainer,
		"--restart", "unless-stopped",
		"--network", proxyNetwork,
		"--add-host", "host.docker.internal:host-gateway",
		"-p", "80:80", "-p", "443:443",
		"-v", caddyDataVol + ":/data",
		"-v", caddyConfigVol + ":/config",
		image,
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

// inspectCaddy 经 docker inspect 探测目标主机上的 pipewright-caddy 容器:
//   - inspect 非零退出(No such object)= 容器不存在 → Installed:false(非错误)。
//   - inspect 成功 → Installed:true,并从 --format 输出解析 running / image。
//   - 端口为 best-effort:再发一次 inspect 读 NetworkSettings.Ports,解析失败给 ""(前端按 80/443 兜底)。
//
// 仅 target.Exec 的传输层错误(SSH 不可达 / vault 未配 / 主机不存在)才返回 error。
func inspectCaddy(ctx context.Context, tg target.Service, serverID string) (*CaddyStatus, error) {
	st := &CaddyStatus{}
	// 一次 inspect 同时拿运行态与镜像(format 以 | 分隔,避免拼 shell)。
	insp, err := tg.Exec(ctx, serverID, []string{
		"docker", "inspect", caddyContainer,
		"--format", "{{.State.Running}}|{{.Config.Image}}",
	})
	if err != nil {
		return nil, mapExecErr(err)
	}
	if insp.ExitCode != 0 {
		// 容器不存在(或 inspect 失败但非传输层)→ 视为未安装,不报错。
		return st, nil
	}
	st.Installed = true
	out := strings.TrimSpace(insp.Stdout)
	if i := strings.IndexByte(out, '|'); i >= 0 {
		st.Running = strings.EqualFold(strings.TrimSpace(out[:i]), "true")
		st.Image = strings.TrimSpace(out[i+1:])
	}
	// 端口摘要(best-effort:任何失败都给 "",不阻断也不报错)。
	st.Ports = inspectCaddyPorts(ctx, tg, serverID)
	return st, nil
}

// inspectCaddyPorts best-effort 读 pipewright-caddy 的发布端口摘要(如 "80,443")。
// 解析 NetworkSettings.Ports 的 key(形如 "80/tcp"),取端口号去重升序。任何失败给 ""。
func inspectCaddyPorts(ctx context.Context, tg target.Service, serverID string) string {
	res, err := tg.Exec(ctx, serverID, []string{
		"docker", "inspect", caddyContainer,
		"--format", "{{json .NetworkSettings.Ports}}",
	})
	if err != nil || res == nil || res.ExitCode != 0 {
		return ""
	}
	var ports map[string]any
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &ports); jerr != nil {
		return ""
	}
	seen := map[string]struct{}{}
	nums := make([]int, 0, len(ports))
	for k := range ports {
		p := k
		if i := strings.IndexByte(p, '/'); i >= 0 {
			p = p[:i] // 去掉 "/tcp" 后缀
		}
		n, cerr := strconv.Atoi(p)
		if cerr != nil {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return ""
	}
	sort.Ints(nums)
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// removeCaddy 停止并删除目标主机上的 pipewright-caddy 容器(array 不拼 shell)。
// 保留命名卷 pipewright_caddy_data(证书/ACME 账户持久,避免重建时重签触发 LE 限速)。
// 幂等:容器已不存在时,docker stop/rm 报「No such container」视为成功;仅传输层错误才返回 error。
func removeCaddy(ctx context.Context, tg target.Service, serverID string) error {
	// 1) stop(容器不存在的 "No such container" 容忍)。
	if _, err := tg.Exec(ctx, serverID, []string{"docker", "stop", caddyContainer}); err != nil {
		return mapExecErr(err)
	}
	// 2) rm(同样容忍不存在)。绝不删卷(无 docker volume rm)。
	if _, err := tg.Exec(ctx, serverID, []string{"docker", "rm", caddyContainer}); err != nil {
		return mapExecErr(err)
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
	// 1.5) 临时文件可能含 DNS-01 token(渲染进 tls { dns ... }),立即 chmod 0600 收紧权限
	//      (best-effort:chmod 失败不阻断;path 作 array 参数不拼 shell)。
	if chRes, chErr := tg.Exec(ctx, serverID, []string{"chmod", "600", caddyfileTmpPath}); chErr != nil {
		return mapExecErr(chErr)
	} else {
		_ = chRes
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

// caddyImageRef 返回反代镜像引用:PIPEWRIGHT_CADDY_IMAGE 覆盖,否则默认自构建镜像。
// 仅接受合法镜像引用字符集(防经 env 注入 docker 额外 flag/参数);非法即回退默认。
func caddyImageRef() string {
	v := strings.TrimSpace(os.Getenv(caddyImageEnv))
	if v == "" {
		return defaultCaddyImage
	}
	if !imageRefRe.MatchString(v) {
		return defaultCaddyImage
	}
	return v
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

// splitDNSCred 把「id,secret」形态的 DNS 凭据按首个逗号切成两段(两侧去空白)。
// 用于 alidns(AccessKeyId,AccessKeySecret)、tencentcloud(SecretId,SecretKey)等需两字段的厂商。
func splitDNSCred(token string) (id, secret string) {
	parts := strings.SplitN(token, ",", 2)
	id = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		secret = strings.TrimSpace(parts[1])
	}
	return id, secret
}

// mapExecErr 把 target.Exec/Upload 的传输层错误透传(领域错误已是人话;由上层 humanize)。
func mapExecErr(err error) error {
	if err == nil {
		return nil
	}
	return err
}
