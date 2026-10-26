/**
 * 后端实体与接口出入参类型定义。
 * 字段命名与后端 JSON 契约保持一致（snake_case），缺失字段一律按可选处理。
 */

/** 统一响应信封 */
export interface Envelope<T> {
  code: string
  message: string
  data: T
}

/** 分页列表通用结构 */
export interface PageResult<T> {
  items: T[]
  total: number
  page?: number
  page_size?: number
}

/** 分页查询通用参数 */
export interface PageQuery {
  page?: number
  page_size?: number
}

/* ------------------------------------------------------------------ 初始化 */

export interface SetupStatus {
  initialized: boolean
  step?: string
  device_name?: string
  has_super_admin?: boolean
  sealed?: boolean
  /** 后端实际返回的字段名（与文档契约不同），一并兼容 */
  current_step?: string
  next_action?: string
  super_admin_sealed?: boolean
  admin_count?: number
  steps?: Array<Record<string, unknown>>
}

export interface SetupInitPayload {
  username: string
  password: string
  display_name: string
  device_name: string
}

export interface SetupInitResult {
  setup_token: string
  [key: string]: unknown
}

export interface SetupAdminItem {
  role_code: string
  username: string
  password: string
  display_name: string
}

/* -------------------------------------------------------------------- 认证 */

export interface LoginPayload {
  username: string
  password: string
  totp_code?: string
  /** 图形验证码标识；仅在连续失败达到阈值后由前端附带 */
  captcha_id?: string
  captcha_code?: string
}

export interface LoginResult {
  access_token: string
  refresh_token: string
  token_type: string
  expires_in: number
  user_id: number
  username: string
  display_name: string
  roles: string[]
  permissions: string[]
  must_change_password: boolean
  password_expired?: boolean
  mfa_enabled: boolean

  // ---- MFA 二次认证（账密已通过，等待第二因子）----
  //
  // mfa_required 为 true 时 access_token 为空，前端应弹出第二因子弹窗，
  // 校验通过后调用 /auth/mfa/verify 才能拿到令牌。
  mfa_required?: boolean
  mfa_type?: 'none' | 'totp' | 'sms' | 'ukey'
  /** 登录挑战标识，索取验证码与提交校验都要带上 */
  challenge_id?: string
  /**
   * UKey 挑战的签名原文（仅 mfa_type=ukey 时返回）。
   *
   * 客户端签的**不是**它，而是下面的 src_data；服务端验签时以库中
   * sign_data 为准逐字节比对，因此客户端不得自造原文。
   */
  sign_data?: string
  /**
   * 建议交给 UKey 中间件签名的字符串，即 base64(sign_data)。
   *
   * 服务端直接下发这个"标准答案"，避免每个客户端各自猜编码方式：
   * 真机中间件把它当数据本身签名、不做 base64 解码。
   */
  src_data?: string
  /** UKey 挑战到期时间，供前端倒计时与"已过期"提示 */
  challenge_expire_time?: string
  /** 脱敏手机号（138****8000），供弹窗展示 */
  phone_masked?: string
  mfa_hint?: string
}

export interface CurrentUser {
  user_id: number
  username: string
  display_name: string
  roles: string[]
  permissions: string[]
  must_change_password?: boolean
  mfa_enabled?: boolean
}

export interface TotpStartResult {
  secret: string
  otpauth_url: string
}

export interface TotpConfirmResult {
  recovery_codes: string[]
}

/* -------------------------------------------------------------------- 资产 */

export interface Asset {
  asset_id: number
  asset_name: string
  asset_type: string
  protocol: string
  host: string
  port: number
  level: string
  os_type?: string
  tags?: string[]
  status: string
  /** 后端可能返回布尔值或 'unknown' 等字符串，界面按三态展示 */
  reachable?: boolean | string
  group_id?: number
  account_count?: number
  remark?: string
  create_time?: string
}

export interface AssetPayload {
  asset_name: string
  asset_type: string
  protocol: string
  host: string
  port?: number | null
  level: string
  os_type: string
  tags: string[]
  group_id: number
  remark: string
}

export interface AssetQuery extends PageQuery {
  keyword?: string
  level?: string
  protocol?: string
}

export interface AssetGroup {
  group_id: number
  group_name: string
  parent_id: number
  group_path?: string
  children?: AssetGroup[]
}

export interface Account {
  account_id: number
  account_name: string
  account_type?: string
  /** 凭据模式：managed（堡垒机托管）/ manual（不托管，连接时现场输入） */
  credential_mode?: 'managed' | 'manual'
  privileged: boolean
  privilege_mode?: string
  rotate_days: number
  has_password: boolean
  has_private_key: boolean
  status: string
}

export interface AccountPayload {
  account_name: string
  account_type: string
  password?: string
  private_key?: string
  privileged: boolean
  privilege_mode: string
  rotate_days: number
  /**
   * 凭据模式：managed（堡垒机托管，口令加密存储）/ manual（不托管，
   * 运维连接时现场输入）。
   *
   * 切到 manual 时后端会清空凭据材料；切回 managed 必须同时给出
   * 口令或私钥，否则账号建出来也连不上。
   */
  credential_mode?: 'managed' | 'manual'
}

/* ---------------------------------------------------------------- 用户角色 */

export interface UserItem {
  user_id: number
  username: string
  display_name: string
  email?: string
  phone?: string
  status: string
  roles: string[]
  must_change_password?: boolean
  /**
   * 口令有效期（天）。
   * 过期判断里 ≤0 视为不过期，但创建接口会把 ≤0 改写成 90，不会按「永不过期」落库。
   */
  password_expire_days?: number
  mfa_enabled?: boolean
  /** 第二因子类型：none / totp / sms / ukey */
  mfa_type?: 'none' | 'totp' | 'sms' | 'ukey'
  last_login_time?: string
  create_time?: string
  remark?: string
}

export interface UserPayload {
  username: string
  display_name: string
  password?: string
  email: string
  phone: string
  roles: string[]
  remark: string
  /**
   * 口令有效期（天）。
   * 创建时缺省或 ≤0 由服务端按 90 天写入；更新时仅 >0 会覆盖原值。
   */
  password_expire_days?: number
  /** 第二因子类型；创建时可选，更新时为空表示不修改 */
  mfa_type?: 'none' | 'totp' | 'sms' | 'ukey'
}

export interface UserQuery extends PageQuery {
  keyword?: string
  status?: string
  role_code?: string
}

/**
 * 管理员重置用户口令的返回。
 *
 * 服务端当前下发 temporary_password（形如 Bastion@ 加随机字符），
 * 并置 must_change_password。password 只兼容更早的字段名。
 */
export interface ResetUserPasswordResult {
  user_id?: number
  temporary_password?: string
  password?: string
  must_change_password?: boolean
  warning?: string
}

export interface Role {
  role_id: number
  role_code: string
  role_name: string
  description: string
  role_type?: string
  data_scope?: string
  is_builtin?: boolean
}

export interface Permission {
  perm_id?: number
  perm_code: string
  perm_name: string
  perm_type?: string
  module?: string
  sort_no?: number
  description?: string
}

export interface UserStats {
  total?: number
  enabled?: number
  disabled?: number
  locked?: number
  [key: string]: unknown
}

/* ---------------------------------------------------------------- 命令策略 */

export interface CommandPolicy {
  policy_id: number
  policy_name: string
  policy_type: string
  match_type: string
  pattern: string
  risk_level: string
  action: string
  priority: number
  enabled: boolean
  is_builtin: boolean
  timeout_seconds: number
  remark: string
}

export interface CommandPolicyPayload {
  policy_name?: string
  policy_type?: string
  match_type?: string
  pattern?: string
  risk_level?: string
  action?: string
  priority?: number
  enabled?: boolean
  timeout_seconds?: number
  remark?: string
}

export interface PolicyQuery extends PageQuery {
  keyword?: string
  action?: string
  risk_level?: string
  enabled?: string
}

export interface PolicyTestPayload {
  command: string
  asset_id?: number
  asset_level?: string
  user_id?: number
  protocol?: string
}

export interface PolicyTestMatchedRule {
  name: string
  pattern: string
  policy_id?: number
  match_type?: string
  action?: string
  priority?: number
  timeout_seconds?: number
}

export interface PolicyTestDecision {
  action: string
  risk_level: string
  reason: string
  matched: boolean
  matched_rule: PolicyTestMatchedRule | null
  timeout_seconds: number
}

export interface PolicyTestResult {
  command: string
  normalized: string
  command_name: string
  decision: PolicyTestDecision
}

export interface PolicyStats {
  enabled_total: number
  by_action: Record<string, number>
  engine_rules: number
  default_action: string
  whitelist_mode: boolean
}

/* ---------------------------------------------------------------- 工单授权 */

export interface TicketItemRef {
  asset_id: number
  asset_name: string
  account_id: number
  account_name: string
}

export interface TicketRecord {
  approver_name: string
  action: string
  comment: string
  acted_at: string
}

export interface Ticket {
  ticket_id: number
  ticket_no: string
  title: string
  reason: string
  applicant_id: number
  applicant_name: string
  status: string
  priority: string
  valid_from: string
  valid_to: string
  is_emergency: boolean
  submit_time: string
  items: TicketItemRef[]
  records?: TicketRecord[]
}

export interface TicketPayload {
  title: string
  reason: string
  ticket_type: string
  priority: string
  valid_hours: number
  items: Array<{ asset_id: number; account_id: number }>
  is_emergency: boolean
  emergency_note: string
}

export interface TicketQuery extends PageQuery {
  status?: string
  keyword?: string
}

export interface TicketStats {
  total: number
  by_status: Record<string, number>
  active_grants: number
}

export interface Grant {
  grant_id: number
  asset_id: number
  asset_name: string
  account_id: number
  account_name: string
  grant_source: string
  can_login: boolean
  can_file: boolean
  can_copy: boolean
  can_paste: boolean
  valid_to: string
  status: string
}

/* -------------------------------------------------------------------- 审计 */

export interface AuditSession {
  session_id: number
  session_no: string
  username: string
  display_name: string
  asset_name: string
  account_name: string
  protocol: string
  client_type: string
  client_ip: string
  start_time: string
  end_time: string
  duration_sec: number
  status: string
  end_reason: string
  command_count: number
  block_count: number
  record_status: string
  record_size: number
  record_digest: string
}

export interface SessionCommand {
  seq_no: number
  command_text: string
  policy_action: string
  risk_level: string
  exec_result: string
  matched_rule: string
  command_time: string
}

export interface AuditSessionDetail {
  session: AuditSession
  commands: SessionCommand[]
  record_play_url?: string
}

export interface AuditSessionStats {
  sessions: {
    total: number
    today: number
    active: number
    by_status: Record<string, number>
    blocked_commands: number
  }
  commands: {
    by_action: Record<string, number>
    by_risk: Record<string, number>
    deny: number
    high_risk: number
  }
}

export interface AuditCommand {
  command_id: number
  session_id: number
  seq_no: number
  command_text: string
  policy_action: string
  risk_level: string
  exec_result: string
  matched_rule?: string
  command_time: string
}

export interface AuditCommandQuery extends PageQuery {
  risk_level?: string
  policy_action?: string
  keyword?: string
  session_id?: number
  user_id?: number
}

export interface OperationLog {
  log_id: number
  module: string
  action: string
  target_type?: string
  target_name?: string
  target_id?: string | number
  after_value?: string
  operator_name_snap?: string
  result: string
  client_ip?: string
  operate_time: string
}

export interface OperationLogQuery extends PageQuery {
  module?: string
  action?: string
  result?: string
}

/* -------------------------------------------------------------------- 系统 */

export interface SystemInfo {
  version?: string
  /** 运行平台，如 linux/amd64 */
  platform?: string
  service?: string
  start_time?: string
  uptime_seconds?: number
  // 兼容旧字段：接口已精简，保留可选以免其它引用处报错
  runtime?: string
  go_version?: string
  database?: string
  os?: string
  arch?: string
  goroutines?: number
  cpu_count?: number
  crypto?: {
    provider?: string
    backend?: string
    description?: string
    desc?: string
  }
  /** 软件完整性校验状态 */
  integrity?: SystemIntegrity
  /** 各依赖服务的运行状态 */
  services?: ServiceStatus[]
  [key: string]: unknown
}

/** 软件完整性校验状态 */
export interface SystemIntegrity {
  /** 是否成功完成校验（无法读取程序文件时为 false） */
  checked: boolean
  /** 当前摘要与启动基线是否一致 */
  match?: boolean
  algorithm?: string
  digest?: string
  baseline?: string
  checked_at?: string
  /** 校验范围说明 */
  scope?: string
  /** 能力边界说明：只覆盖文件是否被替换，不含运行内存度量 */
  limitation?: string
  /** checked=false 时的原因 */
  reason?: string
}

/** 服务运行状态 */
export interface ServiceStatus {
  name: string
  key: string
  status: 'running' | 'down'
  detail?: string
}

/** 运行指标：仪表盘图表与系统信息页共用 */
export interface SystemMetrics {
  metrics: {
    /** 当前平台是否支持采集；false 时其余数值无意义 */
    available: boolean
    platform: string
    cpu_percent: number
    cpu_cores: number
    mem_total: number
    mem_used: number
    mem_percent: number
    net_rx_bps: number
    net_tx_bps: number
    proc_mem_alloc: number
    /**
     * 本次吞吐计算的采样间隔（秒）。
     * 为 0 表示这是首次采样、尚未建立基线，此时 CPU 与网络速率必然为 0，
     * 界面应显示"正在采集"而不是展示一个真实的 0%。
     */
    sample_interval: number
  }
  uptime_seconds: number
  collected_at: string
  online_users: number
  active_sessions: number
}

export interface SystemConfigItem {
  /** 后端实际返回 key / value，文档契约为 config_key / config_value，两者都兼容 */
  config_key?: string
  config_value?: string
  key?: string
  value?: string
  value_type: string
  category: string
  description: string
}

export interface CryptoSelfTestItem {
  name: string
  passed: boolean
  detail?: string
  cost_ms?: number
}

export interface CryptoSelfTestResult {
  healthy: boolean
  items: CryptoSelfTestItem[]
  duration_ms: number
}

/* --------------------------------------------------- UKey（智能密码钥匙）证书 */

/** 证书用途：device 设备证书 / client 客户端证书（用户 UKey）/ ca 签发机构证书 */
export type UKeyCertType = 'device' | 'client' | 'ca'

/**
 * 证书状态。
 *
 * 注意 expired 是**后端算出来的**结论（按 not_after 判定），不是前端用
 * 当前时间自己比出来的：前后端各算一遍，时钟不同步时就会出现
 * "界面显示有效、绑定却被拒"这种互相矛盾的现象。
 */
export type UKeyCertStatus = 'active' | 'revoked' | 'expired'

/** 证书来源：internal 内部 CA 签发 / external 外部 CA 签发（需先导入信任锚） */
export type UKeyCertSource = 'internal' | 'external'

/** 证书记录（km_cert） */
export interface UKeyCertRecord {
  cert_id: number
  tenant_id: number
  cert_type: UKeyCertType | string
  cert_name: string
  subject_dn: string
  issuer_dn: string
  serial_number: string
  fingerprint: string
  cert_pem?: string
  key_id?: string
  not_before?: string | null
  not_after?: string | null
  is_ca: boolean
  is_trusted_root: boolean
  chain_id?: number | null
  status: UKeyCertStatus | string
  revoke_time?: string | null
  revoke_reason?: string
  remark?: string
  owner_id?: number | null
  owner_type?: string
  cert_source: UKeyCertSource | string
  create_time: string
  update_time: string
  /** 归属用户的显示名（关联查询填充） */
  owner_name?: string
  /**
   * 该证书是"宽松登记"的：登记时服务端没有通过证书链校验
   * （ukey.require_internal_ca=false 时才会产生）。
   *
   * 后端由 remark 前缀推导后下发。界面上必须给醒目告警：这类证书意味着
   * 一张来源不明的证书已被接受，属于需要立刻处理的信任问题。
   * 老版本后端可能不返回该字段，故为可选。
   */
  unverified_chain?: boolean
  /**
   * 公钥算法（后端可能补的字段，命名尚未最终确定，故两种都按可选处理）。
   *
   * 取不到时前端从 cert_pem 自行解析兜底（见 utils/ukeyAgent 的
   * certAlgorithmLabel / detectCertAlgorithm），保证"证书算法"这一列永远有值。
   */
  key_algorithm?: string
  /** 签名算法（同上，可选） */
  signature_algorithm?: string
  /** 添加人的 user_id（后端已返回） */
  create_by?: number | null
  /**
   * 添加人用户名（后端查询时 LEFT JOIN sys_user 填充，非本表字段）。
   *
   * 证书（尤其是 CA 信任锚）是安全性极高的对象，"这张证书是谁加进来的"
   * 是事后追责的第一条线索；界面渲染时必须对空值兜底成占位符，
   * 不能把 undefined / null 直接显示出来。
   */
  creator_name?: string
}

export interface UKeyCertQuery extends PageQuery {
  cert_type?: string
  status?: string
  cert_source?: string
  /** 按归属用户过滤，用户管理页查"某人绑了哪些证书"用这个 */
  owner_id?: number
  keyword?: string
}

export interface UKeyRegisterPayload {
  user_id: number
  /** PEM 文本；中间件给的是 base64(DER)，需先转换 */
  cert_pem: string
  cert_source: UKeyCertSource
  cert_name?: string
  remark?: string
}

export interface UKeyIssuePayload {
  user_id: number
  csr_pem: string
  common_name?: string
  validity_days?: number
  email?: string
  remark?: string
}

/**
 * 内部两级 CA 的现状。
 *
 * 系统支持**两套并存的内部 CA**：SM2 链与 RSA 链，因此这里有同构的两组字段。
 * SM2 组是历史字段名（root_* / issuer_*），RSA 组以后端实际实现为准，
 * 在字段落地之前一律按**可选**处理：为空即表示该链尚未初始化，
 * 界面上显示"未初始化"而不是报错。
 */
export interface UKeyCAInfo {
  root_cert_id?: number
  root_subject_dn?: string
  root_not_after?: string
  root_pem?: string
  issuer_cert_id?: number
  issuer_subject_dn?: string
  issuer_not_after?: string
  issuer_pem?: string
  /** 本次调用是否真的生成了 CA；false 表示命中已有的（幂等返回） */
  created?: boolean

  // ---- RSA 链（后端新增中，字段名以最终实现为准） ----
  rsa_root_cert_id?: number
  rsa_root_subject_dn?: string
  rsa_root_not_after?: string
  rsa_root_pem?: string
  rsa_issuer_cert_id?: number
  rsa_issuer_subject_dn?: string
  rsa_issuer_not_after?: string
  rsa_issuer_pem?: string
}

export interface UKeyCAStatus {
  initialized: boolean
  internal: UKeyCAInfo | null
  /** 已登记的 CA 证书（内部两级 + 已导入的外部信任锚） */
  cas: UKeyCertRecord[]
  /** 配置项 ukey.enabled：关闭时任何 UKey 登录都会被拒 */
  enabled: boolean
  /**
   * 配置项 ukey.require_internal_ca。
   *
   * 为 true 时登记证书必须能链到内部签发 CA，也就是"外部 CA 签发的证书一律拒收"。
   * 界面据此提示用户：直接绑定读到的外部证书会被服务端拒绝，应该改用
   * 「用内部 CA 重新签发并写入 UKey」。后端早期版本可能不返回该字段，故为可选。
   */
  require_internal_ca?: boolean
}

/**
 * 「按公钥签发证书」的持有证明断言。
 *
 * 这条链路是为真机上的 UKey 准备的：中间件没有产生 P10(CSR) 的接口，
 * 因此只能把公钥取出来交给服务端签，再灌回 Key。安全性靠 challenge + 签名
 * 证明"调用方持有对应私钥"，详见后端 IssueUserCertByPublicKey 的说明。
 */
export interface UKeyIssueByPubkeyPayload {
  user_id: number
  challenge_id: string
  /** base64：65 字节未压缩点 04||X||Y / 64 字节 X||Y / SPKI DER 三者皆可 */
  public_key: string
  /** 原样回传服务端下发的 src_data */
  src_data: string
  /** base64 签名值（DER 或裸 r||s） */
  signature: string
  common_name?: string
  validity_days?: number
  email?: string
  remark?: string
}

/** 领取"按公钥签发证书"的持有证明挑战的请求体 */
export interface UKeyIssueChallengePayload {
  user_id: number
}

/** UKey 登录挑战（账密通过后下发，一次性、短时效、绑定来源 IP） */
export interface UKeyChallenge {
  challenge_id: string
  /** 服务端签名原文（bastion-ukey-login|<用户名>|<nonce>） */
  sign_data: string
  /**
   * 交给 UKey 中间件签名的字符串，即 base64(sign_data)。
   *
   * **必须原样使用**：中间件把它当数据本身签名、不做 base64 解码，
   * 服务端验签对象也是这串字符的字节。自行解码或重新编码必然验签失败。
   */
  src_data: string
  nonce: string
  mfa_type: string
  expire_time: string
  /** 剩余有效期（秒）；只有按 challenge_id 取回时后端才带上 */
  ttl_seconds?: number
}

export interface UKeyLoginAssertion {
  challenge_id: string
  /** 证书内容（PEM 文本） */
  cert: string
  /** 原样回传服务端下发的 src_data */
  src_data: string
  /** base64 签名值（DER 或裸 r||s 都支持） */
  signature: string
}

/** UKey 登录结果：与其它第二因子的返回结构保持一致，便于复用同一套落地逻辑 */
export interface UKeyLoginResult {
  login: LoginResult
  auth_method: string
}
