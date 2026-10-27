<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  BOND_MODES,
  cancelAdvanced,
  confirmAdvanced,
  createBond,
  createBridge,
  createVlan,
  deleteAdvancedInterface,
  getAdvancedPending,
  getNetworkSnapshot,
  updateAdvancedMembers,
  type AdvancedChange,
  type BridgeRequest,
  type BondRequest,
  type NetworkIface,
  type NetworkSnapshot,
  type VlanRequest,
} from '@/api/network'
import Modal from '@/components/Modal.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TagTone } from '@/utils/constants'

/**
 * 高级网络配置：链路聚合 / VLAN 子接口 / 网桥。
 *
 * 与单网口配置（NetworkConfig.vue）共用同一套"改错能回来"的安全模型，
 * 但风险面更大，因此界面上有三处必须讲清楚：
 *
 *   1. **多文件变更**：建 bond0 要同时写 bond0 与每个成员的 ifcfg，
 *      所以回滚也是整体的 —— 不会留下"成员已改、聚合不存在"的中间状态；
 *   2. **成员的管理口会被清掉地址**：把正在承载当前连接的网口加进聚合/网桥，
 *      等于立刻切断自己的访问通道，必须显式勾选确认；
 *   3. **桥接是串联部署的可选能力**：业务流量物理经过本设备，配错直接断链路，
 *      所以桥接一律要求确认，而不仅当成员是管理口时。
 */
const app = useAppStore()
const auth = useAuthStore()

/**
 * 是否有权改动高级网络配置。
 *
 * 聚合 / VLAN / 网桥的创建、改成员、删除都能让设备直接失联
 * （改错要进机房），服务端写接口要求 system:config:write；
 * 界面据此收敛入口，避免"点下去才发现没有权限"。
 */
const canWrite = computed(() => auth.has('system:config:write'))

type Kind = 'bond' | 'vlan' | 'bridge'

/** 类型中文名与说明，列表与弹窗标题共用 */
const KIND_LABEL: Record<Kind, string> = {
  bond: '链路聚合',
  vlan: 'VLAN 子接口',
  bridge: '网桥',
}

const KIND_TONE: Record<Kind, 'primary' | 'info' | 'warning'> = {
  bond: 'primary',
  vlan: 'info',
  bridge: 'warning',
}

/** 高级接口类型集合：后端 TYPE 字段小写化后的取值 */
const ADVANCED_TYPES = ['bond', 'vlan', 'bridge']

const loading = ref(false)
const snapshot = ref<NetworkSnapshot | null>(null)

/** 当前待确认的高级变更；非空时顶部显示倒计时条 */
const pending = ref<AdvancedChange | null>(null)
const remainSeconds = ref(0)
let tickTimer: number | undefined

const activeKind = ref<Kind>('bond')
const dialogVisible = ref(false)
const submitting = ref(false)
/** 高风险操作的知情确认勾选 */
const riskAcknowledged = ref(false)

interface IpForm {
  bootproto: 'none' | 'static' | 'dhcp'
  ipaddr: string
  prefix: number
  gateway: string
  dns: string
  mtu: number
}

function blankIP(): IpForm {
  return { bootproto: 'none', ipaddr: '', prefix: 24, gateway: '', dns: '', mtu: 1500 }
}

const bondForm = reactive({
  name: 'bond0',
  mode: 'active-backup',
  members: [] as string[],
  ip: blankIP(),
})

const vlanForm = reactive({
  parent: '',
  vlan_id: 100,
  ip: blankIP(),
})

const bridgeForm = reactive({
  name: 'br0',
  members: [] as string[],
  stp: false,
  ip: blankIP(),
})

/** 当前编辑的表单 IP 段（三种类型共用同一套字段） */
const activeIP = computed<IpForm>(() => {
  if (activeKind.value === 'bond') return bondForm.ip
  if (activeKind.value === 'vlan') return vlanForm.ip
  return bridgeForm.ip
})

/** 已有高级接口（聚合 / VLAN / 网桥） */
const advancedIfaces = computed(() =>
  (snapshot.value?.interfaces ?? []).filter((i) => isAdvanced(i)),
)

/** 物理网口：可作聚合成员、桥接成员 */
const physicalIfaces = computed(() =>
  (snapshot.value?.interfaces ?? []).filter(
    (i) => !i.is_loopback && !i.is_virtual && !isAdvanced(i),
  ),
)

/** VLAN 父接口：物理网口或聚合接口（在 bond 上划 VLAN 是常见做法），排除 VLAN 自身 */
const parentCandidates = computed(() =>
  (snapshot.value?.interfaces ?? []).filter(
    (i) => !i.is_loopback && !i.is_virtual && i.type !== 'vlan',
  ),
)

function isAdvanced(i: NetworkIface): boolean {
  return ADVANCED_TYPES.includes((i.type || '').toLowerCase())
}

/** 类型中文名：后端返回的 TYPE 可能是 ethernet 等意外值，兜底显示原文 */
function kindLabel(type: string): string {
  const key = (type || '').toLowerCase() as Kind
  return KIND_LABEL[key] || type || '—'
}

function kindTone(type: string): TagTone {
  const key = (type || '').toLowerCase() as Kind
  return KIND_TONE[key] || 'default'
}

function findIface(name: string): NetworkIface | undefined {
  return (snapshot.value?.interfaces ?? []).find((i) => i.name === name)
}

function fmtAddresses(list: string[]): string {
  return list && list.length ? list.join('、') : '—'
}

/** 当前表单选中的成员 / 父接口 */
const selectedNames = computed<string[]>(() => {
  if (activeKind.value === 'bond') return bondForm.members
  if (activeKind.value === 'bridge') return bridgeForm.members
  return vlanForm.parent ? [vlanForm.parent] : []
})

/** 选中的网口中承载当前管理连接的，提交前必须显式确认 */
const riskIfaces = computed(() =>
  selectedNames.value
    .map((n) => findIface(n))
    .filter((i): i is NetworkIface => Boolean(i?.is_management)),
)

/** VLAN 的父接口没有配置文件时，后端会补写一份基础 ifcfg（不分配地址） */
const parentWithoutConfig = computed(
  () => activeKind.value === 'vlan' && Boolean(vlanForm.parent) && !findIface(vlanForm.parent)?.has_config_file,
)

const bridgeWarning = computed(() => activeKind.value === 'bridge')
const needRiskAck = computed(() => riskIfaces.value.length > 0 || bridgeWarning.value)

/** 预览 VLAN 子接口名（后端留空时按 <父接口>.<VLANID> 生成） */
const vlanPreviewName = computed(() => {
  if (!vlanForm.parent) return '—'
  const vid = Number(vlanForm.vlan_id)
  return vid ? `${vlanForm.parent}.${vid}` : vlanForm.parent
})

const modeHint = computed(
  () => BOND_MODES.find((m) => m.value === bondForm.mode)?.hint ?? '',
)

/* ------------------------------------------------------------ 数据加载 */

async function load(): Promise<void> {
  loading.value = true
  try {
    snapshot.value = await getNetworkSnapshot()
  } catch {
    snapshot.value = null
  } finally {
    loading.value = false
  }
}

async function loadPending(): Promise<void> {
  try {
    const res = await getAdvancedPending()
    const first = res.items?.[0] ?? null
    if (first) startCountdown(first)
  } catch {
    /* 无待确认变更或接口不可用，不影响主流程 */
  }
}

async function reload(): Promise<void> {
  await load()
  void loadPending()
}

function startCountdown(change: AdvancedChange): void {
  pending.value = change
  updateRemain()
  if (tickTimer !== undefined) window.clearInterval(tickTimer)
  tickTimer = window.setInterval(() => {
    updateRemain()
    if (remainSeconds.value <= 0 && tickTimer !== undefined) {
      window.clearInterval(tickTimer)
      tickTimer = undefined
      app.info('确认超时，配置已自动恢复')
      pending.value = null
      void reload()
    }
  }, 1000)
}

function updateRemain(): void {
  if (!pending.value) {
    remainSeconds.value = 0
    return
  }
  const diff = Math.ceil((new Date(pending.value.deadline).getTime() - Date.now()) / 1000)
  remainSeconds.value = diff > 0 ? diff : 0
}

onBeforeUnmount(() => {
  if (tickTimer !== undefined) window.clearInterval(tickTimer)
})

onMounted(async () => {
  await load()
  void loadPending()
})

/* -------------------------------------------------------------- 表单 */

function openCreate(kind: Kind): void {
  activeKind.value = kind
  riskAcknowledged.value = false
  if (kind === 'bond') {
    bondForm.name = 'bond0'
    bondForm.mode = 'active-backup'
    bondForm.members = []
    bondForm.ip = blankIP()
  } else if (kind === 'vlan') {
    // 默认选中第一个物理网口，避免用户面对空下拉框
    vlanForm.parent = physicalIfaces.value[0]?.name ?? parentCandidates.value[0]?.name ?? ''
    vlanForm.vlan_id = 100
    vlanForm.ip = blankIP()
  } else {
    bridgeForm.name = 'br0'
    bridgeForm.members = []
    bridgeForm.stp = false
    bridgeForm.ip = blankIP()
  }
  dialogVisible.value = true
}

/* -------------------------------------------------------------- 校验 */

function isIPv4(s: string): boolean {
  const parts = s.split('.')
  if (parts.length !== 4) return false
  return parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255)
}

function ipToInt(s: string): number {
  const p = s.split('.').map(Number)
  return (((p[0] << 24) >>> 0) + (p[1] << 16) + (p[2] << 8) + p[3]) >>> 0
}

function sameSubnet(a: string, b: string, prefix: number): boolean {
  const mask = prefix <= 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
  return (ipToInt(a) & mask) === (ipToInt(b) & mask)
}

function parseDNS(s: string): string[] {
  return s
    .split(/[,，\s]+/)
    .map((v) => v.trim())
    .filter(Boolean)
}

/** 与服务端 validateIPConfig 同规则，先在本地拦住明显错误，减少一次往返 */
function validateIP(f: IpForm): string | null {
  if (f.bootproto === 'static') {
    const ip = f.ipaddr.trim()
    if (!ip) return '静态配置必须填写 IP 地址'
    if (!isIPv4(ip)) return `IP 地址格式不正确：${ip}`
    // 输入框清空后 v-model.number 会留下空串，必须按数值判断，
    // 否则 NaN 与所有比较都为 false，会一路漏到服务端
    const prefix = Number(f.prefix)
    if (!Number.isInteger(prefix) || prefix < 1 || prefix > 32) {
      return '子网前缀应在 1~32 之间'
    }
    const gw = f.gateway.trim()
    if (gw) {
      if (!isIPv4(gw)) return `网关地址格式不正确：${gw}`
      if (!sameSubnet(ip, gw, prefix)) return `网关 ${gw} 与本机地址 ${ip}/${prefix} 不在同一网段`
    }
  }
  for (const d of parseDNS(f.dns)) {
    if (!isIPv4(d)) return `DNS 地址格式不正确：${d}`
  }
  const mtu = Number(f.mtu) || 0
  if (mtu !== 0 && (mtu < 576 || mtu > 9000)) return 'MTU 应在 576~9000 之间'
  return null
}

function validateForm(): string | null {
  if (activeKind.value === 'bond') {
    const name = bondForm.name.trim() || 'bond0'
    if (!/^bond\d+$/i.test(name)) return '聚合接口名应形如 bond0'
    if (bondForm.members.length < 2) {
      return `链路聚合至少需要两个成员网口（当前 ${bondForm.members.length} 个）`
    }
  } else if (activeKind.value === 'vlan') {
    if (!vlanForm.parent) return '请选择父接口'
    const vid = Number(vlanForm.vlan_id)
    if (!Number.isInteger(vid) || vid < 1 || vid > 4094) return 'VLAN ID 应在 1~4094 之间'
  } else {
    const name = bridgeForm.name.trim() || 'br0'
    if (!/^br\d+$/i.test(name)) return '网桥名应形如 br0'
    if (bridgeForm.members.length === 0) return '请至少选择一个桥接成员网口'
  }
  return validateIP(activeIP.value)
}

/** 组装 IP 参数：bootproto 为空表示"暂不配置地址"，此时服务端按 none 写入 */
function ipPayload(f: IpForm) {
  const mtu = Number(f.mtu) || 0
  const stat = f.bootproto === 'static'
  return {
    bootproto: f.bootproto === 'none' ? undefined : f.bootproto,
    ipaddr: stat ? f.ipaddr.trim() : undefined,
    prefix: stat ? Number(f.prefix) : undefined,
    gateway: stat ? f.gateway.trim() || undefined : undefined,
    dns: parseDNS(f.dns),
    mtu: mtu > 0 ? mtu : undefined,
  }
}

/* -------------------------------------------------------------- 提交 */

async function submit(): Promise<void> {
  if (needRiskAck.value && !riskAcknowledged.value) {
    app.error('请先勾选确认，表示你了解该操作的风险')
    return
  }
  const invalid = validateForm()
  if (invalid) {
    app.error(invalid)
    return
  }

  submitting.value = true
  try {
    let change: AdvancedChange
    if (activeKind.value === 'bond') {
      const body: BondRequest = {
        name: bondForm.name.trim() || 'bond0',
        mode: bondForm.mode,
        members: [...bondForm.members],
        ...ipPayload(bondForm.ip),
        force: needRiskAck.value,
      }
      change = await createBond(body)
    } else if (activeKind.value === 'vlan') {
      const body: VlanRequest = {
        parent: vlanForm.parent,
        vlan_id: Number(vlanForm.vlan_id),
        ...ipPayload(vlanForm.ip),
        force: needRiskAck.value,
      }
      change = await createVlan(body)
    } else {
      const body: BridgeRequest = {
        name: bridgeForm.name.trim() || 'br0',
        members: [...bridgeForm.members],
        stp: bridgeForm.stp,
        ...ipPayload(bridgeForm.ip),
        force: needRiskAck.value,
      }
      change = await createBridge(body)
    }
    dialogVisible.value = false
    startCountdown(change)
    app.success(
      `${KIND_LABEL[activeKind.value]}已生效，请在 ${change.timeout_seconds} 秒内确认；未确认将自动恢复原配置`,
    )
    await load()
  } catch (error) {
    app.error(error instanceof Error ? error.message : '配置提交失败')
  } finally {
    submitting.value = false
  }
}

async function doConfirm(): Promise<void> {
  if (!pending.value) return
  try {
    await confirmAdvanced(pending.value.change_id)
    if (tickTimer !== undefined) window.clearInterval(tickTimer)
    tickTimer = undefined
    pending.value = null
    app.success('配置已确认')
    await reload()
  } catch (error) {
    app.error(error instanceof Error ? error.message : '确认失败')
  }
}

async function doCancel(): Promise<void> {
  if (!pending.value) return
  try {
    await cancelAdvanced(pending.value.change_id)
    if (tickTimer !== undefined) window.clearInterval(tickTimer)
    tickTimer = undefined
    pending.value = null
    app.success('已恢复原配置')
    await reload()
  } catch (error) {
    app.error(error instanceof Error ? error.message : '回滚失败')
  }
}

/* ------------------------------------------------- 改成员 / 删除 */

/**
 * 改成员与删除共用的一个事实：成员关系只由成员自己的 ifcfg 声明
 * （MASTER= / BRIDGE=），上层接口不记录成员。所以"移出成员"做不到
 * 还原它加入前的配置 —— 那时就已被覆盖。界面上必须说明这一点，
 * 而不是让用户以为可以随便加加减减。
 */
const membersDialog = ref(false)
const membersTarget = ref<NetworkIface | null>(null)
const membersSelection = ref<string[]>([])
const membersSubmitting = ref(false)
const membersRiskAck = ref(false)

const deleteDialog = ref(false)
const deleteTarget = ref<NetworkIface | null>(null)
const deleteSubmitting = ref(false)
const deleteRiskAck = ref(false)

/** 可参与改成员的网口：物理网口 + 该接口当前的成员 */
const memberCandidates = computed<NetworkIface[]>(() => {
  const target = membersTarget.value
  if (!target) return []
  const list = [...physicalIfaces.value]
  for (const name of target.members ?? []) {
    if (!list.some((i) => i.name === name)) {
      const found = findIface(name)
      if (found) list.push(found)
    }
  }
  return list
})

/** 勾选结果与当前成员的差集 */
const membersDiff = computed(() => {
  const before = new Set(membersTarget.value?.members ?? [])
  const after = new Set(membersSelection.value)
  return {
    add: [...after].filter((n) => !before.has(n)),
    remove: [...before].filter((n) => !after.has(n)),
  }
})

const membersKind = computed<'bond' | 'bridge'>(() =>
  membersTarget.value?.type === 'bridge' ? 'bridge' : 'bond',
)

/** 选中的网口里承载当前管理连接的 */
const membersRiskIfaces = computed(() =>
  membersSelection.value
    .map((n) => findIface(n))
    .filter((i): i is NetworkIface => Boolean(i?.is_management)),
)
const membersNeedRiskAck = computed(() => membersRiskIfaces.value.length > 0)

/** 删除时会被一并脱离的成员 */
const deleteMembers = computed<NetworkIface[]>(() => {
  const target = deleteTarget.value
  if (!target || !target.members?.length) return []
  return target.members
    .map((n) => findIface(n))
    .filter((i): i is NetworkIface => Boolean(i))
})

/** 删除风险：接口自身是管理口，或成员里有管理口 */
const deleteNeedRiskAck = computed(() => {
  const target = deleteTarget.value
  if (!target) return false
  return Boolean(target.is_management) || deleteMembers.value.some((i) => i.is_management)
})

function openMembers(iface: NetworkIface): void {
  membersTarget.value = iface
  membersSelection.value = [...(iface.members ?? [])]
  membersRiskAck.value = false
  membersDialog.value = true
}

function openDelete(iface: NetworkIface): void {
  deleteTarget.value = iface
  deleteRiskAck.value = false
  deleteDialog.value = true
}

async function submitMembers(): Promise<void> {
  const target = membersTarget.value
  if (!target) return
  const kind = membersKind.value
  const diff = membersDiff.value
  if (!diff.add.length && !diff.remove.length) {
    app.error('成员没有变化')
    return
  }
  if (kind === 'bond' && membersSelection.value.length < 2) {
    app.error('链路聚合至少需要两个成员；如需删除聚合请使用「删除」')
    return
  }
  if (kind === 'bridge' && membersSelection.value.length < 1) {
    app.error('网桥至少需要一个成员；如需删除网桥请使用「删除」')
    return
  }
  if (membersNeedRiskAck.value && !membersRiskAck.value) {
    app.error('请先勾选确认，表示你了解该操作的风险')
    return
  }

  membersSubmitting.value = true
  try {
    const change = await updateAdvancedMembers({
      kind,
      name: target.name,
      add: diff.add,
      remove: diff.remove,
      force: membersNeedRiskAck.value,
    })
    membersDialog.value = false
    startCountdown(change)
    app.success(
      `成员已更新并生效，请在 ${change.timeout_seconds} 秒内确认；未确认将自动恢复原配置`,
    )
    await load()
  } catch (error) {
    app.error(error instanceof Error ? error.message : '成员修改失败')
  } finally {
    membersSubmitting.value = false
  }
}

async function submitDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target) return
  if (deleteNeedRiskAck.value && !deleteRiskAck.value) {
    app.error('请先勾选确认，表示你了解该操作的风险')
    return
  }

  deleteSubmitting.value = true
  try {
    const change = await deleteAdvancedInterface({
      name: target.name,
      force: deleteNeedRiskAck.value,
    })
    deleteDialog.value = false
    startCountdown(change)
    app.success(
      `${target.name} 已删除，请在 ${change.timeout_seconds} 秒内确认；未确认将自动恢复`,
    )
    await load()
  } catch (error) {
    app.error(error instanceof Error ? error.message : '删除失败')
  } finally {
    deleteSubmitting.value = false
  }
}
</script>

<template>
  <div class="card mb-16">
    <div class="card-header">
      <span class="card-title">高级网络配置</span>
      <div class="page-menu" role="toolbar" aria-label="高级网络配置">
        <button class="btn" type="button" :disabled="loading" @click="reload">刷新</button>
        <template v-if="canWrite">
          <button class="btn" type="button" @click="openCreate('vlan')">新建 VLAN 子接口</button>
          <button class="btn" type="button" @click="openCreate('bridge')">新建网桥</button>
          <button class="btn btn-primary" type="button" @click="openCreate('bond')">新建链路聚合</button>
        </template>
      </div>
    </div>

    <!-- 待确认倒计时：必须置于最显眼位置 -->
    <div
      v-if="pending"
      class="card-body"
      style="background: #fffbeb; border-bottom: 1px solid var(--color-border)"
    >
      <div class="flex-between" style="align-items: center; gap: 12px; flex-wrap: wrap">
        <div>
          <StatusTag text="等待确认" tone="warning" />
          <StatusTag :text="kindLabel(pending.kind)" :tone="kindTone(pending.kind)" />
          <strong style="margin-left: 8px">{{ pending.interface }}</strong>
          <span class="text-secondary" style="margin-left: 8px">
            配置已生效，{{ remainSeconds }} 秒后未确认将自动恢复原配置
          </span>
        </div>
        <div v-if="canWrite" class="btn-group">
          <button class="btn btn-primary btn-sm" @click="doConfirm">确认配置无误</button>
          <button class="btn btn-sm" @click="doCancel">立即恢复</button>
        </div>
      </div>
      <div class="text-secondary" style="font-size: 12px; margin-top: 6px; line-height: 1.7">
        本次变更涉及 <strong>{{ pending.files.length }}</strong> 个配置文件（
        <span class="text-mono">{{ pending.files.join('、') }}</span>
        ）—— 它们作为一组同时生效，回滚时也会一起恢复，不会只还原一半。
      </div>
      <div
        v-if="pending.deleted?.length"
        class="text-secondary"
        style="font-size: 12px; margin-top: 4px"
      >
        其中被<strong>删除</strong>的文件：
        <span class="text-mono">{{ pending.deleted.join('、') }}</span>
        —— 回滚时会把原内容写回。
      </div>
      <div v-if="pending.is_management" class="text-danger" style="font-size: 13px; margin-top: 6px">
        本次变更涉及多个配置文件，服务端按保守策略标记为<strong>可能影响管理通道</strong>
        —— 若选中的成员里有正在承载当前连接的网口，远程访问会立即中断。
        若页面仍能正常操作，说明配置有效；若你已从其它网络访问，请直接点「确认配置无误」。
      </div>
    </div>

    <div class="card-body" style="padding-top: 12px; padding-bottom: 12px">
      <span class="text-secondary" style="font-size: 12px">
        <template v-if="canWrite">
          聚合做链路冗余，VLAN 在同一物理口上划分多个网段，网桥用于串联部署
        </template>
        <template v-else>
          只读：修改网络配置需要「系统配置修改」权限（系统管理员），请联系管理员操作
        </template>
      </span>
    </div>

    <div class="table-wrap">
      <table class="table">
        <thead>
          <tr>
            <th style="width: 140px">接口</th>
            <th style="width: 100px">类型</th>
            <th style="width: 210px">成员 / 父接口</th>
            <th style="width: 180px">地址</th>
            <th>配置文件</th>
            <th style="width: 150px">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading">
            <td colspan="6" class="table-empty"><span class="spinner" /> 正在读取网络信息…</td>
          </tr>
          <tr v-else-if="!advancedIfaces.length">
            <td colspan="6" class="table-empty">
              当前设备上没有链路聚合、VLAN 子接口或网桥。
            </td>
          </tr>
          <tr v-for="iface in advancedIfaces" :key="iface.name">
            <td>
              <strong>{{ iface.name }}</strong>
              <StatusTag
                v-if="iface.is_management"
                text="管理口"
                tone="danger"
                style="margin-left: 6px"
              />
              <div class="text-secondary" style="font-size: 12px; margin-top: 2px">
                {{ iface.state === 'UP' ? '已启用' : iface.state }}
              </div>
            </td>
            <td>
              <StatusTag :text="kindLabel(iface.type)" :tone="kindTone(iface.type)" />
            </td>
            <td style="font-size: 13px">
              <!-- 成员关系由服务端从成员 ifcfg 的 MASTER / BRIDGE 反向聚合得到 -->
              <template v-if="iface.type === 'vlan'">
                父接口 <span class="text-mono">{{ iface.parent || '—' }}</span>
              </template>
              <template v-else-if="iface.members?.length">
                <span class="text-mono">{{ iface.members.join('、') }}</span>
                <div class="text-secondary" style="font-size: 12px">
                  {{ iface.members.length }} 个成员
                </div>
              </template>
              <span v-else class="text-secondary">未解析出成员</span>
            </td>
            <td class="text-mono" style="font-size: 13px">{{ fmtAddresses(iface.addresses) }}</td>
            <td class="text-mono text-secondary" style="font-size: 12px; word-break: break-all">
              {{ iface.config_path || '无配置文件' }}
            </td>
            <td>
              <div v-if="canWrite" class="btn-group">
                <button
                  v-if="iface.type === 'bond' || iface.type === 'bridge'"
                  class="btn btn-sm"
                  @click="openMembers(iface)"
                >
                  改成员
                </button>
                <button class="btn btn-link danger" @click="openDelete(iface)">删除</button>
              </div>
              <span v-else class="text-secondary" style="font-size: 12px">只读</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="snapshot?.warnings?.length" class="card-body">
      <div
        v-for="(w, i) in snapshot.warnings"
        :key="i"
        class="text-secondary"
        style="font-size: 12px"
      >
        {{ w }}
      </div>
    </div>

    <div class="card-body text-secondary" style="font-size: 12px; line-height: 1.8">
      创建、改成员、删除遵循同一套保护：改动<strong>立即生效</strong>并进入倒计时确认，
      超时未确认由服务端把本次涉及的<strong>全部配置文件</strong>一起还原。
      删除聚合或网桥时成员会被一并写回一份最小独立配置（开机启用、暂不分配地址）——
      它们加入聚合前的配置在那一刻就已被覆盖，无法自动还原，需要随后在「网络配置」里补回地址。
    </div>

    <!-- 创建表单 -->
    <Modal v-model="dialogVisible" :title="`新建${KIND_LABEL[activeKind]}`" size="lg">
      <div class="form-grid">
        <!-- 接口名 -->
        <div v-if="activeKind !== 'vlan'" class="form-item">
          <label class="form-label">
            {{ activeKind === 'bond' ? '聚合接口名' : '网桥名' }}<span class="required">*</span>
          </label>
          <input
            v-if="activeKind === 'bond'"
            v-model.trim="bondForm.name"
            class="input text-mono"
            placeholder="bond0"
          />
          <input v-else v-model.trim="bridgeForm.name" class="input text-mono" placeholder="br0" />
          <span class="form-hint">需以 {{ activeKind === 'bond' ? 'bond' : 'br' }} 开头</span>
        </div>

        <!-- 聚合模式 -->
        <div v-if="activeKind === 'bond'" class="form-item">
          <label class="form-label">聚合模式<span class="required">*</span></label>
          <select v-model="bondForm.mode" class="select">
            <option v-for="m in BOND_MODES" :key="m.value" :value="m.value">{{ m.label }}</option>
          </select>
          <span class="form-hint">{{ modeHint }}</span>
        </div>

        <!-- VLAN 参数 -->
        <template v-if="activeKind === 'vlan'">
          <div class="form-item">
            <label class="form-label">父接口<span class="required">*</span></label>
            <select v-model="vlanForm.parent" class="select">
              <option value="">请选择父接口</option>
              <option v-for="i in parentCandidates" :key="i.name" :value="i.name">
                {{ i.name }}（{{ i.type }}）
              </option>
            </select>
            <span class="form-hint">物理网口或已有的聚合接口</span>
          </div>
          <div class="form-item">
            <label class="form-label">VLAN ID<span class="required">*</span></label>
            <input
              v-model.number="vlanForm.vlan_id"
              class="input text-mono"
              type="number"
              min="1"
              max="4094"
            />
            <span class="form-hint">范围 1~4094，将创建 {{ vlanPreviewName }}</span>
          </div>
        </template>

        <!-- 网桥 STP -->
        <div v-if="activeKind === 'bridge'" class="form-item">
          <label class="form-label">生成树（STP）</label>
          <select v-model="bridgeForm.stp" class="select">
            <option :value="false">关闭（推荐）</option>
            <option :value="true">开启</option>
          </select>
          <span class="form-hint">
            单网桥接成员时开启会引入学习期，接入后数十秒不通，默认关闭
          </span>
        </div>

        <!-- 成员选择 -->
        <div v-if="activeKind !== 'vlan'" class="form-item span-2">
          <label class="form-label">
            成员网口<span class="required">*</span>
            <span class="text-secondary" style="margin-left: 6px">
              {{ activeKind === 'bond' ? '至少两个' : '至少一个' }}
            </span>
          </label>
          <div
            v-if="physicalIfaces.length"
            class="result-panel"
            style="display: flex; flex-wrap: wrap; gap: 14px"
          >
            <label v-for="i in physicalIfaces" :key="i.name" class="checkbox">
              <input
                v-if="activeKind === 'bond'"
                v-model="bondForm.members"
                type="checkbox"
                :value="i.name"
              />
              <input v-else v-model="bridgeForm.members" type="checkbox" :value="i.name" />
              <span class="text-mono">{{ i.name }}</span>
              <StatusTag v-if="i.is_management" text="当前管理口" tone="danger" />
              <span v-else-if="i.state !== 'UP'" class="text-secondary" style="font-size: 12px">
                {{ i.state }}
              </span>
            </label>
          </div>
          <span v-else class="text-secondary" style="font-size: 13px">
            未读取到可用作成员的物理网口。
          </span>
          <span class="form-hint">
            成员的 IP 配置会被移除，地址由{{ activeKind === 'bond' ? '聚合接口' : '网桥' }}承载。
          </span>
        </div>

        <!-- 共用 IP 配置 -->
        <div class="form-item span-2" style="margin-top: 4px">
          <label class="form-label">接口地址</label>
          <div class="form-hint" style="line-height: 1.7">
            聚合 / VLAN / 网桥自身的地址。选「暂不配置」时只写 BOOTPROTO=none，
            设备重启后该接口不会自动获取地址，适合先建接口、稍后再配地址的场景。
          </div>
        </div>
        <div class="form-item">
          <label class="form-label">地址获取方式</label>
          <select v-model="activeIP.bootproto" class="select">
            <option value="none">暂不配置地址</option>
            <option value="static">静态地址</option>
            <option value="dhcp">自动获取（DHCP）</option>
          </select>
        </div>
        <template v-if="activeIP.bootproto === 'static'">
          <div class="form-item">
            <label class="form-label">IP 地址<span class="required">*</span></label>
            <input v-model.trim="activeIP.ipaddr" class="input text-mono" placeholder="192.168.1.10" />
          </div>
          <div class="form-item">
            <label class="form-label">子网前缀<span class="required">*</span></label>
            <input
              v-model.number="activeIP.prefix"
              class="input text-mono"
              type="number"
              min="1"
              max="32"
            />
          </div>
          <div class="form-item">
            <label class="form-label">网关</label>
            <input
              v-model.trim="activeIP.gateway"
              class="input text-mono"
              placeholder="留空表示不设默认路由"
            />
            <span class="form-hint">
              留空时写入 DEFROUTE=no，避免抢走管理口的默认路由
            </span>
          </div>
        </template>
        <div class="form-item">
          <label class="form-label">DNS 服务器</label>
          <input v-model.trim="activeIP.dns" class="input text-mono" placeholder="多个用逗号分隔" />
        </div>
        <div class="form-item">
          <label class="form-label">MTU</label>
          <input
            v-model.number="activeIP.mtu"
            class="input text-mono"
            type="number"
            min="576"
            max="9000"
          />
          <span class="form-hint">VLAN 子接口留空时继承父接口的 MTU</span>
        </div>
      </div>

      <!-- 风险提示 -->
      <div
        v-if="needRiskAck"
        class="result-panel mt-12"
        style="border-left: 3px solid var(--color-danger)"
      >
        <StatusTag text="高风险操作" tone="danger" />
        <div style="font-size: 13px; margin-top: 6px; line-height: 1.7">
          <template v-if="riskIfaces.length">
            成员
            <strong>{{ riskIfaces.map((i) => i.name).join('、') }}</strong>
            正在承载<strong>你当前使用的连接</strong>。加入后该网口的 IP 会被移除、配置被改写，
            远程访问会立即中断，只能去机房直连设备恢复。
          </template>
          <template v-else>
            网桥用于<strong>串联部署</strong>：业务流量将物理经过本设备，配置错误会直接中断链路
            （单臂旁路模式不需要网桥）。
          </template>
          请确认：
        </div>
        <label style="display: flex; gap: 8px; align-items: flex-start; margin-top: 8px">
          <input v-model="riskAcknowledged" type="checkbox" style="margin-top: 3px" />
          <span style="font-size: 13px">
            我了解该操作的风险，并确认可从带外通道（console / 机房直连）恢复
          </span>
        </label>
      </div>

      <div
        v-if="parentWithoutConfig"
        class="result-panel mt-12"
        style="border-left: 3px solid var(--color-warning, #d97706)"
      >
        <StatusTag text="注意" tone="warning" />
        <div style="font-size: 13px; margin-top: 6px; line-height: 1.7">
          父接口 <strong>{{ vlanForm.parent }}</strong> 当前没有配置文件。
          为保证带 VLAN 标签的帧不被丢弃，本次会为它补写一份基础 ifcfg（BOOTPROTO=none，
          <strong>不带地址</strong>）。如果该网口现在依赖其它方式持有地址，请先确认这样写没有影响。
        </div>
      </div>

      <div class="text-secondary" style="font-size: 12px; margin-top: 10px; line-height: 1.7">
        提交后配置<strong>立即生效</strong>，但需要你在倒计时内点击「确认配置无误」；
        超时未确认，服务端会把本次涉及的
        <strong>全部</strong>配置文件一起恢复原样。
      </div>

      <template #footer>
        <button class="btn" @click="dialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="submitting" @click="submit">
          <span v-if="submitting" class="spinner light" />
          {{ submitting ? '提交中…' : '应用配置' }}
        </button>
      </template>
    </Modal>

    <!-- 改成员 -->
    <Modal v-model="membersDialog" :title="`修改 ${membersTarget?.name || ''} 的成员`" size="md">
      <div class="text-secondary" style="font-size: 13px; line-height: 1.7">
        勾选即纳入，取消勾选即移出。被移出的网口会写回一份<strong>最小独立配置</strong>
        （开机启用、暂不分配地址）：它加入聚合前的配置在加入那一刻就已被覆盖，
        无法自动还原，需要之后在「网络配置」里补回地址。
      </div>

      <div
        v-if="memberCandidates.length"
        class="result-panel mt-12"
        style="display: flex; flex-wrap: wrap; gap: 14px"
      >
        <label v-for="i in memberCandidates" :key="i.name" class="checkbox">
          <input v-model="membersSelection" type="checkbox" :value="i.name" />
          <span class="text-mono">{{ i.name }}</span>
          <StatusTag v-if="i.master === membersTarget?.name" text="当前成员" tone="info" />
          <StatusTag v-if="i.is_management" text="管理口" tone="danger" />
          <span v-else-if="i.state !== 'UP'" class="text-secondary" style="font-size: 12px">
            {{ i.state }}
          </span>
        </label>
      </div>
      <div v-else class="text-secondary mt-12" style="font-size: 13px">
        未读取到可用作成员的物理网口。
      </div>

      <div class="text-secondary mt-12" style="font-size: 13px">
        本次将加入 <strong>{{ membersDiff.add.join('、') || '无' }}</strong>
        ，移出 <strong>{{ membersDiff.remove.join('、') || '无' }}</strong> 。
        <span v-if="membersKind === 'bond'">链路聚合至少要保留两个成员。</span>
      </div>

      <div
        v-if="membersNeedRiskAck"
        class="result-panel mt-12"
        style="border-left: 3px solid var(--color-danger)"
      >
        <StatusTag text="高风险操作" tone="danger" />
        <div style="font-size: 13px; margin-top: 6px; line-height: 1.7">
          选中的成员
          <strong>{{ membersRiskIfaces.map((i) => i.name).join('、') }}</strong>
          正在承载<strong>你当前使用的连接</strong>，写回独立配置会中断远程访问。
        </div>
        <label style="display: flex; gap: 8px; align-items: flex-start; margin-top: 8px">
          <input v-model="membersRiskAck" type="checkbox" style="margin-top: 3px" />
          <span style="font-size: 13px">
            我了解该操作的风险，并确认可从带外通道（console / 机房直连）恢复
          </span>
        </label>
      </div>

      <template #footer>
        <button class="btn" @click="membersDialog = false">取消</button>
        <button class="btn btn-primary" :disabled="membersSubmitting" @click="submitMembers">
          <span v-if="membersSubmitting" class="spinner light" />
          {{ membersSubmitting ? '提交中…' : '应用成员变更' }}
        </button>
      </template>
    </Modal>

    <!-- 删除 -->
    <Modal v-model="deleteDialog" :title="`删除 ${deleteTarget?.name || ''}`" size="md">
      <div class="result-panel" style="border-left: 3px solid var(--color-danger)">
        <StatusTag text="高风险操作" tone="danger" />
        <div style="font-size: 13px; margin-top: 6px; line-height: 1.7">
          将删除配置文件
          <span class="text-mono">
            {{ deleteTarget?.config_path || `ifcfg-${deleteTarget?.name || ''}` }}
          </span>
          ，并重新加载网络配置让接口停止工作。
        </div>
        <div v-if="deleteMembers.length" style="font-size: 13px; margin-top: 8px; line-height: 1.7">
          成员 <strong>{{ deleteMembers.map((i) => i.name).join('、') }}</strong>
          会一并脱离，写回一份最小独立配置（开机启用、暂不分配地址）。
          不这样做的话，它们会停在「MASTER 指向一个已不存在的接口」的状态。
        </div>
        <div
          v-if="deleteTarget?.type === 'vlan'"
          style="font-size: 13px; margin-top: 8px; line-height: 1.7"
        >
          父接口 <span class="text-mono">{{ deleteTarget?.parent || '—' }}</span> 不受影响。
        </div>
      </div>

      <div
        v-if="deleteNeedRiskAck"
        class="result-panel mt-12"
        style="border-left: 3px solid var(--color-danger)"
      >
        <div style="font-size: 13px; line-height: 1.7">
          该接口（或其成员）承载<strong>你当前使用的连接</strong>，删除会立即中断远程访问。
        </div>
        <label style="display: flex; gap: 8px; align-items: flex-start; margin-top: 8px">
          <input v-model="deleteRiskAck" type="checkbox" style="margin-top: 3px" />
          <span style="font-size: 13px">
            我了解该操作的风险，并确认可从带外通道（console / 机房直连）恢复
          </span>
        </label>
      </div>

      <div class="text-secondary" style="font-size: 12px; margin-top: 10px; line-height: 1.7">
        删除后配置<strong>立即生效</strong>，同样进入倒计时确认：超时未确认，
        服务端会把被删文件与成员配置一起恢复原样。
      </div>

      <template #footer>
        <button class="btn" @click="deleteDialog = false">取消</button>
        <button class="btn btn-danger" :disabled="deleteSubmitting" @click="submitDelete">
          <span v-if="deleteSubmitting" class="spinner light" />
          {{ deleteSubmitting ? '删除中…' : '确认删除' }}
        </button>
      </template>
    </Modal>
  </div>
</template>
