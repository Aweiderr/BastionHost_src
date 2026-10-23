'use strict'
/**
 * 用 Chrome DevTools Protocol 驱动无头浏览器截图
 *
 * 相比 `chrome --screenshot`，CDP 方式可以先注入登录会话、
 * 等页面真正渲染完成再截图，因此能验证"登录后的界面"。
 *
 * 用法：
 *   node browser-shot.js <url> <输出png> [会话JSON]
 *
 * 会话 JSON 形如：
 *   {"accessToken":"...","refreshToken":"...","user":{...}}
 * 传入后会先访问 /login 并写入 localStorage（键 bastion.auth.session），再打开目标地址。
 */
const { spawn } = require('child_process')
const fs = require('fs')
const os = require('os')
const path = require('path')

const CHROME = process.env.CHROME_PATH ||
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe'
const PORT = parseInt(process.env.CDP_PORT || '9333', 10)
const ORIGIN = process.env.BASTION_BASE || 'http://127.0.0.1:8080'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const [target, out, sessionJSON] = process.argv.slice(2)
  if (!target || !out) {
    console.log('用法: node browser-shot.js <url> <out.png> [sessionJSON] [waitMs]')
    process.exit(2)
  }
  const waitMs = parseInt(process.argv[5] || '4500', 10)

  const profile = path.join(os.tmpdir(), 'bastion-cdp-' + Date.now())
  const chrome = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-sandbox', '--no-first-run',
    '--no-default-browser-check', '--hide-scrollbars',
    '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + profile,
    '--window-size=1440,1000',
    'about:blank',
  ], { stdio: 'ignore' })

  let ws
  try {
    // 等待调试端口就绪
    let targets = null
    for (let i = 0; i < 30; i++) {
      await sleep(300)
      try {
        const res = await fetch(`http://127.0.0.1:${PORT}/json/list`)
        targets = await res.json()
        if (targets.some((t) => t.type === 'page')) break
      } catch (e) { /* 还没起来 */ }
    }
    const page = (targets || []).find((t) => t.type === 'page')
    if (!page) throw new Error('未找到可调试页面')

    ws = new WebSocket(page.webSocketDebuggerUrl)
    await new Promise((resolve, reject) => {
      ws.addEventListener('open', resolve)
      ws.addEventListener('error', reject)
    })

    let seq = 0
    const pending = new Map()
    const consoleErrors = []
    ws.addEventListener('message', (ev) => {
      const msg = JSON.parse(ev.data)
      if (msg.id && pending.has(msg.id)) {
        // 统一回传 CDP 的 result 体，调用方少解一层
        pending.get(msg.id)(msg.result || {})
        pending.delete(msg.id)
        return
      }
      if (msg.method === 'Runtime.exceptionThrown') {
        const d = msg.params?.exceptionDetails
        consoleErrors.push(d?.exception?.description || d?.text || 'unknown')
      }
      if (msg.method === 'Runtime.consoleAPICalled' && msg.params?.type === 'error') {
        consoleErrors.push((msg.params.args || []).map((a) => a.value || a.description).join(' '))
      }
    })
    const send = (method, params) => new Promise((resolve) => {
      const id = ++seq
      pending.set(id, resolve)
      ws.send(JSON.stringify({ id, method, params: params || {} }))
    })

    await send('Page.enable')
    await send('Runtime.enable')

    if (sessionJSON) {
      await send('Page.navigate', { url: ORIGIN + '/login' })
      await sleep(2000)
      const expr = `localStorage.setItem("bastion.auth.session", ${sessionJSON})`
      await send('Runtime.evaluate', { expression: expr })
    }

    await send('Page.navigate', { url: target })
    await sleep(waitMs)

    const href = await send('Runtime.evaluate', { expression: 'location.href', returnByValue: true })
    const ready = await send('Runtime.evaluate', { expression: 'document.readyState', returnByValue: true })
    const htmlLen = await send('Runtime.evaluate', { expression: 'document.documentElement.outerHTML.length', returnByValue: true })
    console.log('实际地址 : ' + (href.result?.value || '?'))
    console.log('文档状态 : ' + (ready.result?.value || '?') + '  HTML 长度: ' + (htmlLen.result?.value ?? '?'))

    const title = await send('Runtime.evaluate', { expression: 'document.title', returnByValue: true })
    const text = await send('Runtime.evaluate', {
      expression: '(document.body.innerText || "").replace(/\\s+/g, " ").slice(0, 300)',
      returnByValue: true,
    })
    const shot = await send('Page.captureScreenshot', { format: 'png' })
    if (!shot.data) throw new Error('截图失败: ' + JSON.stringify(shot).slice(0, 200))
    fs.writeFileSync(out, Buffer.from(shot.data, 'base64'))

    console.log('URL      : ' + target)
    console.log('标题     : ' + (title.result?.value || ''))
    console.log('正文预览 : ' + (text.result?.value || '(空)'))
    if (consoleErrors.length) {
      console.log('控制台错误 (' + consoleErrors.length + '):')
      consoleErrors.slice(0, 5).forEach((e) => console.log('  - ' + String(e).split('\n')[0].slice(0, 200)))
    } else {
      console.log('控制台错误: 无')
    }
    console.log('截图     : ' + out + ' (' + fs.statSync(out).size + ' 字节)')
  } finally {
    try { if (ws) ws.close() } catch (e) { }
    chrome.kill()
  }
}

main().catch((e) => { console.error('失败:', e.message); process.exit(1) })
