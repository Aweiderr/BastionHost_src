<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Modal from '@/components/Modal.vue'
import type { OpsAsset } from '@/api/ops'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { oracleGatewayHost } from '@/utils/constants'
import {
  BASTION_PASSWORD_PLACEHOLDER,
  DB_PASSWORD_PLACEHOLDER,
  buildOracleConnectGuide,
} from '@/utils/oracleConnect'

/**
 * Oracle 客户端连接说明。
 *
 * 只展示可复制的连接串和占位符，不读取、不回填堡垒机口令或库口令。
 */
const props = defineProps<{
  modelValue: boolean
  asset: OpsAsset | null
}>()

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

const app = useAppStore()
const auth = useAuthStore()
const selectedAccountId = ref(0)

watch(
  () => [props.modelValue, props.asset?.asset_id] as const,
  ([open]) => {
    if (!open) return
    selectedAccountId.value = props.asset?.accounts?.[0]?.account_id ?? 0
  },
)

const accounts = computed(() => props.asset?.accounts ?? [])

const selectedAccount = computed(() => {
  return accounts.value.find((item) => item.account_id === selectedAccountId.value) ?? null
})

const guide = computed(() => {
  if (!props.asset) return null
  return buildOracleConnectGuide({
    opsUsername: auth.user?.username,
    assetId: props.asset.asset_id,
    accountName: selectedAccount.value?.account_name,
    gatewayHost: oracleGatewayHost(),
  })
})

const listedEndpoint = computed(() => {
  if (!props.asset) return ''
  return `${props.asset.host}:${props.asset.port}`
})

function close(): void {
  emit('update:modelValue', false)
}

async function copyText(text: string, label: string): Promise<void> {
  const value = text.trim()
  if (!value) return
  try {
    await navigator.clipboard.writeText(value)
    app.success(`已复制${label}`)
  } catch {
    app.error('浏览器拒绝了剪贴板访问，请手动复制')
  }
}
</script>

<template>
  <Modal
    :model-value="modelValue"
    title="Oracle 客户端连接说明"
    size="lg"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template v-if="asset && guide">
      <div class="result-panel guide-warn mb-16">
        请在本机使用 sqlplus 或 DBeaver 连接下方网关，不要打开 Web 终端。
        列表中的资产地址
        <span class="text-mono">{{ listedEndpoint }}</span>
        是堡垒机转发用的目标（常见为 127.0.0.1:1522），工作站不要直连。
      </div>

      <div class="asset-line mb-12">
        <span class="text-secondary">资产</span>
        <span>{{ asset.asset_name }}</span>
      </div>

      <div class="field-row">
        <div class="field-label">网关地址</div>
        <div>
          <div class="field-value text-mono">{{ guide.gateway }}</div>
          <div class="form-hint">SQL 客户端填写此地址。端口为堡垒机 Oracle 监听，默认 1521。</div>
        </div>
        <button class="btn btn-sm" type="button" @click="copyText(guide.gateway, '网关地址')">复制</button>
      </div>

      <div class="field-row">
        <div class="field-label">SERVICE_NAME</div>
        <div>
          <div class="field-value text-mono">{{ guide.serviceName }}</div>
          <div class="form-hint">
            运维用户名@资产 ID。选定账号后写成 运维用户名@资产 ID#账号名。
          </div>
        </div>
        <button class="btn btn-sm" type="button" @click="copyText(guide.serviceName, 'SERVICE_NAME')">
          复制
        </button>
      </div>

      <div class="field-row">
        <div class="field-label">数据库用户名</div>
        <div>
          <select
            v-if="accounts.length > 1"
            v-model.number="selectedAccountId"
            class="select"
          >
            <option v-for="acc in accounts" :key="acc.account_id" :value="acc.account_id">
              {{ acc.account_name }}
            </option>
          </select>
          <div v-else-if="guide.dbUsername" class="field-value text-mono">{{ guide.dbUsername }}</div>
          <div v-else class="field-value text-muted">未绑定可用账号</div>
          <div class="form-hint">
            <template v-if="accounts.length > 1">
              该资产有多个账号，服务名必须带 #账号名。切换后下面的命令会一起更新。
            </template>
            <template v-else-if="guide.dbUsername">当前账号已写入服务名。</template>
            <template v-else>请联系管理员绑定账号。未选定账号时服务名不带 #。</template>
          </div>
        </div>
        <button
          v-if="guide.dbUsername"
          class="btn btn-sm"
          type="button"
          @click="copyText(guide.dbUsername, '数据库用户名')"
        >
          复制
        </button>
      </div>

      <div class="snippet-block">
        <div class="snippet-head">
          <span class="form-label">sqlplus</span>
          <button class="btn btn-sm" type="button" @click="copyText(guide.sqlplus, 'sqlplus 命令')">
            复制
          </button>
        </div>
        <pre class="snippet text-mono">{{ guide.sqlplus }}</pre>
        <div class="form-hint">
          在运维工作站执行。服务名已按当前账号填好；请把
          <span class="text-mono">{{ DB_PASSWORD_PLACEHOLDER }}</span>
          换成数据库口令，把
          <span class="text-mono">{{ BASTION_PASSWORD_PLACEHOLDER }}</span>
          换成堡垒机登录口令。
        </div>
      </div>

      <div class="snippet-block">
        <div class="snippet-head">
          <span class="form-label">DBeaver / JDBC</span>
          <button class="btn btn-sm" type="button" @click="copyText(guide.jdbcUrl, 'JDBC URL')">
            复制
          </button>
        </div>
        <pre class="snippet text-mono">{{ guide.jdbcUrl }}</pre>
        <div class="form-hint">
          DBeaver 选择 Oracle、驱动 Thin，URL 粘贴这一行。用户名填数据库用户名，口令填数据库口令。URL 里的
          <span class="text-mono">{{ BASTION_PASSWORD_PLACEHOLDER }}</span>
          同样换成堡垒机登录口令。描述符与上面的 sqlplus 相同。
        </div>
      </div>

      <div class="field-row">
        <div class="field-label">BASTION_PASSWORD</div>
        <div>
          <div class="field-value">您登录本堡垒机的口令</div>
          <div class="form-hint">
            与 Web 登录口令相同。连接串里保留占位符
            <span class="text-mono">{{ BASTION_PASSWORD_PLACEHOLDER }}</span>
            ，请自行替换，本页不显示、不代填真实口令。若开启了动态令牌，在口令末尾追加英文冒号和六位数字。
          </div>
        </div>
      </div>

      <div class="field-row">
        <div class="field-label">数据库口令</div>
        <div>
          <div class="field-value">该 Oracle 账号自己的登录口令</div>
          <div class="form-hint">
            请向管理员索取，或按运维策略从凭据保管处获取。连接串中的
            <span class="text-mono">{{ DB_PASSWORD_PLACEHOLDER }}</span>
            是占位符，本页不显示、不代填该口令。
          </div>
        </div>
      </div>
    </template>

    <template #footer>
      <button class="btn" type="button" @click="close">关闭</button>
    </template>
  </Modal>
</template>

<style scoped>
.guide-warn {
  border-left: 3px solid var(--color-warning);
  background: var(--color-warning-soft);
  font-size: 13px;
  line-height: 1.65;
}

.asset-line {
  display: flex;
  gap: 12px;
  font-size: 13px;
}

.field-row {
  display: grid;
  grid-template-columns: 148px minmax(0, 1fr) auto;
  gap: 4px 12px;
  align-items: start;
  padding: 8px 0;
  border-top: 1px solid var(--color-border);
}

.field-label {
  padding-top: 4px;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.field-value {
  padding-top: 4px;
  font-size: 13px;
  line-height: 1.5;
  word-break: break-all;
}

.field-row .btn {
  margin-top: 1px;
}

.snippet-block {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--color-border);
}

.snippet-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.snippet {
  margin: 0 0 8px;
  padding: 10px 12px;
  white-space: pre-wrap;
  word-break: break-all;
  font-size: 12.5px;
  line-height: 1.55;
  background: var(--color-surface-soft);
  border: 1px solid var(--color-border);
  border-radius: var(--radius);
}
</style>
