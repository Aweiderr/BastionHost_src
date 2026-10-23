'use strict'
// 使用无头 Chrome DevTools Protocol 截取超长页面（支持按文本定位）
const { spawn } = require('child_process')
const fs = require('fs')
const http = require('http')
const os = require('os')

const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe'

function getJSON(path, port) {
  return new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port, path }, (res) => {
      let d = ''
      res.on('data', (c) => (d += c))
      res.on('end', () => { try { resolve(JSON.parse(d)) } catch (e) { reject(e) } })
    }).on('error', reject)
  })
}
const wait = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const url = process.argv[2]
  const out = process.argv[3]
  const width = parseInt(process.argv[4] || '1000', 10)
  let y0 = parseInt(process.argv[5] || '0', 10)
  const height = parseInt(process.argv[6] || '20000', 10)
  const findText = process.argv[7] || ''
  const offset = parseInt(process.argv[8] || '0', 10)

  const port = 9300 + Math.floor(Math.random() * 500)
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
    '--remote-debugging-port=' + port,
    '--user-data-dir=' + os.tmpdir() + '\\cdp-' + port,
    '--window-size=' + width + ',1200',
    'about:blank',
  ], { stdio: 'ignore' })

  let tabs = null
  for (let i = 0; i < 60; i++) {
    try { tabs = await getJSON('/json/list', port); if (tabs && tabs.length) break } catch (e) { }
    await wait(300)
  }
  if (!tabs || !tabs.length) { proc.kill(); throw new Error('chrome devtools not available') }
  const target = tabs.find((t) => t.type === 'page') || tabs[0]

  const WebSocket = require('ws')
  const ws = new WebSocket(target.webSocketDebuggerUrl, { perMessageDeflate: false, maxPayload: 512 * 1024 * 1024 })
  let id = 0
  const pending = new Map()
  ws.on('message', (raw) => {
    const msg = JSON.parse(raw.toString())
    if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id) }
  })
  const send = (method, params) => new Promise((resolve) => {
    const mid = ++id
    pending.set(mid, resolve)
    ws.send(JSON.stringify({ id: mid, method, params: params || {} }))
  })
  await new Promise((r) => ws.on('open', r))
  await send('Page.enable')
  await send('Emulation.setDeviceMetricsOverride', { width, height: 1200, deviceScaleFactor: 1, mobile: false })
  await send('Page.navigate', { url })
  await wait(2800)

  if (findText) {
    const expr = `(() => {
      const t = ${JSON.stringify(findText)};
      const all = document.querySelectorAll('p,h1,h2,h3,h4,div,span,td');
      for (const el of all) {
        if (el.children.length === 0 && el.textContent && el.textContent.indexOf(t) >= 0) {
          const r = el.getBoundingClientRect();
          window.scrollTo(0, 0);
          return r.top + window.scrollY;
        }
      }
      return -1;
    })()`
    const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true })
    const top = r.result && r.result.result ? r.result.result.value : -1
    if (top >= 0) { y0 = Math.max(0, Math.round(top) + offset); console.log('located "' + findText + '" at y=' + y0) }
    else console.log('text not found, fallback y=' + y0)
  }

  const res = await send('Page.captureScreenshot', {
    format: 'png',
    captureBeyondViewport: true,
    clip: { x: 0, y: y0, width, height, scale: 1 },
  })
  if (!res.result || !res.result.data) {
    console.error(JSON.stringify(res).slice(0, 400))
    throw new Error('screenshot failed')
  }
  fs.writeFileSync(out, Buffer.from(res.result.data, 'base64'))
  console.log('saved', out, fs.statSync(out).size)
  ws.close()
  proc.kill()
}

main().catch((e) => { console.error(e.message); process.exit(1) })
