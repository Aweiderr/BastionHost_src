#!/usr/bin/env bash
# 把已经编好的 bastion-desktop 打成 tar.gz，并在工具齐全时再打 rpm 或 deb。
# 没有二进制时不会造一个空包。
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
BIN="${ROOT}/build/bin/bastion-desktop"
OUT="${ROOT}/build/dist"
# 不能叫 VERSION。后面会 source /etc/os-release，麒麟上 VERSION="V10 (Lance)"，
# 会把软件版本盖掉，rpm 的 Version 只能是一个词。
PKG_VERSION="${BASTION_DESKTOP_VERSION:-0.1.0}"

if [[ ! -x "$BIN" ]]; then
  echo "没有可执行文件 ${BIN}。" >&2
  echo "请先在麒麟或同类 Linux 上运行 packaging/kylin/build.sh。本仓库不提交这个二进制。" >&2
  exit 1
fi

arch="$(uname -m)"
case "$arch" in
  x86_64) debarch=amd64; rpmarch=x86_64 ;;
  aarch64|arm64) debarch=arm64; rpmarch=aarch64 ;;
  *)
    echo "未识别的机器架构 ${arch}。仍会打 tar.gz，不打 deb/rpm。" >&2
    debarch=""
    rpmarch=""
    ;;
esac

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "${stage}/opt/bastion-desktop" "${stage}/usr/share/applications" "$OUT"
install -m 0755 "$BIN" "${stage}/opt/bastion-desktop/bastion-desktop"
install -m 0644 "${HERE}/bastion-desktop.desktop" "${stage}/usr/share/applications/bastion-desktop.desktop"
install -m 0755 "${HERE}/install.sh" "${stage}/install.sh"
cp "${HERE}/bastion-desktop.desktop" "${stage}/bastion-desktop.desktop"

tarname="bastion-desktop-${PKG_VERSION}-linux-${arch}.tar.gz"
tar -C "$stage" -czf "${OUT}/${tarname}" \
  opt/bastion-desktop/bastion-desktop \
  usr/share/applications/bastion-desktop.desktop \
  install.sh \
  bastion-desktop.desktop
echo "已生成 ${OUT}/${tarname}"

os_id=""
if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  os_id="${ID:-}${ID_LIKE:-}"
fi

if command -v dpkg-deb >/dev/null 2>&1 && [[ -n "$debarch" ]]; then
  debroot="${stage}/deb"
  mkdir -p "${debroot}/DEBIAN" "${debroot}/opt/bastion-desktop" "${debroot}/usr/share/applications"
  install -m 0755 "$BIN" "${debroot}/opt/bastion-desktop/bastion-desktop"
  install -m 0644 "${HERE}/bastion-desktop.desktop" "${debroot}/usr/share/applications/bastion-desktop.desktop"
  depends="libgtk-3-0, libwebkit2gtk-4.0-37"
  if printf '%s' "$os_id" | grep -Eqi 'kylin|uos|openeuler|fedora|centos|rhel'; then
    echo "当前更像 rpm 系，跳过 deb 的 Debian 依赖名。下面若有 rpmbuild 会打 rpm。" >&2
  else
    cat > "${debroot}/DEBIAN/control" <<EOF
Package: bastion-desktop
Version: ${PKG_VERSION}
Architecture: ${debarch}
Depends: ${depends}
Maintainer: bastion <bastion@localhost>
Description: 堡垒机运维桌面客户端
 连接管理服务进行运维。需要系统 WebKitGTK 4.0。
EOF
    dpkg-deb --build "$debroot" "${OUT}/bastion-desktop_${PKG_VERSION}_${debarch}.deb"
    echo "已生成 ${OUT}/bastion-desktop_${PKG_VERSION}_${debarch}.deb"
  fi
else
  echo "未使用 dpkg-deb（未安装，或架构不在约定内）。tar.gz 仍可安装。" >&2
fi

if command -v rpmbuild >/dev/null 2>&1 && [[ -n "$rpmarch" ]]; then
  rpmtop="$(mktemp -d)"
  mkdir -p "${rpmtop}/BUILD" "${rpmtop}/RPMS" "${rpmtop}/SOURCES" "${rpmtop}/SPECS" "${rpmtop}/BUILDROOT"
  cp "$BIN" "${rpmtop}/SOURCES/bastion-desktop"
  cp "${HERE}/bastion-desktop.desktop" "${rpmtop}/SOURCES/bastion-desktop.desktop"
  cat > "${rpmtop}/SPECS/bastion-desktop.spec" <<EOF
Name: bastion-desktop
Version: ${PKG_VERSION}
Release: 1
Summary: 堡垒机运维桌面客户端
License: Proprietary
BuildArch: ${rpmarch}

%description
连接管理服务进行运维。运行时需要 GTK 3 和 WebKitGTK 4.0（麒麟上通常是 webkit2gtk3）。

%install
mkdir -p %{buildroot}/opt/bastion-desktop
mkdir -p %{buildroot}/usr/share/applications
cp %{_sourcedir}/bastion-desktop %{buildroot}/opt/bastion-desktop/bastion-desktop
cp %{_sourcedir}/bastion-desktop.desktop %{buildroot}/usr/share/applications/bastion-desktop.desktop
chmod 0755 %{buildroot}/opt/bastion-desktop/bastion-desktop

%files
/opt/bastion-desktop/bastion-desktop
/usr/share/applications/bastion-desktop.desktop
EOF
  if rpmbuild --define "_topdir ${rpmtop}" -bb "${rpmtop}/SPECS/bastion-desktop.spec"; then
    find "${rpmtop}/RPMS" -type f -name '*.rpm' -exec cp {} "$OUT" \;
    echo "已把 rpm 复制到 ${OUT}"
  else
    echo "rpmbuild 失败。tar.gz 仍然可用：${OUT}/${tarname}" >&2
  fi
  rm -rf "$rpmtop"
else
  echo "未使用 rpmbuild。在银河麒麟上安装 rpm-build 后重跑本脚本可得到 rpm。" >&2
fi

echo "安装包没有做国密签名。签名需要现场的密钥，不在这个脚本里。"
