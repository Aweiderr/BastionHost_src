'use strict'
// 生成《堡垒机详细设计文档》
const path = require('path')
const { DocBuilder } = require('./lib/docbuild')

async function main() {
  console.log('rendering figures ...')
  require('./figs-dd-proto-a')
  require('./figs-dd-proto-b')
  require('./figs-dd-flow')
  require('./figs-dd-arch')
  require('./figs-dd-state')

  const d = new DocBuilder({
    title: '堡垒机详细设计文档',
    subject: '堡垒机（运维安全审计系统）详细设计说明',
    header: '堡垒机详细设计文档  V1.0',
    footerLeft: '内部公开',
    footerRight: 'BHL-DD-2025-001',
    author: '信息安全与基础架构部',
  })

  require('./content-dd-a')(d)
  require('./content-dd-b')(d)
  require('./content-dd-c')(d)
  require('./content-dd-d')(d)

  const out = path.join(__dirname, '..', 'docs', '堡垒机详细设计文档.docx')
  await d.build(out)
  console.log('built:', out)
}

main().catch((e) => { console.error(e); process.exit(1) })
