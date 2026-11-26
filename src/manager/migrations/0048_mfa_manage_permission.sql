-- ============================================================================
-- 0048 二次认证管理权限：归安全管理员
--
-- 背景：系统管理员（sys_admin）负责账号、组织、资产；二次认证方式属于
-- 安全策略，归安全管理员（sec_admin）。用户创建时不再指定因子，由安全
-- 管理员通过 PUT /api/v1/users/{id}/mfa 单独设置。
--
-- 安全管理员只增加只读用户列表权限和二次认证管理权限，不授予
-- system:user:create / update / delete，避免其越权修改角色、口令和状态。
-- ============================================================================

INSERT INTO sys_permission (perm_code, perm_name, perm_type, module, sort_no)
VALUES ('security:mfa:manage', '二次认证管理', 'api', 'security', 730)
ON CONFLICT (perm_code) DO NOTHING;

-- 超级管理员保留紧急恢复入口；安全管理员日常管理。
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code IN ('super_admin', 'sec_admin')
  AND p.perm_code = 'security:mfa:manage'
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- 安全管理员需要能打开用户列表，才能选择目标用户；仅查看，不含修改权限。
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code = 'sec_admin'
  AND p.perm_code = 'system:user:list'
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- 已有角色权限缓存/菜单按权限码刷新；无需改 sys_role。
COMMENT ON TABLE sys_user IS
    '系统用户。二次认证方式由安全管理员设置；系统管理员创建账号时 mfa_type 为 none。';
