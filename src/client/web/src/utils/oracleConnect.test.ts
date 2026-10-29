import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ORACLE_GATEWAY_HOST, ORACLE_GATEWAY_PORT, resolveOracleGatewayHost } from './constants.ts'
import {
  BASTION_PASSWORD_PLACEHOLDER,
  DB_PASSWORD_PLACEHOLDER,
  DB_USERNAME_PLACEHOLDER,
  OPS_USERNAME_PLACEHOLDER,
  buildOracleConnectGuide,
  isOracleProtocol,
} from './oracleConnect.ts'

test('协议名大小写不敏感，只有 oracle 走客户端说明', () => {
  assert.equal(isOracleProtocol('oracle'), true)
  assert.equal(isOracleProtocol('Oracle'), true)
  assert.equal(isOracleProtocol(' ORACLE '), true)
  assert.equal(isOracleProtocol('ssh'), false)
  assert.equal(isOracleProtocol('mysql'), false)
  assert.equal(isOracleProtocol(''), false)
  assert.equal(isOracleProtocol(null), false)
  assert.equal(isOracleProtocol(undefined), false)
})

test('没有页面主机时网关地址用前端常量', () => {
  assert.equal(resolveOracleGatewayHost(undefined), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost(''), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('localhost'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('127.0.0.1'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('::1'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('10.1.1.8'), '10.1.1.8')
  assert.equal(ORACLE_GATEWAY_HOST, '192.168.106.114')
  assert.equal(ORACLE_GATEWAY_PORT, 1521)
})

test('选定账号时服务名带 #账号，口令只留占位符', () => {
  const guide = buildOracleConnectGuide({
    opsUsername: 'ops01',
    assetId: 42,
    accountName: 'bastion',
  })
  assert.equal(guide.gateway, '192.168.106.114:1521')
  assert.equal(guide.serviceName, 'ops01@42#bastion')
  assert.equal(guide.dbUsername, 'bastion')
  assert.equal(
    guide.description,
    '(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=192.168.106.114)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@42#bastion)(BASTION_PASSWORD=BASTION_PASSWORD)))',
  )
  assert.equal(
    guide.sqlplus,
    "sqlplus 'bastion/数据库口令@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=192.168.106.114)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@42#bastion)(BASTION_PASSWORD=BASTION_PASSWORD)))'",
  )
  assert.equal(guide.jdbcUrl, `jdbc:oracle:thin:@${guide.description}`)
  assert.ok(guide.sqlplus.includes(guide.description))
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
  assert.match(guide.sqlplus, new RegExp(`^sqlplus '${DB_USERNAME_PLACEHOLDER}/${DB_PASSWORD_PLACEHOLDER}@`))
  assert.equal(guide.sqlplus.includes('#'), false)
})

test('缺少登录名时服务名用占位符，不写入真实口令', () => {
  const guide = buildOracleConnectGuide({ opsUsername: '', assetId: 1, accountName: 'app' })
  assert.equal(guide.serviceName, `${OPS_USERNAME_PLACEHOLDER}@1#app`)
  assert.equal(guide.sqlplus.includes('OpsSecret'), false)
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
  assert.ok(guide.jdbcUrl.endsWith(guide.description))
})
