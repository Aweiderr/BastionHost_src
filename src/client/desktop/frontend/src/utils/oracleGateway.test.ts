import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ORACLE_GATEWAY_HOST, ORACLE_GATEWAY_PORT, resolveOracleGatewayHost } from './oracleGateway.ts'

test('管理服务是回环或 Wails 窗口时，网关主机用与 Web 相同的常量', () => {
  assert.equal(ORACLE_GATEWAY_HOST, '192.168.106.114')
  assert.equal(ORACLE_GATEWAY_PORT, 1521)
  assert.equal(resolveOracleGatewayHost(undefined), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost(''), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('   '), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('localhost'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('127.0.0.1'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('::1'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('[::1]'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('wails.localhost'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('http://127.0.0.1:8080'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('http://localhost:8080/bastion'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('http://wails.localhost:34115'), ORACLE_GATEWAY_HOST)
  assert.equal(resolveOracleGatewayHost('http://'), ORACLE_GATEWAY_HOST)
})

test('管理服务写了局域网地址时，网关主机跟这个地址，去掉端口和路径', () => {
  assert.equal(resolveOracleGatewayHost('http://10.1.1.8:8080'), '10.1.1.8')
  assert.equal(resolveOracleGatewayHost('http://10.1.1.8:8080/bastion'), '10.1.1.8')
  assert.equal(resolveOracleGatewayHost('https://10.1.1.8'), '10.1.1.8')
  assert.equal(resolveOracleGatewayHost('10.1.1.8:8080'), '10.1.1.8')
  assert.equal(resolveOracleGatewayHost('http://192.168.106.114:8080'), '192.168.106.114')
  assert.equal(resolveOracleGatewayHost('http://[2001:db8::1]:8080'), '2001:db8::1')
})
