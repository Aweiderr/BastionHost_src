#!/usr/bin/env bash
# 在银河麒麟或同类 Linux（amd64 / arm64）上编译桌面客户端。
# 产物写到 client/desktop/build/bin，不提交进 git。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

fail() {
  echo "$*" >&2
  exit 1
}

command -v go >/dev/null 2>&1 || fail "缺少 go。请安装 Go 1.22 及以上，并与 go.mod 里的工具链一致。"
command -v npm >/dev/null 2>&1 || fail "缺少 npm。请安装 Node.js 20 及以上。"
command -v pkg-config >/dev/null 2>&1 || fail "缺少 pkg-config。"

missing=0
for pkg in gtk+-3.0 webkit2gtk-4.0; do
  if ! pkg-config --exists "$pkg"; then
    echo "pkg-config 找不到 ${pkg}" >&2
    missing=1
  fi
done
if [[ "$missing" -ne 0 ]]; then
  cat >&2 <<'EOF'
这个脚本不会在缺少 WebKitGTK 4.0 头文件时假装已经打出安装包。

银河麒麟 V10 常见装法（包名以本机 yum/dnf 为准）：
  sudo yum install -y gcc pkgconf-pkg-config gtk3-devel webkit2gtk3-devel

Debian / Ubuntu：
  sudo apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev

托盘走会话总线，不需要额外的 libayatana。有图形桌面（UKUI / MATE）时图标才会出现。
EOF
  exit 1
fi

if ! command -v wails >/dev/null 2>&1; then
  fail "未找到 wails。安装与仓库一致的版本：go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0"
fi

arch="$(go env GOARCH)"
case "$arch" in
  amd64|arm64) ;;
  *) fail "当前架构是 ${arch}。脚本只约定 amd64 和 arm64。" ;;
esac

echo "安装前端依赖并编译（linux/${arch}）..."
(cd frontend && npm ci)
wails build -platform "linux/${arch}" -clean

bin="${ROOT}/build/bin/bastion-desktop"
if [[ ! -x "$bin" ]]; then
  fail "wails 没有生成 ${bin}"
fi
echo "已生成 ${bin}"
echo "下一步：packaging/kylin/package.sh"
