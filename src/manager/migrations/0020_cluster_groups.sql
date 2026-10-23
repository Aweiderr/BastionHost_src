-- ============================================================================
-- 0020 集群定义与成员（运维手动录入）
--
-- 与 0019（sys_node 节点自注册）的分工：
--
--   0019 回答"谁在跑、有多忙"—— 进程自己上报的运行事实，用于负载观测
--        与准入下发；
--   0020 回答"谁应该在一起"—— 运维定义的集群归属，是管理意图。
--
-- 为什么集群成员要手动录入，而不是让节点自注册自动成团：
--   自注册只能说明"这个进程在跑"，说明不了"它属于哪个集群"。
--   同一个管理服务下面往往还挂着测试机、备用机、甚至别的用途的代理节点，
--   它们都会注册进来。自动成团等于替运维做了"这些应该在一起"的判断，
--   而运维真正想表达的是"我这次只把 A、B 两台组成双机"。
--
-- 前置条件由运维保证：成员设备之间网络可达、且设备上已部署堡垒机服务端
-- （默认探测 :8081）。管理服务对每个成员做 HTTP 探测，据此展示在线状态；
-- 探测失败只影响"状态显示"，不回滚任何配置。
--
-- 「一台设备只能属于一个集群」由 uk_cluster_member_ip 唯一索引保证：
-- 放在数据库而不是只做应用层校验 —— 并发录入时"先查再插"挡不住
-- 两个请求同时通过检查，最后会留下一条谁都以为不存在的重复成员。
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_cluster (
    cluster_id      BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    cluster_name    VARCHAR(64)  NOT NULL,
    description     TEXT,
    -- 创建人，便于追责与联络
    created_by      BIGINT,
    created_by_name VARCHAR(64),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE sys_cluster IS '集群定义（运维手动录入的设备分组）';

CREATE UNIQUE INDEX IF NOT EXISTS uk_cluster_name ON sys_cluster (tenant_id, cluster_name);

CREATE TABLE IF NOT EXISTS sys_cluster_member (
    member_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    cluster_id      BIGINT       NOT NULL,
    -- 设备 IP：集群归属的唯一标识（一台设备只能在一个集群内）
    device_ip       VARCHAR(64)  NOT NULL,
    -- 堡垒机服务端观测端口，默认 8081
    device_port     INT          NOT NULL DEFAULT 8081,
    -- 设备别名（可选），留空时界面直接显示 IP
    device_name     VARCHAR(128) NOT NULL DEFAULT '',

    -- ---- 运维意图（只能由人修改，探测不会覆盖）----
    -- active 正常接单 / draining 停止接收新会话 / disabled 摘除
    admin_state     VARCHAR(16)  NOT NULL DEFAULT 'active',
    weight          INT          NOT NULL DEFAULT 100,
    max_sessions    INT          NOT NULL DEFAULT 300,

    -- ---- 探测结果（由管理服务主动探测写入）----
    reachable       BOOLEAN      NOT NULL DEFAULT false,
    probe_error     TEXT,
    -- 探测到的对端身份，用于确认"这个 IP 上确实是堡垒机服务端"
    node_id         VARCHAR(64)  NOT NULL DEFAULT '',
    version         VARCHAR(64)  NOT NULL DEFAULT '',
    active_sessions INT          NOT NULL DEFAULT 0,
    last_probe_at   TIMESTAMPTZ,
    last_ok_at      TIMESTAMPTZ,

    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  sys_cluster_member IS '集群成员设备：运维录入的 IP + 探测得到的运行状态';
COMMENT ON COLUMN sys_cluster_member.device_ip IS '设备 IP，全局唯一（一台设备只能属于一个集群）';
COMMENT ON COLUMN sys_cluster_member.admin_state IS '运维意图：active / draining / disabled，不被探测覆盖';

-- 集群内查询
CREATE INDEX IF NOT EXISTS idx_cluster_member_cluster ON sys_cluster_member (cluster_id, device_ip);
-- 心跳按 IP 反查成员（用于下发准入结论）
CREATE INDEX IF NOT EXISTS idx_cluster_member_ip ON sys_cluster_member (device_ip);

-- 一台设备只能属于一个集群
CREATE UNIQUE INDEX IF NOT EXISTS uk_cluster_member_ip ON sys_cluster_member (device_ip);

-- ---------------------------------------------------------------------------
-- 配置项
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'cluster.probe.interval_seconds', '15', 'int', 'system',
  '集群成员探测间隔（秒）。探测是管理服务主动发起的 HTTP 请求，间隔过短会在设备较多时产生可观流量'),
 (0, 'cluster.probe.timeout_seconds', '3', 'int', 'system',
  '单个成员探测超时（秒）。超过即判定不可达'),
 (0, 'cluster.member.default_port', '8081', 'int', 'system',
  '成员设备上堡垒机服务端的默认观测端口，录入 IP 时可单独指定')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
