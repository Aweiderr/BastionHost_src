-- ============================================================================
-- 0009 内置角色的权限分配
--
-- 背景：0008 创建了权限点与内置角色，但未建立二者的关联，
--       导致角色登录后权限列表为空、无法通过细粒度授权校验。
--
-- 分配原则（体现三权分立，对应需求 IR-22 ~ IR-30）：
--   super_admin  全部权限（仅初始化与紧急恢复使用）
--   sys_admin    用户/组织/资产/配置/设备运维 —— 不参与授权与审计
--   sec_admin    授权/策略/密钥/租户访问策略 —— 不参与用户管理与审计
--   audit_admin  审计查询、回放、报表、取证导出 —— 只读，不参与管理
--   operator     发起运维会话、查看被授权资产
--   approver     工单审批
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 超级管理员：全部权限
-- ---------------------------------------------------------------------------
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'super_admin'
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 系统管理员：系统管理（不含角色权限授予的敏感项）+ 资产 + 设备运维
-- 不包含：acl:*（授权）、audit:*（审计）、policy:*（安全策略）、key:*（密钥）
--
-- 特别注意：asset:account:viewpwd（查看明文口令）**不授予系统管理员** ——
-- 凭据属安全管理员管辖，系统管理员可管理账号（增删改、重置口令）但不可查看明文。
-- 这是"能改不能看"的最小权限设计。
-- ---------------------------------------------------------------------------
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'sys_admin'
  AND (
        p.module IN ('system', 'asset', 'device')
        AND p.perm_code NOT IN (
              'system:role:manage',      -- 角色权限授予归安全管理员
              'system:accessmode:write', -- 准入模式归安全管理员
              'asset:account:viewpwd'    -- 查看明文口令归安全管理员
            )
      )
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 安全管理员：授权 + 策略 + 密钥 + 租户访问策略 + 角色权限授予 + 准入模式
-- 不包含：audit:*（审计）、system:user:*（用户管理）
-- ---------------------------------------------------------------------------
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'sec_admin'
  AND (
        p.module IN ('acl', 'policy', 'key', 'ticket')
        OR p.perm_code IN ('system:role:manage', 'system:accessmode:write',
                           'system:config:read', 'system:idp:manage',
                           'asset:account:list', 'asset:account:manage', 'asset:account:viewpwd',
                           'tenant:policy:manage', 'tenant:tenant:list')
      )
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 审计管理员：审计与报表（只读，可导出取证）
-- 不包含任何管理与授权权限
-- ---------------------------------------------------------------------------
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'audit_admin'
  AND p.module IN ('audit', 'ticket')
  AND p.perm_code NOT IN ('ticket:apply', 'ticket:approve', 'ticket:emergency')
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 运维人员：发起运维会话、查看自己会话、文件传输、查看被授权资产
-- ---------------------------------------------------------------------------
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'operator'
  AND p.perm_code IN (
        'ops:session:connect', 'ops:session:list', 'ops:session:share',
        'ops:file:transfer',
        'asset:asset:list', 'asset:account:list',
        'ticket:apply', 'ticket:list:own'
      )
ON CONFLICT (role_id, perm_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 审批人：审批工单、查看全部工单、紧急放行
--   ticket:list:own 也必须有：工单列表接口以 list:own 作为入口权限，
--   审批人若只有 list:all，会出现"能审但打不开列表"的权限断链。
--   ticket:apply 同理：紧急放行（先授权后补单）本身也是一次"提交申请"，
--   若审批人没有提交入口，ticket:emergency 就成了永远用不到的死权限。
-- ---------------------------------------------------------------------------
INSERT INTO sys_role_permission (tenant_id, role_id, perm_id)
SELECT 0, r.role_id, p.perm_id
FROM sys_role r CROSS JOIN sys_permission p
WHERE r.role_code = 'approver'
  AND p.perm_code IN (
        'ticket:apply', 'ticket:approve', 'ticket:list:all', 'ticket:list:own',
        'ticket:emergency',
        'asset:asset:list'
      )
ON CONFLICT (role_id, perm_id) DO NOTHING;
