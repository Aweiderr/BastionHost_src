-- ============================================================================
-- 0030 资产台账归系统管理员独占
--
-- 需求：用户明确要求"资产只有系统管理员管理"。
--
-- 现状与问题：
--   除 sys_admin 之外，还有两个角色持有资产台账权限：
--     - super_admin 持有 asset:asset:list（0009 起就带着，属历史遗留）；
--     - approver    持有 asset:asset:list（审批人）。
--   这造成两处不一致：
--     1. super_admin 有 asset:asset:list 却没有 asset:account:list，
--        于是他进资产页点「账号」必然 403。前端已加权限早退"不再发这个请求"，
--        但那是绕开症状；权限集合本身就不该出现"能看台账、不能看账号"的怪组合。
--     2. 与 0026 的收敛方向相悖 —— 0026 已把 operator 的 asset:asset:* 收回，
--        理由是"运维持有它会看到全部资产清单，属信息越权，正确视图是授权过滤后的
--        /api/v1/ops/assets"。同理，资产台账应当只归承担资产管理职责的系统管理员。
--
-- 本迁移回收**除 sys_admin 之外所有角色**的 asset:asset:* 权限。
--
-- 刻意**不**回收 asset:account:*（账号凭据），理由见 0009 的原始设计：
--   "asset:account:viewpwd（查看明文口令）不授予系统管理员 ——
--    凭据属安全管理员管辖，系统管理员可管理账号（增删改、重置口令）但不可查看明文。
--    这是'能改不能看'的最小权限设计。"
--   即：**账号凭据与资产台账是两个维度**，凭据归安全管理员是刻意划分，
--   不属本次"资产只有系统管理员管理"的回收范围。若将来要求凭据也归系统管理员，
--   需先撤销 0009 的这条设计并单独评估（它涉及明文口令的可见性）。
--
-- 影响面很小：/assets 路由对 approver 本就是 hideForRoles（他从不看资产页），
-- 且工单接口会自带资产信息，审批体验不受影响。
-- ============================================================================

DELETE FROM sys_role_permission rp
USING sys_role r, sys_permission p
WHERE rp.role_id = r.role_id
  AND rp.perm_id = p.perm_id
  AND r.role_code <> 'sys_admin'
  AND p.perm_code LIKE 'asset:asset:%';

COMMENT ON TABLE sys_role_permission IS
    '角色-权限关联；0028 起 sec_admin 额外持有只读的 system:user:list（用于证书绑定选人）；'
    '0030 起 asset:asset:*（资产台账）仅授予 sys_admin';
