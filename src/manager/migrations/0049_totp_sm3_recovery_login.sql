-- ============================================================================
-- 0049 动态令牌：国密 SM3、绑定参数持久化、恢复码登录后强制改绑
--
-- 算法、位数、步长必须随账号一起保存：校验时用绑定当时的参数，
-- 不能拿“当前默认算法”去验旧绑定。历史数据默认 SHA1，保持可登录。
-- mfa_totp_rebind_required 表示认证器已不可用（恢复码登录或管理员重置），
-- 必须先重新绑定动态令牌，不能签发业务会话。
-- ============================================================================

ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_totp_algorithm VARCHAR(16) NOT NULL DEFAULT 'SHA1';
ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_totp_digits INT NOT NULL DEFAULT 6;
ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_totp_period INT NOT NULL DEFAULT 30;
ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS mfa_totp_rebind_required BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN sys_user.mfa_totp_algorithm IS
    '动态令牌 HMAC 算法：SHA1 或 SM3。验证时使用绑定当时写入的值';
COMMENT ON COLUMN sys_user.mfa_totp_digits IS
    '动态令牌位数，当前产品固定 6，写入时一并持久化';
COMMENT ON COLUMN sys_user.mfa_totp_period IS
    '动态令牌时间步长（秒），当前产品固定 30，写入时一并持久化';
COMMENT ON COLUMN sys_user.mfa_totp_rebind_required IS
    'true 表示认证器已不可用，必须先重新绑定动态令牌；不签发业务会话';

-- 历史绑定全部按 SHA1 / 6 位 / 30 秒处理，不要求重新绑定。
UPDATE sys_user
   SET mfa_totp_algorithm = 'SHA1'
 WHERE mfa_totp_algorithm IS NULL OR mfa_totp_algorithm = '';
