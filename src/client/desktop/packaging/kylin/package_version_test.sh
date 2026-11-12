#!/usr/bin/env bash
# 用夹具 /etc/os-release（VERSION="V10 (Lance)"）跑 package.sh，
# 确认 tar、deb、rpm 用的仍是软件版本，而不是发行版版本。
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
PKG="${HERE}/package.sh"
BIN_DIR="${ROOT}/build/bin"
DIST="${ROOT}/build/dist"
BIN="${BIN_DIR}/bastion-desktop"

fail() {
  echo "$*" >&2
  exit 1
}

command -v unshare >/dev/null 2>&1 || fail "需要 unshare 才能把夹具挂到 /etc/os-release"
unshare --user --mount --map-root-user true || fail "user mount namespace 不可用"

arch="$(uname -m)"
case "$arch" in
  x86_64) debarch=amd64; rpmarch=x86_64 ;;
  aarch64|arm64) debarch=arm64; rpmarch=aarch64 ;;
  *) fail "测试只覆盖 x86_64 和 aarch64，当前是 ${arch}" ;;
esac

work="$(mktemp -d)"
bin_existed=0
dist_existed=0
if [[ -e "$BIN" ]]; then
  bin_existed=1
fi
if [[ -d "$DIST" ]]; then
  dist_existed=1
fi
before="$(mktemp)"
if [[ -d "$DIST" ]]; then
  find "$DIST" -maxdepth 1 -type f -printf '%f\n' | sort > "$before"
else
  : > "$before"
fi

cleanup() {
  local name
  if [[ -d "$DIST" ]]; then
    while IFS= read -r name; do
      [[ -n "$name" ]] || continue
      if ! grep -Fxq "$name" "$before"; then
        rm -f "${DIST}/${name}"
      fi
    done < <(find "$DIST" -maxdepth 1 -type f -printf '%f\n')
    if [[ "$dist_existed" -eq 0 ]]; then
      rmdir "$DIST" 2>/dev/null || true
    fi
  fi
  if [[ "$bin_existed" -eq 1 ]]; then
    if [[ -f "${work}/bin.bak" ]]; then
      mv "${work}/bin.bak" "$BIN"
    fi
  else
    rm -f "$BIN"
    rmdir "$BIN_DIR" 2>/dev/null || true
  fi
  rm -rf "$work"
  rm -f "$before"
}
trap cleanup EXIT

if [[ "$bin_existed" -eq 1 ]]; then
  cp -a "$BIN" "${work}/bin.bak"
fi
mkdir -p "$BIN_DIR" "$DIST"
printf '#!/bin/sh\nexit 0\n' > "$BIN"
chmod 0755 "$BIN"

cat > "${work}/os-release-kylin" <<'EOF'
NAME="Kylin Linux Advanced Server"
VERSION="V10 (Lance)"
ID="kylin"
ID_LIKE="fedora"
VERSION_ID="V10"
PRETTY_NAME="Kylin Linux Advanced Server V10 (Lance)"
EOF

cat > "${work}/os-release-deb" <<'EOF'
NAME="Debian GNU/Linux"
VERSION="V10 (Lance)"
ID=debian
VERSION_ID="10"
PRETTY_NAME="Debian GNU/Linux V10 (Lance)"
EOF

cat > "${work}/rpmbuild" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
spec=""
topdir=""
prev=""
for arg in "$@"; do
  if [[ "$prev" == "--define" && "$arg" == _topdir\ * ]]; then
    topdir="${arg#_topdir }"
  fi
  if [[ "$arg" == *.spec ]]; then
    spec="$arg"
  fi
  prev="$arg"
done
if [[ -z "$spec" || ! -f "$spec" ]]; then
  echo "rpmbuild stub: missing spec" >&2
  exit 1
fi
if [[ -z "$topdir" || ! -d "${topdir}/RPMS" ]]; then
  echo "rpmbuild stub: missing RPMS under '${topdir}'" >&2
  exit 1
fi
cp "$spec" "${RPM_SPEC_OUT:?}"
ver="$(sed -n 's/^Version:[[:space:]]*//p' "$spec" | head -n 1)"
case "$ver" in
  ""|*[!0-9A-Za-z.+~]*)
    echo "rpmbuild stub: Version is not a single token: [${ver}]" >&2
    exit 1
    ;;
esac
buildarch="$(sed -n 's/^BuildArch:[[:space:]]*//p' "$spec" | head -n 1)"
mkdir -p "${topdir}/RPMS/${buildarch}"
: > "${topdir}/RPMS/${buildarch}/bastion-desktop-${ver}-1.${buildarch}.rpm"
exit 0
EOF
chmod 0755 "${work}/rpmbuild"

real_dpkg="$(command -v dpkg-deb || true)"
cat > "${work}/dpkg-deb" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" != "--build" || -z "${2:-}" || -z "${3:-}" ]]; then
  echo "dpkg-deb wrapper: unexpected args: $*" >&2
  exit 1
fi
cp "$2/DEBIAN/control" "${DEB_CONTROL_OUT:?}"
if [[ -n "${REAL_DPKG_DEB:-}" ]]; then
  exec "$REAL_DPKG_DEB" "$@"
fi
: > "$3"
EOF
chmod 0755 "${work}/dpkg-deb"

run_package() {
  local release="$1"
  local spec_out="$2"
  local control_out="$3"
  local desktop_version="$4"
  local -a env_cmd
  if [[ -n "$desktop_version" ]]; then
    env_cmd=(
      env
      "PATH=${work}:${PATH}"
      "RPM_SPEC_OUT=${spec_out}"
      "DEB_CONTROL_OUT=${control_out}"
      "REAL_DPKG_DEB=${real_dpkg}"
      "BASTION_DESKTOP_VERSION=${desktop_version}"
    )
  else
    env_cmd=(
      env -u BASTION_DESKTOP_VERSION
      "PATH=${work}:${PATH}"
      "RPM_SPEC_OUT=${spec_out}"
      "DEB_CONTROL_OUT=${control_out}"
      "REAL_DPKG_DEB=${real_dpkg}"
    )
  fi
  unshare --user --mount --map-root-user \
    "${env_cmd[@]}" \
    bash -c 'set -euo pipefail; mount --bind "$1" /etc/os-release; "$2"' \
    bash "$release" "$PKG"
}

spec_version() {
  local spec="$1"
  local count got
  count="$(grep -c '^Version:' "$spec" || true)"
  [[ "$count" -eq 1 ]] || fail "${spec} 应只有一行 Version，实际 ${count}"
  got="$(sed -n 's/^Version:[[:space:]]*//p' "$spec")"
  printf '%s\n' "$got"
}

assert_version_token() {
  local got="$1"
  local want="$2"
  local where="$3"
  [[ "$got" == "$want" ]] || fail "${where} 版本是 [${got}]，期望 [${want}]"
  [[ "$got" != *" "* && "$got" != *"("* ]] || fail "${where} 版本含空格或括号: [${got}]"
  [[ "$got" != *"Lance"* && "$got" != V10* ]] || fail "${where} 用了 /etc/os-release 的 VERSION: [${got}]"
}

assert_tar() {
  local ver="$1"
  local tarpath="${DIST}/bastion-desktop-${ver}-linux-${arch}.tar.gz"
  local listing
  [[ -f "$tarpath" ]] || fail "缺少 ${tarpath}"
  listing="$(tar -tzf "$tarpath" | sort)"
  [[ "$listing" == "$(printf '%s\n' \
    bastion-desktop.desktop \
    install.sh \
    opt/bastion-desktop/bastion-desktop \
    usr/share/applications/bastion-desktop.desktop | sort)" ]] \
    || fail "tar.gz 内容变了: ${listing}"
}

# 麒麟：打 tar.gz 和 rpm，跳过 deb。默认版本 0.1.0。
kylin_spec="${work}/kylin.spec"
kylin_control="${work}/kylin.control"
run_package "${work}/os-release-kylin" "$kylin_spec" "$kylin_control" ""
assert_version_token "$(spec_version "$kylin_spec")" "0.1.0" "rpm spec"
grep -q '/opt/bastion-desktop/bastion-desktop' "$kylin_spec" || fail "rpm 安装路径变了"
assert_tar "0.1.0"
[[ -f "${DIST}/bastion-desktop-0.1.0-1.${rpmarch}.rpm" ]] || fail "rpm 文件名没有用软件版本"
[[ ! -e "$kylin_control" ]] || fail "麒麟上不应打 deb，control 被写出来了"
if compgen -G "${DIST}/bastion-desktop_0.1.0_"*.deb >/dev/null; then
  fail "麒麟上不应生成 deb"
fi

# 非 rpm 系，os-release 仍是 VERSION="V10 (Lance)"。deb control 和文件名用覆盖版本。
deb_spec="${work}/deb.spec"
deb_control="${work}/deb.control"
run_package "${work}/os-release-deb" "$deb_spec" "$deb_control" "9.8.7"
assert_version_token "$(spec_version "$deb_spec")" "9.8.7" "rpm spec"
assert_version_token "$(spec_version "$deb_control")" "9.8.7" "deb control"
assert_tar "9.8.7"
[[ -f "${DIST}/bastion-desktop-9.8.7-1.${rpmarch}.rpm" ]] || fail "缺少按软件版本命名的 rpm"
debfile="${DIST}/bastion-desktop_9.8.7_${debarch}.deb"
[[ -f "$debfile" ]] || fail "缺少 ${debfile}"
if [[ -n "$real_dpkg" ]]; then
  assert_version_token "$("$real_dpkg" -f "$debfile" Version)" "9.8.7" "deb 包"
fi

echo "ok: os-release VERSION=\"V10 (Lance)\" 没有进入 tar/deb/rpm 版本"
