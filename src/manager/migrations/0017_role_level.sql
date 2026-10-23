-- ============================================================================
-- 0017 角色等级与授权上限（修复提权漏洞）
--
-- ---------------------------------------------------------------------------
-- 漏洞：用户管理与角色授予是两条独立路径，前者**绕过了后者的权限检查**
--
--   POST /api/v1/users            要求 system:user:create
--   POST /api/v1/users/{id}/roles 要求 system:role:manage
--
-- 看似合理，但创建/更新用户的请求体里**也带 roles 字段**，
-- 而 CreateUser / UpdateUser 会直接调用 AssignRoles，
-- 于是 system:role:manage 这一层检查被完全绕过。
--
-- 实际后果：系统管理员（有 user:create，无 role:manage）
-- 只要在创建用户时写上 roles:["super_admin"]，就能**自己造一个超管**，
-- 或把自己改成超管。三权分立就此失效。
--
-- ---------------------------------------------------------------------------
-- 修复思路：引入**角色等级**，并规定一条不可逾越的规则 ——
--
--     任何人都只能创建或授予**等级严格低于自己**的角色。
--
--   super_admin(100) → 可授予三员(80)、审批(60)、运维(40)
--   sys_admin(80)    → 可授予审批(60)、运维(40)；**不能**授予任何同级或更高级
--   sec_admin(80)    → 同上
--   audit_admin(80)  → 同上（审计员通常只读，不应建号，权限层面另行限制）
--
-- 这条规则天然满足需求方的要求：
--   · 超管能创建其它管理员（它是唯一等级最高的角色）；
--   · 系统管理员只能创建运维/审批账号，造不出管理员；
--   · 不存在"同级可以互建"的情况 —— 那会让三员互相造号，制衡失效。
--
-- ---------------------------------------------------------------------------
-- 另：超级管理员必须**有且只有一个**，用 singleton 标记 + 清理历史数据实现。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 1. 角色等级
-- ---------------------------------------------------------------------------
ALTER TABLE sys_role ADD COLUMN IF NOT EXISTS level INT NOT NULL DEFAULT 50;

COMMENT ON COLUMN sys_role.level IS
    '角色等级：数值越大权限越高。授权规则为"只能授予等级严格低于自己的角色"';

UPDATE sys_role SET level = 100 WHERE role_code = 'super_admin';
UPDATE sys_role SET level = 80  WHERE role_code IN ('sys_admin', 'sec_admin', 'audit_admin');
UPDATE sys_role SET level = 60  WHERE role_code = 'approver';
UPDATE sys_role SET level = 40  WHERE role_code = 'operator';

-- ---------------------------------------------------------------------------
-- 2. 单例角色标记
--
-- 标为 singleton 的角色在系统中只能被一个账号持有（目前仅超级管理员）。
-- 用应用层校验 + 这里的数据清理共同保证：数据库层面无法用简单的唯一索引
-- 表达"关联表中某 role_id 只能出现一次"以外的约束（那其实是可行的，
-- 但会让 sys_user_role 的语义变得特殊），因此选择应用层强制。
-- ---------------------------------------------------------------------------
ALTER TABLE sys_role ADD COLUMN IF NOT EXISTS singleton BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN sys_role.singleton IS
    '是否为单例角色：为 true 时系统中只能有一个账号持有它（如超级管理员）';

UPDATE sys_role SET singleton = true WHERE role_code = 'super_admin';

-- 清理历史上可能存在的多个超管：保留 user_id 最小的那个（通常是初始化创建的）
DELETE FROM sys_user_role
 WHERE role_id IN (SELECT role_id FROM sys_role WHERE role_code = 'super_admin' AND singleton = true)
   AND user_id NOT IN (
        SELECT keep_id FROM (
            SELECT ur.user_id AS keep_id
              FROM sys_user_role ur
              JOIN sys_role r ON r.role_id = ur.role_id
             WHERE r.role_code = 'super_admin'
             ORDER BY ur.user_id
             LIMIT 1
        ) AS t
   );

-- ---------------------------------------------------------------------------
-- 3. 更新角色描述，把"谁能建谁"写清楚，界面上也能看到
-- ---------------------------------------------------------------------------
UPDATE sys_role SET description = '负责创建与管理管理员账号（三员由其建立）；系统仅允许存在一个超级管理员'
 WHERE role_code = 'super_admin';
UPDATE sys_role SET description = '负责用户、资产与系统配置；只能创建运维/审批类账号，不能创建管理员'
 WHERE role_code = 'sys_admin';
