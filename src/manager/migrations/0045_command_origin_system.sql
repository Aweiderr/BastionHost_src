-- ============================================================================
-- 0045 命令来源增加 system
--
-- 产品内 Oracle SQL 工作台会用已经打开的会话读取数据字典
-- （ALL_USERS / ALL_TABLES / ALL_VIEWS / ALL_TAB_COLUMNS），用来画对象树。
-- 这些语句不是运维人员敲的，也不是 JDBC/OCI 连上后的身份探测，
-- 不能标成 user，也不能标成 client_auto。
--
-- 0038 的检查约束只允许 user / client_auto。不放宽就写不进 system。
-- 本迁移只放宽取值，不改已有行，不删除任何留痕。
-- ============================================================================

ALTER TABLE ops_session_command
    DROP CONSTRAINT IF EXISTS ck_command_origin;

ALTER TABLE ops_session_command
    ADD CONSTRAINT ck_command_origin CHECK (command_origin IN ('user', 'client_auto', 'system'));

COMMENT ON COLUMN ops_session_command.command_origin IS
    '命令来源：user（用户输入，缺省）/ client_auto（客户端自动发出）/ system（堡垒机代发的只读元数据）。只用于区分展示，不改变策略处置。';

-- 筛「仅系统元数据」时用。绝大多数行仍是 user，不给这列建全量索引。
CREATE INDEX IF NOT EXISTS idx_cmd_system
    ON ops_session_command (tenant_id, command_time DESC)
    WHERE command_origin = 'system';
