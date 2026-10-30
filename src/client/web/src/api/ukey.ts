/**
 * UKey（智能密码钥匙）证书认证相关接口。
 *
 * 按鉴权要求分三组，调用方不要混用：
 *
 *   - `fetchUKeyChallenge` / `ukeyLogin` 属于**登录链路**，无需令牌
 *     （auth: false）。但"无需认证"不等于"无前置条件"：登录挑战一律由
 *     `/auth/login` 在账密通过后下发（响应里已带 challenge_id / sign_data /
 *     src_data），客户端不应另找入口领挑战。
 *
 *   - **给某个用户绑定/查看/吊销他的 UKey 证书** —— 见 UKEY_BIND_PERMISSION。
 *     这类操作是"维护这个用户的认证方式"，与"修改用户"同权限；用户管理页的
 *     「证书链」按钮与 UkeyCertDialog 弹窗（含"重新签发并写入 UKey"）都归它。
 *
 *   - **证书链自身的台账**（内部 CA、外部信任锚、CA 证书列表）——
 *     见 UKEY_CHAIN_PERMISSION，归安全管理员。
 */
import { request } from './http'
import type {
  PageResult,
  UKeyCAInfo,
  UKeyCAStatus,
  UKeyCertQuery,
  UKeyCertRecord,
  UKeyChallenge,
  UKeyExpiringCertsResult,
  UKeyIssueByPubkeyPayload,
  UKeyIssuePayload,
  UKeyLoginAssertion,
  UKeyLoginResult,
  UKeyRegisterPayload,
} from './types'

/**
 * 「给用户绑定/查看/吊销 UKey 证书」所需的权限点。
 *
 * 用 system:user:update（与"修改用户"同权限），**不是** key:cert:manage：
 * 绑定证书本质上是修改该用户的认证方式，属用户维度的操作。系统管理员
 * （sys_admin）持有本权限但不持有 key 模块权限，因此用 key:cert:manage
 * 会让他在人员管理里看不到这个入口。
 */
export const UKEY_BIND_PERMISSION = 'system:user:update'

/**
 * 「证书链管理」页面所需的权限点（key:cert:manage，中文名即「证书链管理」）。
 *
 * 管的是 CA 台账本身（初始化内部 CA、导入外部 CA 信任锚、查看各条链），
 * 归安全管理员；与上面"给用户绑证书"是两个职责，务必区分。
 */
export const UKEY_CHAIN_PERMISSION = 'key:cert:manage'

/* ------------------------------------------------------------------ 登录链路 */

/**
 * 按 challenge_id 取回挑战原文（含剩余有效期）。
 *
 * 只读、不消费挑战、不需要任何口令：挑战 TTL 只有 120 秒，而客户端在弹窗
 * 打开、插 Key、输 PIN、重试签名之后可能已经超时，需要重新取回签名原文。
 *
 * 注意：**不存在**"用账密换挑战"的封装。这条路径原本有 `POST
 * /auth/ukey/challenge`（提交账号口令直接领挑战），它只依赖失败计数、
 * 不校验图形验证码，构成一条比正常登录更便宜的口令爆破路径，后端已把该
 * 分支整个移除。现在领取挑战的唯一正确方式是走 `/auth/login`（见
 * LoginView 的 mfa_required 分支：响应里已经带回全部挑战参数）。
 */
export function fetchUKeyChallenge(challengeId: string) {
  return request<UKeyChallenge>(
    `/auth/ukey/challenge?challenge_id=${encodeURIComponent(challengeId)}`,
    { auth: false },
  )
}

/**
 * 提交 UKey 断言，换取访问令牌。
 *
 * 三个字段都不做加工：cert 用中间件给的证书（PEM 或 base64(DER) 服务端都认）、
 * src_data 原样回传、signature 直接给中间件返回的 base64 签名值。
 */
export function ukeyLogin(assertion: UKeyLoginAssertion) {
  return request<UKeyLoginResult>('/auth/ukey/login', {
    method: 'POST',
    body: assertion,
    auth: false,
  })
}

/* -------------------------------------------------------------- 证书管理 */

/** 证书分页查询；用户管理页按 owner_id 过滤即可得到"某人的证书" */
export function listUKeyCerts(query: UKeyCertQuery = {}) {
  return request<PageResult<UKeyCertRecord>>('/ukey/certs', { params: { ...query } })
}

/**
 * 临期 / 已过期的用户 UKey 证书。
 *
 * 默认窗口与登录提示一致（约 30 天）。服务端在返回列表之前会把已经过了
 * not_after 的 active client 证书标成 expired，因此进入证书链页或点刷新
 * 就会顺带完成这次状态修正，前端不用再打别的接口。
 * 权限点是 key:cert:manage。
 */
export function listExpiringUKeyCerts(withinDays = 30) {
  return request<UKeyExpiringCertsResult>('/ukey/certs/expiring', {
    params: { within_days: withinDays },
  })
}

/**
 * 登记并绑定一张证书。
 *
 * 失败信息必须**原样展示**：后端已经把"未注册 / 已吊销 / 链不可信 /
 * 归属不符 / 未含客户端认证用途"区分开了，替换成"绑定失败"会把这些
 * 排障线索整个丢掉。
 */
export function registerUKeyCert(payload: UKeyRegisterPayload) {
  return request<UKeyCertRecord>('/ukey/certs', { method: 'POST', body: payload })
}

/** 用内部签发 CA 为用户签发证书（P10 / CSR 流程，不导出私钥） */
export function issueUKeyCert(payload: UKeyIssuePayload) {
  return request<UKeyCertRecord>('/ukey/certs/issue', { method: 'POST', body: payload })
}

/**
 * 领取「按公钥签发证书」的持有证明挑战。
 *
 * 这条路径专为 UKey 真机场景准备：中间件不提供生成 P10(CSR) 的接口，
 * 因此改成"客户端取公钥 + 对服务端挑战签名 → 服务端用内部 CA 签发 →
 * 客户端把新证书灌回 UKey"。
 *
 * 挑战是安全基础：没有它，"提交公钥即签发"等于允许任何人抄一个公钥换证书。
 * 服务端在签发时会校验该挑战属于 user_id 对应用户，并消费掉这个一次性挑战。
 */
export function fetchUKeyIssueChallenge(userId: number) {
  return request<UKeyChallenge>('/ukey/certs/issue-by-pubkey/challenge', {
    method: 'POST',
    body: { user_id: userId },
  })
}

/**
 * 提交公钥 + 持有证明，由内部签发 CA 签发证书并**直接登记绑定**到该用户。
 *
 * 返回值的 cert_pem 就是新证书，调用方应再调中间件 DownloadSingleCert 灌回 UKey。
 * 注意：签发成功即已绑定，**不需要**（也不应该）再调 registerUKeyCert 重复登记。
 */
export function issueUKeyCertByPubkey(payload: UKeyIssueByPubkeyPayload) {
  return request<UKeyCertRecord>('/ukey/certs/issue-by-pubkey', { method: 'POST', body: payload })
}

/** 吊销证书（软状态变更，记录保留可追溯） */
export function revokeUKeyCert(certId: number, reason: string) {
  return request<UKeyCertRecord>(`/ukey/certs/${certId}/revoke`, {
    method: 'POST',
    body: { reason },
  })
}

/** 查询内部 CA 现状与信任锚列表（未初始化时 initialized=false，不报错） */
export function getUKeyCA() {
  return request<UKeyCAStatus>('/ukey/ca')
}

/** 初始化内部两级 CA；幂等，已存在时返回现状 */
export function initUKeyCA() {
  return request<UKeyCAInfo>('/ukey/ca/init', { method: 'POST', body: {} })
}

/** 导入外部 CA 根证书作为信任锚（让外部 CA 签发的 UKey 也能登记） */
export function importExternalCA(pem: string) {
  return request<UKeyCertRecord>('/ukey/external-ca', { method: 'POST', body: { pem } })
}
