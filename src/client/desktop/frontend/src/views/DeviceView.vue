<script lang="ts" setup>
/**
 * 设备与准入页。
 *
 * 展示本机设备指纹的采集结果。之所以要把它做得可见、可复制：
 * 运维人员在管理端登记准入设备时需要提供设备标识，过去只能靠
 * 让用户手工执行 wmic / dmidecode 去拼，既麻烦又容易出错。
 */
import { computed } from 'vue'
import { state, notify } from '../store'

const d = computed(() => state.device)

const rows = computed(() => [
  { k: '主机名', v: d.value?.hostname || '—' },
  { k: '操作系统', v: d.value?.os || '—' },
  { k: '系统架构', v: d.value?.arch || '—' },
  { k: '网卡 MAC', v: d.value?.mac || '（未采集到）' },
  { k: '磁盘序列号', v: d.value?.disk_serial || '（未采集到）' },
  { k: '主板序列号', v: d.value?.board_serial || '（未采集到）' },
  { k: 'CPU 标识', v: d.value?.cpu || '（未采集到）' },
  { k: '采集时间', v: d.value?.collected_at || '—' },
])

function copy(text: string, label: string) {
  if (!text) return
  navigator.clipboard?.writeText(text)
  notify(`${label}已复制`, 'ok')
}
</script>

<template>
  <div class="card">
    <h3>设备标识（Device ID）</h3>
    <p class="hint" style="margin-top: -4px">
      由主板序列号、系统盘序列号、CPU 标识、物理网卡 MAC 经 SM3 归一化生成。
      主机名不参与计算 —— 改名不应导致准入许可失效。
    </p>
    <div
      style="
        display: flex; align-items: center; gap: 12px;
        background: #101a30; border: 1px solid var(--line);
        border-radius: 8px; padding: 12px 14px; margin: 10px 0 4px;
      "
    >
      <code style="flex: 1; font-family: var(--mono); font-size: 13.5px; color: #bfdbfe; word-break: break-all">
        {{ d?.device_id || '（尚未采集）' }}
      </code>
      <button class="ghost sm" @click="copy(d?.device_id || '', '设备标识')">复制</button>
    </div>
  </div>

  <div class="card">
    <h3>指纹明细</h3>
    <div class="kv">
      <template v-for="r in rows" :key="r.k">
        <div class="k">{{ r.k }}</div>
        <div class="v">{{ r.v }}</div>
      </template>
    </div>
  </div>

  <div v-if="d?.warnings?.length" class="card">
    <h3>采集告警 <span class="tag warn">{{ d?.warnings?.length }}</span></h3>
    <ul class="hint" style="margin: 0; padding-left: 18px">
      <li v-for="(w, i) in d?.warnings" :key="i">{{ w }}</li>
    </ul>
    <p class="hint" style="margin-bottom: 0">
      部分硬件信息在虚拟机、容器或权限受限的环境下无法读取，此时 Device ID
      会退化为由剩余要素计算。若需要在准入中强绑定设备，请在物理机上运行。
    </p>
  </div>

  <div class="card">
    <h3>客户端前置准入说明</h3>
    <p class="hint">
      本客户端会把上述设备标识用于运维准入登记：
    </p>
    <ul class="hint" style="padding-left: 18px">
      <li>首次在新设备上登录时，管理端可将该设备登记为可信设备；</li>
      <li>后续建立运维会话时，服务端会校验「账号 + 设备」的绑定关系；</li>
      <li>更换设备或重装系统会导致 Device ID 变化，需要重新登记。</li>
    </ul>
    <p class="hint" style="margin-bottom: 0">
      注意：设备指纹是准出的<strong>辅助因子</strong>，不能单独作为身份凭证 ——
      虚拟网卡 MAC 与部分主板信息可被伪造，因此服务端校验时必须结合用户身份
      与许可有效期共同判断。
    </p>
  </div>
</template>
