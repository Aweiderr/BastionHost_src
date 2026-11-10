-- ============================================================================
-- 0043 多节点会话调度与管理服务选主
--
-- 编号说明：GitHub 主干的最后一份迁移是 0039。生产库和 SVN 已经占用并执行了
-- 0040（SSH 资产的 SFTP 开关）、0041（资产级 can_file / can_copy / can_paste）
-- 和 0042（按协议收敛这些上限，并关掉超出上限的授权）。本文件必须排在它们后面，
-- 所以用 0043。它只增加节点地址列、选主租约和调度配置，不改 ast_asset 或 acl_grant。
--
-- 调度本身读的是 sys_node 上的心跳与运维意图，任意一台管理服务都能算出
-- 同一个结果。选主租约只回答「这一刻谁是管理面的主」：多活时可选要求
-- 只有持有租约的实例受理分配（cluster.schedule.require_leader）。
-- 租约过期后其它实例可以接管，不依赖 Redis。
--
-- local_ips 是心跳上报的运行事实（本机 IPv4），用来把自注册节点关联到
-- 运维录入的集群成员。成员被停用或摘除时，即使节点自己的 admin_state
-- 仍是 active，调度也不能再把新会话分给它。
-- ============================================================================

ALTER TABLE sys_node ADD COLUMN IF NOT EXISTS local_ips TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN sys_node.local_ips IS '心跳上报的本机 IPv4，逗号分隔；用于按 IP 关联集群成员，不被当作运维意图';

CREATE TABLE IF NOT EXISTS sys_manager_leader (
    scope        VARCHAR(32)  PRIMARY KEY,
    leader_id    VARCHAR(64)  NOT NULL,
    leader_addr  VARCHAR(255) NOT NULL DEFAULT '',
    lease_until  TIMESTAMPTZ  NOT NULL,
    elected_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE sys_manager_leader IS '管理服务选主租约。过期后其它实例可接管；调度决策本身仍以 sys_node 为准';
COMMENT ON COLUMN sys_manager_leader.scope IS '租约范围，会话调度使用 schedule';
COMMENT ON COLUMN sys_manager_leader.lease_until IS '租约到期时间；到期前其它实例不得覆盖 leader_id';

INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'cluster.schedule.strategy', 'least_load', 'string', 'system',
  '新会话分配策略：least_load 按负载（会话数/权重）选最空闲节点；consistent_hash 按用户+资产+协议稳定落到同一健康节点'),
 (0, 'cluster.schedule.ssh_port', '2222', 'int', 'system',
  '分配结果里 SSH 网关端口。节点地址只带 HTTP 端口，SSH 端口用这项补齐'),
 (0, 'cluster.schedule.ws_scheme', 'ws', 'string', 'system',
  '分配给浏览器和桌面图形会话的 WebSocket 协议：ws 或 wss'),
 (0, 'cluster.schedule.require_leader', 'false', 'bool', 'system',
  '为 true 时只有持有选主租约的管理服务受理会话分配。默认 false：任意管理实例都按同一份节点表分配，避免选主抖动拒绝全部新会话'),
 (0, 'cluster.leader.lease_seconds', '15', 'int', 'system',
  '管理服务选主租约时长（秒）。持有者按约三分之一的间隔续约；到期后其它实例接管。规划目标是管理服务选主 ≤ 30 秒')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
