-- ============================================================================
-- 0014 多因素认证（MFA 二次认证）
--
-- 需求：创建用户时，除账密外可选择**额外的认证方式**（手机验证码 / TOTP / UKey）。
-- 登录时先校验账密，通过后再走第二因子，**两步都通过才发放令牌**。
--
-- 与"短信直接登录"的区别（这是两种不同的东西）：
--   短信直接登录 = 短信**替代**账密，凭手机号 + 验证码即可进入；
--   MFA 二次认证 = 账密**之后**再加一道，手机号来自用户档案而非现场输入。
-- 本迁移服务于后者。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 用户表：MFA 类型
--
-- 已有的 mfa_enabled 表示"是否启用"，这里补上"用哪一种"。
-- 两者都保留：mfa_enabled=true 且 mfa_type=none 属于历史脏数据，
-- 登录时按"未启用 MFA"处理（宁可放行也不要把用户锁在门外）。
-- ---------------------------------------------------------------------------
ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_type VARCHAR(16) NOT NULL DEFAULT 'none';

COMMENT ON COLUMN sys_user.mfa_type IS
    '第二因子类型：none / totp / sms / ukey；mfa_enabled 为 false 时该值被忽略';

ALTER TABLE sys_user DROP CONSTRAINT IF EXISTS ck_user_mfa_type;
ALTER TABLE sys_user ADD CONSTRAINT ck_user_mfa_type
    CHECK (mfa_type IN ('none', 'totp', 'sms', 'ukey'));

-- 短信 MFA 必须有手机号，否则用户会卡在弹窗上无码可收
ALTER TABLE sys_user DROP CONSTRAINT IF EXISTS ck_user_sms_needs_phone;
ALTER TABLE sys_user ADD CONSTRAINT ck_user_sms_needs_phone
    CHECK (mfa_type <> 'sms' OR (phone IS NOT NULL AND phone <> ''));

-- 历史数据：原先启用了 MFA 的用户按 TOTP 处理（此前只实现了 TOTP）
UPDATE sys_user SET mfa_type = 'totp'
 WHERE mfa_enabled = true AND mfa_type = 'none';

-- ---------------------------------------------------------------------------
-- 登录挑战（账密通过后的中间态）
--
-- 为什么需要它：如果"发送验证码"接口只看手机号，那它就是一个对外的
-- 开放短信接口 —— 任何人输入手机号都能触发发送，系统沦为短信轰炸机。
-- 有了 challenge，**只有通过账密校验的会话**才能为对应账号触发验证码，
-- 且 challenge 一次性、短时效、绑定来源 IP。
-- ---------------------------------------------------------------------------
CREATE TABLE sys_login_challenge (
    challenge_id VARCHAR(64) PRIMARY KEY,
    user_id      BIGINT      NOT NULL,
    username     VARCHAR(64) NOT NULL,
    mfa_type     VARCHAR(16) NOT NULL,
    -- phone_masked 只用于界面展示（138****8000），明文手机号不落这张表
    phone_masked VARCHAR(32),
    -- pending 待二次校验 / completed 已完成 / expired 已过期 / failed 失败次数超限
    status       VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts     INT         NOT NULL DEFAULT 0,
    client_ip    VARCHAR(64),
    user_agent   VARCHAR(256),
    expire_time  TIMESTAMPTZ NOT NULL,
    complete_time TIMESTAMPTZ,
    create_time  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE sys_login_challenge IS
    '登录挑战：账密通过后、第二因子校验前的中间态；一次性且短时效';
COMMENT ON COLUMN sys_login_challenge.phone_masked IS
    '脱敏手机号，仅供界面展示；不保存明文号码，减少个人信息暴露面';

CREATE INDEX idx_challenge_user ON sys_login_challenge (user_id, status, create_time DESC);
CREATE INDEX idx_challenge_expire ON sys_login_challenge (status, expire_time);

-- ---------------------------------------------------------------------------
-- 验证码表：补充"绑定到挑战"的维度
--
-- scene 已有 login/bind/reset/verify，这里新增 mfa 场景用于二次认证，
-- 并记录 challenge_id，使得一个验证码只能用于它所属的那次登录。
-- ---------------------------------------------------------------------------
ALTER TABLE sys_sms_code ADD COLUMN IF NOT EXISTS challenge_id VARCHAR(64);
COMMENT ON COLUMN sys_sms_code.challenge_id IS
    '所属登录挑战；非空时该验证码只能用于这次登录，避免跨会话复用';

CREATE INDEX IF NOT EXISTS idx_sms_challenge ON sys_sms_code (challenge_id);

-- ---------------------------------------------------------------------------
-- 配置项
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'mfa.challenge.ttl_seconds', '300', 'int', 'auth',
  '登录挑战有效期（秒）：账密通过后须在此时间内完成第二因子校验'),
 (0, 'mfa.challenge.max_attempts', '5', 'int', 'auth',
  '单个挑战允许的校验失败次数，超过即作废并要求重新登录'),
 (0, 'mfa.sms.auto_send_on_open', 'true', 'bool', 'auth',
  '弹窗打开时是否自动发送验证码（关闭后需用户手动点击）'),
 (0, 'captcha.enabled', 'true', 'bool', 'auth', '是否启用图形验证码'),
 (0, 'captcha.after_failures', '3', 'int', 'auth',
  '同一账号连续登录失败达到该次数后，登录需先通过图形验证码'),
 (0, 'captcha.ttl_seconds', '120', 'int', 'auth', '图形验证码有效期（秒）'),
 (0, 'captcha.length', '4', 'int', 'auth', '图形验证码字符数')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
