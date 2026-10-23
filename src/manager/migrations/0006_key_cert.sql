-- ============================================================================
-- 0006 密钥与证书
--
-- 说明：密钥使用记录统一由 ops_key_usage（0004 已建）承载，
--       因此本迁移不重复创建 km_key_usage，避免同一事实两处记录。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 密钥（元数据；密钥材料由密码服务托管，可落库或落密码硬件）
-- ---------------------------------------------------------------------------
CREATE TABLE km_key (
    id              BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    key_id          VARCHAR(128) NOT NULL,
    key_type        VARCHAR(32)  NOT NULL,
    key_usage       VARCHAR(64)  NOT NULL,
    alias           VARCHAR(128),
    owner_id        BIGINT,
    owner_type      VARCHAR(32),
    provider        VARCHAR(32)  NOT NULL DEFAULT 'soft',
    version         INTEGER      NOT NULL DEFAULT 1,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    secret_cipher   TEXT,
    public_key      TEXT,
    fingerprint     VARCHAR(128),
    key_store_ref   VARCHAR(512),
    expire_at       TIMESTAMPTZ,
    rotate_days     INTEGER      NOT NULL DEFAULT 0,
    last_rotate_time TIMESTAMPTZ,
    backup_status   VARCHAR(16)  NOT NULL DEFAULT 'none',
    backup_time     TIMESTAMPTZ,
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE km_key IS '密钥元数据表（对应 FR-09 密钥全生命周期管理）';
COMMENT ON COLUMN km_key.key_type IS '类型：SM2 / SM4 / HMAC';
COMMENT ON COLUMN km_key.key_usage IS '用途：data_encrypt / data_sign / session_key / credential_encrypt / token_sign / ssh_host_key';
COMMENT ON COLUMN km_key.provider IS '托管实现：soft（软件）/ pkcs11（密码卡、密码机）';
COMMENT ON COLUMN km_key.secret_cipher IS '密钥材料密文（硬件托管时为空，仅存 key_store_ref）';
COMMENT ON COLUMN km_key.backup_status IS '密钥备份状态：none / backed_up / verified（对应 FR-09-13）';
CREATE UNIQUE INDEX uk_km_key_id ON km_key (key_id);
CREATE INDEX idx_km_key_usage ON km_key (tenant_id, key_usage, status);
CREATE INDEX idx_km_key_expire ON km_key (expire_at) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- 证书
-- ---------------------------------------------------------------------------
CREATE TABLE km_cert (
    cert_id         BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    cert_type       VARCHAR(32)  NOT NULL DEFAULT 'device',
    cert_name       VARCHAR(128) NOT NULL,
    subject_dn      VARCHAR(512),
    issuer_dn       VARCHAR(512),
    serial_number   VARCHAR(128),
    fingerprint     VARCHAR(128),
    cert_pem        TEXT,
    private_key_cipher TEXT,
    key_id          VARCHAR(128),
    not_before      TIMESTAMPTZ,
    not_after       TIMESTAMPTZ,
    is_ca           BOOLEAN      NOT NULL DEFAULT false,
    is_trusted_root BOOLEAN      NOT NULL DEFAULT false,
    chain_id        BIGINT,
    status          VARCHAR(16)  NOT NULL DEFAULT 'active',
    revoke_time     TIMESTAMPTZ,
    revoke_reason   VARCHAR(255),
    remark          VARCHAR(512),
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    create_by       BIGINT
);
COMMENT ON TABLE km_cert IS '证书表（设备证书、客户端证书、CA 证书）';
COMMENT ON COLUMN km_cert.cert_type IS '类型：device（设备证书）/ client（客户端、UKey）/ ca（根与中间证书）';
CREATE UNIQUE INDEX uk_cert_fingerprint ON km_cert (fingerprint) WHERE fingerprint IS NOT NULL;
CREATE INDEX idx_cert_type ON km_cert (tenant_id, cert_type, status);
CREATE INDEX idx_cert_expire ON km_cert (not_after) WHERE status = 'active';
