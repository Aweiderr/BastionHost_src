-- ============================================================================
-- 0034 会话空闲超时：说明生效位置，字典与审计文案对齐
--
-- security.session.idle_minutes 自 0008 起写在系统配置里，默认 30。
-- 管理服务和代理都不读它。真正掐断空闲运维会话的是代理环境变量
-- BASTION_IDLE_TIMEOUT（默认 30m，与这 30 分钟同一默认值）。
--
-- 本迁移只改说明和会话状态字典，不新增列、不改已有会话行。
-- 结束原因编码仍是 timeout（与 status 相同），不另造 idle_timeout，
-- 以便和审计页即将使用的结束原因字典对齐。
-- ============================================================================

UPDATE sys_config
   SET description = '文档默认值（分钟），与代理环境变量 BASTION_IDLE_TIMEOUT 的默认 30m 一致。实际空闲断开只看该环境变量；本项不参与判定。',
       update_time = now()
 WHERE tenant_id = 0
   AND config_key = 'security.session.idle_minutes';

UPDATE sys_dict_item AS i
   SET item_label = '超时结束',
       update_time = now()
  FROM sys_dict AS d
 WHERE i.dict_id = d.dict_id
   AND d.dict_code = 'session_status'
   AND i.item_value = 'timeout'
   AND i.item_label IS DISTINCT FROM '超时结束';
