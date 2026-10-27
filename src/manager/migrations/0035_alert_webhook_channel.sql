-- ============================================================================
-- 0035 高危命令告警：在种子通道上追加 webhook
--
-- 0031 写入 cmd_high_risk 时 notify_channels 只有 ["db"]（仅落库）。
-- 推送地址不放进 notify_targets，也不写进本迁移：
--   只读环境变量 BASTION_ALERT_WEBHOOK_URL。
-- 未配置该变量时，webhook 通道记为 skipped，告警仍落库，不报错。
--
-- 只改仍是种子值 ["db"] 的规则，避免覆盖已经手工调整过的通道。
-- 邮件 / 钉钉没有现成客户端，不在这里塞未接线的通道名。
-- ============================================================================

UPDATE audit_alert_rule
   SET notify_channels = '["db", "webhook"]'::jsonb,
       update_time = now()
 WHERE tenant_id = 0
   AND rule_code = 'cmd_high_risk'
   AND notify_channels = '["db"]'::jsonb;
