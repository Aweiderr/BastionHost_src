-- ============================================================================
-- 0002 资产与账号
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 资产
-- ---------------------------------------------------------------------------
CREATE TABLE ast_asset (
    asset_id        BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    group_id        BIGINT,
    asset_code      VARCHAR(64),
    asset_name      VARCHAR(128) NOT NULL,
    asset_type      VARCHAR(32)  NOT NULL DEFAULT 'host',
    protocol        VARCHAR(32)  NOT NULL DEFAULT 'ssh',
    host            VARCHAR(255) NOT NULL,
    port            INTEGER      NOT NULL,
    level           VARCHAR(16)  NOT NULL DEFAULT 'normal',
    os_type         VARCHAR(32),
    tags            JSONB        NOT NULL DEFAULT '[]'::jsonb,
    attributes      JSONB,
    gateway_id      BIGINT,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    reachable       VARCHAR(16)  NOT NULL DEFAULT 'unknown',
    last_check_time TIMESTAMPTZ,
    last_check_msg  VARCHAR(512),
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT,
    update_by       BIGINT
);
COMMENT ON TABLE ast_asset IS '资产台账表';
COMMENT ON COLUMN ast_asset.asset_type IS '类型：host / network / database / web / custom';
COMMENT ON COLUMN ast_asset.protocol IS '主协议：ssh / rdp / vnc / telnet / mysql / postgresql / oracle / sftp';
COMMENT ON COLUMN ast_asset.level IS '重要级别：normal / important / core（core 可强制走审批、禁用第三方客户端）';
COMMENT ON COLUMN ast_asset.gateway_id IS '所属网关 ID（多网段场景，可空）';
COMMENT ON COLUMN ast_asset.reachable IS '连通性：unknown / ok / fail';
CREATE UNIQUE INDEX uk_asset_code ON ast_asset (tenant_id, asset_code) WHERE asset_code IS NOT NULL;
CREATE INDEX idx_asset_group ON ast_asset (tenant_id, group_id);
CREATE INDEX idx_asset_host ON ast_asset (tenant_id, host);
CREATE INDEX idx_asset_level ON ast_asset (tenant_id, level);
CREATE INDEX idx_asset_status ON ast_asset (tenant_id, status);

-- ---------------------------------------------------------------------------
-- 资产分组
-- ---------------------------------------------------------------------------
CREATE TABLE ast_asset_group (
    group_id        BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    parent_id       BIGINT       NOT NULL DEFAULT 0,
    group_name      VARCHAR(128) NOT NULL,
    group_path      VARCHAR(512) NOT NULL DEFAULT '',
    sort_no         INTEGER      NOT NULL DEFAULT 0,
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE ast_asset_group IS '资产分组表（树形，group_path 为物化路径）';
CREATE INDEX idx_asset_group_parent ON ast_asset_group (tenant_id, parent_id);
CREATE INDEX idx_asset_group_path ON ast_asset_group (group_path);

-- ---------------------------------------------------------------------------
-- 资产标签
-- ---------------------------------------------------------------------------
CREATE TABLE ast_asset_tag (
    id              BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    asset_id        BIGINT       NOT NULL,
    tag_key         VARCHAR(64)  NOT NULL,
    tag_value       VARCHAR(128),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE ast_asset_tag IS '资产标签表（与 ast_asset.tags 冗余，便于按标签检索）';
CREATE UNIQUE INDEX uk_asset_tag ON ast_asset_tag (asset_id, tag_key);
CREATE INDEX idx_asset_tag_key ON ast_asset_tag (tenant_id, tag_key, tag_value);

-- ---------------------------------------------------------------------------
-- 资产账号（托管账号）
-- ---------------------------------------------------------------------------
CREATE TABLE ast_account (
    account_id      BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    asset_id        BIGINT       NOT NULL,
    account_name    VARCHAR(128) NOT NULL,
    account_type    VARCHAR(32)  NOT NULL DEFAULT 'password',
    password_cipher TEXT,
    password_version INTEGER     NOT NULL DEFAULT 1,
    private_key_cipher TEXT,
    ssh_pub_key     TEXT,
    sudo_password_cipher TEXT,
    privileged      BOOLEAN      NOT NULL DEFAULT false,
    privilege_mode  VARCHAR(32),
    rotate_days     INTEGER      NOT NULL DEFAULT 0,
    last_rotate_time TIMESTAMPTZ,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT,
    update_by       BIGINT
);
COMMENT ON TABLE ast_account IS '资产账号（托管账号）表';
COMMENT ON COLUMN ast_account.account_type IS '认证方式：password / key / both';
COMMENT ON COLUMN ast_account.password_cipher IS '口令密文（SM4，密钥由密码服务托管），任何接口不返回明文';
COMMENT ON COLUMN ast_account.rotate_days IS '自动改密周期（天），0 表示不改密';
CREATE UNIQUE INDEX uk_account ON ast_account (asset_id, account_name);
CREATE INDEX idx_account_asset ON ast_account (tenant_id, asset_id);
CREATE INDEX idx_account_status ON ast_account (tenant_id, status);

-- ---------------------------------------------------------------------------
-- 账号历史（口令变更留痕，仅存密文与指纹）
-- ---------------------------------------------------------------------------
CREATE TABLE ast_account_history (
    history_id      BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    account_id      BIGINT       NOT NULL,
    asset_id        BIGINT       NOT NULL,
    account_name    VARCHAR(128) NOT NULL,
    password_cipher TEXT         NOT NULL,
    password_version INTEGER     NOT NULL,
    password_digest VARCHAR(128),
    change_type     VARCHAR(32)  NOT NULL DEFAULT 'manual',
    operator_id     BIGINT,
    operator_name   VARCHAR(64),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE ast_account_history IS '账号口令历史表（用于回滚与审计，保留最近 N 个版本）';
COMMENT ON COLUMN ast_account_history.change_type IS '变更方式：manual / auto_rotate / import / rollback';
CREATE INDEX idx_account_hist ON ast_account_history (account_id, password_version DESC);
