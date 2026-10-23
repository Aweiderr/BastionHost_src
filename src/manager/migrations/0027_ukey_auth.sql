-- ============================================================================
-- 0027 UKey（智能密码钥匙）证书登录
--
-- 需求：支持以 USB Key 中的 SM2 客户端证书完成登录（可作第二因子）。
--
-- 参考实现为 svs-universal（同厂商签名验签平台），本迁移在设计上刻意做了取舍：
--
--   1. 只做 **SM2 单链两级内部 CA**（自签根 CA → 签发 CA）。
--      参考项目额外并行维护一套 RSA 链，目的是兼容只支持 RSA 的老客户端；
--      本系统全栈国密，RSA 链会带来一份无人使用的私钥与一条无人校验的信任路径，
--      扩大攻击面却不产生任何能力，故不引入。
--
--   2. 只签发 **签名证书**，不签发加密证书。
--      参考项目的双证书（签名 + 加密）用于生成 P7 数字信封并灌入 UKey；
--      堡垒机的 UKey 只用于身份认证签名，传输机密性由 TLS 承担，
--      加密证书在本系统没有使用场景。
--
--   3. 证书链校验采用与 Go 标准库同语义的 smx509.Verify（校验签名、有效期、
--      basicConstraints、pathLen、keyUsage、链长），不使用参考项目"逐对人工比对"
--      的做法——后者不检查 pathLen 与 keyUsage，只要能用某个 CA 的私钥签出
--      一张中间证书，即可越权签发任意用户证书。
--
--   4. 吊销以 **本库 km_cert 状态为权威源**，不引入 CRL / OCSP。
--      内部 CA 自签自发，本库就是权威；参考项目的 OCSP 路径把响应签名校验
--      整段注释掉了，等于只解析状态码不验签，引入反而是负资产。
--      将来若要对接外部 CA，再按需补 CRL/OCSP（并必须验签 + 验 nonce）。
--
--   5. CA 私钥不写 km_cert.private_key_cipher，而是交给密码服务
--      （km_key + crypto.Provider）以 key_id 托管，签名走 Provider.KeySign，
--      私钥不出安全边界。参考项目把 CA 私钥经 SM4 主密钥双重加密后直接存库，
--      数据库被脱库即等于 CA 沦陷，可伪造任意用户证书。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 证书表：归属对象与证书来源
--
-- km_cert 由 0006 建立时只有 cert_type（device / client / ca），没有归属字段，
-- 因而无法回答"这张 UKey 证书属于哪个用户"——而 UKey 登录的全部意义就是
-- 把证书序列号映射到一个用户。owner_* 与 km_key 的同名字段保持一致的语义。
-- ---------------------------------------------------------------------------
ALTER TABLE km_cert ADD COLUMN IF NOT EXISTS owner_id BIGINT;
ALTER TABLE km_cert ADD COLUMN IF NOT EXISTS owner_type VARCHAR(32);

COMMENT ON COLUMN km_cert.owner_id IS
    '归属对象 ID：owner_type=user 时为 sys_user.id；CA 证书为空';
COMMENT ON COLUMN km_cert.owner_type IS
    '归属对象类型：user（用户 UKey 证书）/ asset（资产证书）/ device（本机设备证书）；CA 证书为空';

-- cert_source 区分"本机内部 CA 签发"与"外部导入"。
-- 二者在校验路径上不同：内部签发的必须能链到内部根；外部导入的要么由受信任的
-- 外部 CA 签发，要么由管理员显式标记为信任锚。混在一起会导致"内部 CA 未启用时
-- 任意自签证书都能登录"。
ALTER TABLE km_cert ADD COLUMN IF NOT EXISTS cert_source VARCHAR(16) NOT NULL DEFAULT 'internal';

COMMENT ON COLUMN km_cert.cert_source IS
    '证书来源：internal（本机内部 CA 签发）/ external（外部导入，需其签发链在信任列表内）';

ALTER TABLE km_cert DROP CONSTRAINT IF EXISTS ck_cert_source;
ALTER TABLE km_cert ADD CONSTRAINT ck_cert_source
    CHECK (cert_source IN ('internal', 'external'));

-- chain_id 在 0006 已存在但语义只在代码注释里，这里落到库注释上：
-- 指向签发本证书的那张 CA 证书（km_cert.cert_id）；根证书为 NULL。
COMMENT ON COLUMN km_cert.chain_id IS
    '签发者：指向 km_cert.cert_id（签发 CA 证书）；自签根证书为 NULL；UKey 证书指向内部签发 CA';

-- ---------------------------------------------------------------------------
-- 索引
-- ---------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_cert_owner ON km_cert (tenant_id, owner_type, owner_id)
    WHERE owner_id IS NOT NULL;

-- 序列号必须唯一：登录时以序列号定位用户，出现两张同序列号证书会导致
-- "绑定关系指向哪一张"不确定，属于可被利用的歧义。序列号为签发时生成的
-- 128 位随机数，全局唯一是天然成立的。
--
-- 建唯一索引前先清理历史重复（不删数据，只把后来者的序列号置空使其不参与登录），
-- 避免因存量脏数据导致整个迁移失败。
UPDATE km_cert SET serial_number = NULL
 WHERE serial_number IS NOT NULL
   AND cert_id NOT IN (
       SELECT MIN(cert_id) FROM km_cert WHERE serial_number IS NOT NULL GROUP BY serial_number
   );

CREATE UNIQUE INDEX IF NOT EXISTS uk_cert_serial ON km_cert (serial_number)
    WHERE serial_number IS NOT NULL;

-- 登录时需要"按序列号 + 租户"快速定位，且只关心未吊销的客户端证书
CREATE INDEX IF NOT EXISTS idx_cert_client ON km_cert (tenant_id, cert_type, status)
    WHERE cert_type = 'client';

-- ---------------------------------------------------------------------------
-- 登录挑战表：补充 UKey 挑战字段
--
-- 复用 sys_login_challenge（0014 已建）而不是新建表：账密通过后的中间态只有一份，
-- 挑战的一次性、短时效、失败计数、绑定来源 IP 这套机制对 TOTP / 短信 / UKey
-- 完全一致，重复建表会让"清理过期挑战""失败计数超限"这类逻辑出现两份实现。
-- ---------------------------------------------------------------------------
ALTER TABLE sys_login_challenge ADD COLUMN IF NOT EXISTS challenge_nonce VARCHAR(128);
ALTER TABLE sys_login_challenge ADD COLUMN IF NOT EXISTS sign_data VARCHAR(512);
ALTER TABLE sys_login_challenge ADD COLUMN IF NOT EXISTS cert_id BIGINT;

COMMENT ON COLUMN sys_login_challenge.challenge_nonce IS
    'UKey 登录的一次性挑战随机串（base64url，32 字节熵）；验签成功后立即置空以杜绝重放';
COMMENT ON COLUMN sys_login_challenge.sign_data IS
    '服务端构造的签名原文。验签时**以库中这一列为准**逐字节重建，绝不采信客户端回传的
     明文——否则攻击者可自造一段明文与签名，把"证明持有 UKey"降级为"证明能签任意数据"';
COMMENT ON COLUMN sys_login_challenge.cert_id IS
    '本次 UKey 登录校验通过的客户端证书 km_cert.cert_id，用于审计追溯';

-- UKey 登录前必须先在客户端侧做一次"取挑战"，这一步会用用户名 + 未完成挑战定位记录
CREATE INDEX IF NOT EXISTS idx_challenge_nonce ON sys_login_challenge (challenge_nonce)
    WHERE challenge_nonce IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 配置项
--
-- ukey.enabled 默认 false：内部 CA 与管理员证书尚未初始化时开启，会让所有
-- 选择了 ukey 的用户直接无法登录，属于自锁。初始化完成后由管理员显式开启。
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'ukey.enabled', 'false', 'bool', 'auth',
  '是否启用 UKey（智能密码钥匙）登录；内部 CA 未初始化前保持关闭，避免把用户锁在门外'),
 (0, 'ukey.challenge.ttl_seconds', '120', 'int', 'auth',
  'UKey 登录挑战有效期（秒）。挑战是一次性的，超时或已消费即作废'),
 (0, 'ukey.require_internal_ca', 'true', 'bool', 'auth',
  'UKey 客户端证书是否必须能链到本机内部 CA。关闭后仅校验有效期与自签一致性，'
  '安全性显著下降，仅用于对接外部 CA 的过渡期'),
 (0, 'ukey.check_revocation', 'true', 'bool', 'auth',
  'UKey 登录时是否检查证书吊销状态（以 km_cert.status / revoke_time 为准）'),
 (0, 'ukey.ca.root_validity_days', '7300', 'int', 'auth',
  '内部根 CA 有效期（天），默认 20 年；根 CA 只用于签发下级 CA，长期有效可避免全网换根'),
 (0, 'ukey.ca.issue_validity_days', '3650', 'int', 'auth',
  '内部签发 CA 有效期（天），默认 10 年；到期前需用根 CA 重签并轮换'),
 (0, 'ukey.cert.validity_days', '365', 'int', 'auth',
  '用户 UKey 证书默认有效期（天）。签发时会被截断到不超过签发 CA 的到期时间'),
 (0, 'ukey.max_certs_per_user', '3', 'int', 'auth',
  '单个用户允许绑定的 UKey 证书数量上限，防止无限注册证书囤积信任关系')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
