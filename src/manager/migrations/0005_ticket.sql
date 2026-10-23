-- ============================================================================
-- 0005 工单与审批
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 审批流程定义
-- ---------------------------------------------------------------------------
CREATE TABLE wf_approval_flow (
    flow_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    flow_code       VARCHAR(64)  NOT NULL,
    flow_name       VARCHAR(128) NOT NULL,
    flow_type       VARCHAR(32)  NOT NULL DEFAULT 'access',
    nodes           JSONB        NOT NULL DEFAULT '[]'::jsonb,
    timeout_minutes INTEGER      NOT NULL DEFAULT 60,
    timeout_action  VARCHAR(16)  NOT NULL DEFAULT 'notify',
    allow_emergency BOOLEAN      NOT NULL DEFAULT true,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE wf_approval_flow IS '审批流程定义表';
COMMENT ON COLUMN wf_approval_flow.flow_type IS '类型：access（访问申请）/ change（变更）/ emergency（紧急放行）';
COMMENT ON COLUMN wf_approval_flow.timeout_action IS '超时动作：notify / auto_approve / auto_reject';
CREATE UNIQUE INDEX uk_flow_code ON wf_approval_flow (tenant_id, flow_code);

-- ---------------------------------------------------------------------------
-- 工单
-- ---------------------------------------------------------------------------
CREATE TABLE wf_ticket (
    ticket_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    ticket_no       VARCHAR(64)  NOT NULL,
    ticket_type     VARCHAR(32)  NOT NULL DEFAULT 'access',
    title           VARCHAR(255) NOT NULL,
    reason          TEXT,
    applicant_id    BIGINT       NOT NULL,
    applicant_name  VARCHAR(64),
    flow_id         BIGINT,
    current_node    VARCHAR(64),
    approver_ids    JSONB,
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    priority        VARCHAR(16)  NOT NULL DEFAULT 'normal',
    valid_from      TIMESTAMPTZ,
    valid_to        TIMESTAMPTZ,
    is_emergency    BOOLEAN      NOT NULL DEFAULT false,
    emergency_note  VARCHAR(1024),
    submit_time     TIMESTAMPTZ,
    finish_time     TIMESTAMPTZ,
    remark          VARCHAR(1024),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE wf_ticket IS '工单表（访问申请、临时授权、紧急放行）';
COMMENT ON COLUMN wf_ticket.status IS '状态：draft / pending / approved / rejected / cancelled / expired / supplement（待补单）';
COMMENT ON COLUMN wf_ticket.is_emergency IS '是否紧急放行（事后必须补单）';
CREATE UNIQUE INDEX uk_ticket_no ON wf_ticket (ticket_no);
CREATE INDEX idx_ticket_applicant ON wf_ticket (tenant_id, applicant_id, create_time DESC);
CREATE INDEX idx_ticket_status ON wf_ticket (tenant_id, status, create_time DESC);
CREATE INDEX idx_ticket_emergency ON wf_ticket (tenant_id, create_time DESC) WHERE is_emergency = true;

-- ---------------------------------------------------------------------------
-- 工单明细（申请的资产与账号）
-- ---------------------------------------------------------------------------
CREATE TABLE wf_ticket_item (
    item_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    ticket_id       BIGINT       NOT NULL,
    asset_id        BIGINT       NOT NULL,
    asset_name      VARCHAR(128),
    account_id      BIGINT,
    account_name    VARCHAR(128),
    permission      JSONB,
    valid_from      TIMESTAMPTZ,
    valid_to        TIMESTAMPTZ,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE wf_ticket_item IS '工单明细表（一条工单可申请多个资产）';
CREATE INDEX idx_ticket_item ON wf_ticket_item (ticket_id);
CREATE INDEX idx_ticket_item_asset ON wf_ticket_item (asset_id);

-- ---------------------------------------------------------------------------
-- 审批记录
-- ---------------------------------------------------------------------------
CREATE TABLE wf_approval_record (
    record_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    ticket_id       BIGINT       NOT NULL,
    node_code       VARCHAR(64),
    node_name       VARCHAR(128),
    approver_id     BIGINT,
    approver_name   VARCHAR(64),
    action          VARCHAR(16)  NOT NULL,
    comment         VARCHAR(1024),
    acted_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    duration_ms     BIGINT,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE wf_approval_record IS '审批记录表';
COMMENT ON COLUMN wf_approval_record.action IS '动作：approve / reject / transfer（转办）/ add_sign（加签）/ recall（撤回）';
CREATE INDEX idx_approval_ticket ON wf_approval_record (ticket_id, acted_at);
CREATE INDEX idx_approval_approver ON wf_approval_record (tenant_id, approver_id, acted_at DESC);
