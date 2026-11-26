<script setup lang="ts">
/**
 * 我的资源：仅展示绑定给当前用户的资产（/ops/assets）。
 * SSH 等协议跳 Web 终端；Oracle 先选本机 CMD / DBeaver，或进入 SQL 工作台；需审批的引导去工单申请。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { listOpsAssets, type OpsAsset } from '@/api/ops'
import DataTable from '@/components/DataTable.vue'
import OracleConnectDialog from '@/components/OracleConnectDialog.vue'
import type { TableColumn } from '@/types/ui'
import { PROTOCOL_MAP, dictLabel } from '@/utils/constants'
import { formatTime } from '@/utils/format'
import { isGraphicalProtocol } from '@/utils/graphical'
import { isOracleProtocol } from '@/utils/oracleConnect'
import { oracleWorkbenchLocation } from '@/utils/oracleWorkbench'
import { filterResources, normalizeActiveProtocol, resourceGroups } from '@/utils/opsResources'

defineOptions({ name: 'OpsResourcesView' })

const router = useRouter()
const loading = ref(false)
const items = ref<OpsAsset[]>([])
const keyword = ref('')
const activeProtocol = ref('')
const oracleOpen = ref(false)
const oracleAsset = ref<OpsAsset | null>(null)

const columns: TableColumn[] = [
  { key: 'asset', title: '资产' },
  { key: 'endpoint', title: '地址', width: '200px' },
  { key: 'protocol', title: '协议', width: '100px' },
  { key: 'accounts', title: '可用账号' },
  { key: 'grant', title: '授权来源', width: '140px' },
  { key: 'valid', title: '有效期至', width: '160px' },
  { key: 'action', title: '操作', width: '160px', align: 'center' },
]

const groups = computed(() => resourceGroups(items.value))

const filtered = computed(() => filterResources(items.value, activeProtocol.value, keyword.value))

function selectProtocol(protocol: string): void {
  activeProtocol.value = activeProtocol.value === protocol ? '' : protocol
}

watch(groups, (list) => {
  activeProtocol.value = normalizeActiveProtocol(list, activeProtocol.value)
})

function grantLabel(row: OpsAsset): string {
  if (row.grant_source === 'ticket') return '工单通行证'
  if (row.grant_kind === 'pass') return '通行证'
  if (row.grant_source === 'manual') return '管理员绑定'
  return row.grant_source || row.grant_kind || '—'
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listOpsAssets()
    items.value = result.items ?? []
  } catch {
    items.value = []
  } finally {
    loading.value = false
  }
}

function connect(row: OpsAsset): void {
  if (row.need_approval) {
    void router.push({ path: '/tickets' })
    return
  }
  // Oracle 先选连接方式。本机工具留在弹窗里，堡垒机 SQL 会话进入独立工作台。
  if (isOracleProtocol(row.protocol)) {
    oracleAsset.value = row
    oracleOpen.value = true
    return
  }
  if (isGraphicalProtocol(row.protocol)) {
    void router.push({
      path: '/desktop',
      query: { asset: String(row.asset_id), connect: '1' },
    })
    return
  }
  void router.push({
    path: '/terminal',
    query: { asset: String(row.asset_id), connect: '1' },
  })
}

function openOracleWorkbench(accountId: number): void {
  const asset = oracleAsset.value
  if (!asset || !accountId) return
  const account = asset.accounts?.find((item) => item.account_id === accountId)
  oracleOpen.value = false
  void router.push(
    oracleWorkbenchLocation(asset.asset_id, accountId, {
      assetName: asset.asset_name,
      accountName: account?.account_name ?? '',
    }),
  )
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">我的资源</div>
        <div class="page-subtitle">
          仅列出已授权给您的资产。先按协议筛选，再点「连接」：SSH 打开 Web 终端；RDP / VNC 打开图形会话；Oracle 选择本机 CMD / DBeaver，或进入 SQL 工作台。需审批的请先提交工单。
        </div>
      </div>
      <button class="btn" type="button" :disabled="loading" @click="load">
        <span v-if="loading" class="spinner" />
        刷新
      </button>
    </div>

    <div v-if="items.length" class="protocol-grid">
      <button
        class="protocol-card"
        :class="{ active: !activeProtocol }"
        type="button"
        :aria-pressed="!activeProtocol"
        @click="activeProtocol = ''"
      >
        <span class="protocol-mark">全</span>
        <span class="protocol-copy">
          <span class="protocol-name">全部</span>
          <span class="protocol-hint">所有已授权资源</span>
        </span>
        <span class="protocol-count">{{ items.length }}</span>
      </button>
      <button
        v-for="group in groups"
        :key="group.protocol"
        class="protocol-card"
        :class="{ active: activeProtocol === group.protocol }"
        type="button"
        :aria-pressed="activeProtocol === group.protocol"
        @click="selectProtocol(group.protocol)"
      >
        <span class="protocol-mark">{{ group.label.slice(0, 1) }}</span>
        <span class="protocol-copy">
          <span class="protocol-name">{{ group.label }}</span>
          <span class="protocol-hint">{{ group.hint }}</span>
        </span>
        <span class="protocol-count">{{ group.count }}</span>
      </button>
    </div>

    <div class="card mb-16">
      <div class="card-body tight resource-toolbar">
        <div>
          <div class="card-title">
            {{ activeProtocol ? dictLabel(PROTOCOL_MAP, activeProtocol) : '全部资源' }}
          </div>
          <div class="text-secondary">共 {{ filtered.length }} 条</div>
        </div>
        <div class="flex-center gap-12">
          <span class="text-secondary">筛选</span>
          <input
            v-model="keyword"
            class="input"
            style="width: 280px"
            type="search"
            placeholder="资产名 / IP / 协议 / 账号"
          />
        </div>
      </div>
    </div>

    <DataTable
      :columns="columns"
      :loading="loading"
      :empty="!loading && filtered.length === 0"
      :empty-text="
        items.length
          ? '当前筛选没有匹配的资源'
          : '暂无可用资源，请先申请工单或联系管理员绑定'
      "
    >
      <tr v-for="row in filtered" :key="row.asset_id">
        <td>
          <div class="asset-name">{{ row.asset_name }}</div>
          <div v-if="row.remark" class="text-secondary" style="font-size: 12px">{{ row.remark }}</div>
        </td>
        <td class="text-mono">{{ row.host }}:{{ row.port }}</td>
        <td>
          <span class="tag tag-info">{{ dictLabel(PROTOCOL_MAP, row.protocol) }}</span>
          <span v-if="row.sftp_enabled || row.protocol === 'sftp'" class="tag">SFTP</span>
        </td>
        <td>
          <template v-if="row.accounts?.length">
            <span
              v-for="acc in row.accounts"
              :key="acc.account_id"
              class="tag"
              :class="{ 'tag-warning': acc.privileged }"
              style="margin: 2px 4px 2px 0"
            >
              {{ acc.account_name }}
            </span>
          </template>
          <span v-else class="text-muted">—</span>
        </td>
        <td>
          <div>{{ grantLabel(row) }}</div>
          <div v-if="row.need_approval" class="text-secondary" style="font-size: 12px">需审批后连接</div>
        </td>
        <td class="text-secondary">{{ formatTime(row.valid_to) }}</td>
        <td class="text-center">
          <button
            v-if="row.need_approval"
            class="btn btn-sm"
            type="button"
            @click="connect(row)"
          >
            去申请
          </button>
          <button
            v-else
            class="btn btn-primary btn-sm"
            type="button"
            @click="connect(row)"
          >
            连接
          </button>
        </td>
      </tr>
    </DataTable>

    <OracleConnectDialog v-model="oracleOpen" :asset="oracleAsset" @open-workbench="openOracleWorkbench" />
  </div>
</template>

<style scoped>
.asset-name {
  font-weight: 650;
}

.protocol-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.protocol-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--color-border, #d7dee8);
  border-radius: 12px;
  background: var(--color-surface, #fff);
  cursor: pointer;
  text-align: left;
}

.protocol-card:hover,
.protocol-card.active {
  border-color: var(--color-primary, #2563eb);
}

.protocol-card.active {
  box-shadow: inset 0 0 0 1px var(--color-primary, #2563eb);
}

.protocol-mark {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  background: #e0f2fe;
  color: #0369a1;
  font-weight: 700;
}

.protocol-copy {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.protocol-name {
  font-weight: 650;
}

.protocol-hint,
.protocol-count {
  color: #64748b;
  font-size: 12px;
}

.protocol-hint {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.protocol-count {
  font-size: 20px;
  font-weight: 700;
  color: #0f172a;
}

.resource-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

@media (max-width: 720px) {
  .protocol-grid {
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  }

  .resource-toolbar {
    align-items: flex-start;
  }

  .resource-toolbar .input {
    width: 100% !important;
  }
}
</style>