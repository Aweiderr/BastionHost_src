#!/bin/sh
# 把已编译的 bastion-desktop 装到本机。需要 root。
set -eu
cd "$(dirname "$0")"
if [ ! -f ./bastion-desktop ]; then
  echo "当前目录没有 bastion-desktop。请先在本机运行 build.sh。" >&2
  exit 1
fi
install -d /opt/bastion-desktop /usr/share/applications
install -m 0755 ./bastion-desktop /opt/bastion-desktop/bastion-desktop
if [ -f ./bastion-desktop.desktop ]; then
  install -m 0644 ./bastion-desktop.desktop /usr/share/applications/bastion-desktop.desktop
fi
echo "已安装到 /opt/bastion-desktop/bastion-desktop"
echo "图形界面依赖本机的 GTK 3 和 WebKitGTK 4.0。关闭窗口会留在托盘；没有会话总线时关闭即退出。"
