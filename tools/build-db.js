'use strict'
// 生成《堡垒机数据库设计文档》
const path = require('path')
const { DocBuilder } = require('./lib/docbuild')

async function main() {
  console.log('rendering figures ...')
  require('./figs-db-er')

  const d = new DocBuilder({
    title: '堡垒机数据库设计文档',
    subject: '堡垒机（运维安全审计系统）数据库设计说明',
    header: '堡垒机数据库设计文档  V1.0',
    footerLeft: '内部公开',
    footerRight: 'BHL-DBD-2025-001',
    author: '信息安全与基础架构部',
  })

  require('./content-db-a')(d)
  require('./content-db-b')(d)
  require('./content-db-c')(d)

  const out = path.join(__dirname, '..', 'docs', '堡垒机数据库设计文档.docx')
  await d.build(out)
  console.log('built:', out)
}

main().catch((e) => { console.error(e); process.exit(1) })
