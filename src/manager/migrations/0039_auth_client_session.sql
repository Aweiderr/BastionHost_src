-- ============================================================================
-- 0039 Web 与桌面登录互斥
--
-- 同一用户在桌面客户端登录成功后，Web 上已有的访问令牌和刷新令牌立即失效；
-- 反过来也一样。同一类客户端默认可以同时保留多个会话。
--
-- 不在两端共用一个令牌：每次登录仍各自签发 jti。这里只登记“这个 jti 属于
-- 哪一次、哪一端的登录”，校验时拒绝已被作废的 jti。
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_auth_session (
    jti            VARCHAR(64)  PRIMARY KEY,
    user_id        BIGINT       NOT NULL,
    client_type    VARCHAR(16)  NOT NULL,
    token_scope    VARCHAR(16)  NOT NULL,
    family_id      VARCHAR(64)  NOT NULL,
    expires_at     TIMESTAMPTZ  NOT NULL,
    revoked        BOOLEAN      NOT NULL DEFAULT false,
    revoke_reason  VARCHAR(32),
    create_time    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT ck_auth_session_client CHECK (client_type IN ('web', 'desktop')),
    CONSTRAINT ck_auth_session_scope CHECK (token_scope IN ('access', 'refresh'))
);

COMMENT ON TABLE sys_auth_session IS
    '登录会话登记：按 jti 记录令牌属于哪一端。另一端登录或本端登出时把对应行标成已作废';
COMMENT ON COLUMN sys_auth_session.family_id IS
    '同一次登录的访问令牌与刷新令牌共享此标识；登出时整族作废，避免刷新令牌换回新会话';
COMMENT ON COLUMN sys_auth_session.revoke_reason IS
    'opposite 被另一端登录踢下线 / same 被本端新登录替换 / logout 主动登出';

CREATE INDEX IF NOT EXISTS idx_auth_session_user_client
    ON sys_auth_session (user_id, client_type)
    WHERE revoked = false;

CREATE INDEX IF NOT EXISTS idx_auth_session_family
    ON sys_auth_session (family_id);

-- 挑战上记下是哪一端发起的账密登录。
-- 短信 / UKey 第二因子通过后签发的令牌必须沿用这一端，不能被第二次请求改掉。
ALTER TABLE sys_login_challenge
    ADD COLUMN IF NOT EXISTS client_type VARCHAR(16) NOT NULL DEFAULT 'web';

COMMENT ON COLUMN sys_login_challenge.client_type IS
    '发起这次登录的客户端：web / desktop。第二因子通过后按此签发令牌';

ALTER TABLE sys_login_challenge DROP CONSTRAINT IF EXISTS ck_challenge_client_type;
ALTER TABLE sys_login_challenge
    ADD CONSTRAINT ck_challenge_client_type CHECK (client_type IN ('web', 'desktop'));

INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'auth.kick_opposite_client', 'true', 'bool', 'security',
  '同一用户在 Web 与桌面客户端之间互斥登录。开启（默认）时，一端登录成功会立即作废另一端的会话；关闭后两端可以同时在线。'),
 (0, 'auth.kick_same_client', 'false', 'bool', 'security',
  '同一客户端类型是否只保留一个会话。默认关闭，Web 可以同时开多个浏览器、桌面也可以保留多个会话；设为 true 后，新登录会踢掉该端的其它会话。')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
