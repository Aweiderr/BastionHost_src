-- ============================================================================
-- 0031 主机指纹 / 会话强制中断权限 / 高危命令告警种子
-- ============================================================================

ALTER TABLE ast_asset
    ADD COLUMN IF NOT EXISTS ssh_host_key_fp     VARCHAR(128),
    ADD COLUMN IF NOT EXISTS ssh_host_key_policy VARCHAR(16) NOT NULL DEFAULT 'tofu';

COMMENT ON COLUMN ast_asset.ssh_host_key_fp IS
    '目标主机 SSH 公钥指纹（SHA256:base64，与 ssh.FingerprintSHA256 一致）';
COMMENT ON COLUMN ast_asset.ssh_host_key_policy IS
    '指纹策略：strict（必须匹配）/ tofu（首次信任并落库，变更阻断）/ insecure（仅调试）';

INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT r.tenant_id, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code IN ('sec_admin', 'sys_admin', 'super_admin')
  AND p.perm_code = 'ops:session:kill'
ON CONFLICT (role_id, perm_id) DO NOTHING;

INSERT INTO audit_alert_rule (
    tenant_id, rule_code, rule_name, rule_type, severity,
    condition_expr, threshold, window_sec, notify_channels, status, remark
)
SELECT 0, 'cmd_high_risk', '高危命令告警', 'command_policy', 'critical',
       '{"match":"risk_level in (danger,critical) or action=deny"}'::jsonb,
       1, 60, '["db"]'::jsonb, 'active',
       '策略命中高危/阻断命令时写入 audit_alert'
WHERE NOT EXISTS (
    SELECT 1 FROM audit_alert_rule WHERE tenant_id = 0 AND rule_code = 'cmd_high_risk'
);
