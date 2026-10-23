<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  addClusterMembers,
  createCluster,
  deleteCluster,
  getCluster,
  listClusters,
  probeCluster,
  removeClusterMember,
  setClusterMemberState,
  updateClusterMember,
  NODE_STATE_META,
  type ClusterDetail,
  type ClusterMember,
  type ClusterView,
  type MemberAdminState,
  type NodeHealth,
} from '@/api/cluster'
import Modal from '@/components/Modal.vue'
import DeviceIpInput from '@/components/DeviceIpInput.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TagTone } from '@/utils/constants'
import type { DeviceRow } from '@/api/cluster'

/**
 * 集群管理（运维手动定义的设备分组）。
 *
 * 与"节点自注册"的区别，是这个页面的全部设计前提：
 *
 *   节点  进程自己报上来的 —— 回答"谁在跑"，界面上不再直接呈现；
 *   集群  运维填出来的     —— 回答"这几台设备属于一起"。
 *
 * 因此创建集群就是填一个名字加一串设备 IP：设备之间需要网络互通、
 * 且已部署堡垒机服务端（默认探测 :8081）。管理服务主动去探测它们，
 * 而不是等对方来注册 —— 一台设备没装或没启动服务时，界面必须
 * 明确显示"连接被拒绝"，而不是静静地什么都不出现。
 *
 * 「一台设备只能属于一个集群」由服务端的唯一索引保证；
 * 界面上把冲突原因如实显示出来即可。
 */
const app = useAppStore()
const auth = useAuthStore()

const canManage = computed(() => auth.has('system:config:write'))

const HEALTH_TONE: Record<NodeHealth, TagTone> = {
  normal: 'success',
  busy: 'warning',
  draining: 'warning',
  disabled: 'default',
  offline: 'danger',
}

const loading = ref(false)
const clusters = ref<ClusterView[]>([])
let timer: number | undefined

/* ------------------------------------------------------------ 列表 */

async function loadClusters(silent = false): Promise<void> {
  if (!silent) loading.value = true
  try {
    const res = await listClusters()
    clusters.value = res.items ?? []
  } catch {
    /* 错误提示由 http 层统一处理 */
  } finally {
    if (!silent) loading.value = false
  }
}

onMounted(() => {
  void loadClusters()
  // 探测周期默认 15 秒，界面同频刷新即可
  timer = window.setInterval(() => {
    void loadClusters(true)
    if (detailVisible.value) void refreshDetail(true)
  }, 15000)
})

onBeforeUnmount(() => {
  if (timer !== undefined) window.clearInterval(timer)
})

/* ------------------------------------------------------------ 详情 */

const detailVisible = ref(false)
const detail = ref<ClusterDetail | null>(null)
const detailLoading = ref(false)

async function openDetail(c: ClusterView): Promise<void> {
  detailVisible.value = true
  detail.value = null
  detailLoading.value = true
  try {
    detail.value = await getCluster(c.cluster_id)
  } catch {
    detailVisible.value = false
  } finally {
    detailLoading.value = false
  }
}

async function refreshDetail(silent = false): Promise<void> {
  const id = detail.value?.cluster_id
  if (!id) return
  if (!silent) detailLoading.value = true
  try {
    detail.value = await getCluster(id)
  } catch {
    /* 忽略：下一轮会再试 */
  } finally {
    if (!silent) detailLoading.value = false
  }
}

async function doProbe(): Promise<void> {
  const id = detail.value?.cluster_id
  if (!id) return
  detailLoading.value = true
  try {
    detail.value = await probeCluster(id)
    app.success('已完成一次探测')
    await loadClusters(true)
  } catch (error) {
    app.error(error instanceof Error ? error.message : '探测失败')
  } finally {
    detailLoading.value = false
  }
}

/* ------------------------------------------------------------ 新建集群 */

const createVisible = ref(false)
const createSaving = ref(false)
const createForm = reactive({ name: '', description: '', vip: '' })
// 默认给两行：双机是常见形态，省得每次都要点一次"新增设备"
const createRows = ref<DeviceRow[]>([
  { ip: '', port: '' },
  { ip: '', port: '' },
])

function openCreate(): void {
  createForm.name = ''
  createForm.description = ''
  createForm.vip = ''
  createRows.value = [
    { ip: '', port: '' },
    { ip: '', port: '' },
  ]
  createVisible.value = true
}

async function submitCreate(): Promise<void> {
  const name = createForm.name.trim()
  if (!name) {
    app.error('请填写集群名称')
    return
  }
  const vip = createForm.vip.trim()
  if (vip && !isIPv4(vip)) {
    app.error(`VIP 不是合法的 IPv4 地址：${vip}`)
    return
  }
  const { devices, error } = collectDevices(createRows.value)
  if (error) {
    app.error(error)
    return
  }

  createSaving.value = true
  try {
    const created = await createCluster({
      name,
      description: createForm.description.trim(),
      vip_addr: vip,
      devices,
    })
    createVisible.value = false
    app.success(`集群「${created.cluster_name}」已创建，共 ${created.member_count} 台设备`)
    await loadClusters(true)
    await openDetail(created)
  } catch (error) {
    app.error(error instanceof Error ? error.message : '创建集群失败')
  } finally {
    createSaving.value = false
  }
}

/* ------------------------------------------------------------ 追加设备 */

const addVisible = ref(false)
const addSaving = ref(false)
const addRows = ref<DeviceRow[]>([{ ip: '', port: '' }])

function openAdd(): void {
  addRows.value = [{ ip: '', port: '' }]
  addVisible.value = true
}

async function submitAdd(): Promise<void> {
  const id = detail.value?.cluster_id
  if (!id) return
  const { devices, error } = collectDevices(addRows.value)
  if (error) {
    app.error(error)
    return
  }

  addSaving.value = true
  try {
    detail.value = await addClusterMembers(id, devices)
    addVisible.value = false
    app.success(`已添加 ${devices.length} 台设备`)
    await loadClusters(true)
  } catch (error) {
    app.error(error instanceof Error ? error.message : '添加设备失败')
  } finally {
    addSaving.value = false
  }
}

/* ------------------------------------------------------------ 成员操作 */

const memberEditVisible = ref(false)
const memberEditSaving = ref(false)
const memberEditTarget = ref<ClusterMember | null>(null)
const memberForm = reactive({ device_name: '', weight: 100, max_sessions: 300 })

function openMemberEdit(m: ClusterMember): void {
  memberEditTarget.value = m
  memberForm.device_name = m.device_name
  memberForm.weight = m.weight
  memberForm.max_sessions = m.max_sessions
  memberEditVisible.value = true
}

async function submitMemberEdit(): Promise<void> {
  const id = detail.value?.cluster_id
  const m = memberEditTarget.value
  if (!id || !m) return
  if (memberForm.weight < 0 || memberForm.weight > 1000) {
    app.error('权重应在 0~1000 之间（0 表示不再接收新会话）')
    return
  }
  if (memberForm.max_sessions < 1 || memberForm.max_sessions > 100000) {
    app.error('会话上限应在 1~100000 之间')
    return
  }

  memberEditSaving.value = true
  try {
    await updateClusterMember(id, m.member_id, {
      device_name: memberForm.device_name.trim(),
      weight: memberForm.weight,
      max_sessions: memberForm.max_sessions,
    })
    memberEditVisible.value = false
    app.success('设备配置已更新')
    await refreshDetail(true)
  } catch (error) {
    app.error(error instanceof Error ? error.message : '更新失败')
  } finally {
    memberEditSaving.value = false
  }
}

const actionVisible = ref(false)
const actionSaving = ref(false)
const actionTarget = ref<ClusterMember | null>(null)
const actionKind = ref<MemberAdminState | 'remove'>('draining')

const ACTION_TEXT: Record<string, { title: string; body: string; confirm: string }> = {
  active: {
    title: '恢复接收新会话',
    body: '该设备重新参与新会话的分配。若它的服务端尚未启动，状态仍会显示不可达。',
    confirm: '恢复接单',
  },
  draining: {
    title: '置为维护中',
    body: '新会话不再分配到该设备，存量会话继续跑完（首版不做会话热迁移）。适合升级、停机检修前使用。该状态由设备上的服务端在下一次心跳时取走，是真正生效的。',
    confirm: '开始维护',
  },
  disabled: {
    title: '摘除设备',
    body: '该设备不再参与任何调度，管理服务也不再探测它。这不会掐断已经在进行的运维会话。',
    confirm: '确认摘除',
  },
  remove: {
    title: '从集群移除设备',
    body: '仅解除集群归属关系，不影响该设备本身的运行。移除后可以把它加入别的集群。',
    confirm: '确认移除',
  },
}

const actionText = computed(() => ACTION_TEXT[actionKind.value])

function openMemberAction(m: ClusterMember, kind: MemberAdminState | 'remove'): void {
  actionTarget.value = m
  actionKind.value = kind
  actionVisible.value = true
}

async function submitMemberAction(): Promise<void> {
  const id = detail.value?.cluster_id
  const m = actionTarget.value
  if (!id || !m) return

  actionSaving.value = true
  try {
    if (actionKind.value === 'remove') {
      await removeClusterMember(id, m.member_id)
      app.success(`已从集群移除 ${m.device_ip}`)
    } else {
      await setClusterMemberState(id, m.member_id, actionKind.value)
      app.success(`${m.device_ip} 已${NODE_STATE_META[actionKind.value].label}`)
    }
    actionVisible.value = false
    await refreshDetail(true)
    await loadClusters(true)
  } catch (error) {
    app.error(error instanceof Error ? error.message : '操作失败')
  } finally {
    actionSaving.value = false
  }
}

/* ------------------------------------------------------------ 删除集群 */

const removeClusterVisible = ref(false)
const removeClusterSaving = ref(false)
const removeClusterTarget = ref<ClusterView | null>(null)

function openRemoveCluster(c: ClusterView): void {
  removeClusterTarget.value = c
  removeClusterVisible.value = true
}

async function submitRemoveCluster(): Promise<void> {
  const c = removeClusterTarget.value
  if (!c) return
  removeClusterSaving.value = true
  try {
    await deleteCluster(c.cluster_id)
    removeClusterVisible.value = false
    if (detail.value?.cluster_id === c.cluster_id) detailVisible.value = false
    app.success(`集群「${c.cluster_name}」已删除`)
    await loadClusters(true)
  } catch (error) {
    app.error(error instanceof Error ? error.message : '删除失败')
  } finally {
    removeClusterSaving.value = false
  }
}

/* ------------------------------------------------------------ 工具 */

/**
 * 把输入行整理成服务端需要的设备列表。
 *
 * 逐行校验而不是整块文本：出错时能直接说"第 2 行的 IP 不合法"，
 * 而不是让用户自己数到第几行。
 */
function collectDevices(rows: DeviceRow[]): { devices: string[]; error: string } {
  const devices: string[] = []
  const seen = new Set<string>()

  for (let i = 0; i < rows.length; i++) {
    const ip = rows[i].ip.trim()
    const port = rows[i].port.trim()
    // 整行空着就跳过：默认给两行，用户只填一台是正常用法
    if (!ip && !port) continue

    if (!ip) return { devices: [], error: `第 ${i + 1} 行只填了端口，请补上 IP` }
    if (!isIPv4(ip)) return { devices: [], error: `第 ${i + 1} 行的 IP 不合法：${ip}` }
    if (seen.has(ip)) return { devices: [], error: `设备 IP 重复：${ip}` }
    seen.add(ip)

    if (port) {
      const n = Number(port)
      if (!Number.isInteger(n) || n < 1 || n > 65535) {
        return { devices: [], error: `第 ${i + 1} 行的端口不合法：${port}` }
      }
      devices.push(`${ip}:${n}`)
    } else {
      devices.push(ip)
    }
  }

  if (!devices.length) return { devices: [], error: '请至少填写一台设备 IP' }
  return { devices, error: '' }
}

function isIPv4(s: string): boolean {
  const parts = s.split('.')
  if (parts.length !== 4) return false
  return parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255)
}

function fmtTime(value?: string): string {
  if (!value) return '—'
  return new Date(value).toLocaleString()
}

/** 相对时间：切换发生在"多久以前"比绝对时间更有用 */
function fmtAgo(value?: string): string {
  if (!value) return '—'
  const seconds = Math.floor((Date.now() - new Date(value).getTime()) / 1000)
  if (seconds < 60) return `${seconds} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return `${Math.floor(seconds / 86400)} 天前`
}

function fmtProbeAge(m: ClusterMember): string {
  if (m.probe_age_seconds < 0) return '从未探测'
  if (m.probe_age_seconds < 60) return `${m.probe_age_seconds} 秒前`
  return `${Math.floor(m.probe_age_seconds / 60)} 分钟前`
}

function fmtPercent(v: number): string {
  return `${(v || 0).toFixed(1)}%`
}

const MODE_TONE: Record<string, TagTone> = {
  cluster: 'success',
  'ha-pair': 'info',
  standalone: 'warning',
  empty: 'default',
}

/** 主备事件的标签样式：丢主与脑裂都是要立刻处理的，统一用红 */
const FAILOVER_TONE: Record<string, TagTone> = {
  elect: 'info',
  switch: 'warning',
  lost: 'danger',
  split: 'danger',
}
</script>

<template>
  <div>
    <!-- 集群列表 -->
    <div class="card mb-16">
      <div class="card-header">
        <span class="card-title">集群</span>
        <div class="btn-group">
          <button v-if="canManage" class="btn btn-sm btn-primary" @click="openCreate">
            新建集群
          </button>
          <button class="btn btn-sm" :disabled="loading" @click="loadClusters()">
            <span v-if="loading" class="spinner" />
            {{ loading ? '读取中…' : '刷新' }}
          </button>
        </div>
      </div>

      <div class="card-body text-secondary" style="font-size: 12px; line-height: 1.8">
        集群由运维手动定义：填写集群名称与<strong>设备 IP</strong>（设备之间需网络互通，
        且已部署堡垒机服务端，默认探测 <span class="text-mono">:8081</span>）。
        管理服务每隔约 15 秒主动探测一次成员状态。
        <strong>一台设备只能属于一个集群</strong>——重复录入会被拒绝。
        <br />
        主备（Keepalived）模式下再填一个 <strong>VIP</strong>：管理服务按「谁持有 VIP」判定主备，
        当前主节点显示在列表里；VIP 同时被两台持有时会标红提示脑裂。
      </div>

      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th style="width: 180px">集群</th>
              <th style="width: 100px">设备数</th>
              <th style="width: 110px">模式</th>
              <th style="width: 190px">主节点 / VIP</th>
              <th style="width: 120px">接收新会话</th>
              <th style="width: 100px">并发会话</th>
              <th style="width: 160px">创建</th>
              <th style="width: 160px">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading && !clusters.length">
              <td colspan="8" class="table-empty"><span class="spinner" /> 正在读取集群…</td>
            </tr>
            <tr v-else-if="!clusters.length">
              <td colspan="8" class="table-empty">
                还没有集群。点击右上角「新建集群」，填写集群名称与各台设备的 IP 即可。
              </td>
            </tr>
            <tr v-for="c in clusters" :key="c.cluster_id">
              <td>
                <strong>{{ c.cluster_name }}</strong>
                <div v-if="c.description" class="text-secondary" style="font-size: 12px">
                  {{ c.description }}
                </div>
              </td>
              <td>
                <span style="font-weight: 600">{{ c.online_count }} / {{ c.member_count }}</span>
                <div class="text-secondary" style="font-size: 12px">在线 / 总数</div>
              </td>
              <td>
                <StatusTag :text="c.mode_text" :tone="MODE_TONE[c.mode] || 'default'" />
              </td>
              <td style="font-size: 13px">
                <!-- 主备视图：持有 VIP 的那台就是主节点 -->
                <template v-if="c.vip_addr">
                  <template v-if="c.vip_held_count > 1">
                    <StatusTag text="脑裂" tone="danger" />
                    <div class="text-danger" style="font-size: 12px">
                      {{ c.vip_held_count }} 台同时持有 VIP
                    </div>
                  </template>
                  <template v-else-if="c.master_ip">
                    <StatusTag text="主" tone="success" />
                    <span class="text-mono" style="margin-left: 4px">{{ c.master_ip }}</span>
                    <div class="text-secondary" style="font-size: 12px">
                      VIP {{ c.vip_addr }} · 备 {{ c.backup_count }} 台
                    </div>
                    <div
                      v-if="c.last_failover"
                      class="text-secondary"
                      style="font-size: 12px"
                      :title="c.last_failover.detail"
                    >
                      最近{{ c.last_failover.event_text }} {{ fmtAgo(c.last_failover.create_time) }}
                    </div>
                  </template>
                  <template v-else>
                    <StatusTag text="无主" tone="danger" />
                    <div class="text-secondary" style="font-size: 12px">
                      VIP {{ c.vip_addr }} 当前无人持有
                    </div>
                  </template>
                </template>
                <span v-else class="text-secondary">未配置 VIP</span>
              </td>
              <td>
                <span
                  :class="c.accepting_count === 0 && c.member_count > 0 ? 'text-danger' : ''"
                  style="font-weight: 600"
                >
                  {{ c.accepting_count }} 台
                </span>
                <div
                  v-if="c.accepting_count === 0 && c.member_count > 0"
                  class="text-danger"
                  style="font-size: 12px"
                >
                  新会话将无处可去
                </div>
              </td>
              <td class="text-secondary">{{ c.active_sessions }}</td>
              <td class="text-secondary" style="font-size: 12px">
                <div>{{ c.created_by_name || '—' }}</div>
                <div>{{ fmtTime(c.create_time) }}</div>
              </td>
              <td>
                <div class="btn-group">
                  <button class="btn btn-sm" @click="openDetail(c)">设备与状态</button>
                  <button
                    v-if="canManage"
                    class="btn btn-link danger"
                    @click="openRemoveCluster(c)"
                  >
                    删除
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 集群详情：设备与状态 -->
    <Modal
      v-model="detailVisible"
      :title="`集群「${detail?.cluster_name || ''}」的设备`"
      size="lg"
    >
      <div class="flex-between" style="align-items: center; gap: 10px; flex-wrap: wrap">
        <div class="text-secondary" style="font-size: 13px">
          <template v-if="detail">
            {{ detail.mode_text }} · 在线 {{ detail.online_count }}/{{ detail.member_count }} ·
            并发会话 {{ detail.active_sessions }} ·
            <span :class="detail.accepting_count === 0 ? 'text-danger' : ''">
              接收新会话 {{ detail.accepting_count }} 台
            </span>
            <template v-if="detail.vip_addr">
              · VIP <span class="text-mono">{{ detail.vip_addr }}</span>
              <template v-if="detail.vip_held_count > 1">
                <span class="text-danger">
                  · 脑裂：{{ detail.vip_held_count }} 台同时持有 VIP
                </span>
              </template>
              <template v-else-if="detail.master_ip">
                · 主节点 <span class="text-mono">{{ detail.master_ip }}</span>
              </template>
              <template v-else>
                <span class="text-danger">· 当前无主（无人持有 VIP）</span>
              </template>
            </template>
          </template>
        </div>
        <div class="btn-group">
          <button v-if="canManage" class="btn btn-sm" @click="openAdd">添加设备</button>
          <button class="btn btn-sm" :disabled="detailLoading" @click="doProbe">
            <span v-if="detailLoading" class="spinner" />
            立即探测
          </button>
        </div>
      </div>

      <div class="table-wrap mt-12">
        <table class="table">
          <thead>
            <tr>
              <th style="width: 140px">设备 IP</th>
              <th style="width: 130px">角色</th>
              <th style="width: 140px">状态</th>
              <th style="width: 140px">身份</th>
              <th style="width: 140px">负载</th>
              <th style="width: 120px">最近探测</th>
              <th style="width: 190px">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="detailLoading && !detail">
              <td colspan="7" class="table-empty"><span class="spinner" /> 正在读取…</td>
            </tr>
            <tr v-else-if="!detail?.members?.length">
              <td colspan="7" class="table-empty">该集群还没有设备。</td>
            </tr>
            <tr v-for="m in detail?.members || []" :key="m.member_id">
              <td>
                <strong class="text-mono">{{ m.device_ip }}</strong>
                <span class="text-secondary" style="font-size: 12px">:{{ m.device_port }}</span>
                <div v-if="m.device_name" class="text-secondary" style="font-size: 12px">
                  {{ m.device_name }}
                </div>
              </td>
              <td>
                <template v-if="m.role">
                  <StatusTag
                    :text="m.role === 'master' ? '主节点' : m.role === 'backup' ? '备节点' : '未知'"
                    :tone="m.role === 'master' ? 'success' : m.role === 'backup' ? 'info' : 'default'"
                  />
                  <div
                    v-if="m.role === 'master' && m.local_ips"
                    class="text-secondary"
                    style="font-size: 12px; margin-top: 2px"
                  >
                    持有 VIP
                  </div>
                </template>
                <span v-else class="text-secondary">—</span>
              </td>
              <td>
                <StatusTag :text="m.health_text" :tone="HEALTH_TONE[m.health] || 'default'" />
                <div class="text-secondary" style="font-size: 12px; margin-top: 2px">
                  运维设置：{{ NODE_STATE_META[m.admin_state]?.label || m.admin_state }}
                  <template v-if="!m.accept_new_sessions"> · 不接新会话</template>
                </div>
                <div v-if="m.probe_error" class="text-danger" style="font-size: 12px">
                  {{ m.probe_error }}
                </div>
              </td>
              <td class="text-secondary" style="font-size: 12px">
                <div class="text-mono">{{ m.node_id || '—' }}</div>
                <div>{{ m.version || '—' }}</div>
              </td>
              <td style="font-size: 13px">
                <div>会话 {{ m.active_sessions }} / {{ m.max_sessions }}</div>
                <div class="text-secondary" style="font-size: 12px">
                  占用 {{ fmtPercent(m.usage_percent) }} · 权重 {{ m.weight }}
                </div>
              </td>
              <td class="text-secondary" style="font-size: 12px">
                <div>{{ fmtProbeAge(m) }}</div>
                <div>{{ fmtTime(m.last_ok_at) }}</div>
              </td>
              <td>
                <div v-if="canManage" class="btn-group" style="flex-wrap: wrap; gap: 6px">
                  <button class="btn btn-sm" @click="openMemberEdit(m)">配置</button>
                  <button
                    v-if="m.admin_state === 'active'"
                    class="btn btn-sm"
                    @click="openMemberAction(m, 'draining')"
                  >
                    置为维护
                  </button>
                  <button v-else class="btn btn-sm" @click="openMemberAction(m, 'active')">
                    恢复接单
                  </button>
                  <button class="btn btn-link danger" @click="openMemberAction(m, 'remove')">
                    移除
                  </button>
                </div>
                <span v-else class="text-secondary" style="font-size: 12px">只读</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 主备事件：回答"什么时候切的、切了几次、为什么切" -->
      <div v-if="detail?.vip_addr" class="mt-12">
        <div style="font-weight: 600; font-size: 13px; margin-bottom: 6px">
          主备事件
          <span class="text-secondary" style="font-weight: 400; font-size: 12px">
            （最近 {{ detail.failovers?.length || 0 }} 条；只在归属发生变化时记录，
            持续无主或持续脑裂不会重复刷记录）
          </span>
        </div>
        <div v-if="detail.failovers?.length" class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th style="width: 200px">时间</th>
                <th style="width: 170px">事件</th>
                <th>说明</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="ev in detail.failovers" :key="ev.event_id">
                <td class="text-secondary" style="font-size: 12px">
                  <div>{{ fmtTime(ev.create_time) }}</div>
                  <div>{{ fmtAgo(ev.create_time) }}</div>
                </td>
                <td>
                  <StatusTag
                    :text="ev.event_text"
                    :tone="FAILOVER_TONE[ev.event_type] || 'default'"
                  />
                </td>
                <td class="text-secondary" style="font-size: 12px">{{ ev.detail || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="text-secondary" style="font-size: 12px">
          暂无事件记录 —— 说明主节点归属自集群建立以来没有变化过。
        </div>
      </div>

      <div class="text-secondary" style="font-size: 12px; margin-top: 10px; line-height: 1.8">
        「不可达」的常见原因：该设备上未启动堡垒机服务端（连接被拒绝）、
        端口不对、或两台设备之间网络不通。探测只读取状态，不会改动对端任何配置。
      </div>
    </Modal>

    <!-- 新建集群 -->
    <Modal v-model="createVisible" title="新建集群" size="md">
      <div class="form-grid single">
        <div class="form-item">
          <label class="form-label">集群名称<span class="required">*</span></label>
          <input v-model.trim="createForm.name" class="input" placeholder="如：生产双机" />
        </div>
        <div class="form-item">
          <label class="form-label">说明</label>
          <input v-model.trim="createForm.description" class="input" placeholder="可选" />
        </div>
        <div class="form-item">
          <label class="form-label">VIP 地址（主备模式）</label>
          <input
            v-model.trim="createForm.vip"
            class="input text-mono"
            placeholder="留空表示不使用 VIP，如 192.168.106.200"
          />
          <span class="form-hint">
            主备（Keepalived）模式下填写漂移 VIP；管理服务按「谁持有 VIP」判定主备，
            列表与详情里会显示当前主节点。使用前置负载均衡时留空即可。
          </span>
        </div>
        <div class="form-item">
          <label class="form-label">设备 IP<span class="required">*</span></label>
          <DeviceIpInput v-model="createRows" :min-rows="1" />
          <span class="form-hint">
            一台设备一行；设备之间需网络互通，且该设备上已部署堡垒机服务端。
          </span>
        </div>
      </div>
      <div class="text-secondary" style="font-size: 12px; margin-top: 8px; line-height: 1.8">
        创建后会立即探测一次。<strong>一台设备只能属于一个集群</strong>：如果某个 IP 已被其它集群占用，
        创建会被整体拒绝（不会只写入一半设备）。
      </div>
      <template #footer>
        <button class="btn" @click="createVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="createSaving" @click="submitCreate">
          <span v-if="createSaving" class="spinner light" />
          {{ createSaving ? '创建中…' : '创建集群' }}
        </button>
      </template>
    </Modal>

    <!-- 添加设备 -->
    <Modal v-model="addVisible" title="添加设备" size="md">
      <div class="form-item">
        <label class="form-label">设备 IP<span class="required">*</span></label>
        <DeviceIpInput v-model="addRows" :min-rows="1" />
        <span class="form-hint">已被其它集群占用的 IP 会被拒绝。</span>
      </div>
      <template #footer>
        <button class="btn" @click="addVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="addSaving" @click="submitAdd">
          <span v-if="addSaving" class="spinner light" />
          {{ addSaving ? '添加中…' : '添加' }}
        </button>
      </template>
    </Modal>

    <!-- 设备配置 -->
    <Modal
      v-model="memberEditVisible"
      :title="`设备配置 ${memberEditTarget?.device_ip || ''}`"
      size="md"
    >
      <div class="form-grid">
        <div class="form-item">
          <label class="form-label">设备别名</label>
          <input v-model.trim="memberForm.device_name" class="input" placeholder="如：主节点" />
        </div>
        <div class="form-item">
          <label class="form-label">负载权重</label>
          <input
            v-model.number="memberForm.weight"
            class="input text-mono"
            type="number"
            min="0"
            max="1000"
          />
          <span class="form-hint">0 表示不再给该设备分配新会话</span>
        </div>
        <div class="form-item">
          <label class="form-label">并发会话上限</label>
          <input
            v-model.number="memberForm.max_sessions"
            class="input text-mono"
            type="number"
            min="1"
            max="100000"
          />
        </div>
      </div>
      <div class="text-secondary" style="font-size: 12px; margin-top: 8px">
        这里的会话上限只用于界面展示与容量判断；设备自身的上限由它的
        <span class="text-mono">BASTION_MAX_SESSIONS</span> 决定。
      </div>
      <template #footer>
        <button class="btn" @click="memberEditVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="memberEditSaving" @click="submitMemberEdit">
          <span v-if="memberEditSaving" class="spinner light" />
          {{ memberEditSaving ? '保存中…' : '保存' }}
        </button>
      </template>
    </Modal>

    <!-- 成员状态 / 移除确认 -->
    <Modal v-model="actionVisible" :title="actionText?.title" size="md">
      <div v-if="actionTarget">
        <div
          class="result-panel"
          :style="`border-left: 3px solid var(--color-${actionKind === 'active' ? 'primary' : 'danger'})`"
        >
          <div style="font-size: 13px; line-height: 1.8">
            设备 <strong class="text-mono">{{ actionTarget.device_ip }}</strong>
            当前有 <strong>{{ actionTarget.active_sessions }}</strong> 个活跃会话，
            运维设置为「{{ NODE_STATE_META[actionTarget.admin_state]?.label }}」。
          </div>
          <div class="text-secondary" style="font-size: 13px; margin-top: 8px; line-height: 1.8">
            {{ actionText?.body }}
          </div>
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="actionVisible = false">取消</button>
        <button
          class="btn"
          :class="actionKind === 'active' ? 'btn-primary' : 'btn-danger'"
          :disabled="actionSaving"
          @click="submitMemberAction"
        >
          <span v-if="actionSaving" class="spinner light" />
          {{ actionSaving ? '处理中…' : actionText?.confirm }}
        </button>
      </template>
    </Modal>

    <!-- 删除集群 -->
    <Modal v-model="removeClusterVisible" title="删除集群" size="md">
      <div class="result-panel" style="border-left: 3px solid var(--color-danger)">
        <StatusTag text="确认删除" tone="danger" />
        <div style="font-size: 13px; margin-top: 6px; line-height: 1.8">
          将删除集群
          <strong>{{ removeClusterTarget?.cluster_name }}</strong>
          及其 <strong>{{ removeClusterTarget?.member_count }}</strong> 条设备记录。
          这<strong>不会</strong>影响这些设备上正在运行的堡垒机服务，只解除集群归属 ——
          删除后这些 IP 可以重新加入别的集群。
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="removeClusterVisible = false">取消</button>
        <button class="btn btn-danger" :disabled="removeClusterSaving" @click="submitRemoveCluster">
          <span v-if="removeClusterSaving" class="spinner light" />
          {{ removeClusterSaving ? '删除中…' : '确认删除' }}
        </button>
      </template>
    </Modal>
  </div>
</template>
