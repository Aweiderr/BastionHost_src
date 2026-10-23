-- ============================================================================
-- 0012 凭据不托管模式（手动输入资产口令）
--
-- 背景：部分客户的资产所属方不愿把口令交给堡垒机托管，要求运维人员在
-- 连接目标时自行输入。这是堡垒机的标准能力（行业称"凭据不托管 /
-- 手动输入模式"），与现有的"凭据托管"并列，而非替代关系。
--
-- 设计取舍：
--
--   * credential_mode 是**账号级**开关，同一资产可混合 ——
--     例如 root 走托管便于自动化巡检，oracle 走手动满足合规要求；
--
--   * manual 账号的 password_cipher / private_key_cipher / sudo_password_cipher
--     必须为空：堡垒机**不保存任何口令材料**。用 CHECK 约束兜住
--     "绕过界面直接写库"的情况，而不是只靠应用层自觉；
--
--   * rotate_days 对 manual 账号没有意义（口令不在我们手里），
--     界面应禁用该字段，服务端也不对其做自动轮换；
--
--   * 注意**不要复用 account_type**：它表达的是认证方式
--     （password / key / both），与"是否托管"是两个维度。
--
--   * ops_session 记录本次会话实际使用的凭据模式，
--     便于审计区分"用了堡垒机托管的凭据"与"运维人员自己输入的凭据"。
--     无论哪种模式，**口令本身都不入库**。
--
-- 安全性说明：manual 模式下口令只在客户端与网关的内存中短暂存在，
-- 不落库、不进录像、不进命令记录；但无法宣称"口令零接触"，
-- 因为建立连接必须用到它。这一点在文档与界面中都要如实说明。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 账号表：凭据模式
-- ---------------------------------------------------------------------------
ALTER TABLE ast_account
    ADD COLUMN IF NOT EXISTS credential_mode VARCHAR(16) NOT NULL DEFAULT 'managed';

COMMENT ON COLUMN ast_account.credential_mode IS
    '凭据模式：managed（堡垒机托管，口令加密存储）/ manual（不托管，运维连接时手动输入）';

-- 手动模式不得存任何口令材料
ALTER TABLE ast_account DROP CONSTRAINT IF EXISTS ck_account_manual_no_secret;
ALTER TABLE ast_account ADD CONSTRAINT ck_account_manual_no_secret CHECK (
    credential_mode <> 'manual'
    OR (password_cipher IS NULL AND private_key_cipher IS NULL AND sudo_password_cipher IS NULL)
);

CREATE INDEX IF NOT EXISTS idx_account_cred_mode ON ast_account (tenant_id, credential_mode);

-- ---------------------------------------------------------------------------
-- 会话表：本次会话的凭据来源
-- ---------------------------------------------------------------------------
ALTER TABLE ops_session
    ADD COLUMN IF NOT EXISTS credential_mode VARCHAR(16) NOT NULL DEFAULT 'managed';

COMMENT ON COLUMN ops_session.credential_mode IS
    '本次会话使用的凭据模式：managed / manual；manual 表示口令由运维人员现场输入，未留存';

-- ---------------------------------------------------------------------------
-- 系统配置：手动输入的重试策略
--
-- 如果不限制重试次数，手动输入环节会让堡垒机变成一个"带审计的口令爆破入口"：
-- 攻击者拿到一个堡垒机账号后，可对任意资产反复猜口令。
-- 因此默认 3 次失败即断开会话，并把失败尝试计入审计。
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'credential.manual.max_attempts', '3', 'int', 'security',
  '手动输入凭据的最大尝试次数，超过则断开会话并告警'),
 (0, 'credential.manual.timeout_seconds', '60', 'int', 'security',
  '单次手动输入凭据的等待超时（秒），超时按一次失败计')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
