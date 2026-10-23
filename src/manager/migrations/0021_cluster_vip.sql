-- ============================================================================
-- 0021 集群 VIP（主备模式的可见性）
--
-- 主备（Keepalived + VIP）形态下，运维最需要知道两件事：
--   1. **当前主节点是哪一台**；
--   2. 备节点是否健康（切换后能不能顶上）。
--
-- 判定方式刻意选成"由节点自己报"：
--   VIP 在 keepalived 里是通过 `ip addr add` 加到网卡上的，
--   因此持有 VIP 的那台设备，它的本机 IPv4 列表里就会出现这个地址。
--   服务端在自己的观测接口里返回本机 IPv4 列表，管理服务拿它与集群
--   配置的 VIP 比对 —— 谁持有 VIP，谁就是主。
--
-- 这样做的好处：不需要 keepalived 回调、不需要额外 agent，也不依赖
-- "管理服务能访问到 VIP"（管理服务完全可能在另一个网段，
-- 而 VIP 只对客户端所在的网络有意义）。
--
-- keepalived 的 MASTER / BACKUP 状态名是**过程**，VIP 在谁身上才是**事实**；
-- 因此这里只存事实（holds_vip），不存状态名。若确有必要，可由
-- notify 脚本回调管理服务补充（见 deploy/keepalived-ha.md），但属可选。
-- ============================================================================

-- 集群的 VIP 地址；留空表示不使用 VIP（例如前置负载均衡的形态，或纯手动）
ALTER TABLE sys_cluster ADD COLUMN IF NOT EXISTS vip_addr VARCHAR(64) NOT NULL DEFAULT '';

-- 探测得到的本机 IP 列表（逗号分隔），用于判定是否持有 VIP
ALTER TABLE sys_cluster_member ADD COLUMN IF NOT EXISTS local_ips TEXT NOT NULL DEFAULT '';
-- 该设备当前是否持有集群 VIP
ALTER TABLE sys_cluster_member ADD COLUMN IF NOT EXISTS holds_vip BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN sys_cluster.vip_addr IS '集群 VIP 地址，留空表示不使用 VIP（主备模式必填）';
COMMENT ON COLUMN sys_cluster_member.local_ips IS '探测到的对端本机 IPv4 列表，用于判定它是否持有 VIP';
COMMENT ON COLUMN sys_cluster_member.holds_vip IS '该设备当前是否持有集群 VIP（持有者即主节点）';

INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'cluster.keepalived.hint', 'VIP 由 Keepalived 在两台设备之间漂移；管理服务按"谁持有 VIP"判定主备。详见 deploy/keepalived-ha.md', 'string', 'system',
  '主备模式说明（界面上展示，避免运维在别处找不到判定依据）')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
