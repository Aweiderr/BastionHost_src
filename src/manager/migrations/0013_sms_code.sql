-- ============================================================================
-- 0013 短信验证码（手机动态码）
--
-- 背景：常见堡垒机普遍为**运维人员**提供手机验证码登录，
-- 管理人员则使用 UKey / 账密（UKey 后续接入）。
-- 本迁移提供短信验证码所需的数据结构与配置。
--
-- ---------------------------------------------------------------------------
-- 先说清楚它防什么、不防什么（避免用错地方）：
--
--   * **不防 DDoS**。DDoS 是流量/连接层攻击，目的是打垮服务，
--     攻击者根本不会打开登录页，应用层验证码对它没有意义。
--     DDoS 要靠网络层（流量清洗、高防 IP）与连接层（limit_conn）解决。
--
--   * **防的是自动化口令爆破**，以及**短信接口被滥用**。
--     后者尤其重要：若发送不限流，攻击者可把本系统当短信轰炸机，
--     既烧钱又可能导致短信签名被运营商封停 —— 比登录爆破更严重。
--
--   * 短信本身是**弱因子**（SIM 交换、短信嗅探、伪基站均可突破）。
--     因此定位为运维人员的便捷 MFA，**不作为管理员的主认证**；
--     管理员保留 UKey / TOTP 这类强因子。
-- ---------------------------------------------------------------------------
--
-- 测试与正式环境的差异由 provider 决定，而不是靠改业务代码：
--   provider = mock  → 不真正下发短信，验证码写日志 / 固定码，供测试环境使用
--   provider = aliyun / tencent / gateway / smpp → 真实下发并完全校验
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 验证码记录
-- ---------------------------------------------------------------------------
CREATE TABLE sys_sms_code (
    code_id      BIGSERIAL    PRIMARY KEY,
    tenant_id    BIGINT       NOT NULL DEFAULT 0,
    phone        VARCHAR(32)  NOT NULL,
    code         VARCHAR(16)  NOT NULL,
    -- 使用场景：login 登录 / bind 绑定手机 / reset 重置口令 / verify 高危操作二次确认
    scene        VARCHAR(32)  NOT NULL DEFAULT 'login',
    user_id      BIGINT,
    -- pending 待校验 / used 已使用 / expired 已过期 / invalid 已作废（重发时旧的作废）
    status       VARCHAR(16)  NOT NULL DEFAULT 'pending',
    -- 校验尝试次数：超过上限即作废，防止对 6 位码做在线爆破
    attempts     INT          NOT NULL DEFAULT 0,
    send_ip      VARCHAR(64),
    provider     VARCHAR(32),
    -- 短信服务商返回的消息 ID，便于与其账单/日志对账
    provider_msg_id VARCHAR(128),
    send_result  VARCHAR(32),
    expire_time  TIMESTAMPTZ  NOT NULL,
    used_time    TIMESTAMPTZ,
    create_time  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE sys_sms_code IS '短信验证码记录（含发送与校验审计，不保存任何明文凭据）';
COMMENT ON COLUMN sys_sms_code.status IS 'pending / used / expired / invalid';
COMMENT ON COLUMN sys_sms_code.attempts IS '校验尝试次数，超过阈值作废';

-- 按手机号 + 时间查询"最近发送"用于限流
CREATE INDEX idx_sms_phone_time ON sys_sms_code (phone, create_time DESC);
-- 按来源 IP 限流
CREATE INDEX idx_sms_ip_time ON sys_sms_code (send_ip, create_time DESC);
CREATE INDEX idx_sms_status ON sys_sms_code (status, expire_time);

-- ---------------------------------------------------------------------------
-- 配置项
--
-- 默认值按"内网测试友好"设定：provider=mock，表示不真正发短信。
-- 生产部署必须显式改成真实服务商，否则验证码不会送达。
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'sms.provider', 'mock', 'string', 'auth',
  '短信服务商：mock（测试，不真正下发）/ aliyun / tencent / gateway / smpp'),
 (0, 'sms.code.length', '6', 'int', 'auth', '验证码位数'),
 (0, 'sms.code.ttl_seconds', '300', 'int', 'auth', '验证码有效期（秒），默认 5 分钟'),
 (0, 'sms.code.max_verify_attempts', '5', 'int', 'auth',
  '单条验证码允许的校验次数，超过即作废（防止在线爆破 6 位码）'),
 (0, 'sms.limit.per_phone_per_hour', '5', 'int', 'auth',
  '同一手机号每小时最多发送条数（防止把系统当短信轰炸机）'),
 (0, 'sms.limit.per_phone_per_day', '15', 'int', 'auth', '同一手机号每天最多发送条数'),
 (0, 'sms.limit.per_ip_per_hour', '20', 'int', 'auth', '同一来源 IP 每小时最多发送条数'),
 (0, 'sms.limit.resend_interval_seconds', '60', 'int', 'auth',
  '同一手机号两次发送的最小间隔（秒），前端倒计时与此保持一致'),
 (0, 'sms.captcha.after_failures', '3', 'int', 'auth',
  '登录失败达到该次数后要求图形验证码（防自动化爆破，与 DDoS 无关）'),
 (0, 'sms.login.enabled', 'true', 'bool', 'auth', '是否允许使用手机验证码登录'),
 (0, 'sms.login.roles', 'operator', 'string', 'auth',
  '允许短信登录的角色，逗号分隔。默认仅运维人员 —— 管理员应使用 UKey/TOTP 等强因子')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
