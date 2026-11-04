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
