-- ============================================================================
-- 0050 审计展示层：脱敏规则、水印、取证导出审批
--
-- 录像和命令原文仍按原样留在原表。这里只增加“给人看”和“拿出去”的控制：
--   · 脱敏规则可按租户或全局（tenant_id = 0）启用
--   · 水印不能关闭；导出必须先由另一名审计员审批
--   · 查看未脱敏原文使用新权限 audit:record:reveal，并写入操作日志
-- ============================================================================

CREATE TABLE audit_redact_rule (
    rule_id      BIGSERIAL    PRIMARY KEY,
    tenant_id    BIGINT       NOT NULL DEFAULT 0,
    name         VARCHAR(128) NOT NULL,
    kind         VARCHAR(16)  NOT NULL,
    field_type   VARCHAR(32),
    pattern      TEXT,
    replacement  VARCHAR(64),
    enabled      BOOLEAN      NOT NULL DEFAULT true,
    create_time  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT ck_redact_kind CHECK (kind IN ('regex', 'keyword', 'field'))
);
COMMENT ON TABLE audit_redact_rule IS '审计展示层脱敏规则。不改写已入库的命令和录像';
COMMENT ON COLUMN audit_redact_rule.tenant_id IS '0 表示全局规则，对其它租户默认生效';
COMMENT ON COLUMN audit_redact_rule.kind IS 'regex / keyword / field';
COMMENT ON COLUMN audit_redact_rule.field_type IS 'password / id_card / phone / bank_card';
CREATE UNIQUE INDEX uk_redact_rule_name ON audit_redact_rule (tenant_id, name);

INSERT INTO audit_redact_rule (tenant_id, name, kind, field_type, enabled)
VALUES
    (0, '内置-口令', 'field', 'password', true),
    (0, '内置-身份证号', 'field', 'id_card', true),
    (0, '内置-手机号', 'field', 'phone', true),
    (0, '内置-银行卡', 'field', 'bank_card', true)
ON CONFLICT (tenant_id, name) DO NOTHING;

CREATE TABLE audit_watermark_config (
    tenant_id   BIGINT           PRIMARY KEY,
    enabled     BOOLEAN          NOT NULL DEFAULT true,
    style       VARCHAR(16)      NOT NULL DEFAULT 'diagonal',
    opacity     DOUBLE PRECISION NOT NULL DEFAULT 0.18,
    font_size   INTEGER          NOT NULL DEFAULT 14,
    angle       INTEGER          NOT NULL DEFAULT -24,
    template    VARCHAR(255)     NOT NULL,
    update_by   BIGINT,
    update_time TIMESTAMPTZ      NOT NULL DEFAULT now(),
    CONSTRAINT ck_watermark_style CHECK (style IN ('diagonal', 'tile', 'corner')),
    CONSTRAINT ck_watermark_enabled CHECK (enabled)
);
COMMENT ON TABLE audit_watermark_config IS '回放与导出水印。enabled 约束为真，关闭水印的请求在应用层拒绝并记审计';
COMMENT ON COLUMN audit_watermark_config.template IS '支持 ${operator} ${time} ${session} ${asset} ${purpose}';

INSERT INTO audit_watermark_config (tenant_id, enabled, style, opacity, font_size, angle, template)
VALUES (0, true, 'diagonal', 0.18, 14, -24, '${operator} ${session} ${asset} ${time}')
ON CONFLICT (tenant_id) DO NOTHING;

CREATE TABLE audit_export_request (
    request_id      BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    session_id      BIGINT       NOT NULL,
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    reason          VARCHAR(512) NOT NULL,
    watermark       VARCHAR(255) NOT NULL,
    requester_id    BIGINT       NOT NULL,
    requester_name  VARCHAR(64)  NOT NULL,
    request_time    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    approver_id     BIGINT,
    approver_name   VARCHAR(64),
    approve_time    TIMESTAMPTZ,
    approve_comment VARCHAR(512),
    export_log_id   BIGINT,
    CONSTRAINT ck_export_request_status CHECK (status IN ('pending', 'approved', 'rejected', 'exported'))
);
COMMENT ON TABLE audit_export_request IS '取证导出申请。须另一名持有 audit:export:evidence 的用户审批后才能下载';
CREATE INDEX idx_export_request_time ON audit_export_request (tenant_id, request_time DESC);

INSERT INTO sys_permission (perm_code, perm_name, perm_type, module, sort_no)
VALUES ('audit:record:reveal', '查看未脱敏原文', 'api', 'audit', 415)
ON CONFLICT (perm_code) DO NOTHING;

INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code IN ('super_admin', 'audit_admin')
  AND p.perm_code = 'audit:record:reveal'
ON CONFLICT (role_id, perm_id) DO NOTHING;
