-- ============================================================================
-- 0016 图形验证码显示策略
--
-- 背景：0014 只加了 captcha.after_failures（失败 N 次后才要求），
-- 实际使用中发现这个默认值不符合预期 —— 运维希望"登录页一直有验证码"，
-- 而"失败几次才出现"会让人以为功能坏了（明明配了却看不到）。
--
-- 因此引入显式的模式配置，并把默认值定为 always：
--   always          每次登录都要求（默认，最直观，防护也最强）
--   after_failures  仅在连续失败达到阈值后要求（可用性优先，用户无感）
--   off             关闭
--
-- 保留 after_failures 而不是删掉：对"内网、用户固定、极其在意操作步骤"的
-- 场景，让正常用户完全无感是有价值的，只是不该作为默认。
-- ============================================================================

INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'captcha.mode', 'always', 'string', 'auth',
  '图形验证码模式：always 每次登录都要求（默认）/ after_failures 仅连续失败达到阈值后要求 / off 关闭')
ON CONFLICT (tenant_id, config_key) DO UPDATE
  SET description = EXCLUDED.description;

-- 已部署环境若之前写入过旧值，这里只纠正描述，不动用户显式设置的值
UPDATE sys_config SET description = 'captcha.mode=after_failures 时使用的失败次数阈值；captcha.mode=always 时该值不生效'
 WHERE config_key = 'captcha.after_failures';
