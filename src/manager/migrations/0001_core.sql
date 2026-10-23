-- ============================================================================
-- 0001 核心表：租户、组织、用户、角色、权限、字典、配置、许可、初始化记录
--
-- 说明：
--   * 主键统一使用 BIGSERIAL；时间统一使用 TIMESTAMPTZ；
--   * 除租户表本身外，业务表均含 tenant_id（系统级数据为 0）；
--   * 外键不建数据库级约束（避免级联删除风险与写入开销），由应用层保证一致性，
--     并为关联字段建立索引以支撑查询；
--   * 所有表含 create_time / update_time，业务表另含 create_by / update_by。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 租户
-- ---------------------------------------------------------------------------
CREATE TABLE sys_tenant (
    tenant_id       BIGSERIAL    PRIMARY KEY,
    tenant_code     VARCHAR(64)  NOT NULL,
    tenant_name     VARCHAR(128) NOT NULL,
    contact_name    VARCHAR(64),
    contact_phone   VARCHAR(32),
    contact_email   VARCHAR(128),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    expire_at       TIMESTAMPTZ,
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_tenant IS '租户表';
COMMENT ON COLUMN sys_tenant.status IS '状态：active / suspended / expired';
CREATE UNIQUE INDEX uk_tenant_code ON sys_tenant (tenant_code);
CREATE INDEX idx_tenant_status ON sys_tenant (status);

-- ---------------------------------------------------------------------------
-- 组织
-- ---------------------------------------------------------------------------
CREATE TABLE sys_org (
    org_id          BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    parent_id       BIGINT       NOT NULL DEFAULT 0,
    org_code        VARCHAR(64)  NOT NULL,
    org_name        VARCHAR(128) NOT NULL,
    org_path        VARCHAR(512) NOT NULL DEFAULT '',
    org_level       SMALLINT     NOT NULL DEFAULT 1,
    sort_no         INTEGER      NOT NULL DEFAULT 0,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT,
    update_by       BIGINT
);
COMMENT ON TABLE sys_org IS '组织（部门）表';
COMMENT ON COLUMN sys_org.org_path IS '物化路径，如 /1/5/12/，便于子树查询';
CREATE UNIQUE INDEX uk_org_code ON sys_org (tenant_id, org_code);
CREATE INDEX idx_org_parent ON sys_org (tenant_id, parent_id);
CREATE INDEX idx_org_path ON sys_org (org_path);

-- ---------------------------------------------------------------------------
-- 用户
-- ---------------------------------------------------------------------------
CREATE TABLE sys_user (
    user_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    org_id          BIGINT,
    username        VARCHAR(64)  NOT NULL,
    display_name    VARCHAR(128) NOT NULL,
    email           VARCHAR(128),
    phone           VARCHAR(32),
    password_hash   VARCHAR(256) NOT NULL,
    password_salt   VARCHAR(64)  NOT NULL,
    password_algo   VARCHAR(16)  NOT NULL DEFAULT 'SM3',
    password_update_time TIMESTAMPTZ NOT NULL DEFAULT now(),
    password_expire_days INTEGER  NOT NULL DEFAULT 90,
    must_change_password BOOLEAN NOT NULL DEFAULT false,
    mfa_enabled     BOOLEAN      NOT NULL DEFAULT false,
    mfa_secret_cipher VARCHAR(512),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    user_type       VARCHAR(16)  NOT NULL DEFAULT 'local',
    fail_count      INTEGER      NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    last_login_time TIMESTAMPTZ,
    last_login_ip   VARCHAR(45),
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT,
    update_by       BIGINT
);
COMMENT ON TABLE sys_user IS '用户表（超级管理员与三员均在此）';
COMMENT ON COLUMN sys_user.status IS '状态：active / locked / disabled';
COMMENT ON COLUMN sys_user.user_type IS '类型：local（本地）/ ldap / oidc（联邦）';
COMMENT ON COLUMN sys_user.mfa_secret_cipher IS 'TOTP 种子密文（SM4），不存明文';
CREATE UNIQUE INDEX uk_user_username ON sys_user (tenant_id, username);
CREATE INDEX idx_user_org ON sys_user (tenant_id, org_id);
CREATE INDEX idx_user_status ON sys_user (tenant_id, status);

-- ---------------------------------------------------------------------------
-- 角色
-- ---------------------------------------------------------------------------
CREATE TABLE sys_role (
    role_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    role_code       VARCHAR(64)  NOT NULL,
    role_name       VARCHAR(128) NOT NULL,
    role_type       VARCHAR(16)  NOT NULL DEFAULT 'custom',
    data_scope      VARCHAR(16)  NOT NULL DEFAULT 'self',
    is_builtin      BOOLEAN      NOT NULL DEFAULT false,
    description     VARCHAR(512),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT,
    update_by       BIGINT
);
COMMENT ON TABLE sys_role IS '角色表（内置超级管理员与三员）';
COMMENT ON COLUMN sys_role.role_type IS '类型：builtin / custom';
COMMENT ON COLUMN sys_role.data_scope IS '数据范围：all / org / org_child / self / custom';
CREATE UNIQUE INDEX uk_role_code ON sys_role (tenant_id, role_code);

-- ---------------------------------------------------------------------------
-- 用户角色关联
-- ---------------------------------------------------------------------------
CREATE TABLE sys_user_role (
    id              BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    user_id         BIGINT       NOT NULL,
    role_id         BIGINT       NOT NULL,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE sys_user_role IS '用户角色关联表';
CREATE UNIQUE INDEX uk_user_role ON sys_user_role (user_id, role_id);
CREATE INDEX idx_user_role_role ON sys_user_role (role_id);

-- ---------------------------------------------------------------------------
-- 权限
-- ---------------------------------------------------------------------------
CREATE TABLE sys_permission (
    perm_id         BIGSERIAL    PRIMARY KEY,
    perm_code       VARCHAR(128) NOT NULL,
    perm_name       VARCHAR(128) NOT NULL,
    perm_type       VARCHAR(16)  NOT NULL DEFAULT 'api',
    module          VARCHAR(64)  NOT NULL DEFAULT '',
    parent_code     VARCHAR(128),
    sort_no         INTEGER      NOT NULL DEFAULT 0,
    description     VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_permission IS '权限（功能点）表';
COMMENT ON COLUMN sys_permission.perm_type IS '类型：menu / api / data';
CREATE UNIQUE INDEX uk_perm_code ON sys_permission (perm_code);
CREATE INDEX idx_perm_module ON sys_permission (module);

-- ---------------------------------------------------------------------------
-- 角色权限关联
-- ---------------------------------------------------------------------------
CREATE TABLE sys_role_permission (
    id              BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    role_id         BIGINT       NOT NULL,
    perm_id         BIGINT       NOT NULL,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_role_permission IS '角色权限关联表';
CREATE UNIQUE INDEX uk_role_perm ON sys_role_permission (role_id, perm_id);
CREATE INDEX idx_role_perm_perm ON sys_role_permission (perm_id);

-- ---------------------------------------------------------------------------
-- 字典与字典项
-- ---------------------------------------------------------------------------
CREATE TABLE sys_dict (
    dict_id         BIGSERIAL    PRIMARY KEY,
    dict_code       VARCHAR(64)  NOT NULL,
    dict_name       VARCHAR(128) NOT NULL,
    is_system       BOOLEAN      NOT NULL DEFAULT false,
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_dict IS '数据字典表';
CREATE UNIQUE INDEX uk_dict_code ON sys_dict (dict_code);

CREATE TABLE sys_dict_item (
    item_id         BIGSERIAL    PRIMARY KEY,
    dict_id         BIGINT       NOT NULL,
    item_value      VARCHAR(128) NOT NULL,
    item_label      VARCHAR(128) NOT NULL,
    sort_no         INTEGER      NOT NULL DEFAULT 0,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_dict_item IS '字典项表';
CREATE UNIQUE INDEX uk_dict_item ON sys_dict_item (dict_id, item_value);

-- ---------------------------------------------------------------------------
-- 系统配置
-- ---------------------------------------------------------------------------
CREATE TABLE sys_config (
    config_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    config_key      VARCHAR(128) NOT NULL,
    config_value    TEXT,
    value_type      VARCHAR(16)  NOT NULL DEFAULT 'string',
    category        VARCHAR(64)  NOT NULL DEFAULT 'general',
    is_encrypted    BOOLEAN      NOT NULL DEFAULT false,
    description     VARCHAR(512),
    editable        BOOLEAN      NOT NULL DEFAULT true,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_by       BIGINT
);
COMMENT ON TABLE sys_config IS '系统配置表（含准入模式等平台级开关）';
COMMENT ON COLUMN sys_config.value_type IS '类型：string / int / bool / json';
COMMENT ON COLUMN sys_config.is_encrypted IS '是否加密存储（如短信密钥、外部接口凭据）';
CREATE UNIQUE INDEX uk_config_key ON sys_config (tenant_id, config_key);
CREATE INDEX idx_config_category ON sys_config (category);

-- ---------------------------------------------------------------------------
-- 许可
-- ---------------------------------------------------------------------------
CREATE TABLE sys_license (
    license_id      BIGSERIAL    PRIMARY KEY,
    license_key     TEXT         NOT NULL,
    customer_name   VARCHAR(256),
    edition         VARCHAR(32)  NOT NULL DEFAULT 'standard',
    max_assets      INTEGER      NOT NULL DEFAULT 0,
    max_sessions    INTEGER      NOT NULL DEFAULT 0,
    issued_at       TIMESTAMPTZ,
    expire_at       TIMESTAMPTZ,
    features        JSONB        NOT NULL DEFAULT '[]'::jsonb,
    machine_code    VARCHAR(128),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_license IS '许可授权表';
COMMENT ON COLUMN sys_license.max_assets IS '纳管资产上限，0 表示不限';
COMMENT ON COLUMN sys_license.machine_code IS '绑定的设备机器码';
CREATE INDEX idx_license_status ON sys_license (status);

-- ---------------------------------------------------------------------------
-- 初始化记录
-- ---------------------------------------------------------------------------
CREATE TABLE sys_init_record (
    init_id         BIGSERIAL    PRIMARY KEY,
    step_code       VARCHAR(64)  NOT NULL,
    step_name       VARCHAR(128) NOT NULL,
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    detail          JSONB,
    operator_id     BIGINT,
    operator_name   VARCHAR(64),
    start_time      TIMESTAMPTZ,
    finish_time     TIMESTAMPTZ,
    error_message   VARCHAR(1024),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_init_record IS '设备初始化步骤记录（对应需求 IR-01 ~ IR-30）';
COMMENT ON COLUMN sys_init_record.step_code IS '步骤编码，如 device_cert / cryptocard / super_admin / three_admins';
COMMENT ON COLUMN sys_init_record.status IS '状态：pending / running / success / failed';
CREATE UNIQUE INDEX uk_init_step ON sys_init_record (step_code);
CREATE INDEX idx_init_status ON sys_init_record (status);

-- ---------------------------------------------------------------------------
-- 运行日志（设备自身运行日志，区别于审计日志）
-- ---------------------------------------------------------------------------
CREATE TABLE sys_log (
    log_id          BIGSERIAL    PRIMARY KEY,
    node_id         VARCHAR(64),
    log_level       VARCHAR(16)  NOT NULL DEFAULT 'INFO',
    module          VARCHAR(64),
    message         TEXT         NOT NULL,
    detail          JSONB,
    trace_id        VARCHAR(64),
    log_time        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_log IS '运行日志表（按 log_time 月度分区，保留 30 天）';
COMMENT ON COLUMN sys_log.log_level IS '级别：DEBUG / INFO / WARN / ERROR';
CREATE INDEX idx_syslog_time ON sys_log (log_time DESC);
CREATE INDEX idx_syslog_level ON sys_log (log_level, log_time DESC);

-- ---------------------------------------------------------------------------
-- 租户配额
-- ---------------------------------------------------------------------------
CREATE TABLE sys_tenant_quota (
    quota_id        BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL,
    max_users       INTEGER      NOT NULL DEFAULT 0,
    max_assets      INTEGER      NOT NULL DEFAULT 0,
    max_sessions    INTEGER      NOT NULL DEFAULT 0,
    max_asset_groups INTEGER     NOT NULL DEFAULT 0,
    storage_quota_gb BIGINT      NOT NULL DEFAULT 0,
    used_assets     INTEGER      NOT NULL DEFAULT 0,
    used_users      INTEGER      NOT NULL DEFAULT 0,
    used_storage_gb NUMERIC(12,2) NOT NULL DEFAULT 0,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_tenant_quota IS '租户配额表（0 表示不限）';
CREATE UNIQUE INDEX uk_tenant_quota ON sys_tenant_quota (tenant_id);
