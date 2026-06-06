#!/bin/sh
# Pipewright 一键安装(Linux / macOS)。
#
#   curl -fsSL https://raw.githubusercontent.com/huangchengsir/pipewright/master/install.sh | sh
#
# 探测平台 → 从 GitHub Release 下载对应静态二进制 → 校验和核验 → 装到 /usr/local/bin。
# 可配环境变量:
#   VERSION       钉某版本(如 v1.2.3);缺省取 latest。
#   INSTALL_DIR   安装目录;缺省 /usr/local/bin(不可写时自动 sudo)。
#
# Windows 用户请到 Releases 页下载 .zip:
#   https://github.com/huangchengsir/pipewright/releases
set -eu

REPO="huangchengsir/pipewright"
BIN="pipewright"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
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
	TAG="$($DL "https://api.github.com/repos/${REPO}/releases/latest" |
		grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/')"
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

info "完成 ✓  $("${INSTALL_DIR}/${BIN}" --version 2>/dev/null || echo "${BIN} ${TAG}")"
printf '启动:%s\n' "${BIN}   # 默认监听 :8080,数据落当前目录 pipewright.db"
