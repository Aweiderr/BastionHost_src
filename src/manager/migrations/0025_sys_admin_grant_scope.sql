-- ============================================================================
-- 0025 系统管理员获得「访问授权」配置能力
--
-- 背景（业务方明确要求）：
--   原设计把授权完全划归安全管理员 —— 0009 的分配原则写着"sys_admin
--   不参与授权"，acl 模块的四个权限点当时只授予 sec_admin，0015 进一步
--   把"超管不得持有业务能力"固化下来。
--
--   实际使用中，"录入资产与账号"和"给运维人员分配资源"是同一批人在做，
--   拆给两个账号反而多一道交接。业务方要求系统管理员也能直接配置授权。
--
-- 本次授予 sys_admin 三个**实际有入口**的权限：
--   acl:grant:list    查看授权（「访问授权」页可见）
--   acl:grant:create  新建 / 调整绑定（含"访问是否需要审批"开关）
--   acl:grant:revoke  回收授权 / 删除已失效记录
--
-- 刻意**不授予** acl:rule:manage（管理授权规则）：
--   ABAC 条件规则（acl_grant_rule 的时间段 / 来源网段判定）尚未实现，
--   授出去就是一个没有任何入口的空权限。等该功能落地时再单独授予。
--
-- 影响提示（部署与审计需知）：
--   授予后 sec_admin 不再独占授权能力，"谁能访问哪些资产"由系统管理员
--   与安全管理员共同掌握。审计上区分责任时需注意这一点 ——
--   `acl_grant_log` 与 `audit_operation_log` 都保留了操作人快照，
--   事后仍能回答"这条授权是谁配的"。
--
-- 说明：0009 已发布，按迁移约定不可修改，故新增本文件。
-- ============================================================================

INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code = 'sys_admin'
  AND p.perm_code IN ('acl:grant:list', 'acl:grant:create', 'acl:grant:revoke')
ON CONFLICT (role_id, perm_id) DO NOTHING;
