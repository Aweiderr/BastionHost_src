-- ============================================================================
-- 0047 第二因子政策：指定一种因子、身份核验、变更审计
--
-- 登录应是口令加上账号被指定的那一种因子（UKey / 动态令牌 / 短信）。
-- 本迁移不改写任何用户的 mfa_type，不补证书，也不打开强制开关。
-- 两个开关的默认值都是 false：
--   mfa.login.require_assigned_factor  为 true 时，尚未指定因子的账号不能只凭口令登录
--   mfa.privileged.ukey_enforce        为 true 时，管理员档账号必须使用 UKey
-- 打开之前必须看就绪检查。未就绪就打开会把还能登录的管理员锁在门外，接口会拒绝。
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_mfa_stepup (
    proof_hash  VARCHAR(64) PRIMARY KEY,
    actor_id    BIGINT       NOT NULL,
    target_id   BIGINT       NOT NULL,
    action      VARCHAR(32)  NOT NULL,
    factor      VARCHAR(16)  NOT NULL DEFAULT '',
    kind        VARCHAR(16)  NOT NULL,
    expires_at  TIMESTAMPTZ  NOT NULL,
    used_at     TIMESTAMPTZ,
    create_time TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT ck_mfa_stepup_kind CHECK (kind IN ('proof', 'pending'))
);

COMMENT ON TABLE sys_mfa_stepup IS
    'MFA 变更的一次性身份核验。只存证明的 SHA-256，不存口令、验证码或种子。证明绑定操作者、目标与动作，过期或使用后不能再用。';
COMMENT ON COLUMN sys_mfa_stepup.proof_hash IS
    '证明原文的 SHA-256 十六进制摘要。原文只在签发时返回一次。';
COMMENT ON COLUMN sys_mfa_stepup.kind IS
    'proof 可被变更接口消费一次；pending 只表示短信或 UKey 核验还没完成，不能当作变更证明。';

CREATE INDEX IF NOT EXISTS idx_mfa_stepup_expire
    ON sys_mfa_stepup (expires_at)
    WHERE used_at IS NULL;

CREATE TABLE IF NOT EXISTS audit_mfa_change (
    event_id     BIGSERIAL PRIMARY KEY,
    actor_id     BIGINT,
    actor_name   VARCHAR(64),
    target_id    BIGINT,
    target_name  VARCHAR(64),
    action       VARCHAR(32)  NOT NULL,
    old_method   VARCHAR(16),
    new_method   VARCHAR(16),
    result       VARCHAR(16)  NOT NULL,
    reason       VARCHAR(64),
    client_ip    VARCHAR(64),
    create_time  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT ck_mfa_change_result CHECK (result IN ('success', 'denied'))
);

COMMENT ON TABLE audit_mfa_change IS
    '第二因子变更的成功与拒绝。只记录方法名和稳定原因码，不记录验证码、种子、私钥、口令或完整手机号。';

CREATE INDEX IF NOT EXISTS idx_mfa_change_target
    ON audit_mfa_change (target_id, create_time DESC);

-- 因子曾经生效过。解绑、吊销或清空手机号都不清除。
-- 仅“从未指定且从未生效”的账号可以用口令换变更证明。
ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_factor_activated BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN sys_user.mfa_factor_activated IS
    '第二因子曾经生效。true 后不能再只凭口令重新绑定或降级。初次指定但尚未确认的动态令牌保持 false。';

UPDATE sys_user
   SET mfa_factor_activated = true
 WHERE mfa_factor_activated = false
   AND mfa_type IN ('totp', 'sms', 'ukey')
   AND mfa_enabled = true;

INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'mfa.login.require_assigned_factor', 'false', 'bool', 'auth',
  '为 true 时，尚未指定 UKey/动态令牌/短信的账号不能只凭口令登录 Web 或桌面。默认 false，避免把现有账号锁在门外。打开前请先看就绪检查。'),
 (0, 'mfa.privileged.ukey_enforce', 'false', 'bool', 'auth',
  '为 true 时，管理员档账号（super_admin 与三员，或等级 >= 80）登录必须使用 UKey。默认 false：不改写现有管理员，也不拒绝他们当前的因子。打开前就绪检查必须没有阻断项。'),
 (0, 'mfa.stepup.ttl_seconds', '300', 'int', 'auth',
  'MFA 变更身份核验的有效秒数。证明一次性，过期作废。')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
