-- ============================================================================
-- 0022 主备切换事件
--
-- 记录"主节点是谁"的变化过程，解决三个问题：
--
--   1. **什么时候切的**：界面上只能看到"当前主节点"，看不到切换发生在几点。
--      事后核对切换耗时、确认 nopreempt 是否生效，都需要时间点；
--   2. **切了几次**：频繁抖动（VIP 来回漂）从当前状态里完全看不出来，
--      而这恰恰是最需要立刻发现的异常；
--   3. **为什么切的**：区分"正常切换（主机故障）"与"无人持有 VIP"、
--      "多台同时持有（脑裂）"—— 这三者的处理方式完全不同。
--
-- 记录时机：每次探测发现**持有 VIP 的设备发生了变化**时才写一条，
-- 不是每轮探测都写（探测 15 秒一次，全量写入会变成日志垃圾）。
-- 无变化（包括持续无主、持续脑裂）不重复记录，避免同一故障刷出上千条。
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_cluster_failover (
    event_id    BIGSERIAL    PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL DEFAULT 0,
    cluster_id  BIGINT       NOT NULL,
    -- 变更前后的主节点 IP；首次选出主节点时 from_ip 为空
    from_ip     VARCHAR(64)  NOT NULL DEFAULT '',
    to_ip       VARCHAR(64)  NOT NULL DEFAULT '',
    -- elect 首次选出 / switch 主备切换 / lost 主节点丢失 / split 脑裂
    event_type  VARCHAR(16)  NOT NULL,
    detail      TEXT,
    create_time TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  sys_cluster_failover IS '主备切换事件：记录主节点归属的变化过程，用于核对切换耗时与发现抖动';
COMMENT ON COLUMN sys_cluster_failover.event_type IS 'elect 首次选出 / switch 切换 / lost 无人持有 VIP / split 多台同时持有（脑裂）';

-- 界面按集群查最近若干条
CREATE INDEX IF NOT EXISTS idx_failover_cluster ON sys_cluster_failover (cluster_id, create_time DESC);
-- 按时间查全局（排查"什么时候开始抖的"）
CREATE INDEX IF NOT EXISTS idx_failover_time ON sys_cluster_failover (create_time DESC);
