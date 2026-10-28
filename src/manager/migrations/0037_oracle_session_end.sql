-- ============================================================================
-- 0037 Oracle 会话结束原因
--
-- 网关在授权通过后就会写入 ops_session。拨号或 TNS 握手没有走到 ACCEPT
-- 时结束原因是 connect_failed，用来和「已经连上、后来断开」区分开。
-- 列类型不用改，只把注释里的编码补全。
-- ============================================================================

COMMENT ON COLUMN ops_session.end_reason IS
    '结束原因编码：admin_kill / client_disconnect / remote_closed / gateway_shutdown / node_offline / no_live_conn / timeout / policy_deny / connect_failed';
