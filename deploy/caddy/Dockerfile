# Pipewright 自构建 Caddy 镜像:在 stock Caddy 之上经 xcaddy 捆绑反代「零 DNS 体验」(R3)+
# CI/CD 王牌(R4)所需、stock caddy:2 不含的插件:
#   - DNS-01 提供商插件(通配符 *.example.com 证书签发必需,HTTP-01 做不到):
#       caddy-dns/cloudflare · caddy-dns/alidns · caddy-dns/tencentcloud(腾讯云 / DNSPod)
#   - mholt/caddy-ratelimit:站点级限流(R2/R4 滥用防护)
#   - mholt/caddy-l4(layer4):四层(TCP/UDP)反代,R4 预留(Per-PR 预览环境的非 HTTP 服务)
#
# 多阶段:xcaddy 构建器编译出含插件的自定义 caddy 二进制 → 复制进官方 caddy:2 基础镜像
# (保留官方镜像的 entrypoint / CA 证书 / 默认 Caddyfile 等运行期约定)。
#
# 版本钉死(可复现构建);升级时同步改 CADDY_VERSION 并跑一次镜像 CI。

# --- 构建阶段:xcaddy 编译自定义 caddy 二进制 ---
ARG CADDY_VERSION=2.8.4
FROM caddy:${CADDY_VERSION}-builder AS builder

# 插件版本不钉到具体 tag(xcaddy 默认拉各插件最新兼容版);如需完全可复现可在此加 @vX.Y.Z。
# GOTOOLCHAIN=auto:builder 镜像自带的 Go 版本可能低于某些「拉最新」插件的 go.mod 要求
# (如 caddy-dns/tencentcloud v0.4.3 要 go>=1.25),auto 允许 go 按需自动下载对应工具链,
# 避免插件升级把构建卡在 builder 镜像的固定 Go 版本上(默认 local 会直接 FATAL)。
ENV GOTOOLCHAIN=auto
RUN xcaddy build \
    --with github.com/caddy-dns/cloudflare \
    --with github.com/caddy-dns/alidns \
    --with github.com/caddy-dns/tencentcloud \
    --with github.com/mholt/caddy-ratelimit \
    --with github.com/mholt/caddy-l4

# --- 运行阶段:把自定义二进制塞回官方 caddy:2 ---
FROM caddy:${CADDY_VERSION}

# 覆盖官方镜像里的 caddy 二进制为含插件的自构建版本。
COPY --from=builder /usr/bin/caddy /usr/bin/caddy

# 沿用官方镜像的 ENTRYPOINT/CMD(caddy run ...);Pipewright 起容器时显式传 --config/--adapter。
LABEL org.opencontainers.image.source="https://github.com/huangchengsir/pipewright" \
      org.opencontainers.image.description="Caddy with DNS-01 (cloudflare/alidns/tencentcloud), ratelimit and layer4 plugins for Pipewright reverse proxy" \
      org.opencontainers.image.licenses="Apache-2.0"
