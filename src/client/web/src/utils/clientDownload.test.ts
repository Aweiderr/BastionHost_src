import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import {
  CLIENT_PACKAGES,
  KYLIN_RPM_FILENAME,
  KYLIN_RPM_HREF,
  LINUX_TAR_FILENAME,
  LINUX_TAR_HREF,
  WINDOWS_CLIENT_FILENAME,
  WINDOWS_CLIENT_HREF,
  suggestClientPackageId,
} from './clientDownload.ts'

const VERSION = /\d+\.\d+\.\d+/

test('登录页下载地址按操作系统和格式分开，且不含版本号', () => {
  assert.equal(WINDOWS_CLIENT_FILENAME, 'bastion-desktop.exe')
  assert.equal(WINDOWS_CLIENT_HREF, '/bastion/downloads/bastion-desktop.exe')
  assert.equal(KYLIN_RPM_FILENAME, 'bastion-desktop-kylin-x86_64.rpm')
  assert.equal(KYLIN_RPM_HREF, '/bastion/downloads/bastion-desktop-kylin-x86_64.rpm')
  assert.equal(LINUX_TAR_FILENAME, 'bastion-desktop-linux-x86_64.tar.gz')
  assert.equal(LINUX_TAR_HREF, '/bastion/downloads/bastion-desktop-linux-x86_64.tar.gz')

  assert.equal(CLIENT_PACKAGES.length, 3)
  const ids = CLIENT_PACKAGES.map((pkg) => pkg.id)
  assert.deepEqual(ids, ['windows-exe', 'kylin-rpm', 'kylin-tar'])

  const hrefs = new Set<string>()
  for (const pkg of CLIENT_PACKAGES) {
    assert.equal(pkg.href.includes('#'), false)
    assert.equal(pkg.href.includes('?'), false)
    assert.equal(VERSION.test(pkg.filename), false, pkg.filename)
    assert.equal(VERSION.test(pkg.href), false, pkg.href)
    assert.equal(pkg.href.endsWith(pkg.filename), true)
    assert.equal(pkg.href.startsWith('/bastion/downloads/'), true)
    hrefs.add(pkg.href)
  }
  assert.equal(hrefs.size, 3)
})

test('下载按钮写明操作系统和格式，麒麟 RPM 为主、tar.gz 为备选', () => {
  const windows = CLIENT_PACKAGES.find((pkg) => pkg.id === 'windows-exe')
  const rpm = CLIENT_PACKAGES.find((pkg) => pkg.id === 'kylin-rpm')
  const tar = CLIENT_PACKAGES.find((pkg) => pkg.id === 'kylin-tar')
  assert.ok(windows && rpm && tar)

  assert.match(windows.label, /Windows/)
  assert.match(windows.label, /\.exe/)
  assert.equal(windows.role, 'primary')

  assert.match(rpm.label, /麒麟/)
  assert.match(rpm.label, /RPM/)
  assert.equal(rpm.role, 'primary')

  assert.match(tar.label, /麒麟/)
  assert.match(tar.label, /tar\.gz/)
  assert.equal(tar.role, 'alternative')
})

test('识别访问者系统时只建议一份，不从列表里拿掉其它系统', () => {
  assert.equal(
    suggestClientPackageId('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'),
    'windows-exe',
  )
  assert.equal(suggestClientPackageId('Mozilla/5.0 (Windows NT 10.0; WOW64)'), 'windows-exe')
  assert.equal(suggestClientPackageId('Win32'), 'windows-exe')

  assert.equal(
    suggestClientPackageId(
      'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/86.0.4240.198 Safari/537.36 Kylin/V10',
    ),
    'kylin-rpm',
  )
  assert.equal(suggestClientPackageId('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36'), 'kylin-rpm')
  assert.equal(suggestClientPackageId('Linux x86_64'), 'kylin-rpm')

  assert.equal(suggestClientPackageId('Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)'), null)
  assert.equal(
    suggestClientPackageId('Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)', 'Linux x86_64'),
    null,
  )
  assert.equal(
    suggestClientPackageId('Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'Linux x86_64'),
    'windows-exe',
  )
  assert.equal(suggestClientPackageId('', 'Win32'), 'windows-exe')
  assert.equal(suggestClientPackageId('', 'Linux x86_64'), 'kylin-rpm')
  assert.equal(suggestClientPackageId('', 'MacIntel'), null)
  assert.equal(suggestClientPackageId('Mozilla/5.0 (Linux; Android 14; Pixel)'), null)
  assert.equal(suggestClientPackageId('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)'), null)
  assert.equal(suggestClientPackageId(''), null)

  const suggested = suggestClientPackageId('Windows')
  assert.ok(CLIENT_PACKAGES.some((pkg) => pkg.id === suggested))
  assert.equal(CLIENT_PACKAGES.length, 3)
})

test('登录页使用全部安装包，而不是只链到 Windows exe', () => {
  const src = readFileSync(new URL('../views/LoginView.vue', import.meta.url), 'utf8')
  assert.match(src, /CLIENT_PACKAGES/)
  assert.match(src, /v-for="pkg in CLIENT_PACKAGES"/)
  assert.match(src, /suggestedPackageId/)
  assert.match(src, /pkg\.href/)
  assert.match(src, /pkg\.label/)
})
