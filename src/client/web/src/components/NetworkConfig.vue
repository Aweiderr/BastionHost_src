<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  applyNetworkChange,
  cancelNetworkChange,
  confirmNetworkChange,
  getNetworkPending,
  getNetworkSnapshot,
  type NetworkChange,
  type NetworkIface,
  type NetworkSnapshot,
} from '@/api/network'
import Modal from '@/components/Modal.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

/**
 * 设备网络配置。
 *
 * 这个组件的核心不是"填表单"，而是**防止用户把自己锁死**：
 *
 *   1. 管理口（承载当前连接的网口）用红色标记，编辑时必须勾选知情确认；
 *   2. 提交后配置立即生效但处于待确认状态，顶部显示倒计时；
 *      超时未确认服务端会自动回滚，用户必须知道这件事正在发生。
 */
const app = useAppStore()
const auth = useAuthStore()

/**
 * 是否有权修改网络配置。
 *
 * 网络配置的写接口要求 system:config:write：改管理口、聚合、网桥
 * 足以让设备失联，只能去机房恢复。没有该权限的账号可以看状态，
 * 但界面上不给出操作入口 —— 点下去才报 403 是最差的体验。
 */
const canWrite = computed(() => auth.has('system:config:write'))

const loading = ref(false)
const snapshot = ref<NetworkSnapshot | null>(null)

/** 当前待确认的变更；非空时顶部显示倒计时条 */
const pending = ref<NetworkChange | null>(null)
const remainSeconds = ref(0)
let tickTimer: number | undefined

const editing = ref<NetworkIface | null>(null)
const dialogVisible = ref(false)
const submitting = ref(false)
/** 管理口的知情确认勾选 */
const riskAcknowledged = ref(false)

const form = reactive({
  bootproto: 'static' as 'static' | 'dhcp',
  ipaddr: '',
  prefix: 24,
  gateway: '',
  dns: '',
  mtu: 1500,
  onboot: true,
})

/** 聚合 / VLAN / 网桥的类型标识（服务端 TYPE 字段小写化后的取值） */
const ADVANCED_TYPES = ['bond', 'vlan', 'bridge']

function isAdvanced(iface: NetworkIface): boolean {
  return ADVANCED_TYPES.includes((iface.type || '').toLowerCase())
}

const ADVANCED_LABEL: Record<string, string> = {
  bond: '链路聚合',
  vlan: 'VLAN 子接口',
  bridge: '网桥',
}

/**
 * 可配置网口：只列物理网口。
 *
 * 聚合 / VLAN / 网桥**必须排除**：它们的 ifcfg 里有 BONDING_OPTS、PHYSDEV、
 * BRIDGE 这类类型专属键，单网口表单不认识它们，一旦从这里改写，
 * 很容易把聚合参数改坏 —— 那属于「高级网络配置」卡片的职责。
 */
const configurable = computed(() =>
  (snapshot.value?.interfaces ?? []).filter(
    (i) => !i.is_loopback && !i.is_virtual && !isAdvanced(i),
  ),
)

/** 高级接口：只读展示，引导到下方卡片 */
const advancedIfaces = computed(() =>
  (snapshot.value?.interfaces ?? []).filter(isAdvanced),
)

// 回环（lo）与系统自建的虚拟接口（docker0 / veth* / virbr* / br-*）不再列出。
//
// 它们既不可配置、数量又多（一台跑容器的机器上可能有几十个 veth），
// 混在网口列表里会把真正的硬件网口挤到看不见 —— 排障时先要看到的是
// "哪块网卡有地址、是不是管理口"，而不是容器编排的内务。

function fmtAddresses(list: string[]): string {
  return list && list.length ? list.join('、') : '—'
}

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

/** 拉取待确认变更并启动倒计时 */
async function loadPending(): Promise<void> {
  try {
    const res = await getNetworkPending()
    const first = res.items?.[0] ?? null
    if (first) {
      startCountdown(first)
    }
  } catch {
    /* 无待确认变更或接口不可用，均不影响主流程 */
  }
}

function startCountdown(change: NetworkChange): void {
  pending.value = change
  updateRemain()
  if (tickTimer !== undefined) window.clearInterval(tickTimer)
  tickTimer = window.setInterval(() => {
    updateRemain()
    if (remainSeconds.value <= 0 && tickTimer !== undefined) {
      window.clearInterval(tickTimer)
      tickTimer = undefined
      // 倒计时归零：服务端会在几秒内完成回滚，刷新一下让用户看到结果
      app.info('确认超时，配置已自动恢复')
      pending.value = null
      void load()
    }
  }, 1000)
}

function updateRemain(): void {
  if (!pending.value) {
    remainSeconds.value = 0
    return
  }
  const deadline = new Date(pending.value.deadline).getTime()
  const diff = Math.ceil((deadline - Date.now()) / 1000)
  remainSeconds.value = diff > 0 ? diff : 0
}

onBeforeUnmount(() => {
  if (tickTimer !== undefined) window.clearInterval(tickTimer)
})

onMounted(async () => {
  await load()
  void loadPending()
})

function openEdit(iface: NetworkIface): void {
  editing.value = iface
  riskAcknowledged.value = false
  form.bootproto = (iface.bootproto === 'dhcp' ? 'dhcp' : 'static') as 'static' | 'dhcp'
  // 取第一个 IPv4 地址填入表单
  const first = iface.addresses?.find((a) => a.includes('.')) || ''
  const [ip] = first.split('/')
  form.ipaddr = ip || ''
  form.prefix = iface.prefix || 24
  form.gateway = iface.gateway || ''
  form.dns = (iface.dns ?? []).join(', ')
  form.mtu = iface.mtu || 1500
  form.onboot = iface.onboot
  dialogVisible.value = true
}

/** 管理口需要额外确认才能提交 */
const needRiskAck = computed(() => Boolean(editing.value?.is_management))
const canSubmit = computed(() => !needRiskAck.value || riskAcknowledged.value)

async function submit(): Promise<void> {
  const iface = editing.value
  if (!iface) return
  if (needRiskAck.value && !riskAcknowledged.value) {
    app.error('请先勾选确认，表示你了解修改管理口的风险')
    return
  }
  if (form.bootproto === 'static') {
    if (!form.ipaddr.trim()) {
      app.error('请填写 IP 地址')
      return
    }
    if (form.prefix < 1 || form.prefix > 32) {
      app.error('子网前缀应在 1~32 之间')
      return
    }
  }

  submitting.value = true
  try {
    const change = await applyNetworkChange({
      interface: iface.name,
      bootproto: form.bootproto,
      ipaddr: form.bootproto === 'static' ? form.ipaddr.trim() : undefined,
      prefix: form.bootproto === 'static' ? form.prefix : undefined,
      gateway: form.gateway.trim() || undefined,
      dns: form.dns
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean),
      mtu: form.mtu > 0 ? form.mtu : undefined,
      onboot: form.onboot,
      force: needRiskAck.value,
    })
    dialogVisible.value = false
    startCountdown(change)
    app.success(
      `配置已生效，请在 ${change.timeout_seconds} 秒内确认；未确认将自动恢复原配置`,
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
    await confirmNetworkChange(pending.value.change_id)
    if (tickTimer !== undefined) window.clearInterval(tickTimer)
    pending.value = null
    app.success('配置已确认')
    await load()
  } catch (error) {
    app.error(error instanceof Error ? error.message : '确认失败')
  }
}

async function doCancel(): Promise<void> {
  if (!pending.value) return
  try {
    await cancelNetworkChange(pending.value.change_id)
    if (tickTimer !== undefined) window.clearInterval(tickTimer)
    pending.value = null
    app.success('已恢复原配置')
    await load()
  } catch (error) {
    app.error(error instanceof Error ? error.message : '回滚失败')
  }
}
</script>

<template>
  <div class="card mb-16">
    <div class="card-header">
      <span class="card-title">网络配置</span>
      <button class="btn btn-sm" :disabled="loading" @click="load">刷新</button>
    </div>

    <!-- 待确认倒计时：必须置于最显眼位置 -->
    <div
      v-if="pending"
      class="card-body"
      style="
        background: var(--warning-bg, #fffbeb);
        border-bottom: 1px solid var(--border, #e5e7eb);
      "
    >
      <div class="flex-between" style="align-items: center; gap: 12px; flex-wrap: wrap">
        <div>
          <StatusTag text="等待确认" tone="warning" />
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
      <div
        v-if="pending.is_management"
        class="text-danger"
        style="font-size: 13px; margin-top: 6px"
      >
        这是承载当前连接的网口 —— 如果页面还能正常操作，说明配置有效；
        若你已切到其它网络访问，请直接点「确认配置无误」。
      </div>
    </div>

    <div class="table-wrap">
      <table class="table">
        <thead>
          <tr>
            <th style="width: 110px">网口</th>
            <th style="width: 100px">类型</th>
            <th style="width: 90px">状态</th>
            <th style="width: 200px">地址</th>
            <th style="width: 150px">网关 / DNS</th>
            <th>说明</th>
            <th style="width: 90px">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading">
            <td colspan="7" class="table-empty"><span class="spinner" /> 正在读取网口信息…</td>
          </tr>
          <tr v-else-if="!configurable.length && !advancedIfaces.length">
            <td colspan="7" class="table-empty">
              未读取到硬件网口。回环与容器虚拟接口不在此列出。
            </td>
          </tr>
          <tr v-for="iface in configurable" :key="iface.name">
            <td>
              <strong>{{ iface.name }}</strong>
            </td>
            <td class="text-secondary">{{ iface.type }}</td>
            <td>
              <StatusTag
                :text="iface.state === 'UP' ? '已连接' : iface.state"
                :tone="iface.state === 'UP' ? 'success' : 'default'"
              />
            </td>
            <td class="text-mono" style="font-size: 13px">{{ fmtAddresses(iface.addresses) }}</td>
            <td class="text-secondary" style="font-size: 13px">
              <div v-if="iface.gateway">网关 {{ iface.gateway }}</div>
              <div v-if="iface.dns?.length">DNS {{ iface.dns.join('、') }}</div>
              <span v-if="!iface.gateway && !iface.dns?.length">—</span>
            </td>
            <td>
              <StatusTag
                v-if="iface.is_management"
                text="当前管理口 · 修改会中断连接"
                tone="danger"
              />
              <span v-else class="text-secondary" style="font-size: 12px">
                {{ iface.bootproto || '—' }}{{ iface.onboot ? ' · 开机启用' : ' · 开机不启用' }}
              </span>
            </td>
            <td>
              <button v-if="canWrite" class="btn btn-sm" @click="openEdit(iface)">配置</button>
              <span v-else class="text-secondary" style="font-size: 12px">只读</span>
            </td>
          </tr>
          <!-- 聚合 / VLAN / 网桥：只读展示，编辑入口在「高级网络配置」卡片 -->
          <tr v-for="iface in advancedIfaces" :key="iface.name">
            <td>
              <strong>{{ iface.name }}</strong>
            </td>
            <td class="text-secondary">
              {{ ADVANCED_LABEL[iface.type] || iface.type }}
            </td>
            <td>
              <StatusTag
                :text="iface.state === 'UP' ? '已连接' : iface.state"
                :tone="iface.state === 'UP' ? 'success' : 'default'"
              />
            </td>
            <td class="text-mono" style="font-size: 13px">{{ fmtAddresses(iface.addresses) }}</td>
            <td class="text-secondary" style="font-size: 13px">
              <div v-if="iface.gateway">网关 {{ iface.gateway }}</div>
              <div v-if="iface.dns?.length">DNS {{ iface.dns.join('、') }}</div>
              <span v-if="!iface.gateway && !iface.dns?.length">—</span>
            </td>
            <td class="text-secondary" style="font-size: 12px">
              虚拟接口，在下方「高级网络配置」中管理
            </td>
            <td class="text-secondary">—</td>
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

    <div v-if="snapshot" class="card-body text-secondary" style="font-size: 12px">
      主机名 {{ snapshot.hostname || '—' }} · 默认网关 {{ snapshot.default_gateway || '—' }} ·
      读取于 {{ new Date(snapshot.read_at).toLocaleString() }}
      <div style="margin-top: 4px">
        仅列出硬件网口：回环（lo）与容器虚拟接口（docker0 / veth* / virbr* / br-*）不在此展示。
      </div>
    </div>

    <!-- 配置表单 -->
    <Modal v-model="dialogVisible" :title="`配置网口 ${editing?.name || ''}`" size="md">
      <div
        v-if="editing?.is_management"
        class="result-panel mb-12"
        style="border-left: 3px solid var(--danger, #dc2626)"
      >
        <StatusTag text="高风险操作" tone="danger" />
        <div style="font-size: 13px; margin-top: 6px; line-height: 1.7">
          该网口承载<strong>你当前正在使用的连接</strong>。改动它会导致远程访问中断，
          只能去机房直连设备恢复。请确认：
        </div>
        <label style="display: flex; gap: 8px; align-items: flex-start; margin-top: 8px">
          <input v-model="riskAcknowledged" type="checkbox" style="margin-top: 3px" />
          <span style="font-size: 13px">
            我了解修改该网口的风险，并确认可从带外通道（console / 机房直连）恢复
          </span>
        </label>
      </div>

      <div class="form-grid">
        <div class="form-item">
          <label class="form-label">地址获取方式</label>
          <select v-model="form.bootproto" class="select">
            <option value="static">静态地址</option>
            <option value="dhcp">自动获取（DHCP）</option>
          </select>
        </div>
        <template v-if="form.bootproto === 'static'">
          <div class="form-item">
            <label class="form-label">IP 地址</label>
            <input v-model.trim="form.ipaddr" class="input text-mono" placeholder="192.168.1.10" />
          </div>
          <div class="form-item">
            <label class="form-label">子网前缀</label>
            <input v-model.number="form.prefix" class="input text-mono" type="number" min="1" max="32" />
          </div>
        </template>
        <div class="form-item">
          <label class="form-label">网关</label>
          <input v-model.trim="form.gateway" class="input text-mono" placeholder="留空表示不设默认路由" />
          <span class="form-hint">
            留空时会显式写入 DEFROUTE=no，避免抢走管理口的默认路由
          </span>
        </div>
        <div class="form-item">
          <label class="form-label">DNS 服务器</label>
          <input v-model.trim="form.dns" class="input text-mono" placeholder="多个用逗号分隔" />
        </div>
        <div class="form-item">
          <label class="form-label">MTU</label>
          <input v-model.number="form.mtu" class="input text-mono" type="number" min="576" max="9000" />
        </div>
        <div class="form-item">
          <label class="form-label">开机启用</label>
          <select v-model="form.onboot" class="select">
            <option :value="true">启用</option>
            <option :value="false">不启用</option>
          </select>
        </div>
      </div>

      <div class="text-secondary" style="font-size: 12px; margin-top: 8px; line-height: 1.7">
        提交后配置<strong>立即生效</strong>，但需要你在弹窗关闭后的倒计时内点击「确认配置无误」；
        超时未确认，服务端会自动恢复原配置，避免改错后失联。
      </div>

      <template #footer>
        <button class="btn" @click="dialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="submitting || !canSubmit" @click="submit">
          <span v-if="submitting" class="spinner light" />
          {{ submitting ? '提交中…' : '应用配置' }}
        </button>
      </template>
    </Modal>
  </div>
</template>
