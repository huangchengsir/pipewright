#!/bin/sh
# Pipewright 一键安装(Linux / macOS)。
#
#   curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh | sh
#
# 探测平台 → 从 GitHub Release 下载对应静态二进制 → 校验和核验 → 装到 /usr/local/bin。
# 可配环境变量:
#   VERSION         钉某版本(如 v1.2.3);缺省取 latest(API 被限流时自动退回重定向解析)。
#   INSTALL_DIR     安装目录;缺省 /usr/local/bin(不可写时自动 sudo)。
#   INSTALL_DOCKER  =1 时,Linux 下若缺 Docker 自动经官方 get.docker.com 安装(隔离构建/容器部署需要)。
#   SETUP_SERVICE   =1 装为 systemd 服务(开机自启 + 崩溃重启 + 自更新可用;Linux,需 root);=0 强制跳过;
#                   缺省在交互式终端下询问。会持久化 master key 到 /etc/pipewright/master.key、
#                   数据落 /var/lib/pipewright,配置写 /etc/pipewright/pipewright.env。
#   PIPEWRIGHT_ADDR 监听地址(装服务时写入 env 文件);缺省 :8080。
#   PIPEWRIGHT_DB_DRIVER / PIPEWRIGHT_DB_DSN
#                   装服务时选 DB 后端:缺省 sqlite(数据落 /var/lib/pipewright);
#                   传 mysql + DSN(user:pw@tcp(host:3306)/db?parseTime=true&charset=utf8mb4)则用 MySQL。
#
# Windows 用户请到 Releases 页下载 .zip:
#   https://github.com/huangchengsir/pipewright/releases
set -eu

REPO="huangchengsir/pipewright"
BIN="pipewright"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
SERVICE_INSTALLED=0

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m提示:\033[0m %s\n' "$1" >&2; }
err() {
	printf '\033[1;31m错误:\033[0m %s\n' "$1" >&2
	exit 1
}

# 依赖检查:下载器与校验工具。
if command -v curl >/dev/null 2>&1; then
	DL="curl -fsSL"
	DL_O="curl -fsSL -o"
elif command -v wget >/dev/null 2>&1; then
	DL="wget -qO-"
	DL_O="wget -qO"
else
	err "需要 curl 或 wget。"
fi

if command -v sha256sum >/dev/null 2>&1; then
	SHA="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
	SHA="shasum -a 256"
else
	err "需要 sha256sum 或 shasum 做校验和核验。"
fi

# 平台探测。归档命名须与 .goreleaser.yaml 一致:pipewright_<版本无v>_<os>_<arch>.tar.gz
OS="$(uname -s)"
case "$OS" in
Linux) OS="linux" ;;
Darwin) OS="darwin" ;;
*) err "不支持的系统:$OS(Windows 请到 Releases 页下载 .zip)。" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
x86_64 | amd64) ARCH="amd64" ;;
aarch64 | arm64) ARCH="arm64" ;;
*) err "不支持的架构:$ARCH。" ;;
esac

# 版本:缺省取 latest release 的 tag_name。
TAG="${VERSION:-}"
if [ -z "$TAG" ]; then
	info "查询最新版本…"
	# 先走 API;若被限流(常见 403)或网络失败,退回 /releases/latest 的重定向解析,绕开 API。
	TAG="$($DL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
		grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/')"
	if [ -z "$TAG" ] && command -v curl >/dev/null 2>&1; then
		EFF="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" 2>/dev/null || true)"
		case "$EFF" in */releases/tag/*) TAG="${EFF##*/tag/}" ;; esac
	fi
	[ -n "$TAG" ] || err "无法获取最新版本号(GitHub API 限流?可设 VERSION 环境变量钉版本)。"
fi
# 归档名用去掉前导 v 的版本号;下载路径用原始 tag。
VER_NO_V="${TAG#v}"
ARCHIVE="${BIN}_${VER_NO_V}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${TAG}"

info "安装 ${BIN} ${TAG}(${OS}/${ARCH})"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "下载 ${ARCHIVE}…"
$DL_O "${TMP}/${ARCHIVE}" "${BASE}/${ARCHIVE}" || err "下载失败:${BASE}/${ARCHIVE}"
$DL_O "${TMP}/checksums.txt" "${BASE}/checksums.txt" || err "下载校验和失败。"

# 校验和核验:从 checksums.txt 取本归档的期望值,与实际比对。
info "核验校验和…"
EXPECTED="$(grep " ${ARCHIVE}\$" "${TMP}/checksums.txt" | awk '{print $1}')"
[ -n "$EXPECTED" ] || err "checksums.txt 中找不到 ${ARCHIVE} 的条目。"
ACTUAL="$($SHA "${TMP}/${ARCHIVE}" | awk '{print $1}')"
[ "$EXPECTED" = "$ACTUAL" ] || err "校验和不匹配!期望 ${EXPECTED},实际 ${ACTUAL}。"

info "解包…"
tar -xzf "${TMP}/${ARCHIVE}" -C "$TMP" "$BIN" || err "解包失败。"
chmod +x "${TMP}/${BIN}"

# 安装:目录不可写则用 sudo。
info "安装到 ${INSTALL_DIR}…"
if [ -w "$INSTALL_DIR" ]; then
	mv "${TMP}/${BIN}" "${INSTALL_DIR}/${BIN}"
elif command -v sudo >/dev/null 2>&1; then
	sudo mv "${TMP}/${BIN}" "${INSTALL_DIR}/${BIN}"
else
	err "${INSTALL_DIR} 不可写且无 sudo。可设 INSTALL_DIR=\$HOME/.local/bin 重试。"
fi

# SELinux(RHEL/CentOS/Rocky/Fedora 系,强制模式):二进制经临时目录 mv 进来会带上 user_tmp_t
# 类型,systemd(init_t)无权执行它 → 装为服务后起不来(AVC denied execute)。重置为该路径
# 的默认上下文(/usr/local/bin → bin_t),systemd 即可执行。非 SELinux 系统无 restorecon,跳过。
if command -v restorecon >/dev/null 2>&1; then
	restorecon "${INSTALL_DIR}/${BIN}" 2>/dev/null ||
		sudo restorecon "${INSTALL_DIR}/${BIN}" 2>/dev/null || true
fi

info "完成 ✓  $("${INSTALL_DIR}/${BIN}" --version 2>/dev/null || echo "${BIN} ${TAG}")"

# ── 可选:装为 systemd 服务 ──────────────────────────────────────────
# 「部署平台」需要开机自启 + 崩溃重启,且自更新(二进制自替换 + syscall.Exec 自重启)要求
# 进程对 ${INSTALL_DIR} 有写权限 —— 故服务以 root 运行。本函数幂等:重装复用已有 master key /
# env 文件,不覆盖用户改动,升级不丢保险库。
setup_service() {
	SUDO=""
	if [ "$(id -u)" -ne 0 ]; then
		command -v sudo >/dev/null 2>&1 || err "安装 systemd 服务需 root 权限,且未找到 sudo。"
		SUDO="sudo"
	fi

	CONF_DIR=/etc/pipewright
	DATA_DIR=/var/lib/pipewright
	KEY_FILE="${CONF_DIR}/master.key"
	ENV_FILE="${CONF_DIR}/pipewright.env"
	UNIT=/etc/systemd/system/pipewright.service
	ADDR="${PIPEWRIGHT_ADDR:-:8080}"

	info "配置 systemd 服务…"
	$SUDO mkdir -p "$CONF_DIR" "$DATA_DIR"

	# master key:已存在则复用(升级不丢保险库),否则生成 32 字节(base64)。
	if [ -s "$KEY_FILE" ]; then
		info "复用已有 master key:${KEY_FILE}"
	else
		info "生成凭据保险库 master key:${KEY_FILE}"
		head -c 32 /dev/urandom | base64 | tr -d '\n' | $SUDO tee "$KEY_FILE" >/dev/null
		$SUDO chmod 600 "$KEY_FILE"
	fi

	# DB 后端:默认 SQLite(数据落 ${DATA_DIR});传入 PIPEWRIGHT_DB_DRIVER=mysql + PIPEWRIGHT_DB_DSN
	# 则用 MySQL(DSN 为 go-sql-driver 格式,如 user:pw@tcp(host:3306)/db?parseTime=true&charset=utf8mb4)。
	DB_DRIVER="${PIPEWRIGHT_DB_DRIVER:-sqlite}"
	if [ "$DB_DRIVER" = "mysql" ] && [ -z "${PIPEWRIGHT_DB_DSN:-}" ]; then
		err "PIPEWRIGHT_DB_DRIVER=mysql 需同时提供 PIPEWRIGHT_DB_DSN(go-sql-driver DSN,parseTime=true 必带)。"
	fi

	# env 文件:不存在才写(保留用户后续改动);改后 systemctl restart pipewright 生效。
	if [ ! -f "$ENV_FILE" ]; then
		{
			echo '# Pipewright 运行配置(systemd EnvironmentFile);改后 systemctl restart pipewright 生效。'
			echo "PIPEWRIGHT_ADDR=${ADDR}"
			echo "PIPEWRIGHT_MASTER_KEY_FILE=${KEY_FILE}"
			if [ "$DB_DRIVER" = "mysql" ]; then
				echo 'PIPEWRIGHT_DB_DRIVER=mysql'
				echo "PIPEWRIGHT_DB_DSN=${PIPEWRIGHT_DB_DSN}"
			else
				echo "PIPEWRIGHT_DB=${DATA_DIR}/pipewright.db"
			fi
		} | $SUDO tee "$ENV_FILE" >/dev/null
		$SUDO chmod 600 "$ENV_FILE"
	fi

	# systemd unit。User=root:自更新须写 ${INSTALL_DIR}、隔离构建须用 docker、SSH 部署须读密钥。
	# 自更新经 syscall.Exec 自替换映像(同 PID),systemd 无感,与 Restart=always 不冲突。
	printf '%s\n' \
		'[Unit]' \
		'Description=Pipewright CI/CD 平台' \
		'After=network-online.target docker.service' \
		'Wants=network-online.target' \
		'' \
		'[Service]' \
		'Type=simple' \
		'User=root' \
		"WorkingDirectory=${DATA_DIR}" \
		"EnvironmentFile=${ENV_FILE}" \
		"ExecStart=${INSTALL_DIR}/${BIN}" \
		'Restart=always' \
		'RestartSec=3' \
		'' \
		'[Install]' \
		'WantedBy=multi-user.target' |
		$SUDO tee "$UNIT" >/dev/null

	$SUDO systemctl daemon-reload
	$SUDO systemctl enable pipewright >/dev/null 2>&1 || true
	$SUDO systemctl restart pipewright

	SERVICE_INSTALLED=1
	info "服务已启动并设为开机自启。"
	printf '  状态:%s\n' "systemctl status pipewright"
	printf '  日志:%s\n' "journalctl -u pipewright -f"
	printf '  访问:http://<本机IP>%s  (首次登录后在引导页设置管理员账号)\n' "$ADDR"
}

# 决定是否装服务:SETUP_SERVICE=1 装 / =0 跳过 / 交互式询问 / 非交互且未设则给提示。
maybe_setup_service() {
	[ "$OS" = "linux" ] || return 0
	if ! command -v systemctl >/dev/null 2>&1; then
		[ "${SETUP_SERVICE:-}" = "1" ] && warn "未检测到 systemd(systemctl),跳过服务安装。"
		return 0
	fi

	do_svc=0
	case "${SETUP_SERVICE:-}" in
	1) do_svc=1 ;;
	0) do_svc=0 ;;
	*)
		if [ -t 0 ]; then
			printf '  装为 systemd 服务?开机自启 + 崩溃重启 + 自更新可用(以 root 运行)。[Y/n] ' >&2
			read -r ans
			case "$ans" in n | N | no | NO) do_svc=0 ;; *) do_svc=1 ;; esac
		else
			warn "如需开机自启 + 自更新,重跑时加 SETUP_SERVICE=1(装为 systemd 服务)。"
		fi
		;;
	esac

	[ "$do_svc" = "1" ] && setup_service
}

# ── Docker 运行时检测 ────────────────────────────────────────────────
# 「隔离构建 / 容器部署」需要 Docker;控制台 / SSH 部署 / AI 诊断不需要。
# 缺失不致命(平台照常起),但隔离构建会降级到桩 runner —— 故给出明确提示 / 可选自动安装。
check_docker() {
	if command -v docker >/dev/null 2>&1; then
		if docker info >/dev/null 2>&1; then
			info "Docker 已就绪 ✓  隔离构建 / 容器部署可用"
		else
			warn "Docker 已安装但守护进程未运行。启动后隔离构建可用(Linux:sudo systemctl start docker)。"
		fi
		return
	fi

	warn "未检测到 Docker。平台可运行,但「隔离构建 / 容器部署」需要它(否则降级到桩 runner,不做真实构建)。"

	# macOS:Docker Desktop 是 GUI 应用,无法命令行装,只提示。
	if [ "$OS" != "linux" ]; then
		printf '  macOS 请安装 Docker Desktop:https://www.docker.com/products/docker-desktop/\n' >&2
		return
	fi

	# Linux:opt-in 自动装(INSTALL_DOCKER=1 或交互式确认)。官方 get.docker.com 仅支持 Linux。
	do_install=0
	if [ "${INSTALL_DOCKER:-}" = "1" ]; then
		do_install=1
	elif [ -t 0 ]; then
		printf '  是否现在用官方脚本 get.docker.com 自动安装 Docker?[y/N] ' >&2
		read -r ans
		case "$ans" in y | Y | yes | YES) do_install=1 ;; esac
	else
		printf '  Linux 自动安装:重跑时加 INSTALL_DOCKER=1,或手动 curl -fsSL https://get.docker.com | sh\n' >&2
	fi

	if [ "$do_install" = "1" ]; then
		info "经官方脚本安装 Docker(get.docker.com,需要 sudo 提权)…"
		if $DL https://get.docker.com | sh; then
			info "Docker 安装完成。建议:sudo systemctl enable --now docker;将当前用户加入 docker 组(sudo usermod -aG docker \"\$USER\")后重新登录免 sudo。"
		else
			warn "Docker 自动安装失败,请参考 https://docs.docker.com/engine/install/ 手动安装。"
		fi
	fi
}
check_docker
maybe_setup_service

if [ "$SERVICE_INSTALLED" = "1" ]; then
	info "完成 ✓  Pipewright 已作为 systemd 服务运行(开机自启 + 自更新可用)。"
else
	printf '启动:%s\n' "${BIN}   # 默认监听 :8080,数据落当前目录 pipewright.db"
	printf '提示:%s\n' "如需开机自启 + 自更新,可重跑并加 SETUP_SERVICE=1 装为 systemd 服务。"
fi
