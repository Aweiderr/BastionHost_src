-- ============================================================================
-- 0026 收回运维人员的「资产台账」读取权限
--
-- 问题（业务方实测发现）：
--   operator 持有 `asset:asset:list` 与 `asset:account:list`，于是运维人员
--   登录后在「资产管理」里能看到**全部**资产与账号清单 —— 包括根本没有
--   绑定给他的资源。
--
--   这有两层危害：
--     ① 信息越权：资产台账（有哪些机器、地址、账号名）本不该给运维全量看；
--     ② 让"绑定关系"失去意义：没有绑定也能在资产页看到，运维会以为能直接连，
--        实际连接时才被拒（而正确体验是"看不到 = 不能访问"）。
--
--   运维的正确视图是"我可以用哪些资产"，由 `/api/v1/ops/assets` 提供 ——
--   它按授权过滤，而且会把"已绑定但需审批"的资源标出来（need_approval）。
--
-- 处理：收回 operator 的这两个权限。Web 终端与工单申请页改用
--   `/api/v1/ops/assets`，因此功能不受影响。
--
-- approver **保留** `asset:asset:list`：紧急放行（ticket:emergency）需要
--   为他人指定任意资产，那是"先授权后补单"成立的前提。前端通过菜单/路由
--   可见性控制，不让审批人把资产台账当日常页面浏览。
--
-- 说明：0009 已发布，按迁移约定不可修改，故新增本文件。
-- ============================================================================

DELETE FROM sys_role_permission
 WHERE role_id = (SELECT role_id FROM sys_role WHERE role_code = 'operator')
   AND perm_id IN (
        SELECT perm_id FROM sys_permission
         WHERE perm_code IN ('asset:asset:list', 'asset:account:list')
   );
