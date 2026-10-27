<script setup lang="ts">
/**
 * 我的资源：仅展示绑定给当前用户的资产（/ops/assets）。
 * 可直连的跳 Web 终端；需审批的引导去工单申请。
 */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { listOpsAssets, type OpsAsset } from '@/api/ops'
import DataTable from '@/components/DataTable.vue'
import SearchBar from '@/components/SearchBar.vue'
import type { TableColumn } from '@/types/ui'
import { PROTOCOL_MAP, dictLabel } from '@/utils/constants'
import { formatTime } from '@/utils/format'

defineOptions({ name: 'OpsResourcesView' })

const router = useRouter()
const loading = ref(false)
const items = ref<OpsAsset[]>([])
const keyword = ref('')

const columns: TableColumn[] = [
  { key: 'asset', title: '资产' },
  { key: 'endpoint', title: '地址', width: '200px' },
  { key: 'protocol', title: '协议', width: '100px' },
  { key: 'accounts', title: '可用账号' },
  { key: 'grant', title: '授权来源', width: '140px' },
  { key: 'valid', title: '有效期至', width: '160px' },
  { key: 'action', title: '操作', width: '160px', align: 'center' },
]

const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) return items.value
  return items.value.filter((row) => {
    const blob = [
      row.asset_name,
      row.host,
      row.protocol,
      row.remark,
      ...(row.accounts ?? []).map((a) => a.account_name),
    ]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return blob.includes(q)
  })
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
  void router.push({
    path: '/terminal',
    query: { asset: String(row.asset_id), connect: '1' },
  })
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">我的资源</div>
        <div class="page-subtitle">
          仅列出已授权给您的资产。可直连的点「连接」打开 Web 终端；需审批的请先提交工单。
        </div>
      </div>
      <div class="page-menu" role="toolbar" aria-label="我的资源">
        <button class="btn" type="button" :disabled="loading" @click="load">
          <span v-if="loading" class="spinner" />
          刷新
        </button>
      </div>
    </div>

    <SearchBar>
      <span class="text-secondary">筛选</span>
      <input
        v-model="keyword"
        class="input wide"
        type="search"
        placeholder="资产名 / IP / 账号"
      />
      <span class="text-muted">共 {{ filtered.length }} 条</span>
    </SearchBar>

    <DataTable
      class="mt-16"
      :columns="columns"
      :loading="loading"
      :empty="!loading && filtered.length === 0"
      empty-text="暂无可用资源，请先申请工单或联系管理员绑定"
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
  </div>
</template>

<style scoped>
.asset-name {
  font-weight: 650;
}
</style>