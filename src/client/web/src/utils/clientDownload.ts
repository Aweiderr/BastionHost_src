/**
 * 桌面客户端安装包的下载地址。
 *
 * 管理服务把 BASTION_DOWNLOAD_DIR（默认 ./downloads）挂到
 * `<Web 前缀>/downloads/`，不校验登录态。登录页按操作系统和格式分别给出链接。
 *
 * 公开文件名固定，不含版本号，和 Windows 的 bastion-desktop.exe 同一做法。
 * 打包脚本产出的是带版本的文件，例如：
 *   bastion-desktop-<版本>-1.ky10.x86_64.rpm
 *   bastion-desktop-<版本>-linux-x86_64.tar.gz
 * 运维把它们拷贝或在同一目录内符号链接成下面这三个稳定名字即可，不必改页面。
 * 符号链接的目标必须仍在下载目录内。放好后不必重启管理服务。
 * 本页面只提供这几份静态文件，不做自动更新。
 */

/** Windows 便携版。登录页原来就指向这个地址，必须保持不变。 */
export const WINDOWS_CLIENT_FILENAME = 'bastion-desktop.exe'

/** 银河麒麟 RPM。页面上的主选麒麟包。 */
export const KYLIN_RPM_FILENAME = 'bastion-desktop-kylin-x86_64.rpm'

/** 银河麒麟 tar.gz。没有 rpm 时的备选，解压后执行其中的 install.sh。 */
export const LINUX_TAR_FILENAME = 'bastion-desktop-linux-x86_64.tar.gz'

export type ClientPackageId = 'windows-exe' | 'kylin-rpm' | 'kylin-tar'

export interface ClientPackage {
  id: ClientPackageId
  /** 下载目录中的稳定文件名，不含版本号 */
  filename: string
  /** 例如 /bastion/downloads/bastion-desktop.exe */
  href: string
  /** 按钮文字：写明操作系统和格式，让人能区分 */
  label: string
  /** 悬停说明 */
  title: string
  /** primary 是该系统的主选；alternative 是同一系统的备选格式 */
  role: 'primary' | 'alternative'
}

function downloadBase(): string {
  const raw = import.meta.env?.BASE_URL || '/bastion/'
  return raw.endsWith('/') ? raw : `${raw}/`
}

function downloadHref(filename: string): string {
  return `${downloadBase()}downloads/${filename}`
}

/** 例如 /bastion/downloads/bastion-desktop.exe */
export const WINDOWS_CLIENT_HREF = downloadHref(WINDOWS_CLIENT_FILENAME)

/** 例如 /bastion/downloads/bastion-desktop-kylin-x86_64.rpm */
export const KYLIN_RPM_HREF = downloadHref(KYLIN_RPM_FILENAME)

/** 例如 /bastion/downloads/bastion-desktop-linux-x86_64.tar.gz */
export const LINUX_TAR_HREF = downloadHref(LINUX_TAR_FILENAME)

/**
 * 登录页始终展示的全部安装包，顺序固定。
 * 不按访问者系统隐藏：Windows 上的人可能要下麒麟包，反过来也一样。
 */
export const CLIENT_PACKAGES: readonly ClientPackage[] = [
  {
    id: 'windows-exe',
    filename: WINDOWS_CLIENT_FILENAME,
    href: WINDOWS_CLIENT_HREF,
    label: 'Windows 客户端（.exe）',
    title: '下载 Windows 桌面客户端（.exe）',
    role: 'primary',
  },
  {
    id: 'kylin-rpm',
    filename: KYLIN_RPM_FILENAME,
    href: KYLIN_RPM_HREF,
    label: '麒麟客户端（RPM · x86_64）',
    title: '下载银河麒麟桌面客户端 RPM（x86_64）',
    role: 'primary',
  },
  {
    id: 'kylin-tar',
    filename: LINUX_TAR_FILENAME,
    href: LINUX_TAR_HREF,
    label: '麒麟客户端（tar.gz · x86_64）',
    title: '下载银河麒麟桌面客户端 tar.gz（x86_64）。解压后以 root 执行 install.sh',
    role: 'alternative',
  },
]

/**
 * 根据浏览器标识建议一份安装包，只用于标出「推荐」。
 * 无法判断时返回 null，页面不标推荐，三份链接仍然都在。
 * 麒麟和其它 Linux 桌面建议 RPM；tar.gz 保持为备选，不作为推荐项。
 * 手机和 macOS 不推荐桌面包。
 *
 * userAgent 能认出系统时不再看 platform，避免平台字段把已经认出的系统盖掉。
 * platform 只在 userAgent 里没有系统信息时用作补充（例如只剩 Win32 / Linux）。
 */
export function suggestClientPackageId(userAgent: string, platform = ''): ClientPackageId | null {
  const ua = userAgent || ''
  if (/Android|iPhone|iPad|iPod/i.test(ua)) return null
  if (/Windows|Win32|Win64|WOW64/i.test(ua)) return 'windows-exe'
  if (/Macintosh|Mac OS X/i.test(ua)) return null
  if (/Kylin|Linux|X11/i.test(ua)) return 'kylin-rpm'

  const plat = platform || ''
  if (/Android|iPhone|iPad/i.test(plat)) return null
  if (/Win/i.test(plat)) return 'windows-exe'
  if (/Linux/i.test(plat)) return 'kylin-rpm'
  return null
}

/** 读取当前浏览器的标识，供登录页决定给哪一份加「推荐」。 */
export function suggestClientPackageForNavigator(
  nav: { userAgent?: string; platform?: string } | undefined = typeof navigator === 'undefined' ? undefined : navigator,
): ClientPackageId | null {
  if (!nav) return null
  return suggestClientPackageId(nav.userAgent || '', nav.platform || '')
}
