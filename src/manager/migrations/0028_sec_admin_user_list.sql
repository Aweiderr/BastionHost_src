-- ============================================================================
-- 0028 安全管理员补一项只读权限：查看用户列表
--
-- 背景：UKey 证书链管理（权限点 `key:cert:manage`，中文名"证书链管理"）
-- 归属于安全管理员 —— 0009 已把整个 key 模块授予 sec_admin，因此证书链的
-- 初始化、外部根导入、签发、吊销、绑定都在安全管理员手上。
--
-- 但"把一张证书绑定到某个用户"这件事，前提是能知道**系统里有哪些用户可选**。
-- 0009 当时明确写了 sec_admin "不参与用户管理"，连 system:user:list 都没给，
-- 于是会出现一个自相矛盾的处境：安全管理员有权发证书，却打不开用户列表，
-- 证书无处可绑。
--
-- 本迁移只补**只读**的 system:user:list：
--   - 安全管理员需要知道有哪些账号，以便为其签发/绑定/吊销证书；
--   - 但**不需要**创建、修改、删除用户，也不需要重置口令或分配角色
--     —— 那些仍是系统管理员的职责（system:user:create/update/delete/
--     unlock 与 system:role:manage 均**不**授予）。
-- 这是最小权限下的"能看不能改"：既解开了上面的死结，又没有把用户管理
-- 整体交给安全管理员，职责边界仍然清晰。
-- ============================================================================

INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'sec_admin'
  AND p.perm_code = 'system:user:list'
ON CONFLICT (role_id, perm_id) DO NOTHING;

COMMENT ON TABLE sys_role_permission IS
    '角色-权限关联；0028 起 sec_admin 额外持有只读的 system:user:list（用于证书绑定选人）';
