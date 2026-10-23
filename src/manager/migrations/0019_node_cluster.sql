-- ============================================================================
-- 0019 集群节点与心跳
--
-- 需求背景（规划文档 5.3 集群化部署架构、9.1 可用性设计、9.3 扩展性设计；
-- 需求 NFR-01-01、NFR-02-02、NFR-07-01）：
--
--   代理节点   多节点 + 负载均衡，健康检查 3 秒一次、连续 3 次失败摘除，
--              新会话切换目标 ≤ 5 秒，存量会话重连、已采集录像不丢；
--   管理服务   多节点主备选主，心跳 + 仲裁，切换 ≤ 30 秒；
--   扩展方式   增加代理节点并注册到负载均衡，**不停机**。
--
-- 本表落库的是三件事，刻意分开：
--
--   1. **身份**      node_id / 角色 / 地址 / 版本 —— 集群里都有谁；
--   2. **管理意图**  admin_state（active / draining / disabled）——
--                    运维想让这个节点接不接新会话，这是"人的决定"；
--   3. **运行事实**  活跃会话数 / 上限 / CPU / 内存 / 最后心跳时间 ——
--                    "机器现在怎么样"，由节点自己心跳上报。
--
-- 为什么意图与事实要分开存：若只用一个 status 字段，节点每次心跳上报
-- 都会把它自己的状态写回 active，运维刚设置的 draining 会莫名其妙失效 ——
-- "我要把它摘下来做维护"这个意图必须比心跳更权威。
--
-- 在线 / 离线**不落库**，由 last_heartbeat_at 与当前时间推导：
-- 落库就需要有人负责把它改成离线，而那个"有人"恰恰是已经挂掉的节点。
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_node (
    -- 节点标识由节点自己声明（BASTION_NODE_ID），集群内唯一
    node_id           VARCHAR(64)  PRIMARY KEY,
    tenant_id         BIGINT       NOT NULL DEFAULT 0,
    node_name         VARCHAR(128) NOT NULL DEFAULT '',
    -- proxy 代理节点（服务端）/ manager 管理节点
    node_role         VARCHAR(16)  NOT NULL DEFAULT 'proxy',
    -- 服务地址，形如 10.0.0.11:8081，供负载与排障使用
    address           VARCHAR(255) NOT NULL DEFAULT '',
    version           VARCHAR(64)  NOT NULL DEFAULT '',
    -- 运维意图：active 正常接单 / draining 停止接收新会话 / disabled 摘除
    admin_state       VARCHAR(16)  NOT NULL DEFAULT 'active',
    -- 负载权重：一致性哈希与加权分配使用，0 表示不参与新会话分配
    weight            INT          NOT NULL DEFAULT 100,
    max_sessions      INT          NOT NULL DEFAULT 300,
    -- ---- 以下由心跳上报覆盖 ----
    active_sessions   INT          NOT NULL DEFAULT 0,
    cpu_percent       NUMERIC(5,2) NOT NULL DEFAULT 0,
    mem_percent       NUMERIC(5,2) NOT NULL DEFAULT 0,
    last_heartbeat_at TIMESTAMPTZ,
    -- 节点自报的最近一次异常（如密码机不可达），供界面提示
    last_error        TEXT,
    registered_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time       TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  sys_node IS '集群节点注册表：管理意图（admin_state）与运行事实（心跳负载）分开保存';
COMMENT ON COLUMN sys_node.admin_state IS '运维意图：active 正常 / draining 停止接收新会话 / disabled 摘除；不被心跳覆盖';
COMMENT ON COLUMN sys_node.weight IS '负载权重，0 表示不再参与新会话分配（与 draining 等效，但保留在集群中）';
COMMENT ON COLUMN sys_node.last_heartbeat_at IS '最后一次心跳时间；在线与否由此与当前时间推导，不落库';

-- 节点列表按角色与心跳排序，是界面最常用的查询
CREATE INDEX IF NOT EXISTS idx_node_role ON sys_node (node_role, node_id);
CREATE INDEX IF NOT EXISTS idx_node_heartbeat ON sys_node (last_heartbeat_at DESC NULLS LAST);

-- ---------------------------------------------------------------------------
-- 配置项
--
-- 间隔与超时的取值对齐规划文档"健康检查 3 秒一次、连续 3 次失败摘除"：
-- 心跳 3 秒一次，超过 10 秒没有心跳即视为离线（约 3 次丢失）。
-- 判定阈值比 3 次严格一点，是为了避免网络抖动一次就把节点判死。
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'cluster.heartbeat.interval_seconds', '3', 'int', 'system',
  '代理节点心跳上报间隔（秒）。规划文档要求健康检查 3 秒一次'),
 (0, 'cluster.heartbeat.timeout_seconds', '10', 'int', 'system',
  '超过该秒数未收到心跳即判定节点离线（约 3 次心跳丢失）。过长会让故障节点仍被分配新会话'),
 (0, 'cluster.node.max_sessions', '300', 'int', 'system',
  '单个代理节点的默认并发会话上限（与 proxy.max_sessions_per_node 一致）'),
 (0, 'cluster.drain.reject_message', '该节点正在维护，暂不接收新的运维会话，请稍后重试或联系管理员', 'string', 'system',
  '节点处于 draining 状态时拒绝新会话的提示语')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
