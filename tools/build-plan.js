'use strict'
// 生成《堡垒机系统规划文档》
const path = require('path')
const { DocBuilder } = require('./lib/docbuild')

async function main() {
  // 先生成全部插图（require 时即渲染）
  console.log('rendering figures ...')
  require('./figs-plan-a')
  require('./figs-plan-b')
  require('./figs-plan-c')

  const d = new DocBuilder({
    title: '堡垒机系统规划文档',
    subject: '堡垒机（运维安全审计系统）总体规划',
    header: '堡垒机系统规划文档  V1.1',
    footerLeft: '内部公开',
    footerRight: 'BHL-PLAN-2025-001',
    author: '信息安全与基础架构部',
  })

  require('./content-plan-a')(d)

  const out = path.join(__dirname, '..', 'docs', '堡垒机系统规划文档.docx')
  await d.build(out)
  console.log('built:', out)
}

main().catch((e) => { console.error(e); process.exit(1) })
