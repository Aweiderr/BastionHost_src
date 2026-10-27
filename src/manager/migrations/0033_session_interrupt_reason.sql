-- ============================================================================
-- 0033 会话结束原因与强制中断理由
--
-- 背景：
--   ops_session.status 已有 active / closed / terminated / timeout。
--   end_reason 用来记机器编码（admin_kill、client_disconnect 等）。
--   强制中断接口把管理员填写的自由文本直接写进了 end_reason，
--   自定义理由会盖掉 admin_kill，审计详情只能看到一串英文或一段原文，
--   无法同时给出「结束原因」和「中断理由」。
--
--   另外，管理员掐断后代理会上报 finish（status=closed、
--   end_reason=client_disconnect）。旧的 Close 无条件覆盖，
--   被中断的会话在审计里看起来像正常断开。应用层已改为先落库、
--   finish 不再覆盖终态；本迁移补上中断理由列，并整理历史行。
--
-- 本迁移：
--   1. 增加 ops_session.interrupt_reason，存放强制中断时填写的原文。
--   2. 历史 terminated 行：end_reason 若是自由文本，搬到 interrupt_reason，
--      再把结束原因编码归一为 admin_kill。
--      client_disconnect 等编码出现在 terminated 行上，是 finish 覆盖留下的，
--      不是管理员填写的理由，不搬进 interrupt_reason。
--   3. 字典里的会话状态文案与审计页对齐：正常结束 / 被中断 / 超时结束。
-- ============================================================================

ALTER TABLE ops_session
    ADD COLUMN IF NOT EXISTS interrupt_reason VARCHAR(512);

COMMENT ON COLUMN ops_session.interrupt_reason IS
    '管理员强制中断时填写的中断理由；未填写或非强制中断为空。机器结束原因仍在 end_reason。';

COMMENT ON COLUMN ops_session.end_reason IS
    '结束原因编码：admin_kill / client_disconnect / remote_closed / gateway_shutdown / node_offline / no_live_conn / timeout';

-- 自由文本原先塞在 end_reason 里。先搬走，再归一编码。
UPDATE ops_session
   SET interrupt_reason = end_reason
 WHERE status = 'terminated'
   AND coalesce(interrupt_reason, '') = ''
   AND coalesce(end_reason, '') <> ''
   AND end_reason <> 'admin_kill'
   AND end_reason NOT IN (
        'client_disconnect',
        'remote_closed',
        'gateway_shutdown',
        'node_offline',
        'no_live_conn',
        'timeout'
   );

UPDATE ops_session
   SET end_reason = 'admin_kill'
 WHERE status = 'terminated'
   AND coalesce(end_reason, '') IS DISTINCT FROM 'admin_kill';

UPDATE sys_dict_item AS i
   SET item_label = '正常结束',
       update_time = now()
  FROM sys_dict AS d
 WHERE i.dict_id = d.dict_id
   AND d.dict_code = 'session_status'
   AND i.item_value = 'closed'
   AND i.item_label IS DISTINCT FROM '正常结束';

UPDATE sys_dict_item AS i
   SET item_label = '被中断',
       update_time = now()
  FROM sys_dict AS d
 WHERE i.dict_id = d.dict_id
   AND d.dict_code = 'session_status'
   AND i.item_value = 'terminated'
   AND i.item_label IS DISTINCT FROM '被中断';

UPDATE sys_dict_item AS i
   SET item_label = '超时结束',
       update_time = now()
  FROM sys_dict AS d
 WHERE i.dict_id = d.dict_id
   AND d.dict_code = 'session_status'
   AND i.item_value = 'timeout'
   AND i.item_label IS DISTINCT FROM '超时结束';
