-- ============================================================================
-- 0018 自检记录与策略
--
-- 需求：在密码服务自检的基础上，增加**软件完整性自检**，
-- 并把自检从"只有手动触发"扩展为四种时机：
--
--   init      初始化自检 —— 设备首次初始化流程中执行，确认交付状态可用
--   boot      开机自检   —— 服务每次启动时执行，确认依赖就绪
--   periodic  周期自检   —— 按固定间隔执行，用于长期运行的合规留痕
--   manual    单次自检   —— 用户在界面上点击触发
--
-- 关于"只输出通过与否"：界面只呈现结论（通过 / 不通过），
-- 但**明细必须落库**。自检不通过时运维要靠明细定位是哪一项失败；
-- 审计也需要"当时检查了什么"的依据，只留一个"不通过"等于没有信息。
-- 因此这里存 detail(JSONB)，由接口决定暴露多少。
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_selftest_record (
    record_id     BIGSERIAL    PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL DEFAULT 0,
    -- init / boot / periodic / manual
    trigger_type  VARCHAR(16)  NOT NULL,
    -- 整体结论：仅当所有检查项都通过时为 true
    passed        BOOLEAN      NOT NULL,
    -- 检查项明细：[{name, passed, detail, duration_ms}]
    detail        JSONB,
    item_count    INT          NOT NULL DEFAULT 0,
    failed_count  INT          NOT NULL DEFAULT 0,
    duration_ms   INT          NOT NULL DEFAULT 0,
    -- 手动触发时记录操作人；自动触发（开机/周期）为空
    operator_id   BIGINT,
    operator_name VARCHAR(64),
    error_message TEXT,
    create_time   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE sys_selftest_record IS '自检记录：四种触发时机的结论与明细，用于排障与合规留痕';
COMMENT ON COLUMN sys_selftest_record.passed IS '整体结论：所有检查项均通过才为 true';
COMMENT ON COLUMN sys_selftest_record.detail IS '检查项明细，界面只展示结论，明细供排障与审计';

CREATE INDEX IF NOT EXISTS idx_selftest_time ON sys_selftest_record (create_time DESC);
CREATE INDEX IF NOT EXISTS idx_selftest_trigger ON sys_selftest_record (trigger_type, create_time DESC);
-- 快速找出最近一次不通过的记录（界面告警用）
CREATE INDEX IF NOT EXISTS idx_selftest_failed ON sys_selftest_record (create_time DESC) WHERE passed = false;

-- ---------------------------------------------------------------------------
-- 配置项
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'selftest.init.enabled', 'true', 'bool', 'security',
  '初始化流程中执行自检，确认设备交付状态可用'),
 (0, 'selftest.boot.enabled', 'true', 'bool', 'security',
  '服务启动时执行开机自检，确认密码服务与程序完整性'),
 (0, 'selftest.periodic.enabled', 'true', 'bool', 'security',
  '启用周期自检；关闭后仅在开机与手动触发时自检'),
 (0, 'selftest.periodic.hours', '24', 'int', 'security',
  '周期自检间隔（小时）。长期运行的设备需要定期留痕，间隔过短会产生大量冗余记录'),
 (0, 'selftest.keep_days', '365', 'int', 'security',
  '自检记录保留天数。这是合规证据，默认保留一年')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
