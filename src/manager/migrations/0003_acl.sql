-- ============================================================================
-- 0003 访问控制与授权
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 授权（主体 -> 资产/账号）
-- ---------------------------------------------------------------------------
CREATE TABLE acl_grant (
    grant_id        BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    subject_type    VARCHAR(16)  NOT NULL DEFAULT 'user',
    subject_id      BIGINT       NOT NULL,
    asset_id        BIGINT       NOT NULL,
    account_id      BIGINT,
    grant_source    VARCHAR(16)  NOT NULL DEFAULT 'manual',
    ticket_id       BIGINT,
    can_login       BOOLEAN      NOT NULL DEFAULT true,
    can_file        BOOLEAN      NOT NULL DEFAULT true,
    can_copy        BOOLEAN      NOT NULL DEFAULT false,
    can_paste       BOOLEAN      NOT NULL DEFAULT false,
    valid_from      TIMESTAMPTZ,
    valid_to        TIMESTAMPTZ,
    time_ranges     JSONB,
    source_cidrs    JSONB,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE acl_grant IS '授权表（谁可以对哪个资产/账号做什么）';
COMMENT ON COLUMN acl_grant.subject_type IS '主体类型：user / user_group / role';
COMMENT ON COLUMN acl_grant.grant_source IS '授权来源：manual（手动）/ ticket（工单）/ temp（临时）/ binding（租户免审批直连）';
COMMENT ON COLUMN acl_grant.time_ranges IS '允许的时间段，如 [{"days":[1,2,3,4,5],"from":"09:00","to":"18:00"}]';
COMMENT ON COLUMN acl_grant.source_cidrs IS '允许的来源网段，如 ["10.20.0.0/16"]';
CREATE UNIQUE INDEX uk_grant ON acl_grant (tenant_id, subject_type, subject_id, asset_id, COALESCE(account_id, 0));
CREATE INDEX idx_grant_subject ON acl_grant (tenant_id, subject_type, subject_id, status);
CREATE INDEX idx_grant_asset ON acl_grant (tenant_id, asset_id, status);
CREATE INDEX idx_grant_ticket ON acl_grant (ticket_id) WHERE ticket_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 授权规则（细粒度条件，配合 acl_grant 使用）
-- ---------------------------------------------------------------------------
CREATE TABLE acl_grant_rule (
    rule_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    grant_id        BIGINT,
    rule_name       VARCHAR(128) NOT NULL,
    rule_type       VARCHAR(32)  NOT NULL DEFAULT 'condition',
    priority        INTEGER      NOT NULL DEFAULT 100,
    condition_expr  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    effect          VARCHAR(16)  NOT NULL DEFAULT 'allow',
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE acl_grant_rule IS '授权条件规则表（ABAC 条件：时间、来源、资产属性等）';
COMMENT ON COLUMN acl_grant_rule.effect IS '效果：allow / deny（deny 优先）';
CREATE INDEX idx_grant_rule_grant ON acl_grant_rule (grant_id, status);
CREATE INDEX idx_grant_rule_priority ON acl_grant_rule (tenant_id, priority);

-- ---------------------------------------------------------------------------
-- 授权变更日志
-- ---------------------------------------------------------------------------
CREATE TABLE acl_grant_log (
    log_id          BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    grant_id        BIGINT,
    action          VARCHAR(32)  NOT NULL,
    subject_type    VARCHAR(16),
    subject_id      BIGINT,
    asset_id        BIGINT,
    account_id      BIGINT,
    before_value    JSONB,
    after_value     JSONB,
    operator_id     BIGINT,
    operator_name   VARCHAR(64),
    reason          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE acl_grant_log IS '授权变更日志（记录前后值，满足审计要求）';
COMMENT ON COLUMN acl_grant_log.action IS '动作：create / update / revoke / expire / approve';
CREATE INDEX idx_grant_log_grant ON acl_grant_log (grant_id, create_time DESC);
CREATE INDEX idx_grant_log_subject ON acl_grant_log (tenant_id, subject_id, create_time DESC);
