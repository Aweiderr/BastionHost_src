# 银河麒麟桌面客户端打包

Windows 安装包仍用仓库里的 Wails 流程（`wails build` / `wails build -nsis`）。这一目录补的是 **amd64 或 arm64 的 Linux 构建**，面向银河麒麟 V10，同类的统信、openEuler 只要能提供 WebKitGTK 4.0 也可以用同一套脚本。

脚本不会把 `node_modules`、`frontend/dist`、exe 或打好的安装包写进 git。`build/bin` 和 `build/dist` 已被忽略。

## 依赖

必须在 **Linux 本机**编译。Wails 的 Linux 版要 CGO 链接系统 WebKit，不能在 Windows 上交叉编出能跑的图形客户端。

| 需要 | 麒麟 V10 上常见的包 | Debian / Ubuntu |
| --- | --- | --- |
| C 编译器、pkg-config | `gcc`、`pkgconf-pkg-config` | `build-essential`、`pkg-config` |
| GTK 3 | `gtk3-devel` | `libgtk-3-dev` |
| WebKitGTK **4.0** | `webkit2gtk3-devel` | `libwebkit2gtk-4.0-dev` |
| Go | 与 `go.mod` 一致（当前为 1.27） | 同左 |
| Node.js | 20 及以上，含 npm | 同左 |
| Wails | `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0` | 同左 |

运行已装好的程序时还要有对应的运行库（`gtk3`、`webkit2gtk3` 或 `libwebkit2gtk-4.0-37`），以及一个带会话总线的图形桌面。托盘用 D-Bus 状态图标，不依赖 `libayatana`。没有会话总线时托盘起不来，关闭窗口会直接退出，避免进程关不掉。

本机 SFTP 需要 `openssh-clients` 里的 `sftp`，以及一个终端（`mate-terminal`、`ukui-terminal`、`gnome-terminal` 或 `xterm`）。本机 RDP 需要 `xfreerdp`。本机 VNC 需要 `vncviewer` 或 `xtigervncviewer`。这些不是编译依赖。

## 编译

在 `src/client/desktop` 下：

```bash
packaging/kylin/build.sh
```

成功后二进制在 `build/bin/bastion-desktop`。脚本发现没有 WebKitGTK 4.0 头文件会停下来，并写明缺什么，不会生成一个打不开的包。

云端或没有图形开发库的环境跑不完这一步。单元测试不依赖 WebKit，在 `src/client/desktop` 执行 `go test ./...`，前端在 `frontend` 执行 `npm test`。

## 打包

```bash
packaging/kylin/package.sh
```

一定会尝试生成 `build/dist/bastion-desktop-<版本>-linux-<架构>.tar.gz`。包里有二进制、桌面项和 `install.sh`。

- 机器上有 `rpmbuild` 时再打 rpm（麒麟常用）。
- 机器上有 `dpkg-deb` 且不是麒麟这类 rpm 系时再打 deb。
- 两个工具都没有时，只留下 tar.gz。解压后以 root 执行包里的 `install.sh`。

版本号默认 `0.1.0`，可用环境变量 `BASTION_DESKTOP_VERSION` 覆盖。

安装包 **没有国密签名**。签名要用现场密钥，脚本不会伪造。

## 放到登录页供下载

登录页不使用带版本的文件名。把本脚本生成的包放进管理服务的下载目录后，再拷贝或在**同一目录内**做符号链接（链接目标不能跳出该目录）：

| 打包产物 | 登录页使用的稳定文件名 |
| --- | --- |
| `bastion-desktop-<版本>-1.ky10.x86_64.rpm`（例如 `bastion-desktop-0.1.0-1.ky10.x86_64.rpm`） | `bastion-desktop-kylin-x86_64.rpm` |
| `bastion-desktop-<版本>-linux-x86_64.tar.gz`（例如 `bastion-desktop-0.1.0-linux-x86_64.tar.gz`） | `bastion-desktop-linux-x86_64.tar.gz` |

生产目录是 `/opt/bastion/downloads/`。对应地址是 `/bastion/downloads/bastion-desktop-kylin-x86_64.rpm` 和 `/bastion/downloads/bastion-desktop-linux-x86_64.tar.gz`。Windows 的 `bastion-desktop.exe` 仍放在同一目录，地址不变。RPM 是登录页上的麒麟主选，tar.gz 是备选。

## 装好之后

桌面菜单「堡垒机运维客户端」，或直接运行 `/opt/bastion-desktop/bastion-desktop`。管理服务地址仍按客户端原有规则：`BASTION_CORE_URL`、上次记住的地址，否则 `http://127.0.0.1:8080`。
