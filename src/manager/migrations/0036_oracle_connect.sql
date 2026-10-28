-- ============================================================================
-- 0036 Oracle 连接标识（服务名 / SID）
--
-- 历史资产只有 host/port。Oracle 监听靠 SERVICE_NAME 或 SID 定位实例，
-- 缺了这两项网关无法改写 CONNECT 描述符。
-- 服务名与 SID 都可空：非 Oracle 资产保持空；Oracle 资产由接口校验至少填一项。
-- ============================================================================

ALTER TABLE ast_asset
    ADD COLUMN IF NOT EXISTS oracle_service_name VARCHAR(128),
    ADD COLUMN IF NOT EXISTS oracle_sid          VARCHAR(64);

COMMENT ON COLUMN ast_asset.oracle_service_name IS
    'Oracle 服务名（SERVICE_NAME）。与 oracle_sid 至少填一项；两者都有时网关优先服务名';
COMMENT ON COLUMN ast_asset.oracle_sid IS
    'Oracle 实例 SID。与 oracle_service_name 至少填一项';

COMMENT ON COLUMN ops_session.end_reason IS
    '结束原因编码：admin_kill / client_disconnect / remote_closed / gateway_shutdown / node_offline / no_live_conn / timeout / policy_deny';

-- 出厂规则几乎都是 shell。Oracle 网关走同一套引擎，这里补三条只作用于 oracle 的规则，
-- 避免 DROP 这类语句在默认策略下全部放行，也不让这些正则去碰 SSH 命令。
INSERT INTO policy_command
    (tenant_id, policy_name, policy_type, match_type, pattern, risk_level, action, scope, priority, enabled, is_builtin, remark)
VALUES
 (0, 'Oracle 禁止删除对象', 'blacklist', 'regex',
  '(?i)\bDROP\s+(TABLE|USER|INDEX|VIEW|SEQUENCE|PROCEDURE|FUNCTION|PACKAGE|TRIGGER|DATABASE|TABLESPACE|SYNONYM)\b',
  'critical', 'deny', '{"protocols":["oracle"]}'::jsonb, 18, true, true, '删除数据库对象'),
 (0, 'Oracle 禁止清空表', 'blacklist', 'regex',
  '(?i)\bTRUNCATE\s+TABLE\b',
  'critical', 'deny', '{"protocols":["oracle"]}'::jsonb, 19, true, true, '清空表数据'),
 (0, 'Oracle 账号与授权变更告警', 'blacklist', 'regex',
  '(?i)\b(CREATE|ALTER)\s+USER\b|\bIDENTIFIED\s+BY\b|\bGRANT\b|\bREVOKE\b',
  'danger', 'audit', '{"protocols":["oracle"]}'::jsonb, 53, true, true, '账号与授权变更需留痕')
ON CONFLICT (tenant_id, policy_name) DO NOTHING;
