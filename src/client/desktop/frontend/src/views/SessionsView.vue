<script lang="ts" setup>
/** 我的会话：运维人员回看自己的历史会话与命令统计。 */
import { onMounted, ref } from 'vue'
import { notify, type SessionRecord } from '../store'
import { MySessions } from '../../wailsjs/go/main/App'

const list = ref<SessionRecord[]>([])
const loading = ref(false)

onMounted(load)

async function load() {
  loading.value = true
  try {
    const res: any = await MySessions()
    list.value = (res || []) as SessionRecord[]
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  } finally {
    loading.value = false
  }
}

function fmt(t: string) {
  if (!t) return '—'
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) return t
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

function statusTag(s: string) {
  if (s === 'active') return 'ok'
  if (s === 'closed') return ''
  return 'warn'
}
</script>

<template>
  <div class="card">
    <div class="row" style="justify-content: space-between; align-items: center; margin-bottom: 12px">
      <h3 style="margin: 0">我的会话</h3>
      <button class="ghost sm" :disabled="loading" @click="load">
        {{ loading ? '刷新中…' : '刷新' }}
      </button>
    </div>

    <div v-if="loading" class="hint">加载中 …</div>
    <div v-else-if="!list.length" class="empty">暂无会话记录</div>
    <table v-else>
      <thead>
        <tr>
          <th>会话号</th>
          <th>资产</th>
          <th>账号</th>
          <th>客户端</th>
          <th>状态</th>
          <th>开始时间</th>
          <th>命令 / 阻断</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="s in list" :key="s.session_id">
          <td class="mono">{{ s.session_no }}</td>
          <td>{{ s.asset_name }}</td>
          <td class="mono">{{ s.account_name || '—' }}</td>
          <td>{{ s.client_type || '—' }}</td>
          <td><span class="tag" :class="statusTag(s.status)">{{ s.status }}</span></td>
          <td class="mono">{{ fmt(s.start_time) }}</td>
          <td>
            {{ s.command_count }}
            <span v-if="s.block_count" class="tag err" style="margin-left: 6px">
              阻断 {{ s.block_count }}
            </span>
          </td>
        </tr>
      </tbody>
    </table>
    <p class="hint" style="margin-top: 12px; margin-bottom: 0">
      被策略阻断的命令会在「阻断」列单独计数；完整命令明细与录像回放请在
      Web 控制台的「会话审计」中查看。
    </p>
  </div>
</template>
