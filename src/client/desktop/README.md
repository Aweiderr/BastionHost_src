# README

## About

This is the official Wails Vue-TS template.

You can configure the project by editing `wails.json`. More information about the project settings can be found
here: https://wails.io/docs/reference/project-config

## Live Development

To run in live development mode, run `wails dev` in the project directory. This will run a Vite development
server that will provide very fast hot reload of your frontend changes. If you want to develop in a browser
and have access to your Go methods, there is also a dev server that runs on http://localhost:34115. Connect
to this in your browser, and you can call your Go code from devtools.

## Building

To build a redistributable, production mode package, use `wails build`.

## 放到登录页供下载

在 Windows 上执行 `wails build` 后，便携版在 `build/bin/bastion-desktop.exe`
（文件名来自 `wails.json` 的 `outputfilename`）。云端构建环境不编译这个 Windows 程序，
需要在 Windows 机器上编好再拷到管理服务。

管理服务按固定文件名提供安装包，不做自动更新。登录页同时列出 Windows、麒麟 RPM 和麒麟 tar.gz，未登录也可以下载。Windows 这一条的地址仍是：

`http://<管理服务主机>:<端口>/<BASTION_WEB_PREFIX>/downloads/bastion-desktop.exe`

前缀默认 `bastion`，即 `/bastion/downloads/bastion-desktop.exe`。

| 环境 | 把 `bastion-desktop.exe` 放到 |
| --- | --- |
| 生产（systemd，`WorkingDirectory=/opt/bastion`） | `/opt/bastion/downloads/bastion-desktop.exe` |
| 本地（在 `src/` 启动 manager，且未改 `BASTION_DOWNLOAD_DIR`） | `src/downloads/bastion-desktop.exe` |

麒麟包使用同样的目录，公开文件名是 `bastion-desktop-kylin-x86_64.rpm` 和 `bastion-desktop-linux-x86_64.tar.gz`（不含版本号）。打包脚本生成的带版本文件要拷贝或在该目录内符号链接成这两个名字，见 `packaging/kylin/README.md`。

目录由 `BASTION_DOWNLOAD_DIR` 指定，默认 `./downloads`（相对管理服务的工作目录）。
Windows 文件名必须是 `bastion-desktop.exe`。放好之后不必重启管理服务；目录里没有该文件时，
这个地址返回 JSON 404，而不是登录页 HTML。

`wails build -nsis` 生成的安装包不是当前 Windows 按钮的默认文件。若要改发安装包，
仍将安装程序命名为 `bastion-desktop.exe` 后放到上表路径。

## 管理服务地址

登录页地址按这个顺序决定，不会在编译期写死某一台管理机：

1. 进程环境变量 `BASTION_CORE_URL`（展台、自动化）。有值时本次启动用它，不用本地记录，启动时也不写回文件。
2. 本机配置里上次在登录页检测或登录时写入的地址。
3. 都没有时用 `http://127.0.0.1:8080`。

配置文件是 `core-url.json`（字段 `core_url`）。Windows 在 `%AppData%\bastion-desktop\`，Linux 在 `$XDG_CONFIG_HOME/bastion-desktop/`（未设置时为 `~/.config/bastion-desktop/`）。删掉该文件，或换一个没有它的用户配置，地址回到第 3 步。

检测和登录都会先调用 `SetCoreURL`，再去 ping 或登录。服务暂时不可达也会记住这次填写的地址。与本次 `BASTION_CORE_URL` 相同的那一次不写文件，避免展台把操作员已经记住的地址覆盖掉；把输入改成别的地址后再检测或登录会更新文件。只设置 `BASTION_AUTOLOGIN` 且登录成功时不会打开登录页，因此也不会改写文件。

### 点验

1. 删掉上述 `core-url.json`（或使用没有该文件的用户）。启动客户端，管理服务地址应为 `http://127.0.0.1:8080`，点「检测」访问的也是这个地址。
2. 改成一台非本机地址，例如 `http://192.168.106.114:8080`，点「检测」或直接登录。
3. 完全退出后再启动。输入框应是上一步的地址，检测打到该地址，而不是回到环回。
4. 另开一个进程并设置 `BASTION_CORE_URL` 为别的地址。这次启动显示环境变量里的地址，且不会改写第 3 步留下的文件。关掉环境变量后再启动，输入框回到文件里的地址。

## 图形会话（RDP / VNC）

工作台里 SSH 仍是终端标签。协议为 `vnc` 或 `rdp` 时打开图形标签，不再显示「该协议尚未接入」。
协议为 `oracle` 时打开客户端连接说明（网关、SERVICE_NAME、sqlplus、JDBC），口令只留占位符，不在本窗口建立数据库会话，也不拨 SSH 或画面通道。
SFTP 等没有客户端通路的协议仍只展示「尚未接入」，不会拿去拨 SSH。

桌面进程连接代理的 `GET /api/v1/webdesktop`（默认由管理服务地址推导为 `ws://<主机>:8081/api/v1/webdesktop`，
分离部署时用 `BASTION_DESKTOP_GATEWAY` 写成完整的 `ws://` 或 `wss://`）。认证、像素和键鼠与 Web「图形会话」是同一套：

- 首条文本是 `auth`（`client_type` 为 `desktop`）。口令留在代理里。
- 文本帧：`ready`、`credential-required`、`error`、`exit`。
- 二进制帧：`BJ` 头加大端坐标和 JPEG 脏矩形。旧的 `BM` + RGBA 仍能解析。不认识的魔数会提示协议不匹配。代理已经消化 VNC 的 ZRLE、Hextile、CopyRect 和 LastRect，这里按脏矩形画到画布。
- 键鼠文本：`pointer`、`key`（keysym + `KeyboardEvent.code`）、`wheel`。
- 剪贴板文本：`{"type":"clipboard","direction":"paste","text":"..."}`。`ready` 里的 `can_copy` 是远端到本机，`can_paste` 是本机到远端。不允许时服务端回 `clipboard-denied`，不带原文。

### 对一台 VNC 资产点验

在 Windows 上于本目录执行 `wails dev`（或 `wails build` 后运行 `build/bin/bastion-desktop.exe`）。
管理服务（默认 `:8080`）和代理（默认 `:8081`）已启动，当前账号是运维角色，并且该 VNC 资产已授权。

1. 登录后留在「运维工作台」。资产卡片上的 VNC 说明应是「将打开图形会话标签」，而不是「尚未接入」。
2. 选中例如 `192.168.106.114:5901` 这种 VNC 资产，点「连接」。新标签的协议是 VNC，状态从「连接中」变为「已连接」，中间是远端画面，而不是说明卡片。
3. 点击画面后移动鼠标、敲键盘。远端应跟着动。授权勾了「允许复制」时，远端复制的文本会进本机剪贴板；勾了「允许粘贴」时，Ctrl+V 会把本机文本送进远端剪贴板再按键。没勾的方向会被拒绝，状态栏写着「复制允许/禁止 · 粘贴允许/禁止」。
4. 点「关闭标签」，或让会话空闲到被代理掐断。客户端应留在工作台，标签变为「已结束」或被关掉，进程不退出。
5. 再连一台 RDP 资产。它走同一个图形标签；没有可连的 RDP 目标时，标签里是连接失败说明，而不是「该协议尚未接入」。

### 对一台 Oracle 资产点验

同一套客户端，当前账号已获授权，资产协议为 `oracle`。

1. 资产卡片上的说明应是「将打开客户端连接说明」，而不是「尚未接入」。按钮是「查看说明」。
2. 点「查看说明」。新标签协议是 ORACLE，状态是「连接说明」。中间是网关地址、SERVICE_NAME、sqlplus 和 JDBC，可以复制，不是「该协议尚未接入」。
3. 复制「sqlplus（Windows cmd）」。这一行以 `sqlplus "` 开头，口令位置是「数据库口令」和 `BASTION_PASSWORD`，没有真实口令。管理服务若是 `http://192.168.106.114:8080` 这类局域网地址，网关主机与它相同、端口 1521；本机 `127.0.0.1` 会回退到说明里的常量地址。资产卡片上的 `host:port` 不要拿去直连。
4. 再打开 SSH 或 VNC。终端和图形标签与原来一样，不会变成这段说明。

前端帧、分流和 Oracle 说明测试：在 `frontend` 目录执行 `npm test`。
Go 侧画面拨号和断开：在本目录执行 `go test ./internal/desk .`。

## 托盘

窗口打开期间会尝试在系统托盘放一个图标。Windows 用通知区域菜单，麒麟等 Linux 用会话总线上的状态图标。菜单有「打开主窗口」和「退出」。

点窗口关闭按钮时，托盘已经就绪就只把窗口藏起来，登录和会话还在。顶栏「最小化到托盘」也是隐藏。真正退出只用托盘里的「退出」。最小化按钮仍留在任务栏，托盘图标同时在。当前环境没有托盘（例如没有图形会话总线）时，关闭按钮会退出进程，避免关不掉。

## 断线重连

终端或图形会话如果不是自己关掉的，客户端会自动再拨一次。这一次仍走原来的会话分配：先问管理面该连哪台代理，再使用返回的 `ssh_addr` 或 `webdesktop_url`。登录令牌和内存里的口令还在，就不会回到登录页。

自动这一次失败后，标签上可以再点「重连一次」。每次点击只拨一次，进行中再点会提示稍候，不会自己排成循环。失败说明写在标签和提示里。

和管理服务失去联系时也不清登录。先自动探一次，失败后顶栏可以再点「重新连接」。令牌已经被服务端作废时，仍按原来的「已在其他端登录」回到登录页。

## 本机 SFTP / RDP / VNC

这些按钮打开的是本机已经安装的工具，目标地址来自会话分配，不使用资产自己的 host 和 port。

SFTP 要求该资产授权里 `can_file` 为真。命令形式是：

```text
sftp -P <堡垒机 SSH 端口> <运维用户>@<资产名或 ID>#<账号>@<堡垒机地址>
```

口令不放进命令，由弹出的窗口自己问。Windows 优先用 OpenSSH 的 `sftp.exe`，没有时才改用 WinSCP，地址仍然是堡垒机。麒麟上用 `sftp`，并找 `mate-terminal`、`ukui-terminal` 或 `xterm` 来显示窗口。

RDP / VNC 会拉起 `mstsc`、`xfreerdp` 或 `vncviewer`，主机是分配到的堡垒机节点，端口是 3389 或 5900。RDP 文件把 `redirectdrives` 设为 0，不写磁盘映射。服务端这两个端口目前只回提示，不是画面代理，所以进资产仍然用窗口里的图形会话。本机工具连不上时，提示会说明这一点，剪贴板授权也只在那个图形会话里生效。

## 麒麟安装包

可重复的构建和打包说明在 `packaging/kylin/README.md`。需要在麒麟或装有 WebKitGTK 4.0 开发包的 Linux 上执行 `packaging/kylin/build.sh`，再执行 `package.sh`。这里的环境如果没有这些开发包，脚本会停下来并写明缺什么，不会把二进制提交进仓库。
