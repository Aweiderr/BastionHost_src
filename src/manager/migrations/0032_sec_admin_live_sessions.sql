-- ============================================================================
-- 0032 安全管理员：在线会话查看 + 强制中断归属收敛
--
-- 背景：
--   进行中的运维连接记在 ops_session（status = active）。
--   历史会话查询走 GET /api/v1/audit/sessions，权限点是 audit:session:list，
--   只属于审计管理员。安全管理员需要看到「现在谁连着哪台资产」，
--   但不能因此拿到整份审计库。
--
--   强制中断的接口早已存在：
--     POST /api/v1/ops/sessions/{id}/kill
--     POST /api/v1/audit/sessions/{id}/kill
--   两者都要求 ops:session:kill。
--   0031 把该点同时授予了安全管理员、系统管理员和超级管理员。
--   产品决定：强制中断归安全管理员，不归系统管理员，也不归审计管理员。
--   超级管理员在 0031 里已有的应急权限本迁移不动。
--
-- 本迁移做两件事：
--   1. 新增 ops:session:live（查看在线会话），只授予安全管理员。
--      列表接口固定只返回 status=active，不开放历史会话。
--   2. 从系统管理员收回 ops:session:kill。
--      安全管理员的 kill 仍以 0031 为准，这里再写一次是为了幂等。
--
-- 已发布的 0008 / 0009 / 0015 / 0031 不改，变更只加在本文件。
-- ============================================================================

INSERT INTO sys_permission (perm_code, perm_name, perm_type, module, sort_no)
VALUES ('ops:session:live', '查看在线会话', 'api', 'ops', 314)
ON CONFLICT (perm_code) DO NOTHING;

-- 安全管理员：能打开「会话监控 → 在线会话」
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT r.tenant_id, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code = 'sec_admin'
  AND p.perm_code = 'ops:session:live'
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- 安全管理员：能强制中断（0031 已授予；重复执行无副作用）
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT r.tenant_id, r.role_id, p.perm_id
FROM sys_role r
CROSS JOIN sys_permission p
WHERE r.role_code = 'sec_admin'
  AND p.perm_code = 'ops:session:kill'
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- 系统管理员不再持有强制中断。菜单是否出现由 ops:session:live 决定，
-- 系统管理员没有该点，侧边栏保持原样。
DELETE FROM sys_role_permission
 WHERE role_id IN (SELECT role_id FROM sys_role WHERE role_code = 'sys_admin')
   AND perm_id IN (SELECT perm_id FROM sys_permission WHERE perm_code = 'ops:session:kill');

UPDATE sys_role
   SET description = rtrim(description, '。') || '；可查看并强制中断进行中的运维会话'
 WHERE role_code = 'sec_admin'
   AND description NOT LIKE '%强制中断%';
