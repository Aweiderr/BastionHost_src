-- ============================================================================
-- 0023 安全管理员补齐"查看权限点目录"的能力
--
-- 背景（实测发现的权限断链）：
--   角色权限授予（system:role:manage）归安全管理员，
--   而权限点目录接口
--     GET /api/v1/permissions
--     GET /api/v1/roles/{id}/permissions
--   要求的是 system:role:list —— 该权限点在 0009 里只授予了系统管理员。
--   实测：sysadmin 读权限目录 200，secadmin 403。
--
--   即"唯一有权授予角色的角色，打不开权限清单"。
--   当前影响有限（"编辑角色权限"的接口与界面尚未实现，见 TODO），
--   但它是一处必然在功能落地时暴露的断链，因此在这里补齐。
--
-- 说明：0009 已发布，按迁移约定不可修改，故新增本文件。
-- ============================================================================

INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code = 'sec_admin'
  AND p.perm_code = 'system:role:list'
ON CONFLICT (role_id, perm_id) DO NOTHING;
