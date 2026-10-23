<script setup lang="ts">
/**
 * 证书链管理（内部/外部 CA 的证书链台账 + 信任基础运维）。
 *
 * 命名与权限点的中文名一致（`key:cert:manage` = 「证书链管理」），
 * 便于在权限表与界面之间直接对应。该权限点属于 key 模块，只授予安全管理员
 * 与超级管理员。
 *
 * 职责边界（刻意收窄）：本页只维护 CA / 证书链**自身的台账** —— 查看各条链、
 * 导入外部信任锚、下载证书。**不提供**"给某个用户绑证书"的入口：那是用户维度
 * 的操作，统一在用户管理页的「证书链」按钮里完成，两处并存会让"这张证书到底
 * 绑给谁"变得难以回答。内部 CA 的**初始化入口也不在本页**（由部署流程另行指定
 * 位置），本页只如实显示"当前是否已初始化"，不放置任何触发按钮。
 */
import { computed, onMounted, ref } from 'vue'
import { UKEY_CHAIN_PERMISSION, getUKeyCA, importExternalCA, listUKeyCerts } from '@/api/ukey'
import type { UKeyCAStatus, UKeyCertRecord } from '@/api/types'
import DataTable from '@/components/DataTable.vue'
import Modal from '@/components/Modal.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import { formatTime } from '@/utils/format'
import {
  certAlgorithmText,
  certToBase64Der,
  isRsaCertRecord,
  normalizeCertInput,
  toUKeyError,
} from '@/utils/ukeyAgent'

const app = useAppStore()
const auth = useAuthStore()
/**
 * 本页（证书链管理）的权限：key:cert:manage，归安全管理员。
 *
 * 与"给用户绑证书"（system:user:update，在用户管理页）是两个职责：
 * 这里管的是 CA 台账本身 —— 内部 CA 状态、外部信任锚、各条链的构成与下载。
 * 因此**不能**因为要在用户管理页显示绑定入口就放宽这个判断（反之亦然）。
 */
const canManage = computed(() => auth.has(UKEY_CHAIN_PERMISSION))

/** 一行 = 一条证书链（以"末级证书"为代表） */
interface ChainRow {
  /** 链的稳定标识：根证书 cert_id */
  rootId: number
  root: UKeyCertRecord
  /** 该链下的签发 CA（内部两级 CA 有；外部导入的根通常没有） */
  issuers: UKeyCertRecord[]
  /** 末级证书：内部链是签发 CA；只有一张证书的链就是它自己 */
  leaf: UKeyCertRecord
  /** 链内全部证书（根 + 签发 CA），供详情弹窗按层级展示 */
  all: UKeyCertRecord[]
}

const columns: TableColumn[] = [
  { key: 'chain_name', title: '证书链名称' },
  { key: 'source', title: '内外部证书', width: '110px' },
  { key: 'creator', title: '添加人', width: '120px' },
  { key: 'algorithm', title: '证书算法', width: '110px' },
  { key: 'leaf_dn', title: '末级证书 DN' },
  { key: 'not_before', title: '证书生效时间', width: '165px' },
  { key: 'not_after', title: '证书过期时间', width: '165px' },
  { key: 'status', title: '证书状态', width: '120px' },
  { key: 'actions', title: '操作', width: '150px', align: 'right' },
]

/* ------------------------------------------------------------------ 数据加载 */

const caLoading = ref(false)
const caStatus = ref<UKeyCAStatus | null>(null)

const certLoading = ref(false)
const certs = ref<UKeyCertRecord[]>([])
const keyword = ref('')

async function loadCA(): Promise<void> {
  caLoading.value = true
  try {
    caStatus.value = await getUKeyCA()
  } catch (err) {
    caStatus.value = null
    app.error(err instanceof Error ? err.message : 'CA 状态读取失败')
  } finally {
    caLoading.value = false
  }
}

/**
 * 拉取全部 CA 证书后在**前端成链**。
 *
 * 后端目前只有扁平的证书接口，没有"证书链"这个实体：一条链的关系靠
 * `chain_id` 表达（签发 CA 的 chain_id 指向根的 cert_id，根的 chain_id 为空）。
 * 证书数量是"每台部署几条"的量级，一次取全量再组装足够，也避免了分页把
 * 同一条链的证书切到两页去。
 */
async function loadCerts(): Promise<void> {
  certLoading.value = true
  try {
    const res = await listUKeyCerts({ cert_type: 'ca', page: 1, page_size: 500 })
    certs.value = res.items ?? []
  } catch (err) {
    certs.value = []
    app.error(err instanceof Error ? err.message : '证书列表读取失败')
  } finally {
    certLoading.value = false
  }
}

async function refreshAll(): Promise<void> {
  await Promise.all([loadCA(), loadCerts()])
}

onMounted(() => {
  // 无权限时不发请求：全局限权提示只在真正越权时出现
  if (!canManage.value) return
  void refreshAll()
})

/* ------------------------------------------------------------------ 成链逻辑 */

const chainRows = computed<ChainRow[]>(() => {
  const list = certs.value
  const byId = new Map<number, UKeyCertRecord>()
  for (const c of list) byId.set(c.cert_id, c)

  const childrenOf = new Map<number, UKeyCertRecord[]>()
  const roots: UKeyCertRecord[] = []
  for (const c of list) {
    // chain_id 指向父证书；为空（或指向不存在的记录）即视为链根
    const parent = c.chain_id ?? 0
    if (parent > 0 && byId.has(parent)) {
      const arr = childrenOf.get(parent) ?? []
      arr.push(c)
      childrenOf.set(parent, arr)
    } else {
      roots.push(c)
    }
  }

  // 末级证书：取"没有下级的那一层"；一条链只有根时（外部导入的根就是这种形态）
  // 就以根自身作为末级
  const leafOf = (root: UKeyCertRecord, children: UKeyCertRecord[]): UKeyCertRecord => {
    if (!children.length) return root
    for (const c of children) {
      if ((childrenOf.get(c.cert_id) ?? []).length > 0) return c
    }
    return children[0]
  }

  return roots
    .map((root) => {
      const children = childrenOf.get(root.cert_id) ?? []
      const leaf = leafOf(root, children)
      return { rootId: root.cert_id, root, issuers: children, leaf, all: [root, ...children] }
    })
    .sort((a, b) => a.rootId - b.rootId)
})

/** 末级证书若挂在用户名下，把归属显示出来（链上的 CA 本身没有归属） */
function rowOwnerText(row: ChainRow): string {
  return row.leaf.owner_name || ''
}

/**
 * 链名称：优先用根证书的名字，并对后端生成的固定名字做一次友好化。
 *
 * 名字必须**稳定**（同一条链每次渲染结果一致），所以只做确定性映射，
 * 不掺入时间、序号之类会变的东西。
 */
function chainName(row: ChainRow): string {
  const raw = (row.root.cert_name || '').trim()
  if (!raw) return `证书链 #${row.rootId}`
  if (raw === 'Bastion Internal Root CA') return '堡垒机内部根 CA'
  if (raw === 'Bastion Internal Issuer CA') return '堡垒机内部签发 CA'
  return raw
}

/** 查询：按证书链名称 / 末级证书 DN 关键字过滤（链是整体概念，故在前端过滤） */
const filteredRows = computed<ChainRow[]>(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return chainRows.value
  return chainRows.value.filter((row) => {
    const hay = [
      chainName(row),
      row.root.subject_dn,
      row.leaf.subject_dn,
      row.root.cert_name,
      row.leaf.cert_name,
    ]
      .join('\n')
      .toLowerCase()
    return hay.includes(kw)
  })
})

function search(): void {
  // 过滤由 computed 完成，这里去掉首尾空白即可（保留按钮以与其它页面操作一致）
  keyword.value = keyword.value.trim()
}

function resetQuery(): void {
  keyword.value = ''
}

/* ------------------------------------------------------------ 外部 CA 导入 */

const importVisible = ref(false)
const importing = ref(false)
const importPem = ref('')
const importError = ref('')

function openImport(): void {
  importPem.value = ''
  importError.value = ''
  importVisible.value = true
}

async function doImport(): Promise<void> {
  importError.value = ''
  let pem: string
  try {
    // 允许粘贴裸 base64(DER)：CA 根证书从系统证书库导出时常是这种形态
    pem = normalizeCertInput(importPem.value)
  } catch (err) {
    importError.value = toUKeyError(err).message
    return
  }
  importing.value = true
  try {
    const rec = await importExternalCA(pem)
    importVisible.value = false
    app.success(`外部 CA 已导入：${rec.subject_dn || rec.cert_name || '（无主体信息）'}`)
    await refreshAll()
  } catch (err) {
    // 原样展示后端结论（解析失败 / 已存在 / 不是 CA 证书 …）
    importError.value = err instanceof Error ? err.message : '导入失败'
  } finally {
    importing.value = false
  }
}

/* ------------------------------------------------------------------ 证书下载 */

/**
 * 下载证书（PEM 文本）。
 *
 * 用 Blob + 临时 a 标签触发，不新增后端接口：CertRecord 本来就把 cert_pem
 * 带回来了。文件名带上序列号，避免同一用户多张证书下载后互相覆盖。
 */
function downloadCert(cert: UKeyCertRecord, label?: string): void {
  if (!cert.cert_pem) {
    app.error('该记录没有证书内容（cert_pem），无法下载')
    return
  }
  const safeName = (label || cert.cert_name || 'certificate').replace(/[\\/:*?"<>|\s]+/g, '_')
  const blob = new Blob([cert.cert_pem], { type: 'application/x-pem-file' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${safeName}-${cert.serial_number || cert.cert_id}.pem`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  // 立即回收：objectURL 不释放会一直占着这块内存
  URL.revokeObjectURL(url)
}

/** 下载整条链（根 + 签发 CA 依次拼接，导入系统证书库时一次装齐） */
function downloadChain(row: ChainRow): void {
  const parts = row.all.filter((c) => c.cert_pem).map((c) => c.cert_pem as string)
  if (!parts.length) {
    app.error('该证书链没有可下载的证书内容')
    return
  }
  const safeName = chainName(row).replace(/[\\/:*?"<>|\s]+/g, '_')
  const blob = new Blob([parts.join('\n')], { type: 'application/x-pem-file' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${safeName}-chain.pem`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

/** 供模板判断"这张证书是否可下载" */
function hasPem(cert: UKeyCertRecord): boolean {
  return Boolean(cert.cert_pem)
}

/** 逐张下载：PEM 本身不可用时给出可读提示，而不是下载出一个无法导入的文件 */
function safeDownload(cert: UKeyCertRecord): void {
  try {
    // certToBase64Der 会校验证书是不是合法的 base64；不合法就在这里拦下
    certToBase64Der(cert.cert_pem || '')
    downloadCert(cert, cert.cert_name || cert.subject_dn || `cert-${cert.cert_id}`)
  } catch (err) {
    app.error(toUKeyError(err).message)
  }
}

/* ------------------------------------------------------------------ 详情弹窗 */

const detailVisible = ref(false)
const detailRow = ref<ChainRow | null>(null)
const detailExpanded = ref<number | null>(null)

function openDetail(row: ChainRow): void {
  detailRow.value = row
  detailExpanded.value = null
  detailVisible.value = true
}

/** 链内层级文案：根 CA / 签发 CA */
function levelLabel(cert: UKeyCertRecord, row: ChainRow): string {
  if (cert.cert_id === row.root.cert_id) {
    return cert.is_trusted_root ? '根 CA（信任锚）' : '根 CA'
  }
  if (cert.is_ca) return '签发 CA'
  return '证书'
}

/** 链上是否存在"未验证链"的证书（宽松登记才会出现） */
const hasUnverified = computed(() => certs.value.some((c) => c.unverified_chain === true))

/* ------------------------------------------------------------------ 展示辅助 */

function sourceLabel(source: string): string {
  return source === 'external' ? '外部证书' : '内部证书'
}

function statusLabel(status: string): string {
  const map: Record<string, string> = { active: '可用', revoked: '已吊销', expired: '已过期' }
  return map[status] || status || '—'
}

function statusTone(status: string): 'success' | 'danger' | 'warning' | 'default' {
  if (status === 'active') return 'success'
  if (status === 'revoked') return 'danger'
  if (status === 'expired') return 'warning'
  return 'default'
}

function shortDN(dn: string): string {
  if (!dn) return '—'
  return dn.length > 64 ? `…${dn.slice(-62)}` : dn
}

/**
 * 证书算法文案：统一走 ukeyAgent.certAlgorithmText ——
 * 优先用后端字段（`key_algorithm` / `signature_algorithm`），取不到再从
 * `cert_pem` 自行解析兜底，这样"证书算法"这一列永远有值。
 */
function certAlgText(cert: UKeyCertRecord): string {
  return certAlgorithmText(cert)
}

/**
 * 添加人用户名：为空时兜底成 '—'。
 *
 * 不能把 creator_name 直接丢进模板 —— 缺失时会渲染出 undefined / null，
 * 而"这张证书是谁加进来的"正是审计与追责要看的第一条线索。
 */
function creatorName(cert: UKeyCertRecord): string {
  return cert.creator_name || '—'
}

/** 该链末级证书是否为 RSA（弱算法告警用） */
function isRsaLeaf(row: ChainRow): boolean {
  return isRsaCertRecord(row.leaf)
}

/** 链台账里是否存在 RSA 证书：用于页面级弱算法告警 */
const hasRsaCert = computed(() => certs.value.some((c) => isRsaCertRecord(c)))

/**
 * 顶部状态区展示的两套内部 CA。
 *
 * 系统允许 SM2 链与 RSA 链**并存**：后端在 `internal` 里保留原有 SM2 字段，
 * 另加一组同构的 `rsa_*` 字段（字段名以最终实现为准，这里按可选处理）。
 * 任一组字段为空即表示该链尚未初始化 —— 显示"未初始化"而不是报错，
 * 因为"只初始化了一套"是完全合法的状态。
 */
const caChains = computed(() => {
  const info = caStatus.value?.internal
  return [
    {
      key: 'sm2',
      label: 'SM2 链',
      initialized: Boolean(info?.root_subject_dn || info?.issuer_subject_dn),
      rootDN: info?.root_subject_dn || '',
      rootNotAfter: info?.root_not_after || '',
      issuerDN: info?.issuer_subject_dn || '',
      issuerNotAfter: info?.issuer_not_after || '',
    },
    {
      key: 'rsa',
      label: 'RSA 链',
      initialized: Boolean(info?.rsa_root_subject_dn || info?.rsa_issuer_subject_dn),
      rootDN: info?.rsa_root_subject_dn || '',
      rootNotAfter: info?.rsa_root_not_after || '',
      issuerDN: info?.rsa_issuer_subject_dn || '',
      issuerNotAfter: info?.rsa_issuer_not_after || '',
    },
  ]
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">证书链管理</div>
        <div class="page-subtitle">
          管理与维护用户 UKey 证书的信任基础：SM2 / RSA 两套内部 CA、外部信任锚，以及各条证书链的构成。
          用户与证书的绑定关系在用户管理页的「证书链」入口维护。
        </div>
      </div>
      <div class="btn-group">
        <button class="btn" :disabled="caLoading || certLoading" @click="refreshAll">刷新</button>
      </div>
    </div>

    <div v-if="!canManage" class="card">
      <div class="card-body">
        <div class="empty">当前账号没有「{{ UKEY_CHAIN_PERMISSION }}」权限，无法查看或管理证书链。</div>
      </div>
    </div>

    <template v-else>
      <!-- 全局状态：ukey.enabled 与"CA 是否初始化"是两件不同的事，分开显示。
           本页**不提供**初始化入口（初始化位置由部署流程另行指定），只如实显示状态 -->
      <div v-if="caStatus" class="card mb-16">
        <div class="card-body">
          <div class="flex-center gap-8 mb-12" style="flex-wrap: wrap">
            <span class="form-label" style="margin: 0">证书链状态</span>
            <StatusTag
              :text="
                caStatus.enabled
                  ? 'UKey 认证已启用（ukey.enabled=true）'
                  : 'UKey 认证未启用（ukey.enabled=false）'
              "
              :tone="caStatus.enabled ? 'success' : 'warning'"
            />
            <StatusTag
              :text="caStatus.require_internal_ca ? '仅信任内部证书' : '允许外部 CA 证书'"
              :tone="caStatus.require_internal_ca ? 'info' : 'warning'"
            />
          </div>

          <!-- 两套内部 CA 并存：SM2 链与 RSA 链各显示自己的根/签发 CA。
               rsa_* 字段为空即表示该链尚未初始化，显示"未初始化"而不是报错 -->
          <div class="table-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th style="width: 150px">内部 CA</th>
                  <th style="width: 120px">状态</th>
                  <th>根 CA 主体</th>
                  <th style="width: 160px">根 CA 到期</th>
                  <th>签发 CA 主体</th>
                  <th style="width: 160px">签发 CA 到期</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="chain in caChains" :key="chain.key">
                  <td>
                    <span class="tag" :class="{ 'tag-warning': chain.key === 'rsa' }">{{ chain.label }}</span>
                  </td>
                  <td>
                    <StatusTag
                      :text="chain.initialized ? '已初始化' : '未初始化'"
                      :tone="chain.initialized ? 'success' : 'default'"
                    />
                  </td>
                  <td class="text-secondary" style="font-size: 12px" :title="chain.rootDN">
                    {{ chain.rootDN ? shortDN(chain.rootDN) : '—' }}
                  </td>
                  <td class="text-secondary" style="font-size: 12px">
                    {{ chain.rootNotAfter ? formatTime(chain.rootNotAfter) : '—' }}
                  </td>
                  <td class="text-secondary" style="font-size: 12px" :title="chain.issuerDN">
                    {{ chain.issuerDN ? shortDN(chain.issuerDN) : '—' }}
                  </td>
                  <td class="text-secondary" style="font-size: 12px">
                    {{ chain.issuerNotAfter ? formatTime(chain.issuerNotAfter) : '—' }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div
            v-if="!caStatus.initialized"
            class="text-secondary mt-8"
            style="font-size: 12.5px; line-height: 1.7"
          >
            内部 CA 尚未初始化：用户暂时无法获得由内部 CA 签发的证书。
            初始化入口不在本页（由部署流程另行指定位置）。
          </div>
          <div
            v-if="!caStatus.enabled"
            class="text-warning mt-8"
            style="font-size: 12.5px; line-height: 1.7"
          >
            配置项 <span class="text-mono">ukey.enabled</span> 为 false：即使证书已就绪，
            服务端仍会拒绝一切 UKey 登录。
          </div>
        </div>
      </div>

      <!-- RSA 弱算法告警：硬件能力限制导致 RSA 只能用 SHA-1，必须显式告出 -->
      <div
        v-if="hasRsaCert"
        class="card mb-16"
        style="border-color: var(--color-warning)"
      >
        <div class="card-body" style="font-size: 12.5px; line-height: 1.7">
          <StatusTag text="弱算法" tone="warning" />
          <span style="margin-left: 6px">
            当前存在 <b>RSA</b> 证书链。受 UKey 中间件算法能力限制，RSA 客户端签名只能使用
            <b>RSA-SHA1</b>（中间件不提供 RSA-SHA256），安全性弱于 SM2；
            建议仅用于兼容存量 RSA 密钥，条件允许时迁移到 SM2 链。
          </span>
        </div>
      </div>

      <!-- 查询区 -->
      <div class="flex-center gap-8 mb-12" style="flex-wrap: wrap">
        <input
          v-model.trim="keyword"
          class="input wide"
          placeholder="证书链名称 / 末级证书 DN"
          @keyup.enter="search"
        />
        <button class="btn btn-primary" @click="search">查询</button>
        <button class="btn" @click="resetQuery">重置</button>
        <span class="text-muted">共 {{ filteredRows.length }} 条证书链</span>
        <span style="flex: 1" />
        <button class="btn btn-primary" @click="openImport">导入外部 CA</button>
      </div>

      <DataTable
        :columns="columns"
        :loading="certLoading || caLoading"
        :empty="!certLoading && !caLoading && filteredRows.length === 0"
        empty-text="暂无证书链：内部 CA 可能尚未初始化，或尚未导入外部 CA 根证书"
      >
        <tr v-for="row in filteredRows" :key="row.rootId">
          <td>
            <div>{{ chainName(row) }}</div>
            <div class="text-muted" style="font-size: 11.5px">
              {{ row.all.length }} 张证书<template v-if="rowOwnerText(row)">
                · 归属 {{ rowOwnerText(row) }}</template
              >
            </div>
          </td>
          <td>
            <span class="tag">{{ sourceLabel(row.root.cert_source) }}</span>
            <div v-if="row.root.is_trusted_root" class="text-muted" style="font-size: 11.5px">
              信任锚
            </div>
          </td>
          <td class="text-secondary" style="font-size: 12.5px" :title="row.leaf.creator_name || ''">
            {{ creatorName(row.leaf) }}
          </td>
          <td>
            <span class="tag" :class="{ 'tag-warning': isRsaLeaf(row) }">{{ certAlgText(row.leaf) }}</span>
          </td>
          <td>
            <div :title="row.leaf.subject_dn">{{ shortDN(row.leaf.subject_dn) }}</div>
            <div v-if="row.leaf.cert_name" class="text-muted" style="font-size: 11.5px">
              {{ row.leaf.cert_name }}
            </div>
          </td>
          <td class="text-secondary" style="font-size: 12px">
            {{ formatTime(row.leaf.not_before || undefined) }}
          </td>
          <td class="text-secondary" style="font-size: 12px">
            {{ formatTime(row.leaf.not_after || undefined) }}
          </td>
          <td>
            <StatusTag :text="statusLabel(row.leaf.status)" :tone="statusTone(row.leaf.status)" />
            <div v-if="row.leaf.unverified_chain" class="mt-8">
              <StatusTag
                text="未验证链"
                tone="danger"
              />
            </div>
          </td>
          <td class="col-actions">
            <button class="btn-link" @click="openDetail(row)">详情</button>
            <button class="btn-link" :disabled="!hasPem(row.leaf)" @click="downloadChain(row)">
              下载
            </button>
          </td>
        </tr>
      </DataTable>

      <!-- 宽松登记的证书要能一眼看到：当前配置下不应出现，改配置后可能出现 -->
      <div
        v-if="hasUnverified"
        class="card mt-16"
        style="border-color: var(--color-danger)"
      >
        <div class="card-body" style="font-size: 12.5px; line-height: 1.7">
          <StatusTag text="注意" tone="danger" />
          <span style="margin-left: 6px">
            存在<b>未经验证链</b>的证书记录：这类证书由管理员手工登记、未经证书链校验
            （服务端配置 <span class="text-mono">ukey.require_internal_ca=false</span> 时会产生）。
            若不需要，请吊销对应证书或改回严格模式。
          </span>
        </div>
      </div>
    </template>

    <!-- ============================== 详情 ============================== -->
    <Modal
      v-model="detailVisible"
      size="lg"
      :title="detailRow ? `证书链详情 · ${chainName(detailRow)}` : '证书链详情'"
    >
      <template v-if="detailRow">
        <div class="desc-list mb-12">
          <div class="desc-item">
            <div class="desc-label">证书链名称</div>
            <div class="desc-value">{{ chainName(detailRow) }}</div>
          </div>
          <div class="desc-item">
            <div class="desc-label">内外部证书</div>
            <div class="desc-value">{{ sourceLabel(detailRow.root.cert_source) }}</div>
          </div>
          <div class="desc-item">
            <div class="desc-label">链内证书数</div>
            <div class="desc-value">{{ detailRow.all.length }}</div>
          </div>
        </div>

        <div class="form-label">证书链列表</div>
        <div class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th style="width: 130px">层级</th>
                <th style="width: 90px">算法</th>
                <th style="width: 110px">添加人</th>
                <th>主体 DN</th>
                <th style="width: 160px">序列号</th>
                <th style="width: 150px">生效 / 过期</th>
                <th style="width: 100px">状态</th>
                <th style="width: 130px; text-align: right">操作</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="cert in detailRow.all" :key="cert.cert_id">
                <tr>
                  <td>
                    <span class="tag tag-info">{{ levelLabel(cert, detailRow) }}</span>
                    <div v-if="cert.unverified_chain" class="mt-8">
                      <StatusTag text="未验证链" tone="danger" />
                    </div>
                  </td>
                  <td>
                    <span class="tag" :class="{ 'tag-warning': certAlgText(cert) === 'RSA' }">
                      {{ certAlgText(cert) }}
                    </span>
                  </td>
                  <td class="text-secondary" style="font-size: 12px" :title="cert.creator_name || ''">
                    {{ creatorName(cert) }}
                  </td>
                  <td>
                    <div :title="cert.subject_dn">{{ shortDN(cert.subject_dn) }}</div>
                    <div class="text-muted" style="font-size: 11.5px" :title="cert.issuer_dn">
                      签发者：{{ shortDN(cert.issuer_dn) }}
                    </div>
                    <div v-if="cert.revoke_reason" class="text-danger" style="font-size: 11.5px">
                      吊销原因：{{ cert.revoke_reason }}
                      <template v-if="cert.revoke_time">（{{ formatTime(cert.revoke_time) }}）</template>
                    </div>
                  </td>
                  <td class="text-mono" style="font-size: 11.5px">{{ cert.serial_number || '—' }}</td>
                  <td class="text-secondary" style="font-size: 11.5px">
                    <div>{{ formatTime(cert.not_before || undefined) }}</div>
                    <div>至 {{ formatTime(cert.not_after || undefined) }}</div>
                  </td>
                  <td>
                    <StatusTag :text="statusLabel(cert.status)" :tone="statusTone(cert.status)" />
                  </td>
                  <td class="col-actions">
                    <button
                      class="btn-link"
                      @click="detailExpanded = detailExpanded === cert.cert_id ? null : cert.cert_id"
                    >
                      {{ detailExpanded === cert.cert_id ? '收起' : '查看' }}
                    </button>
                    <button class="btn-link" :disabled="!hasPem(cert)" @click="safeDownload(cert)">
                      下载
                    </button>
                  </td>
                </tr>
                <tr v-if="detailExpanded === cert.cert_id" :key="`pem-${cert.cert_id}`">
                  <td colspan="8">
                    <div class="result-panel text-mono" style="font-size: 11.5px; max-height: 220px">
                      {{ cert.cert_pem || '（后端未返回证书内容，可能出于体积考虑省略）' }}
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>

        <div class="text-muted mt-8" style="font-size: 11.5px; line-height: 1.6">
          「未验证链」表示该证书由管理员手工登记、未通过证书链校验；正常配置
          （<span class="text-mono">ukey.require_internal_ca=true</span>）下不会出现。
        </div>
      </template>
      <template #footer>
        <button class="btn" @click="detailRow && downloadChain(detailRow)">下载整条链</button>
        <button class="btn" @click="detailVisible = false">关闭</button>
      </template>
    </Modal>

    <!-- 导入外部 CA -->
    <Modal v-model="importVisible" title="导入外部 CA 根证书" size="lg" :close-on-mask="false">
      <div class="text-secondary mb-12" style="font-size: 12.5px; line-height: 1.7">
        粘贴外部 CA 的<b>根证书</b>（PEM 文本，或直接粘贴 base64 的 DER 内容，这里会自动转成 PEM）。
        导入后该 CA 签发的用户证书可登记，登记时证书来源需选择「外部 CA 签发」。
        <br />
        注意：若配置为 <span class="text-mono">ukey.require_internal_ca=true</span>，
        外部 CA 证书仍会被拒绝登记 —— 那种情况应改用「重新签发并写入 UKey」。
      </div>
      <div class="form-item">
        <label class="form-label">根证书内容<span class="required">*</span></label>
        <textarea
          v-model="importPem"
          class="textarea text-mono"
          rows="9"
          placeholder="-----BEGIN CERTIFICATE-----&#10;…&#10;-----END CERTIFICATE-----"
        />
      </div>
      <div v-if="importError" class="text-danger" style="font-size: 12.5px; line-height: 1.6">
        {{ importError }}
      </div>
      <template #footer>
        <button class="btn" :disabled="importing" @click="importVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="importing || !importPem.trim()" @click="doImport">
          <span v-if="importing" class="spinner light" />
          {{ importing ? '导入中…' : '导入' }}
        </button>
      </template>
    </Modal>
  </div>
</template>
