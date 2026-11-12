import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  ORACLE_CONSOLE_MODE_LABEL as WEB_CONSOLE_LABEL,
  ORACLE_LOCAL_MODE_LABEL as WEB_LOCAL_LABEL,
  buildOracleConnectGuide as buildWebOracleConnectGuide,
} from '../../../../web/src/utils/oracleConnect.ts'
import { ORACLE_GATEWAY_HOST } from './oracleGateway.ts'
import {
  BASTION_PASSWORD_PLACEHOLDER,
  ORACLE_CONSOLE_MODE_LABEL,
  ORACLE_LOCAL_MODE_LABEL,
  DB_PASSWORD_PLACEHOLDER,
  DB_USERNAME_PLACEHOLDER,
  OPS_USERNAME_PLACEHOLDER,
  buildDesktopOracleConnectGuide,
  buildOracleConnectGuide,
  isOracleProtocol,
} from './oracleConnect.ts'

test('协议名大小写不敏感，只有 oracle 走客户端说明', () => {
  assert.equal(isOracleProtocol('oracle'), true)
  assert.equal(isOracleProtocol('Oracle'), true)
  assert.equal(isOracleProtocol(' ORACLE '), true)
  assert.equal(isOracleProtocol('ssh'), false)
  assert.equal(isOracleProtocol('vnc'), false)
  assert.equal(isOracleProtocol('rdp'), false)
  assert.equal(isOracleProtocol('mysql'), false)
  assert.equal(isOracleProtocol(''), false)
  assert.equal(isOracleProtocol(null), false)
  assert.equal(isOracleProtocol(undefined), false)
})

test('选定账号时服务名带 #账号，口令只留占位符', () => {
  const guide = buildOracleConnectGuide({
    opsUsername: 'ops01',
    assetId: 42,
    accountName: 'bastion',
  })
  assert.equal(guide.gateway, `${ORACLE_GATEWAY_HOST}:1521`)
  assert.equal(guide.serviceName, 'ops01@42#bastion')
  assert.equal(guide.dbUsername, 'bastion')
  assert.equal(
    guide.description,
    `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=${ORACLE_GATEWAY_HOST})(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@42#bastion)(BASTION_PASSWORD=BASTION_PASSWORD)))`,
  )
  assert.equal(
    guide.sqlplus,
    `sqlplus "bastion/数据库口令@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=${ORACLE_GATEWAY_HOST})(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@42#bastion)(BASTION_PASSWORD=BASTION_PASSWORD)))"`,
  )
  assert.equal(
    guide.sqlplusBash,
    `sqlplus 'bastion/数据库口令@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=${ORACLE_GATEWAY_HOST})(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@42#bastion)(BASTION_PASSWORD=BASTION_PASSWORD)))'`,
  )
  assert.equal(guide.jdbcUrl, `jdbc:oracle:thin:@${guide.description}`)
  assert.equal(guide.sqlplus.includes(BASTION_PASSWORD_PLACEHOLDER), true)
  assert.equal(guide.sqlplus.includes(DB_PASSWORD_PLACEHOLDER), true)
  assert.equal(guide.sqlplus.includes('127.0.0.1'), false)
  assert.equal(guide.jdbcUrl.includes('127.0.0.1'), false)
})

test('未选账号时服务名不带井号，库用户名用占位符', () => {
  const guide = buildOracleConnectGuide({
    opsUsername: ' ops01 ',
    assetId: '7',
    accountName: '  ',
  })
  assert.equal(guide.serviceName, 'ops01@7')
  assert.equal(guide.dbUsername, '')
  assert.match(
    guide.sqlplus,
    new RegExp(`^sqlplus "${DB_USERNAME_PLACEHOLDER}/${DB_PASSWORD_PLACEHOLDER}@\\(DESCRIPTION=`),
  )
  assert.match(
    guide.sqlplusBash,
    new RegExp(`^sqlplus '${DB_USERNAME_PLACEHOLDER}/${DB_PASSWORD_PLACEHOLDER}@`),
  )
  assert.equal(guide.sqlplus.includes('#'), false)
  assert.equal(guide.sqlplusBash.includes('#'), false)
})

test('缺少登录名时服务名用占位符，不写入真实口令', () => {
  const guide = buildOracleConnectGuide({ opsUsername: '', assetId: 1, accountName: 'app' })
  assert.equal(guide.serviceName, `${OPS_USERNAME_PLACEHOLDER}@1#app`)
  assert.equal(guide.sqlplus.includes('OpsSecret'), false)
  assert.equal(guide.sqlplusBash.includes('OpsSecret'), false)
  assert.equal(guide.jdbcUrl.includes('OpsSecret'), false)
  assert.equal(guide.description.includes(`BASTION_PASSWORD=${BASTION_PASSWORD_PLACEHOLDER}`), true)
})

test('可以覆盖网关主机和端口，sqlplus 与 JDBC 使用同一段描述符', () => {
  const guide = buildOracleConnectGuide({
    opsUsername: 'ops01',
    assetId: 9,
    accountName: 'app',
    gatewayHost: '10.0.0.8',
    gatewayPort: 11521,
  })
  assert.equal(guide.gateway, '10.0.0.8:11521')
  assert.ok(guide.description.includes('(HOST=10.0.0.8)(PORT=11521)'))
  assert.ok(guide.sqlplus.includes(guide.description))
  assert.ok(guide.sqlplusBash.includes(guide.description))
  assert.ok(guide.jdbcUrl.endsWith(guide.description))
})

test('同一输入与 Web 端说明一致，cmd 命令不用外层单引号', () => {
  const input = {
    opsUsername: 'ops01',
    assetId: 42,
    accountName: 'bastion',
    gatewayHost: '10.1.1.8',
    gatewayPort: 1521,
  }
  const desktop = buildOracleConnectGuide(input)
  const web = buildWebOracleConnectGuide(input)
  assert.deepEqual(desktop, web)
  assert.equal(desktop.sqlplus.startsWith('sqlplus "'), true)
  assert.equal(desktop.sqlplus.includes("'"), false)
})

/** CommandLineToArgvW 对不含反斜杠的命令行：双引号成对去掉，"" 变成一个双引号。 */
function windowsArgv(commandLine: string): string[] {
  const args: string[] = []
  let current = ''
  let inQuotes = false
  let started = false
  for (let i = 0; i < commandLine.length; i++) {
    const ch = commandLine[i]
    if (ch === '"') {
      if (inQuotes && commandLine[i + 1] === '"') {
        current += '"'
        i++
      } else {
        inQuotes = !inQuotes
      }
      started = true
      continue
    }
    if (!inQuotes && (ch === ' ' || ch === '\t')) {
      if (started) {
        args.push(current)
        current = ''
        started = false
      }
      continue
    }
    current += ch
    started = true
  }
  if (started) args.push(current)
  return args
}

test('cmd 命令用双引号包住整段登录串，sqlplus 收到的参数不含引号', () => {
  const guide = buildOracleConnectGuide({
    opsUsername: 'ops02',
    assetId: 23,
    accountName: 'user01',
  })
  const logon = `user01/${DB_PASSWORD_PLACEHOLDER}@${guide.description}`
  assert.equal(guide.sqlplus, `sqlplus "${logon}"`)
  assert.equal(guide.sqlplus.includes("'"), false)
  const quoteAt = guide.sqlplus.indexOf('"')
  assert.equal(guide.sqlplus.slice(0, quoteAt).includes('('), false)
  assert.equal(guide.sqlplus.slice(0, quoteAt).includes(')'), false)
  assert.deepEqual(windowsArgv(guide.sqlplus), ['sqlplus', logon])
  assert.equal(guide.sqlplusBash, `sqlplus '${logon}'`)
  assert.equal(guide.sqlplus.includes(DB_PASSWORD_PLACEHOLDER), true)
  assert.equal(guide.sqlplus.includes(BASTION_PASSWORD_PLACEHOLDER), true)
})

test('登录串里的双引号按 cmd 规则加倍，去掉引号后仍是原来的登录串', () => {
  const guide = buildOracleConnectGuide({
    opsUsername: 'ops',
    assetId: 1,
    accountName: 'ab"c',
  })
  const logon = `ab"c/${DB_PASSWORD_PLACEHOLDER}@${guide.description}`
  assert.deepEqual(windowsArgv(guide.sqlplus), ['sqlplus', logon])
  assert.equal(guide.sqlplus.startsWith('sqlplus "'), true)
  assert.equal(guide.sqlplus.endsWith('"'), true)
})

test('工作台说明用管理服务主机，不用资产目标地址，回环管理地址回退常量', () => {
  const lan = buildDesktopOracleConnectGuide({
    coreURL: 'http://10.1.1.8:8080',
    opsUsername: 'ops01',
    assetId: 42,
    accountName: 'bastion',
  })
  assert.equal(lan.gateway, '10.1.1.8:1521')
  assert.equal(lan.serviceName, 'ops01@42#bastion')
  assert.equal(lan.description.includes('(HOST=10.1.1.8)(PORT=1521)'), true)
  assert.equal(lan.sqlplus.includes('127.0.0.1'), false)
  assert.equal(lan.jdbcUrl.includes('192.168.106.114'), false)

  const local = buildDesktopOracleConnectGuide({
    coreURL: 'http://127.0.0.1:8080',
    opsUsername: 'ops01',
    assetId: 42,
    accountName: 'app',
  })
  assert.equal(local.gatewayHost, ORACLE_GATEWAY_HOST)
  assert.equal(local.gateway, `${ORACLE_GATEWAY_HOST}:1521`)
  assert.equal(local.sqlplus.includes('127.0.0.1'), false)
  assert.equal(local.description.includes('BASTION_PASSWORD=BASTION_PASSWORD'), true)
  assert.equal(local.sqlplus.includes('数据库口令'), true)
})

test('桌面端和 Web 端的两种连接方式名称一致', () => {
  assert.equal(ORACLE_LOCAL_MODE_LABEL, '本机 CMD / DBeaver')
  assert.equal(ORACLE_CONSOLE_MODE_LABEL, '堡垒机 SQL 会话')
  assert.equal(ORACLE_LOCAL_MODE_LABEL, WEB_LOCAL_LABEL)
  assert.equal(ORACLE_CONSOLE_MODE_LABEL, WEB_CONSOLE_LABEL)
})
