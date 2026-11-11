-- ============================================================================
-- 0044 外部 CA 的 UKey 吊销检查（CRL / OCSP）
--
-- ukey.check_revocation 仍然是总开关：关闭后，本地 km_cert 吊销表和外部
-- CRL/OCSP 都不拦截。开启时，内部证书仍以 km_cert 为准；外部证书再按证书上的
-- CDP / AIA（或下面的覆盖地址）查询。查不到、超时、签名或 nonce 无效、CRL 过期，
-- 都按「无法确认吊销状态」拒绝登录。
--
-- 缓存上限默认 15 分钟，并且不会超过 CRL/OCSP 的 nextUpdate。失败不缓存。
-- 单次 HTTP 超时默认 5 秒，一次登录里 OCSP 与 CRL 加在一起不超过 15 秒。
-- ============================================================================

UPDATE sys_config
   SET description = 'UKey 登录是否检查吊销。开启时：内部证书看 km_cert；外部 CA 证书还查询 CRL/OCSP，查不到或响应无效则拒绝登录。关闭后本地与外部吊销都不拦截。',
       update_time = now()
 WHERE tenant_id = 0
   AND config_key = 'ukey.check_revocation';

INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'ukey.revocation.crl_url', '', 'string', 'auth',
  '外部 CA 的 CRL 地址覆盖。空则用证书 CDP；非空则替换 CDP（内网镜像，或证书未写 CDP 时使用）。多个地址用英文逗号分隔。'),
 (0, 'ukey.revocation.ocsp_url', '', 'string', 'auth',
  '外部 CA 的 OCSP 地址覆盖。空则用证书 AIA 里的 OCSP；非空则替换。客户端使用 HTTP POST，并校验响应签名与 nonce。'),
 (0, 'ukey.revocation.timeout_seconds', '5', 'int', 'auth',
  '单次 CRL/OCSP 请求超时（秒），允许 1 到 15，默认 5。一次登录的总预算不超过 15 秒。超时视为无法确认吊销状态并拒绝登录。'),
 (0, 'ukey.revocation.cache_ttl_seconds', '900', 'int', 'auth',
  'CRL/OCSP 成功结果的缓存上限（秒），默认 900，允许 30 到 86400。实际不会超过 nextUpdate。失败不缓存。')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
