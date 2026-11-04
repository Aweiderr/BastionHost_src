/**
 * Windows 桌面客户端的下载地址。
 *
 * 管理服务把 BASTION_DOWNLOAD_DIR（默认 ./downloads）挂到
 * `<Web 前缀>/downloads/`，不校验登录态。登录页「下载客户端」就指向这里。
 *
 * 文件名与 client/desktop/wails.json 的 outputfilename 一致：
 * 在 Windows 上执行 `wails build` 后，将 build/bin/bastion-desktop.exe
 * 拷到下载目录，名字保持 bastion-desktop.exe。放好后不必重启管理服务。
 * 本页面只提供这一份静态文件，不做自动更新。
 */
export const WINDOWS_CLIENT_FILENAME = 'bastion-desktop.exe'

function downloadBase(): string {
  const raw = import.meta.env?.BASE_URL || '/bastion/'
  return raw.endsWith('/') ? raw : `${raw}/`
}

/** 例如 /bastion/downloads/bastion-desktop.exe */
export const WINDOWS_CLIENT_HREF = `${downloadBase()}downloads/${WINDOWS_CLIENT_FILENAME}`
