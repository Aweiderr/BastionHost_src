<script setup lang="ts">
/**
 * 用户 UKey 证书弹窗。
 *
 * 交互按参考项目（svs-universal 的"管理员证书认证"）的形态设计：
 * **选证书类型 → 读卡（或选文件）→ 核对三项信息 → 点「确定」**，就这么多。
 *
 * 三条设计原则，改动前请先读：
 *
 * 1. **点一次「确定」跑完整条链。** "内部签发"要经历
 *    读证书取公钥 → 领挑战 → 中间件签名 → 提交签发 → 灌回 UKey 五步，
 *    但这五步**不增加用户任何操作**，所以全部自动化。把中间步骤摊到界面上
 *    （早先那份 8 步进度清单）只会让人觉得复杂，却换不来任何可控性。
 *
 * 2. **持有证明不省，但也不暴露。** 签发接口要求"证明你持有这把私钥"
 *    （challenge + 中间件签名）。这一步必须保留 —— 否则任何人都能抄一个公钥
 *    换一张证书。它只是被藏进自动流程里：用户看到的是
 *    "正在读取 UKey…→ 正在签发…→ 正在写入 UKey…" 这样一行文字。
 *
 * 3. **不让用户理解 cert_source。** 用户只需表达"证书从哪来"（内部签发 /
 *    Key 里已有 / 上传文件）；"该按 internal 还是 external 登记"由前端拿
 *    证书颁发者与内部 CA 主体比对后**自动判断** —— 那是实现细节，
 *    让用户选只会多一次选错的机会（选错必被服务端链校验拒绝）。
 *
 * 入口：用户管理页的「证书链」按钮（权限 system:user:update）。
 */
import { computed, ref, watch } from 'vue'
import {
  fetchUKeyIssueChallenge,
  getUKeyCA,
  issueUKeyCertByPubkey,
  listUKeyCerts,
  registerUKeyCert,
  revokeUKeyCert,
} from '@/api/ukey'
import type { UKeyCAStatus, UKeyCertRecord, UKeyCertSource, UKeyCertStatus } from '@/api/types'
import Modal from '@/components/Modal.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { formatTime } from '@/utils/format'
import {
  DEFAULT_CONTAINER,
  DEFAULT_CSP_NAME,
  UKeyAgentError,
  algTypeForCert,
  base64ToPem,
  bytesToBase64,
  certAlgorithmText,
  certToBase64Der,
  checkComSetup,
  detectCertAlgorithm,
  downloadSingleCert,
  extractSm2PublicKey,
  getCertByContName,
  initProvider,
  normalizeCertInput,
  parseCertInfo,
  sameDN,
  sign,
  subjectPublicKeyInfoDer,
  toUKeyError,
  verifyPin,
  type UKeyCertInfo,
} from '@/utils/ukeyAgent'

const props = defineProps<{
  modelValue: boolean
  userId: number
  username: string
  displayName?: string
  /** 该用户当前配置的第二因子是否为 ukey，用于提示"必须至少有一张有效证书" */
  mfaType?: string
}>()

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

const app = useAppStore()

/* ------------------------------------------------------------------ 证书类型 */

type CertType = 'internal' | 'read' | 'upload'

const TYPE_OPTIONS: Array<{ value: CertType; label: string; hint: string }> = [
  {
    value: 'internal',
    label: '内部签发证书',
    hint: '由堡垒机内部 CA 用 UKey 里的公钥签发一张新证书，并自动写入 UKey。Key 里原有的其它证书不受影响。',
  },
  {
    value: 'read',
    label: '读取 UKey 现有证书',
    hint: '读取 UKey 中已有的证书并登记到系统。该证书的信任链必须已就绪（内部 CA 已签发，或已导入其外部 CA 根证书）。',
  },
  {
    value: 'upload',
    label: '上传证书文件',
    hint: '上传 PEM / DER 格式的证书文件并登记到系统，适用于从其它系统导出的证书。',
  },
]

const certType = ref<CertType>('internal')
const currentHint = computed(() => TYPE_OPTIONS.find((o) => o.value === certType.value)?.hint || '')

/* ------------------------------------------------------------------ 基础状态 */

const busy = ref(false)
/** 一行进度提示；步骤虽多但都自动完成，摊成清单反而是复杂感的主要来源 */
const progressText = ref('')
const errorText = ref('')

const pin = ref('')
const cspName = ref(DEFAULT_CSP_NAME)
const container = ref(DEFAULT_CONTAINER)
/** CSP / 容器名极少需要改，默认收起 */
const showAdvanced = ref(false)

/** 读卡或上传得到的证书信息（界面上要展示的三项） */
const certInfo = ref<UKeyCertInfo | null>(null)
/** 同一张证书的 base64(DER) 形态，内部签发取公钥时用 */
const certBase64 = ref('')
/** 同一张证书的 PEM 形态，登记绑定时用 */
const certPem = ref('')
const uploadedName = ref('')

/* -------------------------------------------------------------- 已绑定证书 */

const certs = ref<UKeyCertRecord[]>([])
const loading = ref(false)
const caStatus = ref<UKeyCAStatus | null>(null)

/** 有效（未吊销且未过期）证书数量：mfa_type=ukey 时这个数为 0 意味着该账号登不进去 */
const activeCount = computed(() => certs.value.filter((c) => c.status === 'active').length)
/** 已绑定列表里是否存在 RSA 证书（弱算法告警） */
const hasRsaCert = computed(() => certs.value.some((c) => certAlgorithmText(c) === 'RSA'))
/** 已绑定列表里是否存在"未验证链"的证书 */
const hasUnverified = computed(() => certs.value.some((c) => c.unverified_chain === true))

async function load(): Promise<void> {
  if (props.userId <= 0) return
  loading.value = true
  try {
    // 一个用户的证书通常个位数，取全量省掉一层分页交互
    const res = await listUKeyCerts({ owner_id: props.userId, page: 1, page_size: 100 })
    certs.value = res.items ?? []
  } catch (err) {
    certs.value = []
    app.error(err instanceof Error ? err.message : '证书列表读取失败')
  } finally {
    loading.value = false
  }
}

/**
 * CA 状态：只用于两件事 —— 内部 CA 是否就绪的提示，
 * 以及登记时**自动判断**证书来源（internal / external）。失败不阻塞主流程。
 */
async function loadCA(): Promise<void> {
  try {
    caStatus.value = await getUKeyCA()
  } catch {
    caStatus.value = null
  }
}

/* ------------------------------------------------------------------ 生命周期 */

/** 清空"本次操作"的证书状态，不动已加载的列表 */
function resetCredentials(): void {
  certInfo.value = null
  certBase64.value = ''
  certPem.value = ''
  uploadedName.value = ''
}

const issuedOk = ref(false)
const wroteCard = ref(false)
const issuedSerial = ref('')
/** 已签发证书的 PEM：灌卡失败后重试时直接用，避免重复签发 */
const issuedPem = ref('')
const retrying = ref(false)

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      resetCredentials()
      errorText.value = ''
      progressText.value = ''
      busy.value = false
      retrying.value = false
      pin.value = ''
      certType.value = 'internal'
      issuedOk.value = false
      wroteCard.value = false
      issuedSerial.value = ''
      issuedPem.value = ''
      revokeTargetId.value = null
      revokeReason.value = ''
      showAdvanced.value = false
      void loadCA()
      void load()
    } else {
      // PIN 是硬件凭据，关窗即清空
      pin.value = ''
      progressText.value = ''
    }
  },
)

/* ------------------------------------------------------------------ 读卡 */

/** 读一次 UKey 里的签名证书，并把界面要展示的三项解析出来 */
async function readFromUKey(): Promise<void> {
  errorText.value = ''
  resetCredentials()
  if (!pin.value.trim()) {
    errorText.value = '请输入 UKey 的 PIN 码'
    return
  }
  busy.value = true
  try {
    await readCardIntoState()
    progressText.value = '已读取到证书，请核对下方信息'
  } catch (err) {
    progressText.value = ''
    const ukeyErr = toUKeyError(err)
    errorText.value = ukeyErr.message
    if (ukeyErr.code === 'PIN_ERROR') pin.value = ''
  } finally {
    busy.value = false
  }
}

/**
 * 读卡并写入状态：检测中间件 → 初始化 → 校验 PIN → 取证书 → 解析三项。
 * 抽出来是因为"读取现有证书"点确定时若尚未读卡，也要走同一段逻辑。
 */
async function readCardIntoState(): Promise<void> {
  progressText.value = '正在检测 UKey 中间件…'
  await checkComSetup()
  progressText.value = '正在初始化 UKey 服务…'
  await initProvider(
    cspName.value.trim() || DEFAULT_CSP_NAME,
    container.value.trim() || DEFAULT_CONTAINER,
  )
  progressText.value = '正在校验 PIN…'
  await verifyPin(pin.value)
  progressText.value = '正在读取 UKey 证书…'
  // iKeyType 固定 2：这是持签名私钥的那张；容器里另一张证书的公钥与私钥不配对
  const base64 = await getCertByContName(container.value.trim() || DEFAULT_CONTAINER)
  certBase64.value = base64
  certPem.value = base64ToPem(base64)
  certInfo.value = parseCertInfo(base64)
}

/* ---------------------------------------------------------------- 上传文件 */

async function onFileChange(event: Event): Promise<void> {
  errorText.value = ''
  resetCredentials()
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  busy.value = true
  try {
    progressText.value = '正在解析证书文件…'
    const pem = normalizeCertInput(await readCertFile(file))
    certPem.value = pem
    certBase64.value = certToBase64Der(pem)
    certInfo.value = parseCertInfo(pem)
    uploadedName.value = file.name
    progressText.value = '已解析证书文件，请核对下方信息'
  } catch (err) {
    progressText.value = ''
    const ukeyErr = toUKeyError(err)
    errorText.value =
      ukeyErr.code === 'NO_CERT'
        ? ukeyErr.message
        : '证书文件解析失败，请确认是 PEM 或 DER 格式的证书'
    input.value = ''
  } finally {
    busy.value = false
  }
}

/**
 * 读证书文件，返回**可直接交给 normalizeCertInput 的文本**。
 *
 * 必须按二进制读、再自己判断形态，不能用 readAsText：
 * DER 是二进制，readAsText 会按 UTF-8 解码成含 U+FFFD 的字符串，
 * 原始字节再也回不来 —— 更糟的是它可能"看似成功"地提交一个**错误的字节串**，
 * 那比直接报错危险得多。判断方式：头部出现 `-----BEGIN` 即 PEM 文本，
 * 否则按二进制 DER 转 base64（normalizeCertInput 接受裸 base64）。
 */
async function readCertFile(file: File): Promise<string> {
  const buffer = await file.arrayBuffer()
  const bytes = new Uint8Array(buffer)
  if (!bytes.length) {
    throw new UKeyAgentError('NO_CERT', '证书文件为空')
  }
  const head = new TextDecoder('utf-8', { fatal: false }).decode(bytes.subarray(0, 64))
  if (head.includes('-----BEGIN')) {
    return new TextDecoder('utf-8', { fatal: false }).decode(bytes)
  }
  // 二进制 DER（国密体系里 .der 很常见）→ base64 文本
  const base64 = bytesToBase64(bytes)
  if (!base64) {
    throw new UKeyAgentError('NO_CERT', '无法识别的证书文件：既不是 PEM 文本也不是有效的 DER 内容')
  }
  return base64
}

/* ------------------------------------------------------- 证书来源自动判断 */

/**
 * 自动判断登记时的 cert_source。
 *
 * 依据：内部 CA 签发的证书，其**颁发者**必然是内部签发 CA（或内部根 CA）。
 * 拿它与 `GET /ukey/ca` 返回的内部 CA 主体做**按属性集合**的比较
 * （见 sameDN：忽略 RDN 顺序与书写差异），而不是逐字符相等 ——
 * 前端是自己解析 DER，Go 侧是 `pkix.Name.String()`，两者没有逐字符一致的保证。
 *
 * **判断不出来时默认 external**（宁可让服务端拒，也不让用户误以为绑定成功）：
 * 若它其实是内部证书，服务端会以"链不受信任"拒绝，用户重试或找管理员即可；
 * 反过来把外部证书当 internal 提交，错误信息同样指向链校验，更难懂。
 * 换句话说，两个方向都会被拒，选错误信息更**具体**的那一边。
 */
function guessCertSource(): UKeyCertSource {
  const issuer = certInfo.value?.issuerDN
  if (!issuer || !caStatus.value) return 'external'
  const candidates: string[] = []
  const info = caStatus.value.internal
  if (info?.issuer_subject_dn) candidates.push(info.issuer_subject_dn)
  if (info?.root_subject_dn) candidates.push(info.root_subject_dn)
  for (const c of caStatus.value.cas ?? []) {
    if (c.cert_source === 'internal' && c.subject_dn) candidates.push(c.subject_dn)
  }
  return candidates.some((dn) => sameDN(dn, issuer)) ? 'internal' : 'external'
}

/* ------------------------------------------------------------- 内部签发链路 */

/**
 * 内部签发：读公钥 → 领挑战 → 签名 → 签发 → 灌卡，全自动串起来。
 *
 * 每一步的失败原因完全不同（中间件没装 / PIN 错 / 挑战过期 / 链不受信任 /
 * 写卡失败），所以进度文字要逐步更新；但**不**做成清单 ——
 * 那些步骤用户既不能跳过也不能干预，列出来只是噪音。
 */
async function issueAndWrite(): Promise<void> {
  errorText.value = ''
  if (!pin.value.trim()) {
    errorText.value = '请输入 UKey 的 PIN 码'
    return
  }
  if (caStatus.value && !caStatus.value.initialized) {
    errorText.value = '内部 CA 尚未初始化，无法签发证书。请联系安全管理员在「证书链管理」中初始化。'
    return
  }
  busy.value = true
  try {
    // 1) 读卡取公钥（刚读过就复用，避免让用户重复输 PIN）
    let base64 = certBase64.value
    if (!base64) {
      await readCardIntoState()
      base64 = certBase64.value
    }
    const algorithm = detectCertAlgorithm(base64)
    // SM2 → 65 字节未压缩点；RSA 没有等价的小结构，改用 SubjectPublicKeyInfo DER
    const publicKey =
      algorithm === 'sm2' ? extractSm2PublicKey(base64) : subjectPublicKeyInfoDer(base64)
    if (!publicKey) {
      throw new UKeyAgentError('NO_CERT', '无法从 UKey 证书中提取公钥，请确认证书格式受支持')
    }

    // 2) 持有证明：领挑战 + 用 Key 里的私钥签名。全自动，用户无感，但一步都不能省
    progressText.value = '正在向服务端申请签发…'
    const ch = await fetchUKeyIssueChallenge(props.userId)
    if (!ch.src_data) {
      throw new UKeyAgentError('BAD_RESPONSE', '服务端未返回挑战原文，请重试')
    }
    progressText.value = '正在用 UKey 签名…'
    // 必须签服务端下发的 src_data 原样字符串：中间件把它当数据本身签
    const signature = await sign(ch.src_data, algTypeForCert(base64))

    progressText.value = '正在签发证书…'
    const record = await issueUKeyCertByPubkey({
      user_id: props.userId,
      challenge_id: ch.challenge_id,
      public_key: publicKey,
      src_data: ch.src_data,
      signature,
      remark: '由堡垒机内部 CA 按 UKey 公钥签发',
    })
    issuedOk.value = true
    issuedPem.value = record.cert_pem || ''
    issuedSerial.value = record.serial_number || ''
    if (!issuedPem.value) {
      throw new UKeyAgentError(
        'BAD_RESPONSE',
        '服务端已签发并绑定，但响应里没有证书内容，无法写入 UKey',
      )
    }

    progressText.value = '正在写入 UKey…'
    await downloadSingleCert(certToBase64Der(issuedPem.value), `bastion-issue-${Date.now()}`)
    wroteCard.value = true
    progressText.value = ''
    await load()
    app.success('证书已由内部 CA 签发并写入 UKey')
  } catch (err) {
    progressText.value = ''
    const ukeyErr = toUKeyError(err)
    errorText.value = ukeyErr.message
    if (ukeyErr.code === 'PIN_ERROR') pin.value = ''
  } finally {
    busy.value = false
  }
}

/**
 * 只重试"写入 UKey"，**不重新签发**。
 *
 * 签发成功即已在服务端绑定；重走整个流程会再签一张（多占一张配额、
 * 多一个需要吊销的残留）。因此优先用内存里刚签发的那张 PEM，
 * 换过页面才回退到查库取最近一张内部 CA 证书。
 */
async function retryWriteCard(): Promise<void> {
  errorText.value = ''
  if (!pin.value.trim()) {
    errorText.value = '请输入 UKey 的 PIN 码后再重试写入'
    return
  }
  retrying.value = true
  try {
    let pem = issuedPem.value
    if (!pem) {
      const res = await listUKeyCerts({
        owner_id: props.userId,
        cert_source: 'internal',
        status: 'active',
        page: 1,
        page_size: 100,
      })
      const withPem = (res.items ?? []).filter((c) => c.cert_pem)
      const ts = (c: UKeyCertRecord): number => {
        const t = new Date(c.create_time || c.update_time || '').getTime()
        return Number.isNaN(t) ? 0 : t
      }
      const latest = withPem.length ? withPem.reduce((a, b) => (ts(b) > ts(a) ? b : a)) : null
      if (!latest?.cert_pem) {
        errorText.value = '没有找到可写入的内部 CA 证书记录，请重新执行一次签发'
        return
      }
      pem = latest.cert_pem
      issuedPem.value = pem
      issuedSerial.value = latest.serial_number || ''
      issuedOk.value = true
    }
    progressText.value = '正在写入 UKey…'
    await checkComSetup()
    await initProvider(
      cspName.value.trim() || DEFAULT_CSP_NAME,
      container.value.trim() || DEFAULT_CONTAINER,
    )
    await verifyPin(pin.value)
    await downloadSingleCert(certToBase64Der(pem), `bastion-issue-${Date.now()}`)
    progressText.value = ''
    wroteCard.value = true
    await load()
    app.success('证书已写入 UKey')
  } catch (err) {
    progressText.value = ''
    errorText.value = toUKeyError(err).message
    if (toUKeyError(err).code === 'PIN_ERROR') pin.value = ''
  } finally {
    retrying.value = false
  }
}

/* ------------------------------------------------------- 登记已有证书（绑定） */

/** "读取现有"与"上传文件"共用：把当前证书登记到该用户名下 */
async function bindExisting(): Promise<void> {
  errorText.value = ''
  if (!certPem.value) {
    errorText.value = certType.value === 'read' ? '请先点「读取 UKey 证书」' : '请先选择证书文件'
    return
  }
  busy.value = true
  try {
    progressText.value = '正在提交证书…'
    // cert_source 由前端按颁发者自动判断，不让用户选
    const source = guessCertSource()
    await registerUKeyCert({
      user_id: props.userId,
      cert_pem: certPem.value,
      cert_source: source,
      cert_name: certInfo.value?.subjectDN || `${props.username} 的 UKey 证书`,
      remark: certType.value === 'upload' ? `上传证书文件：${uploadedName.value || '未命名'}` : '',
    })
    progressText.value = ''
    resetCredentials()
    await load()
    app.success(source === 'internal' ? '证书已绑定（内部 CA 签发）' : '证书已绑定（外部 CA 签发）')
  } catch (err) {
    progressText.value = ''
    // 原样展示服务端结论（未注册/已吊销/链不可信/归属不符/用途不符）
    errorText.value = err instanceof Error ? err.message : '绑定失败'
  } finally {
    busy.value = false
  }
}

/** 点「确定」：按证书类型走对应流程 —— 一次点击完成 */
async function submit(): Promise<void> {
  if (busy.value || retrying.value) return
  if (certType.value === 'internal') {
    await issueAndWrite()
    return
  }
  // "读取现有"若还没读卡，先自动读一次再提交，省掉"先读再确定"的两步
  if (certType.value === 'read' && !certPem.value) {
    await readFromUKey()
    if (!certPem.value) return
  }
  await bindExisting()
}

/* --------------------------------------------------------------------- 吊销 */

const revokeTargetId = ref<number | null>(null)
const revokeReason = ref('')
const revokingId = ref<number | null>(null)

function openRevoke(cert: UKeyCertRecord): void {
  revokeTargetId.value = cert.cert_id
  revokeReason.value = ''
}

async function doRevoke(cert: UKeyCertRecord): Promise<void> {
  revokingId.value = cert.cert_id
  try {
    await revokeUKeyCert(cert.cert_id, revokeReason.value.trim() || '管理员吊销')
    app.success('证书已吊销')
    revokeTargetId.value = null
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '吊销失败')
  } finally {
    revokingId.value = null
  }
}

/* ------------------------------------------------------------------ 展示辅助 */

function sourceLabel(source: string): string {
  return source === 'external' ? '外部 CA' : '内部 CA'
}

function statusLabel(status: string): string {
  const map: Record<string, string> = { active: '可用', revoked: '已吊销', expired: '已过期' }
  return map[status] || status || '—'
}

function statusTone(status: UKeyCertStatus | string): 'success' | 'danger' | 'warning' | 'default' {
  if (status === 'active') return 'success'
  if (status === 'revoked') return 'danger'
  if (status === 'expired') return 'warning'
  return 'default'
}

function shortText(text: string | undefined, max = 34): string {
  if (!text) return '—'
  return text.length > max ? `…${text.slice(-(max - 2))}` : text
}

function certAlgText(cert: UKeyCertRecord): string {
  return certAlgorithmText(cert)
}

function creatorName(cert: UKeyCertRecord): string {
  return cert.creator_name || '—'
}

function close(): void {
  emit('update:modelValue', false)
}
</script>

<template>
  <Modal
    :model-value="props.modelValue"
    size="lg"
    :title="`UKey 证书 · ${props.displayName || props.username}`"
    :close-on-mask="false"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="text-secondary mb-12" style="font-size: 12.5px; line-height: 1.6">
      用户：<b>{{ props.displayName || props.username }}</b>
      <span class="text-mono">（{{ props.username }}）</span>
    </div>

    <!-- mfa_type=ukey 却没有有效证书：这是"配了二次认证但登不进去"的状态，必须告警 -->
    <div
      v-if="props.mfaType === 'ukey' && !loading && activeCount === 0"
      class="mb-12"
      style="
        font-size: 12.5px;
        line-height: 1.6;
        padding: 8px 10px;
        border-radius: var(--radius);
        border: 1px solid var(--color-danger, #dc2626);
        color: var(--color-danger, #dc2626);
      "
    >
      该账号已启用「UKey 证书」二次认证，但当前<b>没有任何有效证书</b>，将无法登录。
      请在下方签发或绑定一张证书。
    </div>

    <!-- 证书类型：用户只需要表达"证书从哪来" -->
    <div class="form-item">
      <label class="form-label">证书类型</label>
      <select v-model="certType" class="select" :disabled="busy">
        <option v-for="opt in TYPE_OPTIONS" :key="opt.value" :value="opt.value">
          {{ opt.label }}
        </option>
      </select>
      <div class="form-hint">{{ currentHint }}</div>
    </div>

    <!-- 读卡：CSP / 容器收进"高级参数"，日常只用输 PIN -->
    <div class="form-grid">
      <div class="form-item">
        <label class="form-label">UKey PIN</label>
        <input
          v-model="pin"
          class="input"
          type="password"
          autocomplete="off"
          placeholder="UKey 的 PIN 码"
          :disabled="busy"
          @keyup.enter="readFromUKey"
        />
      </div>
      <div class="form-item" style="justify-content: flex-end">
        <button class="btn" type="button" :disabled="busy" @click="readFromUKey">
          读取 UKey 证书
        </button>
      </div>
    </div>

    <button class="btn-link mb-12" type="button" @click="showAdvanced = !showAdvanced">
      {{ showAdvanced ? '收起高级参数' : '高级参数（CSP 名称 / 证书容器）' }}
    </button>
    <div v-if="showAdvanced" class="form-grid mb-12">
      <div class="form-item">
        <label class="form-label">CSP 名称</label>
        <input v-model.trim="cspName" class="input" :disabled="busy" />
      </div>
      <div class="form-item">
        <label class="form-label">证书容器名</label>
        <input v-model.trim="container" class="input text-mono" :disabled="busy" />
      </div>
    </div>

    <!-- 上传证书文件 -->
    <div v-if="certType === 'upload'" class="form-item">
      <label class="form-label">证书文件</label>
      <input
        class="input"
        type="file"
        accept=".pem,.crt,.cer,.der,.txt"
        :disabled="busy"
        @change="onFileChange"
      />
      <div v-if="uploadedName" class="form-hint">已选择：{{ uploadedName }}</div>
    </div>

    <!-- 一行进度（不做成步骤清单） -->
    <div v-if="progressText" class="text-secondary mb-8" style="font-size: 12.5px">
      <span class="spinner" />
      <span style="margin-left: 6px">{{ progressText }}</span>
    </div>

    <div v-if="errorText" class="text-danger mb-8" style="font-size: 12.5px; line-height: 1.6">
      {{ errorText }}
    </div>

    <!-- 读卡 / 上传成功后展示的三项信息（与参考项目一致） -->
    <div class="desc-list mb-12">
      <div class="desc-item">
        <div class="desc-label">证书DN</div>
        <div class="desc-value text-mono" style="font-size: 12px; word-break: break-all">
          {{ certInfo?.subjectDN || '—' }}
        </div>
      </div>
      <div class="desc-item">
        <div class="desc-label">证书序列号</div>
        <div class="desc-value text-mono" style="font-size: 12px">
          {{ certInfo?.serialNumber || '—' }}
        </div>
      </div>
      <div class="desc-item">
        <div class="desc-label">证书颁发者</div>
        <div class="desc-value text-mono" style="font-size: 12px; word-break: break-all">
          {{ certInfo?.issuerDN || '—' }}
        </div>
      </div>
      <div v-if="certInfo" class="desc-item">
        <div class="desc-label">公钥算法</div>
        <div class="desc-value">
          <span class="tag" :class="{ 'tag-warning': certInfo.algorithm === 'RSA' }">
            {{ certInfo.algorithm }}
          </span>
        </div>
      </div>
    </div>

    <!-- RSA 是硬件能力限制下的妥协，必须讲明白 -->
    <div
      v-if="certInfo?.algorithm === 'RSA'"
      class="mb-12"
      style="font-size: 12.5px; line-height: 1.7; color: var(--color-warning)"
    >
      <b>弱算法提示：</b>当前 UKey 为 RSA 类型。受 UKey 中间件算法能力限制，签名只能使用
      <b>RSA-SHA1</b>（中间件不提供 RSA-SHA256），安全性弱于 SM2，建议仅用于兼容存量 RSA 密钥。
    </div>

    <!-- 完成态一：签发并写入成功 -->
    <div v-if="wroteCard" class="mb-12" style="font-size: 12.5px; line-height: 1.7">
      <StatusTag text="已完成" tone="success" />
      <span style="margin-left: 6px">
        证书已由内部 CA 签发并写入 UKey（序列号
        <span class="text-mono">{{ issuedSerial || '—' }}</span>）。
      </span>
      <div class="text-secondary mt-8">
        原证书仍保留在 UKey 的另一个证书位置上，未做改动；现在可以用这张新证书登录了。
      </div>
    </div>

    <!-- 完成态二：签发绑定成功但写卡失败（可重试，且不重新签发） -->
    <div
      v-else-if="issuedOk"
      class="mb-12"
      style="font-size: 12.5px; line-height: 1.7; color: var(--color-warning)"
    >
      <b>证书已签发并绑定成功，但写入 UKey 失败。</b>
      该账号现在已有一张内部 CA 签发的有效证书（序列号
      <span class="text-mono">{{ issuedSerial || '—' }}</span>），但 Key 里还没换上它。
      请确认 Key 仍插在本机后点「重新写入 UKey」——重试<b>不会</b>重新签发。
    </div>

    <template #footer>
      <button class="btn" type="button" :disabled="busy || retrying" @click="close">取消</button>
      <button
        v-if="issuedOk && !wroteCard"
        class="btn btn-primary"
        type="button"
        :disabled="busy || retrying"
        @click="retryWriteCard"
      >
        <span v-if="retrying" class="spinner light" />
        {{ retrying ? '写入中…' : '重新写入 UKey' }}
      </button>
      <button v-else class="btn btn-primary" type="button" :disabled="busy || retrying" @click="submit">
        <span v-if="busy" class="spinner light" />
        {{ busy ? '处理中…' : '确定' }}
      </button>
    </template>

    <!-- ============================ 已绑定证书 ============================ -->
    <!--
      只要有绑定就显示（含只有一张的情形）：单张时若整个区域消失，这张证书就
      失去了查看与吊销的入口。做成紧凑一行，不占版面。
    -->
    <div
      v-if="certs.length"
      class="mt-16"
      style="border-top: 1px solid var(--color-border); padding-top: 12px"
    >
      <div class="flex-between mb-8">
        <span class="card-title">已绑定证书</span>
        <span class="tag">{{ certs.length }} 张 · 有效 {{ activeCount }}</span>
      </div>

      <div
        v-if="hasRsaCert || hasUnverified"
        class="mb-8"
        style="font-size: 12px; line-height: 1.7; color: var(--color-warning)"
      >
        <template v-if="hasRsaCert">
          该用户存在 <b>RSA</b> 证书：受中间件算法能力限制签名只能使用 RSA-SHA1，
          建议仅用于兼容存量密钥。
        </template>
        <template v-if="hasUnverified">
          <br />存在<b>未经验证链</b>的证书记录（管理员手工登记、未经链校验），请确认是否仍需要。
        </template>
      </div>

      <div v-for="cert in certs" :key="cert.cert_id" class="mb-12" style="font-size: 12.5px">
        <div class="flex-center gap-8" style="flex-wrap: wrap">
          <span class="text-mono" :title="cert.serial_number">
            {{ shortText(cert.serial_number || undefined) }}
          </span>
          <span class="tag" :class="{ 'tag-warning': certAlgText(cert) === 'RSA' }">
            {{ certAlgText(cert) }}
          </span>
          <span class="tag">{{ sourceLabel(cert.cert_source) }}</span>
          <StatusTag :text="statusLabel(cert.status)" :tone="statusTone(cert.status)" />
          <StatusTag v-if="cert.unverified_chain" text="未验证链" tone="danger" />
          <span class="text-muted">添加人 {{ creatorName(cert) }}</span>
          <span class="text-muted" style="font-size: 11.5px">
            至 {{ formatTime(cert.not_after || undefined) }}
          </span>
          <button
            v-if="cert.status === 'active'"
            class="btn-link danger"
            :disabled="revokingId === cert.cert_id"
            @click="openRevoke(cert)"
          >
            吊销
          </button>
          <span v-else-if="cert.status === 'revoked'" class="text-muted" style="font-size: 11.5px">
            已吊销{{ cert.revoke_reason ? `：${cert.revoke_reason}` : '' }}
          </span>
        </div>

        <!-- 吊销原因 + 二次确认（内联展开，避免弹窗套弹窗） -->
        <div
          v-if="revokeTargetId === cert.cert_id"
          class="flex-center gap-8 mt-8"
          style="flex-wrap: wrap"
        >
          <span class="text-danger" style="font-size: 12.5px">
            吊销后该证书立即无法用于登录，且不可撤销。
          </span>
          <input
            v-model.trim="revokeReason"
            class="input"
            style="flex: 1; min-width: 200px"
            placeholder="吊销原因（记入审计日志）"
          />
          <button
            class="btn btn-sm btn-danger"
            :disabled="revokingId === cert.cert_id"
            @click="doRevoke(cert)"
          >
            <span v-if="revokingId === cert.cert_id" class="spinner light" />
            确认吊销
          </button>
          <button class="btn btn-sm" @click="revokeTargetId = null">取消</button>
        </div>
      </div>

      <div class="text-muted" style="font-size: 11.5px">
        归属以证书序列号为准：不是该用户绑定的证书，即使签名正确也会被服务端拒绝。
      </div>
    </div>
  </Modal>
</template>

<style scoped>
.desc-value {
  word-break: break-all;
}
</style>
