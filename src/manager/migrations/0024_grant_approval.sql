-- ============================================================================
-- 0024 访问授权的「审批属性」：绑定时可选「需要审批 / 不需要审批」
--
-- 业务要求：运维人员与资源的绑定关系有两种形态 ——
--   · 不需要审批：绑定即可访问（免审批直连，适合低风险资产的日常巡检）；
--   · 需要审批：  绑定只表示"这个人**可以申请**这个资源"，
--                 真正访问仍需走工单审批。
--
-- 此前的实现没有这个维度：acl_grant 一行既当"绑定"又当"通行证"，
-- 后果有两个 ——
--   ① 表达不了"绑定了但访问要审批"；
--   ② 工单到期会把绑定关系一并改掉（UpsertFromTicket 就地覆盖同一行），
--      绑定随之消失，下次还得重新配置。
--
-- 因此把两者分开，同一张表用 grant_kind 区分：
--   · binding —— 长期绑定关系，带 require_approval 属性，
--                不因工单到期而消失；
--   · pass    —— 工单审批产生的临时通行证，有有效期。
--
-- 判定顺序（见 repo.GrantRepo.EvaluateAccess）：
--   有效 pass            → 放行（审批结果）
--   否则 binding 免审批  → 放行（绑定直连）
--   否则 binding 需审批  → 拒绝，并明确提示"请提交工单申请"
--   都没有               → 拒绝，"没有可用授权"
--
-- 唯一索引随之下沉到 grant_kind 维度：同一「人 + 资产 + 账号」可以同时
-- 存在一条绑定与一条通行证。
-- ============================================================================

ALTER TABLE acl_grant ADD COLUMN IF NOT EXISTS grant_kind VARCHAR(16) NOT NULL DEFAULT 'pass';
ALTER TABLE acl_grant ADD COLUMN IF NOT EXISTS require_approval BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN acl_grant.grant_kind IS
    '记录类型：binding（长期绑定关系，可带"需审批"属性）/ pass（工单审批产生的通行证）';
COMMENT ON COLUMN acl_grant.require_approval IS
    '仅对 binding 有意义：true 表示访问该资源必须经工单审批（绑定只代表"可申请"）';

-- 历史数据回填：工单来源的是通行证，其余视为绑定（保持原有放行行为）
UPDATE acl_grant SET grant_kind = 'pass'    WHERE grant_source = 'ticket';
UPDATE acl_grant SET grant_kind = 'binding' WHERE grant_source <> 'ticket';

-- 唯一索引加入 grant_kind：绑定与通行证可以并存
DROP INDEX IF EXISTS uk_grant;
CREATE UNIQUE INDEX uk_grant ON acl_grant
    (tenant_id, subject_type, subject_id, asset_id, COALESCE(account_id, 0), grant_kind);

CREATE INDEX IF NOT EXISTS idx_grant_kind
    ON acl_grant (tenant_id, subject_type, subject_id, grant_kind, status);
