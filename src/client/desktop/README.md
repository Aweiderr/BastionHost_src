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

管理服务只按固定路径提供这一份文件，不做自动更新。登录页「下载客户端」的地址是：

`http://<管理服务主机>:<端口>/<BASTION_WEB_PREFIX>/downloads/bastion-desktop.exe`

前缀默认 `bastion`，即 `/bastion/downloads/bastion-desktop.exe`。未登录也可以下载。

| 环境 | 把 `bastion-desktop.exe` 放到 |
| --- | --- |
| 生产（systemd，`WorkingDirectory=/opt/bastion`） | `/opt/bastion/downloads/bastion-desktop.exe` |
| 本地（在 `src/` 启动 manager，且未改 `BASTION_DOWNLOAD_DIR`） | `src/downloads/bastion-desktop.exe` |

目录由 `BASTION_DOWNLOAD_DIR` 指定，默认 `./downloads`（相对管理服务的工作目录）。
文件名必须是 `bastion-desktop.exe`。放好之后不必重启管理服务；目录里没有该文件时，
这个地址返回 JSON 404，而不是登录页 HTML。

`wails build -nsis` 生成的安装包不是当前按钮的默认文件。若要改发安装包，
仍将安装程序命名为 `bastion-desktop.exe` 后放到上表路径。

## 图形会话（RDP / VNC）

工作台里 SSH 仍是终端标签。协议为 `vnc` 或 `rdp` 时打开图形标签，不再显示「该协议尚未接入」。
协议为 `oracle` 时打开客户端连接说明（网关、SERVICE_NAME、sqlplus、JDBC），口令只留占位符，不在本窗口建立数据库会话，也不拨 SSH 或画面通道。
SFTP 等没有客户端通路的协议仍只展示「尚未接入」，不会拿去拨 SSH。

桌面进程连接代理的 `GET /api/v1/webdesktop`（默认由管理服务地址推导为 `ws://<主机>:8081/api/v1/webdesktop`，
分离部署时用 `BASTION_DESKTOP_GATEWAY` 写成完整的 `ws://` 或 `wss://`）。认证、像素和键鼠与 Web「图形会话」是同一套：

- 首条文本是 `auth`（`client_type` 为 `desktop`）。口令留在代理里。
- 文本帧：`ready`、`credential-required`、`error`、`exit`。
- 二进制帧：`BJ` 头加大端坐标和 JPEG 脏矩形。旧的 `BM` + RGBA 仍能解析。不认识的魔数会提示协议不匹配。代理已经消化 VNC 的 ZRLE、Hextile、CopyRect 和 LastRect，这里按脏矩形画到画布。
- 键鼠文本：`pointer`、`key`（keysym + `KeyboardEvent.code`）、`wheel`。画面通道没有剪贴板消息。

### 对一台 VNC 资产点验

在 Windows 上于本目录执行 `wails dev`（或 `wails build` 后运行 `build/bin/bastion-desktop.exe`）。
管理服务（默认 `:8080`）和代理（默认 `:8081`）已启动，当前账号是运维角色，并且该 VNC 资产已授权。

1. 登录后留在「运维工作台」。资产卡片上的 VNC 说明应是「将打开图形会话标签」，而不是「尚未接入」。
2. 选中例如 `192.168.106.114:5901` 这种 VNC 资产，点「连接」。新标签的协议是 VNC，状态从「连接中」变为「已连接」，中间是远端画面，而不是说明卡片。
3. 点击画面后移动鼠标、敲键盘。远端应跟着动。Ctrl+C / Ctrl+V 会作为按键送到远端自己的剪贴板，本机剪贴板不会自动同步。
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
