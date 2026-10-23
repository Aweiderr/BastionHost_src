/**
 * UKey（智能密码钥匙）本机中间件调用封装。
 *
 * 业务模型：用户在浏览器里访问堡垒机，UKey 硬件插在同一台 PC 上，
 * 浏览器 JS 只能通过厂商中间件提供的**本机 HTTP 服务**去驱动硬件
 * （枚举提供者、校验 PIN、读证书、用私钥签名）。这条链路是
 * 「浏览器 → 127.0.0.1 中间件」，与堡垒机后端没有关系。
 *
 * 真机实测得到的几条硬约束（改动前请先读一遍，否则会退化成一类
 * "本机明明插着 Key 却提示中间件未安装"的假故障）：
 *
 * 1. **必须是 application/x-www-form-urlencoded**，body 为 `json=<原始JSON>`。
 *    这是浏览器的「简单请求」，不会触发 CORS 预检。若改用 application/json，
 *    浏览器会先发 OPTIONS 预检，而中间件的预检响应只带
 *    Access-Control-Allow-Origin，缺少 Allow-Methods / Allow-Headers，
 *    请求会被浏览器直接拦下 —— 表现为 onerror，和"中间件没装"长得一样。
 * 2. body 里的 JSON **不做 URL 编码**，中间件按原始字符串解析。
 * 3. HTTPS 页面必须走 https://127.0.0.1:8199。走 http://127.0.0.1:8198
 *    会被混合内容（Mixed Content）策略拦掉，且拦在浏览器内部，
 *    页面拿不到任何有区分度的错误。
 * 4. 用异步 XHR：同步 XHR 已被浏览器废弃，在主线程上还会阻塞 UI。
 */

/** 默认 CSP 名与容器名：界面上的输入框可覆盖，这两个只是开箱默认值。 */
export const DEFAULT_CSP_NAME = 'Longmai mToken GM3000 CSP V1.1'
export const DEFAULT_CONTAINER = 'CCITCont1'

/**
 * 中间件支持的签名算法常量（真机实测，来自 zwPlugin.js）。
 *
 * 只有三个，且**没有 RSA-SHA256**：
 *
 *   ALG_SIGN_RsaWithMD5  = 1   —— 不使用（MD5 已不可接受）
 *   ALG_SIGN_RsaWithSHA1 = 2   —— RSA 证书只能用这个（硬件能力上限）
 *   ALG_SIGN_SM2WithSM3  = 3   —— SM2 证书用这个
 *
 * AlgType 传错会得到一个语法合法但服务端验不过的签名，而中间件**不会报错**，
 * 属于最难排查的一类问题 —— 因此算法必须由证书推导（见 algTypeForCert），
 * 不允许各调用点自己写常量。
 */
export const ALG_SIGN_RSA_WITH_SHA1 = 2
export const ALG_SIGN_SM2_WITH_SM3 = 3

/**
 * 取证书时使用的 iKeyType。
 *
 * 实测：同一个容器里 iKeyType=1 与 iKeyType=2 各能取到一张证书，
 * 两张的 extendedKeyUsage 都含 clientAuth，**但只有 =2 那张的公钥
 * 与中间件实际用于签名的私钥配对**。用 =1 那张提交登录，服务端验签必然失败。
 * 因此这个值不允许在界面上开放修改，只作为常量。
 */
export const IKEY_TYPE_SIGN = 2

/** 中间件调用超时：正常操作在数百毫秒内返回，超过这个时间基本是卡在等 PIN 输入或中间件无响应 */
const DEFAULT_TIMEOUT_MS = 15000

/** 中间件响应信封 */
interface UKeyRawResponse {
  errcode?: number
  errMsg?: string
  fucName?: string
  resultMSG?: string | number
}

/** 调用方需要区分的失败原因（界面据此给不同提示与不同的重试动作） */
export type UKeyErrorCode =
  /** 本机没装中间件/服务没起：当前访问地址与中间件地址不匹配也会落到这里 */
  | 'NOT_INSTALLED'
  /** 中间件在，但 checkComSetup 返回未就绪（通常是 Key 没插好/驱动异常） */
  | 'NOT_READY'
  /** 请求超时 */
  | 'TIMEOUT'
  /** PIN 错误 */
  | 'PIN_ERROR'
  /** 容器里没有可用的签名证书 */
  | 'NO_CERT'
  /** 中间件返回了非 0 的 errcode，message 里是它的原文 */
  | 'MIDDLEWARE'
  /** 响应不是合法 JSON（被网关/杀软改写等） */
  | 'BAD_RESPONSE'

/** UKey 中间件调用失败；message 一律是可直接展示给用户的中文 */
export class UKeyAgentError extends Error {
  readonly code: UKeyErrorCode

  constructor(code: UKeyErrorCode, message: string) {
    super(message)
    this.name = 'UKeyAgentError'
    this.code = code
  }
}

/** 中间件服务地址：按页面协议选择，理由见文件头第 3 条 */
export function middlewareUrl(): string {
  const isHttps = typeof location !== 'undefined' && location.protocol === 'https:'
  return isHttps ? 'https://127.0.0.1:8199' : 'http://127.0.0.1:8198'
}

/** 是否在 HTTPS 页面下访问（用于界面提示"需要 HTTPS 版中间件"） */
export function isSecurePage(): boolean {
  return typeof location !== 'undefined' && location.protocol === 'https:'
}

/** 发起一次中间件调用（Promise 化），失败时抛 UKeyAgentError */
function callMiddleware(
  payload: Record<string, unknown>,
  timeoutMs = DEFAULT_TIMEOUT_MS,
): Promise<UKeyRawResponse> {
  return new Promise((resolve, reject) => {
    let xhr: XMLHttpRequest
    try {
      xhr = new XMLHttpRequest()
    } catch {
      reject(new UKeyAgentError('NOT_INSTALLED', '当前浏览器不支持 XMLHttpRequest，无法调用 UKey 中间件'))
      return
    }

    let settled = false
    const timer = window.setTimeout(() => {
      if (settled) return
      settled = true
      // 超时必须 abort：不中断的话回调仍可能在用户点了"取消"之后再回来，
      // 把一次已作废的签名结果抛给上层
      try {
        xhr.abort()
      } catch {
        /* 忽略 */
      }
      reject(
        new UKeyAgentError(
          'TIMEOUT',
          `UKey 中间件 ${Math.round(timeoutMs / 1000)} 秒内没有响应，请确认中间件正在运行并已插入 UKey`,
        ),
      )
    }, timeoutMs)

    const done = (fn: () => void): void => {
      if (settled) return
      settled = true
      window.clearTimeout(timer)
      fn()
    }

    xhr.open('POST', middlewareUrl(), true)
    // 见文件头第 1 条：必须是 form-urlencoded，且这里**不能**再设别的自定义头，
    // 任何一个自定义头都会让浏览器重新判定为"非简单请求"而触发预检
    xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded')

    xhr.onload = () =>
      done(() => {
        if (xhr.status !== 200) {
          reject(new UKeyAgentError('MIDDLEWARE', `UKey 中间件返回 HTTP ${xhr.status}，请重启本机中间件服务`))
          return
        }
        try {
          resolve(JSON.parse(xhr.responseText) as UKeyRawResponse)
        } catch {
          reject(new UKeyAgentError('BAD_RESPONSE', 'UKey 中间件返回的内容不是合法 JSON，可能被安全软件改写'))
        }
      })

    xhr.onerror = () =>
      done(() => {
        // 这个分支同时覆盖"没装中间件""服务没启动""CORS 被拦""混合内容被拦"
        // 四种情况，浏览器出于安全考虑不告诉 JS 具体是哪种，因此提示里要把
        // 排查方向一并给出，并让用户跑一次连通性自检
        reject(
          new UKeyAgentError(
            'NOT_INSTALLED',
            isSecurePage()
              ? '无法连接本机 UKey 中间件：请确认已安装中间件并启动服务，HTTPS 页面需要 8199 端口的中间件；可先打开「UKey 连通性自检」排查'
              : '无法连接本机 UKey 中间件：请确认已安装中间件并启动服务；可先打开「UKey 连通性自检」排查',
          ),
        )
      })

    xhr.ontimeout = () =>
      done(() => reject(new UKeyAgentError('TIMEOUT', 'UKey 中间件请求超时')))

    try {
      // 见文件头第 2 条：JSON 不做 URL 编码，原样拼在 json= 之后
      xhr.send('json=' + JSON.stringify(payload))
    } catch (err) {
      done(() =>
        reject(
          new UKeyAgentError(
            'NOT_INSTALLED',
            `无法向 UKey 中间件发送请求：${err instanceof Error ? err.message : String(err)}`,
          ),
        ),
      )
    }
  })
}

/** resultMSG 取字符串：中间件在成功时可能给出数字 0（如 checkComSetup） */
function resultText(res: UKeyRawResponse): string {
  const v = res.resultMSG
  if (v === undefined || v === null) return ''
  return String(v).trim()
}

/** 中间件业务失败：把 errMsg 原文带上，它是排障的第一手信息 */
function middlewareError(res: UKeyRawResponse, fallback: string): UKeyAgentError {
  const detail = (res.errMsg || '').trim()
  return new UKeyAgentError(
    'MIDDLEWARE',
    detail
      ? `${fallback}：${detail}（中间件 errcode=${res.errcode ?? '未知'}）`
      : `${fallback}（中间件 errcode=${res.errcode ?? '未知'}）`,
  )
}

/**
 * 检测中间件是否已安装且可用。
 *
 * 注意判定条件是 `resultMSG === 0`（数字），不是 `errcode === 0`：
 * 真机上未就绪时 errcode 仍可能为 0，只有 resultMSG 能区分。
 */
export async function checkComSetup(): Promise<void> {
  const res = await callMiddleware({ method: 'checkComSetup' })
  if (res.errcode !== 0) throw middlewareError(res, 'UKey 中间件调用失败')
  if (String(res.resultMSG) !== '0') {
    throw new UKeyAgentError(
      'NOT_READY',
      `UKey 中间件未就绪（resultMSG=${res.resultMSG ?? '空'}）：请检查 UKey 是否插好、驱动与证书容器是否正常`,
    )
  }
}

/** 枚举本机 CSP 提供者，返回名字数组（中间件用 # 分隔） */
export async function enumProviders(): Promise<string[]> {
  const res = await callMiddleware({ method: 'EnumProvider', iType: 1 })
  if (res.errcode !== 0) throw middlewareError(res, '枚举 CSP 提供者失败')
  return resultText(res)
    .split('#')
    .map((s) => s.trim())
    .filter(Boolean)
}

/** 初始化指定 CSP 提供者与容器；后续所有操作都作用于这个容器 */
export async function initProvider(provName: string, contName: string): Promise<void> {
  const res = await callMiddleware({ method: 'InitProvider', provName, contName })
  if (res.errcode !== 0 || String(res.resultMSG) !== '0') {
    throw middlewareError(res, '初始化 UKey 提供者失败')
  }
}

/** 校验 PIN；PIN 错误会被单独标记，界面提示"请重新输入 PIN"而不是笼统失败 */
export async function verifyPin(pin: string): Promise<void> {
  const res = await callMiddleware({ method: 'VerifyPin', usrPin: pin })
  if (res.errcode !== 0 || String(res.resultMSG) !== '0') {
    const detail = (res.errMsg || '').trim()
    // 中间件不保证在 PIN 错误时给出稳定错误码，因此以错误文案判定为主
    if (/pin|口令|密码/i.test(detail) || res.errcode !== 0) {
      throw new UKeyAgentError('PIN_ERROR', detail || 'PIN 校验失败，请确认 PIN 是否正确')
    }
    throw middlewareError(res, 'PIN 校验失败')
  }
}

/**
 * 读取容器中持有**签名私钥**的那张证书，返回中间件给的 **base64(DER)**。
 *
 * 刻意不在这里转 PEM：登录链路要求把证书**原样**交给服务端（servcie 侧
 * ParseClientCert 能同时吃 PEM 与 base64），而登记绑定时才需要 PEM。
 * 统一在这一层做转换反而会把"原样回传"这条契约抹掉。
 */
export async function getCertByContName(
  containerName: string,
  iKeyType: number = IKEY_TYPE_SIGN,
): Promise<string> {
  const res = await callMiddleware({
    method: 'getCertByContName',
    containerName,
    iKeyType,
  })
  const b64 = resultText(res)
  if (res.errcode !== 0 || !b64 || b64 === '0') {
    throw new UKeyAgentError(
      'NO_CERT',
      (res.errMsg || '').trim() ||
        `在容器「${containerName}」中没有找到可用于签名的证书（iKeyType=${iKeyType}）：请确认 UKey 已插入且证书容器名填写正确`,
    )
  }
  return b64
}

/**
 * 按算法给出中间件签名用的 AlgType。
 *
 * 中间件只有三个算法常量：`ALG_SIGN_RsaWithMD5=1` / `ALG_SIGN_RsaWithSHA1=2` /
 * `ALG_SIGN_SM2WithSM3=3`，**没有 RSA-SHA256**。因此：
 *
 *   - SM2 证书 → 3（SM2WithSM3，与国密标准一致）
 *   - RSA 证书 → 2（RSAWithSHA1）。这是硬件能力上限下的唯一选择，
 *     界面上必须把"RSA 用 SHA-1"作为弱算法告警如实告出
 *   - 两者都识别不出 → **明确报错**，绝不默认用 3 硬签：
 *     拿不准算法还去签名，只会得到一个服务端验不过的签名值，
 *     而报错信息是"验签失败"，比"识别不出算法"难排查得多
 */
export function algTypeForCert(certContent: string): number {
  const alg = detectCertAlgorithm(certContent)
  if (alg === 'sm2') return ALG_SIGN_SM2_WITH_SM3
  if (alg === 'rsa') return ALG_SIGN_RSA_WITH_SHA1
  throw new UKeyAgentError(
    'NO_CERT',
    '无法从证书中识别公钥算法（既不是 SM2 也不是 RSA），无法确定签名算法；请确认 UKey 中的证书格式受支持',
  )
}

/**
 * 用 UKey 私钥对 srcData 签名，返回 base64 签名值。
 *
 * srcData 必须是服务端下发的 src_data **原样**：真机中间件把它当数据本身
 * 签名、不做 base64 解码，服务端验签的对象也正是这串 base64 字符的字节。
 * 客户端自行解码或重新编码都会导致验签失败。
 *
 * **算法必须由调用方从证书推导**（见 algTypeForCert），不要在这里给默认值：
 * 默认值会让"调用方忘了传证书"退化成"用 SM2 算法签了一张 RSA 证书的挑战"，
 * 而中间件不会报错，只有服务端验签会失败。
 */
export async function sign(srcData: string, algType: number): Promise<string> {
  const res = await callMiddleware({ method: 'sign', srcData, AlgType: algType })
  const sig = resultText(res)
  if (res.errcode !== 0 || !sig || sig === '0') {
    throw middlewareError(res, 'UKey 签名失败')
  }
  return sig
}

/**
 * 把一张新证书写回 UKey（中间件 DownloadSingleCert）。
 *
 * 用途：UKey 里原有的证书可能是外部 CA 签发的、而堡垒机拿不到其根/中间
 * 证书（Windows 存储里没有、证书也没写 AIA 扩展），因此"直接绑定读到的
 * 证书"必然被链校验拒绝。正确做法是由内部 CA 用 Key 里的公钥重新签一张，
 * 再用这个接口灌回 Key。
 *
 * packdata 是 **base64(证书 DER)**，不是 PEM —— 中间件按二进制收。
 * 实测：该操作只替换 iKeyType=2，**iKeyType=1 上的原证书保持不变**，
 * 因此不会破坏 Key 里其他系统的凭据（界面上要把这句告诉用户）。
 */
export async function downloadSingleCert(
  certBase64Der: string,
  transactionId: string,
): Promise<void> {
  const packdata = stripWhitespace(certBase64Der)
  if (!packdata) {
    throw new UKeyAgentError('NO_CERT', '待写入的证书内容为空')
  }
  // 事务号只是中间件侧的标识，不影响结果；用时间戳保证同一会话内不重号
  const tid = transactionId.trim() || `bastion-${Date.now()}`
  const res = await callMiddleware({
    method: 'DownloadSingleCert',
    packdata,
    transactionId: tid,
  })
  if (res.errcode !== 0 || String(res.resultMSG) !== '0') {
    const detail = (res.errMsg || '').trim()
    // resultMSG 非 0 常见于"公钥/容器不匹配"或"PIN 已失效"，
    // 把中间件原文带上，否则只能笼统说"写卡失败"
    throw new UKeyAgentError(
      'MIDDLEWARE',
      detail
        ? `写入 UKey 失败：${detail}（resultMSG=${res.resultMSG ?? '空'}）`
        : `写入 UKey 失败（中间件 errcode=${res.errcode ?? '未知'}，resultMSG=${res.resultMSG ?? '空'}）；` +
          '请确认 UKey 仍插在本机且 PIN 未失效，然后重试写入',
    )
  }
}

/** 一次完整的读证书调用涉及的参数 */
export interface UKeyAgentOptions {
  /** PIN码；必填，空值会直接失败而不是"悄悄跳过校验" */
  pin?: string
  provName?: string
  contName?: string
}

/**
 * 按固定顺序跑一遍「初始化 → PIN → 读证书」，返回 base64(DER) 证书。
 *
 * PIN 为空时**直接报错而不跳过**：真机中间件在未校验 PIN 的情况下
 * 读证书/签名会返回含糊的失败，用户看到的是"签名失败"而不是"你忘了输 PIN"。
 * 这里把边界条件摆到明面上，提示才有指向性。
 */
export async function readCertificate(options: UKeyAgentOptions = {}): Promise<string> {
  const provName = options.provName?.trim() || DEFAULT_CSP_NAME
  const contName = options.contName?.trim() || DEFAULT_CONTAINER
  const pin = options.pin?.trim() || ''
  if (!pin) {
    throw new UKeyAgentError('PIN_ERROR', '请输入 UKey 的 PIN 码后再读取证书')
  }
  await checkComSetup()
  await initProvider(provName, contName)
  await verifyPin(pin)
  return getCertByContName(contName)
}

/* ------------------------------------------------------------------ 编码工具 */

/** 去掉所有空白：中间件返回的 base64 常带换行，atob 遇到换行会抛异常 */
function stripWhitespace(input: string): string {
  return input.replace(/\s+/g, '')
}

/** 是否为 PEM 文本 */
export function isPem(input: string): boolean {
  return /-----BEGIN [^-]+-----/.test(input)
}

/** base64(DER) → PEM 文本（每行 64 字符，末尾换行） */
export function base64ToPem(b64: string): string {
  const clean = stripWhitespace(b64)
  if (!clean) throw new UKeyAgentError('NO_CERT', '证书内容为空')
  // 先用 atob 解一遍：既能把二进制串取出用于折行，也顺带验证它确实是合法 base64
  let binary: string
  try {
    binary = window.atob(clean)
  } catch {
    throw new UKeyAgentError('NO_CERT', '证书内容不是合法的 base64 数据，请确认复制完整')
  }
  if (binary.length < 64) {
    throw new UKeyAgentError('NO_CERT', '证书长度明显不足，内容可能被截断')
  }
  const lines = clean.match(/.{1,64}/g) ?? []
  return `-----BEGIN CERTIFICATE-----\n${lines.join('\n')}\n-----END CERTIFICATE-----\n`
}

/**
 * 把用户在文本框里粘贴的内容归一化成 PEM。
 *
 * 中间件给的是 base64(DER)，而登记接口 cert_pem 期望 PEM 文本；
 * 用户从证书文件里复制的可能是 PEM，也可能是去掉了首尾行的 base64。
 * 这里三种都接住，避免"明明粘对了却提示格式错误"。
 */
export function normalizeCertInput(input: string): string {
  const raw = input.trim()
  if (!raw) throw new UKeyAgentError('NO_CERT', '请粘贴证书内容')
  if (isPem(raw)) {
    // 已经是 PEM：只做行宽归一，保证每行 64 字符，避免某些解析器对超长行挑剔
    const body = stripWhitespace(
      raw
        .replace(/-----BEGIN [^-]+-----/g, '')
        .replace(/-----END [^-]+-----/g, ''),
    )
    if (!body) throw new UKeyAgentError('NO_CERT', 'PEM 内容为空')
    const lines = body.match(/.{1,64}/g) ?? []
    return `-----BEGIN CERTIFICATE-----\n${lines.join('\n')}\n-----END CERTIFICATE-----\n`
  }
  return base64ToPem(raw)
}

/**
 * 从 PEM/DER-base64 里取主体 CN，用于自动填充证书名称。
 *
 * 这里**刻意不做完整的 ASN.1 解析**：证书解析与合法性判定是服务端的职责
 * （它会给出"无法解析 / 用途不含客户端认证 / 不在有效期"这类准确结论）。
 * 前端只把 DER 当字节流扫一遍 CN= 的常见写法，取不到就返回空串，
 * 由用户自己填 —— 用一个猜错的名字覆盖真实的证书信息是更糟的结果。
 *
 * 实现上逐字节处理而不是对字符串直接跑正则：DER 里混有任意字节，
 * 正则的元字符（如 $ ^ . *）可能恰好出现在 CN 值里造成误匹配，
 * 而逐字节只接受可打印 ASCII，边界清晰。
 */
export function guessCommonName(cert: string): string {
  try {
    const pem = isPem(cert) ? cert : base64ToPem(cert)
    const der = window.atob(
      pem
        .replace(/-----BEGIN [^-]+-----/g, '')
        .replace(/-----END [^-]+-----/g, '')
        .replace(/\s+/g, ''),
    )
    const isPrintable = (code: number): boolean => code >= 0x20 && code <= 0x7e
    for (let i = 0; i + 3 < der.length; i++) {
      if (der.charCodeAt(i) !== 0x43 || der.charCodeAt(i + 1) !== 0x4e || der.charCodeAt(i + 2) !== 0x3d) {
        continue // 不是 "CN="
      }
      let end = i + 3
      while (end < der.length && isPrintable(der.charCodeAt(end)) && der[end] !== ',' && der[end] !== '/') {
        end++
      }
      const value = der.slice(i + 3, end).trim()
      if (value) return value
    }
    return ''
  } catch {
    return ''
  }
}

/**
 * 从 PEM 或 base64(DER) 取出 **base64(DER)**，用于交给中间件灌卡。
 *
 * DownloadSingleCert 的 packdata 要的是 base64 的 DER，而服务端签发接口
 * 返回的是 PEM 文本，因此这里做一次去头尾 + 去换行。
 */
export function certToBase64Der(cert: string): string {
  const raw = cert.trim()
  if (!raw) throw new UKeyAgentError('NO_CERT', '证书内容为空')
  const b64 = isPem(raw)
    ? stripWhitespace(
        raw
          .replace(/-----BEGIN [^-]+-----/g, '')
          .replace(/-----END [^-]+-----/g, ''),
      )
    : stripWhitespace(raw)
  if (!b64) throw new UKeyAgentError('NO_CERT', '证书内容为空')
  try {
    window.atob(b64)
  } catch {
    throw new UKeyAgentError('NO_CERT', '待写入的证书不是合法的 base64 数据')
  }
  return b64
}

/** base64 → 二进制字符串（每字符一字节），失败时抛可读错误 */
function base64ToBinary(b64: string, what: string): string {
  try {
    return window.atob(stripWhitespace(b64))
  } catch {
    throw new UKeyAgentError('NO_CERT', `${what}不是合法的 base64 数据，请确认内容完整`)
  }
}

/** 二进制字符串 → base64（分块转换，避免超长字符串一次性展开成巨大数组） */
function binaryToBase64(binary: string): string {
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i) & 0xff
  let out = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    const slice = bytes.subarray(i, i + CHUNK)
    let s = ''
    for (let j = 0; j < slice.length; j++) s += String.fromCharCode(slice[j])
    out += window.btoa(s)
  }
  return out
}

/**
 * Uint8Array → base64。
 *
 * 用途：把**二进制 DER 证书文件**转成 base64 文本，好交给 normalizeCertInput
 * （它接受 PEM 与裸 base64 两种）。必须走这条二进制路径 —— 用 readAsText 读
 * DER 会按 UTF-8 解码出 U+FFFD，原始字节再也回不来，而且**可能"看似成功"地
 * 提交一个错误的字节串**，比直接报错危险得多。
 */
export function bytesToBase64(bytes: Uint8Array): string {
  return binaryToBase64(arrayToBinary(bytes))
}

/** Uint8Array → 每字符一字节的二进制串（分块，避免超长参数一次性展开） */
function arrayToBinary(bytes: Uint8Array): string {
  let binary = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    binary += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
  }
  return binary
}

/** SM2 曲线 OID：1.2.156.10197.1.301 的 DER 编码（06 08 2A 81 1C CF 55 01 82 2D） */
const SM2_CURVE_OID = [0x2a, 0x81, 0x1c, 0xcf, 0x55, 0x01, 0x82, 0x2d]

/** RSA 加密 OID：1.2.840.113549.1.1.1 的 DER 编码（06 09 2A 86 48 86 F7 0D 01 01 01） */
const RSA_OID = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01]

/** 公钥算法 */
export type UKeyKeyAlgorithm = 'sm2' | 'rsa' | 'unknown'

/** 在二进制串里查找字节序列，返回首个匹配的起始下标；未命中返回 -1 */
function indexOfBytes(haystack: string, needle: number[], from = 0): number {
  for (let i = Math.max(from, 0); i + needle.length <= haystack.length; i++) {
    let hit = true
    for (let j = 0; j < needle.length; j++) {
      if (haystack.charCodeAt(i + j) !== needle[j]) {
        hit = false
        break
      }
    }
    if (hit) return i
  }
  return -1
}

/** 证书（PEM 或 base64(DER)）→ 二进制字符串（每字符一字节） */
function certDerBinary(cert: string): string {
  const raw = cert.trim()
  if (!raw) throw new UKeyAgentError('NO_CERT', '证书内容为空')
  return isPem(raw)
    ? base64ToBinary(
        raw
          .replace(/-----BEGIN [^-]+-----/g, '')
          .replace(/-----END [^-]+-----/g, ''),
        '证书',
      )
    : base64ToBinary(raw, '证书')
}

/**
 * 从证书中识别公钥算法（SM2 / RSA）。
 *
 * 为什么必须识别：UKey 中间件的签名算法常量只有三个
 * （`ALG_SIGN_RsaWithMD5=1` / `ALG_SIGN_RsaWithSHA1=2` / `ALG_SIGN_SM2WithSM3=3`），
 * **没有 RSA-SHA256**。RSA 类型的 UKey 只能用 AlgType=2 签名；对 RSA 证书硬传 3
 * 会签出一串服务端永远验不过的数据，而错误信息只有笼统的"验签失败"，
 * 排查会一路往"证书不对"的方向找。
 *
 * 识别方式与 extractSm2PublicKey 一致：在 DER 字节流里扫算法 OID。
 * 两个 OID 都出现时取靠前的那个 —— SubjectPublicKeyInfo 在 TBSCertificate 内，
 * 而签名字段里还会再出现一次 OID，靠前的更贴近公钥本身。
 */
export function detectCertAlgorithm(cert: string): UKeyKeyAlgorithm {
  let der: string
  try {
    der = certDerBinary(cert)
  } catch {
    // 证书本身不可解析时返回 unknown 而不是抛错：算法只用于展示与选 AlgType，
    // 真正不可用的证书会由后面的签名/提交环节给出准确错误
    return 'unknown'
  }
  const sm2At = indexOfBytes(der, SM2_CURVE_OID)
  const rsaAt = indexOfBytes(der, RSA_OID)
  if (sm2At < 0 && rsaAt < 0) return 'unknown'
  if (sm2At < 0) return 'rsa'
  if (rsaAt < 0) return 'sm2'
  return sm2At <= rsaAt ? 'sm2' : 'rsa'
}

/** 便于界面展示的算法文案；取不到时返回"未知"而不是空 */
export function certAlgorithmLabel(cert: string): string {
  const alg = detectCertAlgorithm(cert)
  if (alg === 'sm2') return 'SM2'
  if (alg === 'rsa') return 'RSA'
  return '未知'
}

/**
 * 证书是否为 RSA 公钥。
 *
 * 界面据此给出**弱算法告警**：受 UKey 中间件算法能力限制，RSA 客户端签名
 * 只能用 SHA-1 —— 这是必须如实告知用户的安全事实，不能藏在文档里。
 */
export function isRsaCertificate(cert: string): boolean {
  return detectCertAlgorithm(cert) === 'rsa'
}

/**
 * 证书算法展示文案（供界面直接用）。
 *
 * 优先用后端字段（`key_algorithm` / `signature_algorithm`，命名以最终实现为准，
 * 故按可选处理），取不到时从 `cert_pem` 自行解析兜底 —— 这样"证书算法"这一列
 * 永远有值，不会因为后端字段还没落地而空着。
 */
export function certAlgorithmText(cert: {
  cert_pem?: string
  key_algorithm?: string
  signature_algorithm?: string
}): string {
  const fromApi = cert.key_algorithm || cert.signature_algorithm
  if (fromApi) return fromApi.toUpperCase()
  if (cert.cert_pem) return certAlgorithmLabel(cert.cert_pem)
  return '未知'
}

/**
 * 证书记录是否为 RSA（弱算法告警用）。
 *
 * 与 certAlgorithmText 同源：否则会出现"列里显示 RSA、页面级告警却不出现"
 * 这种自相矛盾的展示。
 */
export function isRsaCertRecord(cert: {
  cert_pem?: string
  key_algorithm?: string
  signature_algorithm?: string
}): boolean {
  return certAlgorithmText(cert) === 'RSA'
}

/**
 * 从证书（PEM 或 base64(DER)）中解析出 SM2 公钥，返回 **base64(65 字节 04||X||Y)**。
 *
 * 为什么不引 ASN.1 库：这里只需要从 SubjectPublicKeyInfo 里定位一段固定结构，
 * 引入一个 ASN.1 依赖（并随前端一起打包）为了几十行代码不划算。
 *
 * 定位方式（SM2 证书的 SPKI 结构固定）：
 *   `06 08 2A 81 1C CF 55 01 82 2D`（SM2 曲线 OID）
 *   → 其后（可能隔一个 `05 00` 的 NULL 参数）是 `03 42 00 04`（BIT STRING、长 0x42、
 *     unused=0、未压缩点前缀 0x04）
 *   → 再往后 64 字节即 X||Y。
 *
 * 之所以在字节层扫而不是按固定偏移取：不同签发工具的 AlgorithmIdentifier 参数
 * 有无 NULL、字段顺序略有差异，按偏移取会在某些证书上静默取错字节，
 * 而"取错公钥"表现为"签发成功但登录验签失败"，最难排查。
 *
 * 服务端接受 65 字节未压缩点 / 64 字节 X||Y / SPKI DER 三种编码，这里给 65 字节。
 */
export function extractSm2PublicKey(cert: string): string {
  const raw = cert.trim()
  if (!raw) throw new UKeyAgentError('NO_CERT', '证书内容为空')
  const der = isPem(raw)
    ? base64ToBinary(
        raw
          .replace(/-----BEGIN [^-]+-----/g, '')
          .replace(/-----END [^-]+-----/g, ''),
        '证书',
      )
    : base64ToBinary(raw, '证书')

  // 找 SM2 曲线 OID
  let oidAt = -1
  for (let i = 0; i + SM2_CURVE_OID.length <= der.length; i++) {
    let hit = true
    for (let j = 0; j < SM2_CURVE_OID.length; j++) {
      if (der.charCodeAt(i + j) !== SM2_CURVE_OID[j]) {
        hit = false
        break
      }
    }
    if (hit) {
      oidAt = i
      break
    }
  }
  if (oidAt < 0) {
    throw new UKeyAgentError(
      'NO_CERT',
      '无法从该证书中解析出 SM2 公钥（证书里没有 SM2 曲线标识）。' +
        '若这是 RSA 证书，请改用对应的 RSA 内部 CA 流程（本函数只用于 SM2 公钥）',
    )
  }

  // 从 OID 之后找 BIT STRING 里的未压缩点前缀 0x04
  const searchStart = oidAt + SM2_CURVE_OID.length
  let keyAt = -1
  for (let i = searchStart; i + 4 <= der.length; i++) {
    if (
      der.charCodeAt(i) === 0x03 &&
      der.charCodeAt(i + 1) === 0x42 &&
      der.charCodeAt(i + 2) === 0x00 &&
      der.charCodeAt(i + 3) === 0x04
    ) {
      keyAt = i + 3
      break
    }
  }
  if (keyAt < 0 || keyAt + 65 > der.length) {
    throw new UKeyAgentError(
      'NO_CERT',
      '找到了 SM2 曲线标识，但未能定位到 65 字节的公钥位串；证书格式可能不被支持',
    )
  }

  // 65 字节：0x04 || X(32) || Y(32)，直接 base64 交给服务端
  return binaryToBase64(der.slice(keyAt, keyAt + 65))
}

/**
 * 从证书中取出 **SubjectPublicKeyInfo 的完整 DER**，返回 base64。
 *
 * 为什么需要它：`issue-by-pubkey` 需要"公钥"，而服务端接受 SPKI DER / 65 字节
 * 未压缩点 / 64 字节 X||Y 三种编码。SM2 有 extractSm2PublicKey 直接给未压缩点；
 * **RSA 没有等价的小结构**（模数长度不固定），把它拼成 65 字节点是不可能的，
 * 因此这里统一给出 SPKI DER —— 它对两种算法都成立，也是"整张证书的 DER"
 * 的子集（整张证书不是 SPKI，直接提交证书 DER 服务端会解析失败）。
 *
 * 定位方式：先找到算法 OID，向前回溯到外层 SEQUENCE(0x30)，再按该 SEQUENCE
 * 声明的长度切出完整的一段。找不到 OID 时返回空串，由调用方决定如何提示。
 */
export function subjectPublicKeyInfoDer(cert: string): string {
  let der: string
  try {
    der = certDerBinary(cert)
  } catch {
    return ''
  }
  // 公钥算法 OID 及其 DER 标记（06 <len>）：SM2 与 RSA 各一种。
  // 用元组 [OID 字节, OID 标签头] 而不是对象字面量，纯粹是为了紧凑：
  // 这里只有两个字段，且只在紧跟的循环里用一次
  const algorithms = [
    [SM2_CURVE_OID, [0x06, 0x08]],
    [RSA_OID, [0x06, 0x09]],
  ]
  for (const [oid, prefix] of algorithms) {
    const oidAt = indexOfBytes(der, oid)
    if (oidAt < 0) continue
    const oidTagAt = oidAt - prefix.length
    if (oidTagAt < 0) continue

    /** 读一个 SEQUENCE(0x30) 的总长度；不是合法头时返回 0 */
    const sequenceTotal = (at: number): number => {
      if (at < 0 || at + 1 >= der.length || der.charCodeAt(at) !== 0x30) return 0
      const lenByte = der.charCodeAt(at + 1)
      if (lenByte < 0x80) return 2 + lenByte
      if (lenByte === 0x81) return 3 + der.charCodeAt(at + 2)
      if (lenByte === 0x82) return 4 + ((der.charCodeAt(at + 2) << 8) | der.charCodeAt(at + 3))
      return 0
    }

    // 第一步：从 OID 往前找到**最内层**那个包住它的 SEQUENCE ——
    // 那是 AlgorithmIdentifier（SEQUENCE { OID, 参数 }），还不是 SPKI
    let innerStart = -1
    const innerSearchFrom = Math.max(0, oidTagAt - 8)
    for (let i = oidTagAt; i >= innerSearchFrom; i--) {
      const total = sequenceTotal(i)
      if (total > 0 && i + total <= der.length && i + total > oidAt) {
        innerStart = i
        break
      }
    }
    if (innerStart < 0) continue

    // 第二步：继续往前找能**包住 AlgorithmIdentifier 与公钥位串**的更外层
    // SEQUENCE —— 那才是 SubjectPublicKeyInfo。
    // 判据除了"包住内层"，还要求内层结束之后紧跟 BIT STRING(0x03 = 公钥位串)，
    // 这一条能把 TBSCertificate 之类同样"包住它"的父结构排除掉
    const outerSearchFrom = Math.max(0, innerStart - 48)
    for (let i = innerStart - 1; i >= outerSearchFrom; i--) {
      const total = sequenceTotal(i)
      if (total <= 0 || i + total > der.length) continue
      const innerEnd = innerStart + sequenceTotal(innerStart)
      if (i + total <= innerEnd) continue
      if (der.charCodeAt(innerEnd) !== 0x03) continue
      return binaryToBase64(der.slice(i, i + total))
    }
  }
  return ''
}

/* ------------------------------------------------ 证书基本信息解析（DN / 序列号） */

/** 一个 DER TLV 节点的位置 */
interface Asn1Node {
  tag: number
  /** 内容起始（已跳过 tag 与长度字节） */
  start: number
  /** 内容结束（不含） */
  end: number
}

/**
 * 读一个 TLV 头。
 *
 * 只支持证书里实际会出现的长度形式（短形式与 0x81/0x82/0x83 长形式）。
 * 证书中不存在不定长编码，因此遇到 0x84 或不定长直接返回 null ——
 * 交给调用方报"结构无法解析"，比继续往下猜安全。
 */
function readTlv(der: string, at: number): Asn1Node | null {
  if (at < 0 || at + 1 >= der.length) return null
  const tag = der.charCodeAt(at)
  const lenByte = der.charCodeAt(at + 1)
  let header = 2
  let len = 0
  if (lenByte < 0x80) {
    len = lenByte
  } else if (lenByte === 0x81) {
    len = der.charCodeAt(at + 2)
    header = 3
  } else if (lenByte === 0x82) {
    len = (der.charCodeAt(at + 2) << 8) | der.charCodeAt(at + 3)
    header = 4
  } else if (lenByte === 0x83) {
    len = (der.charCodeAt(at + 2) << 16) | (der.charCodeAt(at + 3) << 8) | der.charCodeAt(at + 4)
    header = 5
  } else {
    return null
  }
  const start = at + header
  const end = start + len
  if (end > der.length) return null
  return { tag, start, end }
}

/** OID 的内容字节 → 点分字符串（如 1.2.156.10197.1.301） */
function oidToString(bytes: string): string {
  if (!bytes.length) return ''
  const first = bytes.charCodeAt(0)
  const parts = [Math.floor(first / 40), first % 40]
  let value = 0
  let pending = false
  for (let i = 1; i < bytes.length; i++) {
    const b = bytes.charCodeAt(i)
    value = value * 128 + (b & 0x7f)
    pending = true
    if ((b & 0x80) === 0) {
      parts.push(value)
      value = 0
      pending = false
    }
  }
  if (pending) parts.push(value)
  return parts.join('.')
}

/** DN 中常见的 OID 短名；未收录的按点分 OID 原样输出，不丢信息 */
const DN_SHORT_NAMES: Record<string, string> = {
  '2.5.4.3': 'CN',
  '2.5.4.6': 'C',
  '2.5.4.7': 'L',
  '2.5.4.8': 'ST',
  '2.5.4.10': 'O',
  '2.5.4.11': 'OU',
  '2.5.4.5': 'SERIALNUMBER',
  '1.2.840.113549.1.9.1': 'E',
  '0.9.2342.19200300.100.1.25': 'DC',
}

/** ASN.1 字符串类型的值 → JS 字符串 */
function decodeAsn1String(der: string, node: Asn1Node): string {
  const raw = der.slice(node.start, node.end)
  if (node.tag === 0x1e) {
    // BMPString：每 2 字节一个 UTF-16BE 码元
    let out = ''
    for (let i = 0; i + 1 < raw.length; i += 2) {
      out += String.fromCharCode((raw.charCodeAt(i) << 8) | raw.charCodeAt(i + 1))
    }
    return out
  }
  // UTF8String / PrintableString / IA5String / TeletexString：
  // 先还原为字节再按 UTF-8 解码（中文 CN 会以 UTF8String 出现）
  try {
    const bytes = new Uint8Array(raw.length)
    for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i) & 0xff
    return new TextDecoder('utf-8', { fatal: false }).decode(bytes)
  } catch {
    return raw
  }
}

/**
 * 解析 X.509 Name（RDNSequence）为可读 DN 串。
 *
 * 输出顺序**反转** DER 的编码顺序：DER 从最一般到最具体（C → O → OU → CN），
 * 而人读 DN 的习惯是 CN 在前（参考项目的展示形态也是这个）。
 */
function parseName(der: string, node: Asn1Node): string {
  const parts: string[] = []
  let p = node.start
  while (p < node.end) {
    const rdn = readTlv(der, p)
    if (!rdn) break
    // RDN 是 SET，内容是一个或多个 AttributeTypeAndValue（SEQUENCE）
    let q = rdn.start
    while (q < rdn.end) {
      const atv = readTlv(der, q)
      if (!atv) break
      if (atv.tag === 0x30) {
        const oidNode = readTlv(der, atv.start)
        if (oidNode && oidNode.tag === 0x06) {
          const oid = oidToString(der.slice(oidNode.start, oidNode.end))
          const valueNode = readTlv(der, oidNode.end)
          const value = valueNode ? decodeAsn1String(der, valueNode) : ''
          if (value) parts.push(`${DN_SHORT_NAMES[oid] || oid}=${value}`)
        }
      }
      q = atv.end
    }
    p = rdn.end
  }
  return parts.reverse().join(',')
}

/** DN 属性名归一化：点分 OID → 短名，短名统一大写，同义写法合并 */
function canonicalDNName(name: string): string {
  const oidToShort: Record<string, string> = {
    '2.5.4.3': 'CN',
    '2.5.4.6': 'C',
    '2.5.4.7': 'L',
    '2.5.4.8': 'ST',
    '2.5.4.10': 'O',
    '2.5.4.11': 'OU',
    '2.5.4.5': 'SERIALNUMBER',
    '1.2.840.113549.1.9.1': 'E',
    '0.9.2342.19200300.100.1.25': 'DC',
  }
  // **先 trim 再查表**：DN 的分隔风格在不同实现间不一致
  //（Go 的 pkix.Name.String() 输出 `CN=x,O=y`，.NET 输出 `CN=x, O=y`），
  // 属性名前会带一个空格。不先 trim 的话 `" 2.5.4.3"` 命中不了 OID 表，
  // 同一个属性就会被当成两个不同的名字，比较必然失败
  const trimmed = name.trim()
  if (oidToShort[trimmed]) return oidToShort[trimmed]
  const upper = trimmed.toUpperCase()
  // 同义写法：不同实现会分别用 EMAILADDRESS / E / emailAddress 表示同一个属性
  if (upper === 'EMAILADDRESS' || upper === 'EMAIL') return 'E'
  return upper
}

/** 按**未转义**的逗号切分 DN（值里的逗号会写成 `\,`） */
function splitDN(dn: string): string[] {
  const parts: string[] = []
  let cur = ''
  let escaped = false
  for (const ch of dn) {
    if (escaped) {
      cur += ch
      escaped = false
      continue
    }
    if (ch === '\\') {
      cur += ch
      escaped = true
      continue
    }
    if (ch === ',') {
      parts.push(cur)
      cur = ''
      continue
    }
    cur += ch
  }
  if (cur) parts.push(cur)
  return parts
}

/** 去掉 DN 值的外围引号与反斜杠转义 */
function unescapeDNValue(value: string): string {
  const unquoted = value.replace(/^"(.*)"$/s, '$1')
  return unquoted.replace(/\\(.)/g, '$1').trim()
}

/**
 * 把 DN 串解析成 `{属性短名 → 值}` 的集合，**与 RDN 顺序、书写形态无关**。
 *
 * 为什么需要它：同一个 DN 在不同实现里写法可以差很多 ——
 * RDN 顺序（RFC 2253 是逆序输出）、属性名长短写（CN vs 2.5.4.3）、
 * 属性间空格、E vs EMAILADDRESS。前端是自己解析 DER，Go 侧用的是
 * `pkix.Name.String()`，**没有任何保证两者逐字符相同**。
 * 按集合比较才稳。
 */
export function dnAttributes(dn: string): Record<string, string> {
  // 先按属性名分桶收集，最后对同名多值排序再拼接：
  // 一个 DN 里出现多个 OU 是合法的，而它们的先后顺序不构成语义差异，
  // 不排序的话 "OU=A,OU=B" 与 "OU=B,OU=A" 会被判成不同主体
  const bucket = new Map<string, string[]>()
  for (const part of splitDN(dn)) {
    const eq = part.indexOf('=')
    if (eq <= 0) continue
    const name = canonicalDNName(part.slice(0, eq))
    if (!name) continue
    const value = unescapeDNValue(part.slice(eq + 1))
    const list = bucket.get(name) ?? []
    list.push(value)
    bucket.set(name, list)
  }
  const out: Record<string, string> = {}
  for (const [name, values] of bucket) {
    out[name] = [...values].sort().join('\u0000')
  }
  return out
}

/**
 * 两个 DN 是否表示同一个主体（按属性集合比较，忽略顺序与大小写）。
 *
 * 空串、解析不出属性的一律视为**不相等** —— 判断不了就不要下结论。
 */
export function sameDN(a: string, b: string): boolean {
  if (!a || !b) return false
  const attrA = dnAttributes(a)
  const attrB = dnAttributes(b)
  const keysA = Object.keys(attrA)
  const keysB = Object.keys(attrB)
  if (!keysA.length || keysA.length !== keysB.length) return false
  return keysA.every((k) => (attrA[k] || '').toLowerCase() === (attrB[k] || '').toLowerCase())
}

/** 证书里要展示给用户的基本信息 */
export interface UKeyCertInfo {
  /** 主体 DN，形如 CN=ops01,OU=UKey,O=Bastion,C=CN */
  subjectDN: string
  /** 颁发者 DN */
  issuerDN: string
  /** 序列号（大写十六进制，去前导 0） */
  serialNumber: string
  /** 公钥算法（SM2 / RSA / 未知） */
  algorithm: string
}

/**
 * 从证书中解析出界面要展示的三项信息（DN / 序列号 / 颁发者）。
 *
 * 为什么在前端解析、而不是新增一个后端接口：这三项要在**读卡之后、提交之前**
 * 显示给用户确认，而那一刻还没有任何服务端交互（参考项目也是这个形态）。
 * 这里只做"把字段读出来"，**不做任何合法性判断** —— 证书能不能用，仍由服务端
 * 在提交时按链校验、有效期、用途逐项判定，前端不重复发明一套结论。
 */
export function parseCertInfo(cert: string): UKeyCertInfo {
  const der = certDerBinary(cert)
  const outer = readTlv(der, 0)
  if (!outer || outer.tag !== 0x30) {
    throw new UKeyAgentError('NO_CERT', '证书结构无法解析（不是合法的 DER SEQUENCE）')
  }
  const tbs = readTlv(der, outer.start)
  if (!tbs || tbs.tag !== 0x30) {
    throw new UKeyAgentError('NO_CERT', '证书结构无法解析（缺少 TBSCertificate）')
  }

  let p = tbs.start
  let node = readTlv(der, p)
  // version 是 [0] EXPLICIT；v1 证书没有该字段
  if (node && node.tag === 0xa0) {
    p = node.end
    node = readTlv(der, p)
  }

  let serialNumber = ''
  if (node && node.tag === 0x02) {
    // INTEGER 是二进制补码：正数可能带一个前导 0x00，展示时要去掉
    const raw = der.slice(node.start, node.end).replace(/^\x00+/, '')
    let hex = ''
    for (let i = 0; i < raw.length; i++) {
      hex += raw.charCodeAt(i).toString(16).padStart(2, '0')
    }
    serialNumber = hex.toUpperCase()
    p = node.end
    node = readTlv(der, p)
  }

  // signature AlgorithmIdentifier（与公钥算法无关，此处只需跳过）
  if (node && node.tag === 0x30) {
    p = node.end
    node = readTlv(der, p)
  }

  let issuerDN = ''
  if (node && node.tag === 0x30) {
    issuerDN = parseName(der, node)
    p = node.end
    node = readTlv(der, p)
  }

  // validity
  if (node && node.tag === 0x30) {
    p = node.end
    node = readTlv(der, p)
  }

  let subjectDN = ''
  if (node && node.tag === 0x30) {
    subjectDN = parseName(der, node)
  }

  return { subjectDN, issuerDN, serialNumber, algorithm: certAlgorithmLabel(cert) }
}

/** 便于界面展示的证书简要信息 */
export interface UKeyCertBrief {
  /** base64(DER)，登录链路要原样回传的形态 */
  base64: string
  /** PEM 文本，登记绑定用 */
  pem: string
  /** 从证书中粗取的 CN，可能为空 */
  commonName: string
}

/** 读证书并同时准备两种形态，供界面调用 */
export async function readCertificateBrief(options: UKeyAgentOptions = {}): Promise<UKeyCertBrief> {
  const base64 = await readCertificate(options)
  const pem = base64ToPem(base64)
  return { base64, pem, commonName: guessCommonName(pem) }
}

/** 把任意异常归一化成 UKeyAgentError，便于界面统一判断 code */
export function toUKeyError(err: unknown): UKeyAgentError {
  if (err instanceof UKeyAgentError) return err
  if (err instanceof Error) return new UKeyAgentError('MIDDLEWARE', err.message)
  return new UKeyAgentError('MIDDLEWARE', String(err))
}
