-- ============================================================================
-- 0029 超级管理员补回"证书链管理"权限
--
-- 背景：UKey 证书链管理的权限点由 `system:user:update` 改为
-- `key:cert:manage`（见 internal/api/ukey_routes.go）。
-- 但 super_admin 的权限集合并不是"全部"，而是 0009 里那 6 项
-- 用户与角色管理权限（system:user:list/create/update/delete、
-- system:role:manage、system:config:read），其中**不含 key 模块**。
-- 于是这次改动会让超级管理员**失去**对证书链的访问 —— 属于权限回退，
-- 并非本意（本意是让**系统管理员**失去它）。
--
-- 这里把 `key:cert:manage` 授予 super_admin，维持"超级管理员可访问全部
-- 管理功能"的预期。最终效果：
--   super_admin 可管理证书链（本迁移）
--   sec_admin   可管理证书链（0009 起持有整个 key 模块）
--   sys_admin   **不可**管理证书链（无 key 模块权限）—— 这正是需求要的边界
-- ============================================================================

INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'super_admin'
  AND p.perm_code = 'key:cert:manage'
ON CONFLICT (role_id, perm_id) DO NOTHING;