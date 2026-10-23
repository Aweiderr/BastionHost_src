-- ============================================================================
-- 0004 会话与审计
--
-- 分区说明：生产环境建议 audit_* 与 ops_session_* 表按时间做月度分区
-- （文档 6.2 节），MVP 阶段先建普通表 + 索引，待数据量增长后再迁移为分区表。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 会话
-- ---------------------------------------------------------------------------
CREATE TABLE ops_session (
    session_id      BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    session_no      VARCHAR(64)  NOT NULL,
    user_id         BIGINT       NOT NULL,
    username        VARCHAR(64)  NOT NULL,
    display_name    VARCHAR(128),
    asset_id        BIGINT       NOT NULL,
    asset_name      VARCHAR(128),
    account_id      BIGINT,
    account_name    VARCHAR(128),
    protocol        VARCHAR(32)  NOT NULL,
    client_type     VARCHAR(16)  NOT NULL DEFAULT 'web',
    client_ip       VARCHAR(45),
    proxy_node      VARCHAR(64),
    grant_source    VARCHAR(16)  NOT NULL DEFAULT 'manual',
    ticket_id       BIGINT,
    app_id          VARCHAR(64),
    operator_id     BIGINT,
    operator_name   VARCHAR(64),
    purpose         VARCHAR(255),
    start_time      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    end_time        TIMESTAMPTZ,
    duration_sec    INTEGER,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    end_reason      VARCHAR(64),
    command_count   INTEGER      NOT NULL DEFAULT 0,
    block_count     INTEGER      NOT NULL DEFAULT 0,
    file_count      INTEGER      NOT NULL DEFAULT 0,
    record_status   VARCHAR(16)  NOT NULL DEFAULT 'none',
    record_path     VARCHAR(1024),
    record_size     BIGINT       NOT NULL DEFAULT 0,
    record_digest   VARCHAR(128),
    record_chunks   INTEGER      NOT NULL DEFAULT 0,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE ops_session IS '运维会话表';
COMMENT ON COLUMN ops_session.client_type IS '客户端类型：web / windows / linux / api / ssh_client / rdp_client';
COMMENT ON COLUMN ops_session.grant_source IS '授权来源：manual / ticket / temp / binding（免审批直连）';
COMMENT ON COLUMN ops_session.app_id IS 'API 接入时的应用标识（FR-08-12 操作人归属）';
COMMENT ON COLUMN ops_session.operator_id IS '实际操作人（API 接入时区别于应用凭证）';
COMMENT ON COLUMN ops_session.status IS '状态：active / closed / terminated / timeout';
COMMENT ON COLUMN ops_session.record_status IS '录像状态：none / recording / sealed / failed';
CREATE UNIQUE INDEX uk_session_no ON ops_session (session_no);
CREATE INDEX idx_session_user ON ops_session (tenant_id, user_id, start_time DESC);
CREATE INDEX idx_session_asset ON ops_session (tenant_id, asset_id, start_time DESC);
CREATE INDEX idx_session_time ON ops_session (start_time DESC);
CREATE INDEX idx_session_status ON ops_session (status) WHERE status = 'active';
CREATE INDEX idx_session_operator ON ops_session (tenant_id, operator_id, start_time DESC) WHERE operator_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 会话命令记录
-- ---------------------------------------------------------------------------
CREATE TABLE ops_session_command (
    command_id      BIGSERIAL    PRIMARY KEY,
    session_id      BIGINT       NOT NULL,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    seq_no          INTEGER      NOT NULL,
    command_text    TEXT         NOT NULL,
    command_digest  VARCHAR(128),
    command_type    VARCHAR(32)  NOT NULL DEFAULT 'shell',
    risk_level      VARCHAR(16)  NOT NULL DEFAULT 'normal',
    policy_action   VARCHAR(16)  NOT NULL DEFAULT 'allow',
    policy_id       BIGINT,
    matched_rule    VARCHAR(255),
    exec_result     VARCHAR(16)  NOT NULL DEFAULT 'unknown',
    output_digest   VARCHAR(128),
    privilege_chain VARCHAR(255),
    duration_ms     INTEGER,
    command_time    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE ops_session_command IS '会话命令记录表（命令级审计的核心）';
COMMENT ON COLUMN ops_session_command.policy_action IS '策略动作：allow / deny / confirm / audit';
COMMENT ON COLUMN ops_session_command.risk_level IS '风险等级：normal / warn / danger / critical';
COMMENT ON COLUMN ops_session_command.privilege_chain IS '提权链路，如 sudo(root) -> su(app)';
CREATE INDEX idx_cmd_session ON ops_session_command (session_id, seq_no);
CREATE INDEX idx_cmd_time ON ops_session_command (command_time DESC);
CREATE INDEX idx_cmd_risk ON ops_session_command (tenant_id, risk_level, command_time DESC) WHERE risk_level <> 'normal';
CREATE INDEX idx_cmd_blocked ON ops_session_command (tenant_id, command_time DESC) WHERE policy_action = 'deny';

-- ---------------------------------------------------------------------------
-- 会话文件传输记录
-- ---------------------------------------------------------------------------
CREATE TABLE ops_session_file (
    file_id         BIGSERIAL    PRIMARY KEY,
    session_id      BIGINT       NOT NULL,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    direction       VARCHAR(16)  NOT NULL,
    file_name       VARCHAR(512) NOT NULL,
    file_path       VARCHAR(1024),
    file_size       BIGINT       NOT NULL DEFAULT 0,
    file_digest     VARCHAR(128),
    file_type       VARCHAR(32),
    policy_action   VARCHAR(16)  NOT NULL DEFAULT 'allow',
    policy_id       BIGINT,
    transfer_result VARCHAR(16)  NOT NULL DEFAULT 'unknown',
    duration_ms     INTEGER,
    file_time       TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE ops_session_file IS '会话文件传输记录表';
COMMENT ON COLUMN ops_session_file.direction IS '方向：upload（上传到资产）/ download（下载到本地）';
CREATE INDEX idx_file_session ON ops_session_file (session_id, file_time);
CREATE INDEX idx_file_digest ON ops_session_file (file_digest) WHERE file_digest IS NOT NULL;
CREATE INDEX idx_file_denied ON ops_session_file (tenant_id, file_time DESC) WHERE policy_action = 'deny';

-- ---------------------------------------------------------------------------
-- 会话参与者（会话共享与旁观）
-- ---------------------------------------------------------------------------
CREATE TABLE ops_session_participant (
    id              BIGSERIAL    PRIMARY KEY,
    session_id      BIGINT       NOT NULL,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    user_id         BIGINT       NOT NULL,
    username        VARCHAR(64)  NOT NULL,
    role            VARCHAR(16)  NOT NULL DEFAULT 'viewer',
    join_time       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    leave_time      TIMESTAMPTZ,
    client_ip       VARCHAR(45)
);
COMMENT ON TABLE ops_session_participant IS '会话参与者表（主控 / 旁观 / 接管）';
COMMENT ON COLUMN ops_session_participant.role IS '角色：owner / viewer / controller';
CREATE INDEX idx_participant_session ON ops_session_participant (session_id);

-- ---------------------------------------------------------------------------
-- 密钥使用记录
-- ---------------------------------------------------------------------------
CREATE TABLE ops_key_usage (
    usage_id        BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    key_id          VARCHAR(128) NOT NULL,
    key_usage       VARCHAR(64)  NOT NULL,
    caller          VARCHAR(128),
    biz_type        VARCHAR(64),
    biz_id          VARCHAR(128),
    result          VARCHAR(16)  NOT NULL DEFAULT 'success',
    detail          VARCHAR(512),
    use_time        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE ops_key_usage IS '密钥使用记录表（对应 FR-09 密钥使用可追溯）';
CREATE INDEX idx_key_usage_key ON ops_key_usage (key_id, use_time DESC);
CREATE INDEX idx_key_usage_biz ON ops_key_usage (biz_type, biz_id);

-- ---------------------------------------------------------------------------
-- 登录日志
-- ---------------------------------------------------------------------------
CREATE TABLE audit_login_log (
    log_id          BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    user_id         BIGINT,
    username        VARCHAR(64),
    login_type      VARCHAR(32)  NOT NULL DEFAULT 'password',
    client_type     VARCHAR(16),
    client_ip       VARCHAR(45),
    user_agent      VARCHAR(512),
    device_info     VARCHAR(255),
    result          VARCHAR(16)  NOT NULL,
    fail_reason     VARCHAR(255),
    mfa_used        BOOLEAN      NOT NULL DEFAULT false,
    login_time      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE audit_login_log IS '登录日志表';
COMMENT ON COLUMN audit_login_log.result IS '结果：success / fail / locked';
CREATE INDEX idx_login_time ON audit_login_log (login_time DESC);
CREATE INDEX idx_login_user ON audit_login_log (tenant_id, user_id, login_time DESC);
CREATE INDEX idx_login_fail ON audit_login_log (tenant_id, login_time DESC) WHERE result <> 'success';

-- ---------------------------------------------------------------------------
-- 管理操作日志
-- ---------------------------------------------------------------------------
CREATE TABLE audit_operation_log (
    log_id          BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    operator_id     BIGINT,
    operator_name_snap VARCHAR(64),
    module          VARCHAR(64)  NOT NULL,
    action          VARCHAR(64)  NOT NULL,
    target_type     VARCHAR(64),
    target_id       VARCHAR(128),
    target_name     VARCHAR(255),
    before_value    JSONB,
    after_value     JSONB,
    result          VARCHAR(16)  NOT NULL DEFAULT 'success',
    error_message   VARCHAR(1024),
    client_ip       VARCHAR(45),
    user_agent      VARCHAR(512),
    operate_time    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE audit_operation_log IS '管理操作日志表（配置变更、授权调整、备份、密钥操作等）';
CREATE INDEX idx_oplog_time ON audit_operation_log (operate_time DESC);
CREATE INDEX idx_oplog_operator ON audit_operation_log (tenant_id, operator_id, operate_time DESC);
CREATE INDEX idx_oplog_target ON audit_operation_log (target_type, target_id, operate_time DESC);

-- ---------------------------------------------------------------------------
-- SQL 审计日志
-- ---------------------------------------------------------------------------
CREATE TABLE audit_sql_log (
    log_id          BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    session_id      BIGINT,
    user_id         BIGINT,
    asset_id        BIGINT,
    account_name    VARCHAR(128),
    sql_text        TEXT         NOT NULL,
    sql_type        VARCHAR(32),
    db_name         VARCHAR(128),
    table_names     VARCHAR(1024),
    affected_rows   BIGINT,
    risk_level      VARCHAR(16)  NOT NULL DEFAULT 'normal',
    policy_action   VARCHAR(16)  NOT NULL DEFAULT 'allow',
    exec_result     VARCHAR(16)  NOT NULL DEFAULT 'unknown',
    duration_ms     INTEGER,
    sql_time        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE audit_sql_log IS 'SQL 审计日志表';
CREATE INDEX idx_sqllog_time ON audit_sql_log (sql_time DESC);
CREATE INDEX idx_sqllog_session ON audit_sql_log (session_id, sql_time);
CREATE INDEX idx_sqllog_risk ON audit_sql_log (tenant_id, risk_level, sql_time DESC) WHERE risk_level <> 'normal';

-- ---------------------------------------------------------------------------
-- 告警规则
-- ---------------------------------------------------------------------------
CREATE TABLE audit_alert_rule (
    rule_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    rule_code       VARCHAR(64)  NOT NULL,
    rule_name       VARCHAR(128) NOT NULL,
    rule_type       VARCHAR(32)  NOT NULL,
    severity        VARCHAR(16)  NOT NULL DEFAULT 'warning',
    condition_expr  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    threshold       INTEGER      NOT NULL DEFAULT 1,
    window_sec      INTEGER      NOT NULL DEFAULT 300,
    notify_channels JSONB        NOT NULL DEFAULT '[]'::jsonb,
    notify_targets  JSONB        NOT NULL DEFAULT '[]'::jsonb,
    silent_minutes  INTEGER      NOT NULL DEFAULT 10,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE audit_alert_rule IS '告警规则表（含去重窗口与静默期）';
COMMENT ON COLUMN audit_alert_rule.severity IS '级别：info / warning / critical / emergency';
COMMENT ON COLUMN audit_alert_rule.notify_channels IS '通知渠道，如 ["email","webhook","syslog"]';
CREATE UNIQUE INDEX uk_alert_rule_code ON audit_alert_rule (tenant_id, rule_code);
CREATE INDEX idx_alert_rule_type ON audit_alert_rule (tenant_id, rule_type, status);

-- ---------------------------------------------------------------------------
-- 告警记录
-- ---------------------------------------------------------------------------
CREATE TABLE audit_alert (
    alert_id        BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    rule_id         BIGINT,
    alert_code      VARCHAR(64),
    severity        VARCHAR(16)  NOT NULL DEFAULT 'warning',
    title           VARCHAR(255) NOT NULL,
    content         TEXT,
    source_type     VARCHAR(32),
    source_id       VARCHAR(128),
    asset_id        BIGINT,
    user_id         BIGINT,
    session_id      BIGINT,
    dedup_key       VARCHAR(255),
    alert_count     INTEGER      NOT NULL DEFAULT 1,
    first_time      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_time       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    status          VARCHAR(16)  NOT NULL DEFAULT 'firing',
    ack_by          BIGINT,
    ack_time        TIMESTAMPTZ,
    resolved_by     BIGINT,
    resolved_time   TIMESTAMPTZ,
    resolve_note    VARCHAR(1024),
    notify_status   VARCHAR(16)  NOT NULL DEFAULT 'pending',
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE audit_alert IS '告警记录表';
COMMENT ON COLUMN audit_alert.status IS '状态：firing / acked / resolved / suppressed';
CREATE INDEX idx_alert_time ON audit_alert (last_time DESC);
CREATE INDEX idx_alert_status ON audit_alert (tenant_id, status, severity, last_time DESC);
CREATE INDEX idx_alert_dedup ON audit_alert (dedup_key, last_time DESC) WHERE dedup_key IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 导出记录（审计的审计）
-- ---------------------------------------------------------------------------
CREATE TABLE audit_export_log (
    log_id          BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    export_type     VARCHAR(32)  NOT NULL,
    export_scope    JSONB,
    record_count    INTEGER      NOT NULL DEFAULT 0,
    file_name       VARCHAR(512),
    file_size       BIGINT       NOT NULL DEFAULT 0,
    file_digest     VARCHAR(128),
    watermark       VARCHAR(255),
    reason          VARCHAR(512),
    operator_id     BIGINT,
    operator_name   VARCHAR(64),
    operator_ip     VARCHAR(45),
    result          VARCHAR(16)  NOT NULL DEFAULT 'success',
    export_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE audit_export_log IS '导出记录表（录像导出、日志导出、取证包导出）';
COMMENT ON COLUMN audit_export_log.export_type IS '类型：record / command / log / evidence';
CREATE INDEX idx_export_time ON audit_export_log (export_time DESC);
CREATE INDEX idx_export_operator ON audit_export_log (tenant_id, operator_id, export_time DESC);
