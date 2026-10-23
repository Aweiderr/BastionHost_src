// UKey DER 公钥解析的回归自测（无需浏览器、无需真机）
//
// 为什么需要它：extractSm2PublicKey 用"字节扫描"定位 SM2 公钥，扫描边界一旦
// 写错，取到的 X||Y 会**静默错位** —— 表现为"服务端签发成功、但登录验签永远
// 失败"，是这类链路里最难在真机上定位的问题。这里用构造的 X.509 结构把正确性
// 钉住：X 全 0x11、Y 全 0x22，错一个字节断言就会失败。
//
// 运行：node ukey-der-selftest.mjs
import { readFileSync, writeFileSync } from 'node:fs'

const out = []
const say = (m) => {
  out.push(String(m))
  console.log(m)
}

const src = readFileSync(new URL('./src/utils/ukeyAgent.ts', import.meta.url), 'utf8')

/** 取出一个函数声明（到行首 `}` 结束），并剥掉 TS 类型注解，便于用 new Function 执行 */
function fn(name) {
  let start = src.indexOf(`export function ${name}(`)
  if (start < 0) start = src.indexOf(`\nfunction ${name}(`)
  if (start < 0) throw new Error(`ukeyAgent.ts 中找不到函数 ${name}`)
  const end = src.indexOf('\n}\n', start)
  if (end < 0) throw new Error(`函数 ${name} 的结尾未找到`)
  return src
    .slice(start, end + 3)
    .replace(`export function ${name}(`, `function ${name}(`)
    .trimStart()
    // 返回类型注解：必须**先**处理，否则会被下面的形参规则吃掉右括号。
    // 先处理泛型返回类型（Record<string, string> 这类）
    .replace(/\)\s*:\s*(Record|Array|Map|Set)<[^>]+>\s*\{/, ') {')
    // 再处理标识符与联合类型（如 `Asn1Node | null`）
    .replace(
      /\)\s*:\s*[A-Za-z_$][\w$.]*(\[\])?(\s*\|\s*[A-Za-z_$][\w$.]*(\[\])?)*\s*\{/,
      ') {',
    )
    // 泛型集合类型（Record<string, string> 这类）
    .replace(/:\s*(Record|Array|Map|Set)<[^>]+>\s*=/g, ' =')
    // 泛型实例化：new Map<string, string[]>() → new Map()
    .replace(/new\s+(Map|Set|Array|Record)<[^>]+>\s*\(/g, 'new $1(')
    // 形参/变量的类型注解：**只剥白名单里的类型名**。
    // 不能写成"任意 标识符:标识符"—— 那会把三元表达式的 `: value`
    // 和对象字面量的 `{ oid: X }` 一起吃掉（我第一版就踩了这个坑）
    .replace(
      /:\s*(string|number|boolean|void|Uint8Array|Asn1Node|UKeyCertInfo|UKeyKeyAlgorithm|UKeyCertBrief|UKeyAgentError)(\[\])?\s*([),={])/g,
      '$3',
    )
    // 无初始值的声明（如 `let der: string`）：只在声明语句上生效
    .replace(
      /\b(let|const|var)\s+([A-Za-z_$][\w$]*)\s*:\s*[A-Za-z_$][\w$]*(\[\])?\s*$/gm,
      '$1 $2',
    )
}

/** 取出错误类声明：类不提升，必须排在调用它的函数之前 */
function errorClass() {
  const start = src.indexOf('export class UKeyAgentError')
  if (start < 0) throw new Error('ukeyAgent.ts 中找不到 UKeyAgentError')
  const end = src.indexOf('\n}\n', start)
  return src
    .slice(start, end + 3)
    .replace('export class', 'class')
    .replace(/readonly code: UKeyErrorCode/, 'code')
    .replace(/code: UKeyErrorCode, message: string/, 'code, message')
}

/** 取出一个常量声明（例如 SM2 曲线 OID 的字节数组） */
function constDecl(name) {
  const start = src.indexOf(`const ${name}`)
  if (start < 0) throw new Error(`ukeyAgent.ts 中找不到常量 ${name}`)
  const end = src.indexOf('\n', start)
  return src.slice(start, end).replace(/;$/, '')
}

/** 取出一个多行的对象常量（按大括号配平），并剥掉 TS 类型注解 */
function constObject(name) {
  const start = src.indexOf(`const ${name}`)
  if (start < 0) throw new Error(`ukeyAgent.ts 中找不到常量 ${name}`)
  const braceStart = src.indexOf('{', start)
  let depth = 0
  let i = braceStart
  for (; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) break
    }
  }
  return src.slice(start, i + 1).replace(/:\s*Record<[^>]+>\s*=/, ' =')
}

const body = [
  errorClass(),
  constDecl('SM2_CURVE_OID'),
  constDecl('RSA_OID'),
  constObject('DN_SHORT_NAMES'),
  fn('stripWhitespace'),
  fn('isPem'),
  fn('base64ToBinary'),
  fn('binaryToBase64'),
  fn('arrayToBinary'),
  fn('bytesToBase64'),
  fn('indexOfBytes'),
  fn('certDerBinary'),
  fn('detectCertAlgorithm'),
  fn('certAlgorithmLabel'),
  fn('readTlv'),
  fn('oidToString'),
  fn('decodeAsn1String'),
  fn('parseName'),
  fn('canonicalDNName'),
  fn('splitDN'),
  fn('unescapeDNValue'),
  fn('dnAttributes'),
  fn('sameDN'),
  fn('parseCertInfo'),
  fn('extractSm2PublicKey'),
  fn('subjectPublicKeyInfoDer'),
  'return { extractSm2PublicKey, detectCertAlgorithm, certDerBinary, subjectPublicKeyInfoDer, parseCertInfo, certAlgorithmLabel, bytesToBase64, sameDN, dnAttributes };',
].join('\n')

// 调试开关：DUMP_BODY=1 时把生成的可执行代码落盘（排查类型剥离问题用）
if (process.env.DUMP_BODY) {
  writeFileSync(new URL('./ukey-der-selftest.body.js', import.meta.url), body, 'utf8')
}

const win = {
  atob: (s) => Buffer.from(s, 'base64').toString('binary'),
  btoa: (s) => Buffer.from(s, 'binary').toString('base64'),
}
const {
  extractSm2PublicKey,
  detectCertAlgorithm,
  certDerBinary,
  subjectPublicKeyInfoDer,
  parseCertInfo,
  certAlgorithmLabel,
  bytesToBase64,
  sameDN,
  dnAttributes,
} = new Function('window', `"use strict";\n${body}`)(win)

/* ------------------------------ 构造测试证书 DER ------------------------------ */

const tlv = (tag, bytes) => {
  const len = bytes.length
  if (len < 0x80) return [tag, len, ...bytes]
  if (len <= 0xff) return [tag, 0x81, len, ...bytes]
  return [tag, 0x82, (len >> 8) & 0xff, len & 0xff, ...bytes]
}
const cat = (...p) => p.flat()
/** 便捷别名：`d(tag, ...bytes)` 等价于 `tlv(tag, bytes)`，长度自动编码 */
const d = (tag, ...bytes) => tlv(tag, bytes)
const OID_SM2 = [0x06, 0x08, 0x2a, 0x81, 0x1c, 0xcf, 0x55, 0x01, 0x82, 0x2d]
const X = new Array(32).fill(0x11)
const Y = new Array(32).fill(0x22)

// SubjectPublicKeyInfo：SM2 OID（带 NULL 参数） + BIT STRING(0x42, unused=0, 0x04 前缀)
const spki = tlv(0x30, cat(tlv(0x30, cat(OID_SM2, [0x05, 0x00])), tlv(0x03, [0x00, 0x04, ...X, ...Y])))
// 外面再包一层证书外壳（用噪声字节占位），模拟真实证书里公钥不在固定偏移上的情况
const noise = new Array(37).fill(0x5a)
const certDer = cat(tlv(0x30, cat(noise, spki, noise)))
const certB64 = Buffer.from(Uint8Array.from(certDer)).toString('base64')
const expected = Buffer.from(Uint8Array.from([0x04, ...X, ...Y])).toString('base64')

/* ---------------------------------- 断言 ---------------------------------- */

const results = []
const expect = (name, cond, extra = '') => results.push({ name, pass: !!cond, extra })

const got = extractSm2PublicKey(certB64)
expect('从证书中提取到 65 字节未压缩点', got === expected, got.slice(0, 20))

const decoded = Buffer.from(got, 'base64')
expect('输出长度 = 65', decoded.length === 65, String(decoded.length))
expect('首字节 = 0x04', decoded[0] === 0x04, String(decoded[0]))
expect('X 段全为 0x11（未错位）', decoded.subarray(1, 33).every((b) => b === 0x11))
expect('Y 段全为 0x22（未错位）', decoded.subarray(33, 65).every((b) => b === 0x22))

// 中间件返回的 base64 常带换行，必须能容忍
const wrapped = certB64.replace(/(.{20})/g, '$1\n')
expect('容忍带换行的 base64 输入', extractSm2PublicKey(wrapped) === expected)

/* ---------------------- 公钥算法识别（决定 AlgType） ---------------------- */

// SM2 证书必须识别为 sm2 → 调用方据此选 AlgType=3
expect('SM2 证书 → 识别为 sm2', detectCertAlgorithm(certB64) === 'sm2', detectCertAlgorithm(certB64))

// RSA 证书：SubjectPublicKeyInfo 里是 RSA OID（1.2.840.113549.1.1.1）+ NULL + 位串
const OID_RSA = [0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01]
const rsaSpki = tlv(
  0x30,
  cat(tlv(0x30, cat(OID_RSA, [0x05, 0x00])), tlv(0x03, [0x00, ...new Array(64).fill(0x33)])),
)
const rsaB64 = Buffer.from(Uint8Array.from(tlv(0x30, cat(noise, rsaSpki, noise)))).toString('base64')
const rsaSpkiB64 = Buffer.from(Uint8Array.from(rsaSpki)).toString('base64')
expect('RSA 证书 → 识别为 rsa', detectCertAlgorithm(rsaB64) === 'rsa', detectCertAlgorithm(rsaB64))
expect(
  'RSA 证书 → 对它取 SM2 公钥会明确报错（而不是返回错位字节）',
  (() => {
    try {
      extractSm2PublicKey(rsaB64)
      return false
    } catch (e) {
      return String(e && e.message).includes('SM2 曲线标识')
    }
  })(),
)

// 两种 OID 都没有（例如 EC P-256 或数据结构异常）：识别为 unknown，
// 调用方 algTypeForCert 会据此报错，绝不默认用 SM2 硬签
const unknownB64 = Buffer.from(
  Uint8Array.from(tlv(0x30, cat(noise, tlv(0x03, [0x00, 0x04, ...X, ...Y])))),
).toString('base64')
expect('两种 OID 都无 → 识别为 unknown', detectCertAlgorithm(unknownB64) === 'unknown')

// 内容坏掉时要返回 unknown 而不是抛异常（算法只用于展示与选 AlgType，不该打断页面渲染）
expect('非法内容 → 识别为 unknown 且不抛异常', detectCertAlgorithm('!!!not-base64!!!') === 'unknown')
expect('PEM 包装的 SM2 证书同样识别为 sm2', detectCertAlgorithm(
  `-----BEGIN CERTIFICATE-----\n${certB64.match(/.{1,64}/g).join('\n')}\n-----END CERTIFICATE-----\n`,
) === 'sm2')
expect('certDerBinary 能解出与原始 DER 等长的字节', certDerBinary(certB64).length === certDer.length)

/* -------------------- SPKI 提取（RSA 走这条，SM2 亦可用） -------------------- */

// 从证书里取出的 SPKI 必须等于构造时放进去的那段，否则服务端会以
// "公钥解析失败"拒绝签发（而报错在签发环节，排查会跑偏到证书本身）
const spkiB64 = Buffer.from(Uint8Array.from(spki)).toString('base64')
const gotSpki = subjectPublicKeyInfoDer(certB64)
expect(
  'SM2 证书 → 提取的 SPKI 与构造值一致',
  gotSpki === spkiB64,
  `got=${gotSpki.slice(0, 24)}(len=${Buffer.from(gotSpki || '', 'base64').length}) want=${
    spkiB64.slice(0, 24)
  }(len=${spki.length})`,
)
expect('RSA 证书 → 提取的 SPKI 与构造值一致', subjectPublicKeyInfoDer(rsaB64) === rsaSpkiB64)
expect('两种 OID 都无 → SPKI 提取返回空串（由调用方决定提示）', subjectPublicKeyInfoDer(unknownB64) === '')

// 没有 SM2 OID（例如 RSA 证书）：必须明确报错，而不是返回错位字节
const noOid = Buffer.from(
  Uint8Array.from(tlv(0x30, cat(noise, tlv(0x03, [0x00, 0x04, ...X, ...Y])))),
).toString('base64')
let msg1 = ''
try {
  extractSm2PublicKey(noOid)
} catch (e) {
  msg1 = String(e && e.message)
}
expect('缺 SM2 OID → 提示无法解析出 SM2 公钥', msg1.includes('无法从该证书中解析出 SM2 公钥'), msg1)

// 有 OID 但位串不足以容纳 65 字节
const shortKey = Buffer.from(
  Uint8Array.from(
    tlv(0x30, cat(tlv(0x30, cat(OID_SM2, [0x05, 0x00])), tlv(0x03, [0x00, 0x04, 0x11, 0x22]))),
  ),
).toString('base64')
let msg2 = ''
try {
  extractSm2PublicKey(shortKey)
} catch (e) {
  msg2 = String(e && e.message)
}
expect('位串过短 → 提示未定位到 65 字节位串', msg2.includes('未能定位到 65 字节的公钥位串'), msg2)

/* -------------------- 证书基本信息解析（界面要显示的三项） -------------------- */

// 构造一份 Name 完整、序列号已知的证书：界面要求"读卡后自动显示
// 证书DN / 证书序列号 / 证书颁发者"，这三项完全由前端解析，必须钉住
const utf8 = (s) => tlv(0x0c, [...new TextEncoder().encode(s)])
const OID_CN = [0x06, 0x03, 0x55, 0x04, 0x03]
const OID_C = [0x06, 0x03, 0x55, 0x04, 0x06]
const OID_O = [0x06, 0x03, 0x55, 0x04, 0x0a]
const OID_OU = [0x06, 0x03, 0x55, 0x04, 0x0b]
const rdn = (oid, value) => tlv(0x31, tlv(0x30, cat(oid, utf8(value))))
const nameOf = (...pairs) => tlv(0x30, cat(...pairs.map(([oid, value]) => rdn(oid, value))))

const subjectName = nameOf([OID_C, 'CN'], [OID_O, 'Bastion'], [OID_OU, 'UKey'], [OID_CN, 'ops01'])
const issuerName = nameOf([OID_C, 'CN'], [OID_O, 'Bastion'], [OID_CN, 'Bastion Internal Issuing CA'])
const serialBytes = [
  0x7d, 0x06, 0x87, 0x42, 0xb7, 0xba, 0xe0, 0x7a, 0x5b, 0x67, 0x03, 0x26, 0xab, 0xb6, 0xad, 0x2c,
]
const certWithDN = Buffer.from(
  Uint8Array.from(
    tlv(
      0x30,
      cat(
        tlv(
          0x30,
          cat(
            d(0xa0, 0x03, 0x02, 0x01, 0x02), // version v3
            tlv(0x02, serialBytes),
            tlv(0x30, cat(OID_SM2, [0x05, 0x00])), // 签名算法
            issuerName,
            tlv(0x30, cat(d(0x17, ...new Array(13).fill(0x32)), d(0x17, ...new Array(13).fill(0x33)))), // validity
            subjectName,
            spki,
          ),
        ),
        tlv(0x30, cat(OID_SM2, [0x05, 0x00])),
        d(0x03, 0x02, 0x00, 0xaa),
      ),
    ),
  ),
).toString('base64')

const info = parseCertInfo(certWithDN)
expect(
  '证书主体 DN → CN 在前、逗号分隔（与参考项目展示一致）',
  info.subjectDN === 'CN=ops01,OU=UKey,O=Bastion,C=CN',
  info.subjectDN,
)
expect(
  '证书颁发者 DN 解析正确',
  info.issuerDN === 'CN=Bastion Internal Issuing CA,O=Bastion,C=CN',
  info.issuerDN,
)
expect(
  '证书序列号 → 大写十六进制、无前导 0',
  info.serialNumber === '7D068742B7BAE07A5B670326ABB6AD2C',
  info.serialNumber,
)
expect('证书算法随信息一并给出', info.algorithm === 'SM2', info.algorithm)

// 序列号带前导 0x00（正整数的补码前缀）时必须去掉，否则与后端存的归一化序列号对不上
const serialWithLeadingZero = Buffer.from(
  Uint8Array.from(
    tlv(
      0x30,
      cat(
        tlv(
          0x30,
          cat(
            d(0xa0, 0x03, 0x02, 0x01, 0x02),
            tlv(0x02, [0x00, 0x7d, 0x06, 0x87]),
            tlv(0x30, cat(OID_SM2, [0x05, 0x00])),
            issuerName,
            tlv(0x30, cat(d(0x17, ...new Array(13).fill(0x32)), d(0x17, ...new Array(13).fill(0x33)))),
            subjectName,
            spki,
          ),
        ),
        tlv(0x30, cat(OID_SM2, [0x05, 0x00])),
        d(0x03, 0x02, 0x00, 0xaa),
      ),
    ),
  ),
).toString('base64')
expect(
  '序列号前导 0x00 被去掉',
  parseCertInfo(serialWithLeadingZero).serialNumber === '7D0687',
  parseCertInfo(serialWithLeadingZero).serialNumber,
)

// v1 证书没有 version 字段：解析必须仍然正确（不能把 serial 当成 version 跳过）
const v1Cert = Buffer.from(
  Uint8Array.from(
    tlv(
      0x30,
      cat(
        tlv(
          0x30,
          cat(
            tlv(0x02, serialBytes),
            tlv(0x30, cat(OID_SM2, [0x05, 0x00])),
            issuerName,
            tlv(0x30, cat(d(0x17, ...new Array(13).fill(0x32)), d(0x17, ...new Array(13).fill(0x33)))),
            subjectName,
            spki,
          ),
        ),
        tlv(0x30, cat(OID_SM2, [0x05, 0x00])),
        d(0x03, 0x02, 0x00, 0xaa),
      ),
    ),
  ),
).toString('base64')
expect(
  'v1 证书（无 version 字段）序列号仍解析正确',
  parseCertInfo(v1Cert).serialNumber === '7D068742B7BAE07A5B670326ABB6AD2C',
  parseCertInfo(v1Cert).serialNumber,
)

// 非证书数据要报可读错误，而不是抛异常或返回空串
let msg3 = ''
try {
  parseCertInfo(Buffer.from('not a certificate at all').toString('base64'))
} catch (e) {
  msg3 = String(e && e.message)
}
expect('非法内容 → 提示证书结构无法解析', msg3.includes('无法解析'), msg3)

expect('certAlgorithmLabel 对 SM2/RSA 分别给出文案', certAlgorithmLabel(certB64) === 'SM2' && certAlgorithmLabel(rsaB64) === 'RSA')

// 真机样本里的 CN 就是中文（"超管"），必须以 UTF8String 解码而不是按字节拼接 ——
// 按字节拼会得到乱码，而证书照样"看起来解析成功"，没人会去核对 CN 的字面值
const chineseCnCert = Buffer.from(
  Uint8Array.from(
    tlv(
      0x30,
      cat(
        tlv(
          0x30,
          cat(
            d(0xa0, 0x03, 0x02, 0x01, 0x02),
            tlv(0x02, serialBytes),
            tlv(0x30, cat(OID_SM2, [0x05, 0x00])),
            issuerName,
            tlv(0x30, cat(d(0x17, ...new Array(13).fill(0x32)), d(0x17, ...new Array(13).fill(0x33)))),
            nameOf([OID_C, 'CN'], [OID_CN, '超管']),
            spki,
          ),
        ),
        tlv(0x30, cat(OID_SM2, [0x05, 0x00])),
        d(0x03, 0x02, 0x00, 0xaa),
      ),
    ),
  ),
).toString('base64')
expect(
  '中文 CN（UTF8String）解码正确',
  parseCertInfo(chineseCnCert).subjectDN === 'CN=超管,C=CN',
  parseCertInfo(chineseCnCert).subjectDN,
)

/* -------------------- 二进制 DER 文件 → base64（上传 .der 的路径） -------------------- */

// 上传 .der 必须走二进制→base64，不能走文本解码：后者会把字节读坏，
// 而且可能"看似成功"地提交一个错误的字节串
const derBytes = Uint8Array.from([0x30, 0x82, 0x01, 0x0a, 0x02, 0x01, 0x02, 0xff, 0x00, 0x80])
expect(
  '二进制 DER → base64 与 Node 的实现一致',
  bytesToBase64(derBytes) === Buffer.from(derBytes).toString('base64'),
  bytesToBase64(derBytes),
)
expect('空字节数组 → 空 base64（由调用方报"无法识别"）', bytesToBase64(new Uint8Array(0)) === '')
// 含 0xff / 0x80 的字节最容易被文本解码读坏，这里单独钉住
expect(
  '含高位字节（0xFF/0x80）不失真',
  Buffer.from(bytesToBase64(Uint8Array.from([0xff, 0x80, 0x00])), 'base64').equals(
    Buffer.from([0xff, 0x80, 0x00]),
  ),
)

/* -------------------- DN 集合比较（cert_source 自动判断的基础） -------------------- */

expect(
  'DN 的 RDN 顺序不同 → 视为同一主体',
  sameDN('CN=ops01,OU=UKey,O=Bastion,C=CN', 'C=CN, O=Bastion, OU=UKey, CN=ops01'),
)
expect('DN 属性名写点分 OID → 视为同一主体', sameDN('CN=ops01', '2.5.4.3=ops01'))
expect('DN 大小写差异 → 视为同一主体', sameDN('CN=OPS01', 'cn=ops01'))
expect('DN 值不同 → 不相等', !sameDN('CN=ops01', 'CN=ops02'))
expect('DN 属性个数不同 → 不相等', !sameDN('CN=ops01,OU=UKey', 'CN=ops01'))
expect('空 DN 一律不相等（判断不了就不下结论）', !sameDN('', 'CN=ops01') && !sameDN('CN=ops01', ''))
expect('EMAILADDRESS 与 E 视为同一属性', sameDN('E=a@b.c', 'EMAILADDRESS=a@b.c'))
expect(
  '多个同名属性（OU）参与比较且顺序无关',
  sameDN('OU=A,OU=B,CN=x', 'OU=B,OU=A,CN=x'),
)
expect(
  'dnAttributes 解析属性名与值',
  dnAttributes('CN=ops01,O=Bastion').CN === 'ops01' && dnAttributes('CN=ops01,O=Bastion').O === 'Bastion',
)

/* ------------- 真实数据回归：两侧 DN 来自**不同系统**，分隔风格不同是外部事实 ------------- */
//
// 前端 issuerDN 来自我们自己的 DER 解析；sameDN 的另一侧来自 Go 的
// pkix.Name.String()。实测：Go 输出 `CN=x,O=y`（逗号后无空格），
// .NET / 证书查看器输出 `CN=x, O=y`（有空格）。
// 没有这几条断言，任何一次"顺手优化"都可能把空白处理弄坏，
// 而故障表现只是"绑定失败"，几乎不可能联想到 DN 分隔符。
expect(
  '真实数据：同一 DN 的两种分隔风格（", " 与 ","）判相等',
  sameDN('CN=tsm for sm2 oca, C=CN', 'CN=tsm for sm2 oca,C=CN'),
)
expect(
  '真实数据：内部签发 CA 的两种分隔风格判相等',
  sameDN(
    'CN=Bastion Internal Issuing CA, O=Bastion, C=CN',
    'CN=Bastion Internal Issuing CA,O=Bastion,C=CN',
  ),
)
expect(
  '真实数据：用户证书主体 ≠ 内部签发 CA 主体（不能误判成 internal）',
  !sameDN('CN=ops01, OU=UKey, O=Bastion, C=CN', 'CN=Bastion Internal Issuing CA,O=Bastion,C=CN'),
)
expect(
  '真实数据：内部根 CA 与内部签发 CA 不是同一主体',
  !sameDN(
    'CN=Bastion Internal Root CA,O=Bastion,C=CN',
    'CN=Bastion Internal Issuing CA,O=Bastion,C=CN',
  ),
)
expect(
  '属性名与值的首尾空白都被剥掉',
  sameDN('  CN=ops01  ,  O=Bastion  ', 'CN=ops01,O=Bastion'),
)
// 属性名前带空格时，点分 OID 也必须能归一成短名。
// 这一条**曾真实失败**：`" 2.5.4.3"` 查不到 OID 表，会被当成独立属性名
expect(
  '带空格的属性名：点分 OID 仍归一成短名',
  Object.keys(dnAttributes(' 2.5.4.3=x ')).length === 1 && dnAttributes(' 2.5.4.3=x ').CN === 'x',
  JSON.stringify(dnAttributes(' 2.5.4.3=x ')),
)
expect(
  '属性名 emailAddress / EMAILADDRESS / E 三者等价',
  sameDN('emailAddress=a@b.c', 'EMAILADDRESS=a@b.c') &&
    sameDN('emailAddress=a@b.c', 'E=a@b.c') &&
    sameDN('EMAILADDRESS=a@b.c', 'E=a@b.c'),
)

const failed = results.filter((r) => !r.pass)
for (const r of results) say(`${r.pass ? 'PASS' : 'FAIL'}  ${r.name}${r.extra ? `  [${r.extra}]` : ''}`)
say(`共 ${results.length} 项，失败 ${failed.length} 项`)
writeFileSync(new URL('./ukey-der-selftest.log', import.meta.url), out.join('\n'), 'utf8')
process.exitCode = failed.length ? 1 : 0
