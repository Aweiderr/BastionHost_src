-- ============================================================================
-- 0046 动态令牌绑定闭环
--
-- 确认绑定之前，种子可以先落在 mfa_secret_cipher 里，但登录不能因此要求验证码。
-- mfa_totp_pending 把「已生成、未确认」和「已确认」分开。
-- mfa_totp_last_step 记下最近一次接受的时间片，同一时间片不能再用。
-- 恢复码只存 SHA-256 摘要，用过即随解绑删除。
-- ============================================================================

ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_totp_pending BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_totp_last_step BIGINT;

COMMENT ON COLUMN sys_user.mfa_totp_pending IS
    'true 表示已生成 TOTP 种子但尚未用动态口令确认；此时登录不要求动态口令';
COMMENT ON COLUMN sys_user.mfa_totp_last_step IS
    '最近一次接受的 TOTP 时间片计数（Unix 秒 / 60）。小于或等于该值的时间片拒绝，保证同一时间片只用一次';

-- 升级前「开始绑定」只写入种子并把 mfa_enabled 留在 false。
-- 这些行还没有确认，标成待确认，避免被当成已经绑定。
UPDATE sys_user
   SET mfa_totp_pending = true
 WHERE mfa_enabled = false
   AND mfa_secret_cipher IS NOT NULL
   AND mfa_secret_cipher <> '';

CREATE TABLE IF NOT EXISTS sys_user_totp_recovery (
    recovery_id BIGSERIAL PRIMARY KEY,
    user_id     BIGINT       NOT NULL REFERENCES sys_user(user_id) ON DELETE CASCADE,
    code_hash   VARCHAR(64)  NOT NULL,
    used_at     TIMESTAMPTZ,
    create_time TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_totp_recovery_user
    ON sys_user_totp_recovery (user_id)
    WHERE used_at IS NULL;

COMMENT ON TABLE sys_user_totp_recovery IS
    'TOTP 恢复码的 SHA-256 摘要，不明文保存。解绑成功后整行删除；并发时靠 used_at 保证同一枚只用一次';
COMMENT ON COLUMN sys_user_totp_recovery.code_hash IS
    '恢复码规范化（去连字符、大写）后的 SHA-256 十六进制摘要';
