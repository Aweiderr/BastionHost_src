-- ============================================================================
-- 0007 设备运维与信任配置
--
-- 对应需求：FR-07-09、FR-07-11 ~ FR-07-19、FR-08-11、FR-08-13、FR-09-13、FR-09-14
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 备份任务与策略
-- ---------------------------------------------------------------------------
CREATE TABLE dev_backup_task (
    task_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    task_name       VARCHAR(128) NOT NULL,
    backup_type     VARCHAR(16)  NOT NULL,
    cron_expr       VARCHAR(64),
    target_type     VARCHAR(16)  NOT NULL DEFAULT 'local',
    target_config   JSONB,
    keep_copies     INTEGER      NOT NULL DEFAULT 7,
    keep_days       INTEGER,
    encrypt_enabled BOOLEAN      NOT NULL DEFAULT true,
    last_run_time   TIMESTAMPTZ,
    last_result     VARCHAR(16),
    next_run_time   TIMESTAMPTZ,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    create_by       BIGINT,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE dev_backup_task IS '设备备份任务与策略表（对应 FR-07-09、FR-07-18）';
COMMENT ON COLUMN dev_backup_task.backup_type IS '类型：config（配置）/ system（配置+数据库）/ record（录像）';
COMMENT ON COLUMN dev_backup_task.target_type IS '目标：local / sftp / nfs / s3';
COMMENT ON COLUMN dev_backup_task.target_config IS '目标参数（凭据以密文引用，不落明文）';
CREATE UNIQUE INDEX uk_backup_task ON dev_backup_task (tenant_id, task_name);
CREATE INDEX idx_backup_task_next ON dev_backup_task (next_run_time) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- 备份执行记录
-- ---------------------------------------------------------------------------
CREATE TABLE dev_backup_record (
    record_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    task_id         BIGINT,
    run_type        VARCHAR(16)  NOT NULL DEFAULT 'schedule',
    start_time      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    end_time        TIMESTAMPTZ,
    duration_sec    INTEGER,
    file_name       VARCHAR(512),
    file_size       BIGINT       NOT NULL DEFAULT 0,
    file_digest     VARCHAR(128),
    target_path     VARCHAR(1024),
    result          VARCHAR(16)  NOT NULL DEFAULT 'running',
    fail_reason     VARCHAR(500),
    operator_id     BIGINT
);
COMMENT ON TABLE dev_backup_record IS '备份执行记录表';
COMMENT ON COLUMN dev_backup_record.run_type IS '触发方式：schedule / manual';
CREATE INDEX idx_backup_rec_task ON dev_backup_record (task_id, start_time DESC);
CREATE INDEX idx_backup_rec_time ON dev_backup_record (start_time DESC);

-- ---------------------------------------------------------------------------
-- 系统自检记录
-- ---------------------------------------------------------------------------
CREATE TABLE dev_selfcheck_record (
    record_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    check_type      VARCHAR(16)  NOT NULL DEFAULT 'manual',
    start_time      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    end_time        TIMESTAMPTZ,
    software_result VARCHAR(16),
    software_diff   JSONB,
    keycard_result  VARCHAR(16),
    keycard_detail  VARCHAR(500),
    key_result      VARCHAR(16),
    key_detail      VARCHAR(500),
    overall_result  VARCHAR(16),
    report_digest   VARCHAR(128),
    operator_id     BIGINT
);
COMMENT ON TABLE dev_selfcheck_record IS '设备系统自检记录表（软件完整性 / 密码卡功能 / 密钥完整性）';
COMMENT ON COLUMN dev_selfcheck_record.check_type IS '触发方式：manual / schedule';
COMMENT ON COLUMN dev_selfcheck_record.keycard_result IS '密码卡自检：pass / fail / absent（无硬件时为 absent）';
CREATE INDEX idx_selfcheck_time ON dev_selfcheck_record (start_time DESC);
CREATE INDEX idx_selfcheck_result ON dev_selfcheck_record (overall_result) WHERE overall_result = 'fail';

-- ---------------------------------------------------------------------------
-- 本机资源指标
-- ---------------------------------------------------------------------------
CREATE TABLE dev_resource_metric (
    metric_id       BIGSERIAL    PRIMARY KEY,
    node_id         VARCHAR(64)  NOT NULL,
    metric_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    granularity     VARCHAR(8)   NOT NULL DEFAULT 'raw',
    cpu_usage       NUMERIC(5,2),
    load_avg        NUMERIC(6,2),
    mem_total       BIGINT,
    mem_usage       NUMERIC(5,2),
    disk_usage      NUMERIC(5,2),
    disk_detail     JSONB,
    net_rx_bytes    BIGINT,
    net_tx_bytes    BIGINT,
    conn_count      INTEGER
);
COMMENT ON TABLE dev_resource_metric IS '本机资源指标表（对应 FR-07-16）';
COMMENT ON COLUMN dev_resource_metric.granularity IS '粒度：raw（分钟）/ hour / day';
CREATE UNIQUE INDEX uk_metric ON dev_resource_metric (node_id, metric_time, granularity);
CREATE INDEX idx_metric_time ON dev_resource_metric (metric_time DESC);

-- ---------------------------------------------------------------------------
-- 证书链
-- ---------------------------------------------------------------------------
CREATE TABLE km_cert_chain (
    chain_id        BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    chain_name      VARCHAR(128) NOT NULL,
    cert_id         BIGINT       NOT NULL,
    parent_cert_id  BIGINT,
    chain_level     SMALLINT     NOT NULL DEFAULT 1,
    is_trusted_root BOOLEAN      NOT NULL DEFAULT false,
    is_default      BOOLEAN      NOT NULL DEFAULT false,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    create_by       BIGINT,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE km_cert_chain IS '证书链表（层级结构，证书本体见 km_cert，对应 FR-09-14）';
CREATE UNIQUE INDEX uk_chain_cert ON km_cert_chain (tenant_id, cert_id);
CREATE INDEX idx_chain_parent ON km_cert_chain (parent_cert_id);
CREATE INDEX idx_chain_default ON km_cert_chain (tenant_id, is_default) WHERE is_default = true;

-- ---------------------------------------------------------------------------
-- 身份源与联邦配置
-- ---------------------------------------------------------------------------
CREATE TABLE sys_idp_config (
    idp_id          BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    idp_type        VARCHAR(16)  NOT NULL,
    idp_name        VARCHAR(128) NOT NULL,
    config_json     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    attr_mapping    JSONB,
    auto_create_user BOOLEAN     NOT NULL DEFAULT false,
    default_org_id  BIGINT,
    default_role_id BIGINT,
    sync_enabled    BOOLEAN      NOT NULL DEFAULT false,
    sync_cron       VARCHAR(64),
    last_sync_time  TIMESTAMPTZ,
    last_sync_result VARCHAR(16),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_idp_config IS '身份源与联邦认证配置表（LDAP / AD / OIDC / SAML / Kerberos）';
COMMENT ON COLUMN sys_idp_config.auto_create_user IS '是否允许自动创建用户，默认关闭（避免脏账号）';
CREATE UNIQUE INDEX uk_idp_name ON sys_idp_config (tenant_id, idp_name);
CREATE INDEX idx_idp_type ON sys_idp_config (tenant_id, idp_type, status);

-- ---------------------------------------------------------------------------
-- 租户访问审批策略
-- ---------------------------------------------------------------------------
CREATE TABLE tenant_access_policy (
    policy_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL,
    access_mode     VARCHAR(16)  NOT NULL DEFAULT 'approval',
    approval_flow_id BIGINT,
    core_asset_force_approval BOOLEAN NOT NULL DEFAULT true,
    session_concurrent_limit INTEGER NOT NULL DEFAULT 0,
    alert_threshold INTEGER      NOT NULL DEFAULT 0,
    update_by       BIGINT,
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE tenant_access_policy IS '租户访问审批策略表（对应 FR-08-11 免审批模式）';
COMMENT ON COLUMN tenant_access_policy.access_mode IS '访问模式：approval（需工单）/ binding（绑定即授权，免审批）';
COMMENT ON COLUMN tenant_access_policy.core_asset_force_approval IS '核心资产是否强制审批（不受免审批影响）';
CREATE UNIQUE INDEX uk_tenant_policy ON tenant_access_policy (tenant_id);

-- ---------------------------------------------------------------------------
-- 运维许可（准入模式凭据，对应 FR-08-13）
-- ---------------------------------------------------------------------------
CREATE TABLE sys_access_permit (
    permit_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    permit_no       VARCHAR(64)  NOT NULL,
    user_id         BIGINT       NOT NULL,
    device_fingerprint VARCHAR(128) NOT NULL,
    client_type     VARCHAR(16)  NOT NULL,
    client_version  VARCHAR(32),
    src_ip          VARCHAR(45)  NOT NULL,
    access_mode     VARCHAR(16)  NOT NULL DEFAULT 'standard',
    issue_time      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expire_time     TIMESTAMPTZ  NOT NULL,
    renew_count     INTEGER      NOT NULL DEFAULT 0,
    derived_types   VARCHAR(256),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    revoke_time     TIMESTAMPTZ,
    revoke_by       BIGINT,
    revoke_reason   VARCHAR(256),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_access_permit IS '运维许可凭据表（准入模式，对应 FR-08-13）';
COMMENT ON COLUMN sys_access_permit.access_mode IS '签发时的准入模式：loose / standard / strict';
COMMENT ON COLUMN sys_access_permit.status IS '状态：active / expiring / expired / revoked / logged_out';
COMMENT ON COLUMN sys_access_permit.derived_types IS '已派生凭据类型：ssh_cert / rdp_ticket / api_token / net_permit';
CREATE UNIQUE INDEX uk_permit_no ON sys_access_permit (permit_no);
CREATE INDEX idx_permit_user ON sys_access_permit (tenant_id, user_id, status);
CREATE INDEX idx_permit_expire ON sys_access_permit (expire_time) WHERE status IN ('active', 'expiring');
