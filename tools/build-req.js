'use strict'
// 生成《堡垒机系统需求文档》
const path = require('path')
const { DocBuilder } = require('./lib/docbuild')

async function main() {
  console.log('rendering figures ...')
  require('./figs-req-a')
  require('./figs-req-b')
  require('./figs-extra')

  const d = new DocBuilder({
    title: '堡垒机系统需求文档',
    subject: '堡垒机（运维安全审计系统）软件需求规格说明',
    header: '堡垒机系统需求文档  V1.1',
    footerLeft: '内部公开',
    footerRight: 'BHL-SRS-2025-001',
    author: '信息安全与基础架构部',
  })

  require('./content-req-a')(d)
  require('./content-req-b')(d)
  require('./content-req-c')(d)
  require('./content-req-d')(d)

  const out = path.join(__dirname, '..', 'docs', '堡垒机系统需求文档.docx')
  await d.build(out)
  console.log('built:', out)
}

main().catch((e) => { console.error(e); process.exit(1) })
