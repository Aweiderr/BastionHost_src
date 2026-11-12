<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { OpsAsset } from '@/api/ops'
import Modal from '@/components/Modal.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { CLIPBOARD_DENIED_MESSAGE, copyToClipboard } from '@/utils/clipboard'
import { oracleGatewayHost } from '@/utils/constants'
import {
  BASTION_PASSWORD_PLACEHOLDER,
  DB_PASSWORD_PLACEHOLDER,
  ORACLE_CONSOLE_MODE,
  ORACLE_CONSOLE_MODE_LABEL,
  ORACLE_LOCAL_MODE,
  ORACLE_LOCAL_MODE_LABEL,
  buildOracleConnectGuide,
} from '@/utils/oracleConnect'

/**
 * Oracle 连接方式。
 *
 * 本机 CMD / DBeaver 只给可复制说明（浏览器拉不起本机程序）。
 * 「堡垒机 SQL 会话」关掉本弹窗，交给独立的 SQL 工作台，不在这里贴 SQL。
 */
const props = defineProps<{
  modelValue: boolean
  asset: OpsAsset | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'open-workbench', accountId: number): void
}>()

const app = useAppStore()
const auth = useAuthStore()
const selectedAccountId = ref(0)
const mode = ref<'' | 'local' | 'console'>('')

watch(
  () => [props.modelValue, props.asset?.asset_id] as const,
  ([open]) => {
    if (!open) return
    selectedAccountId.value = props.asset?.accounts?.[0]?.account_id ?? 0
    mode.value = ''
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

function enterWorkbench(): void {
  if (!selectedAccount.value) return
  emit('open-workbench', selectedAccount.value.account_id)
  emit('update:modelValue', false)
}

async function copyText(text: string, label: string): Promise<void> {
  const value = text.trim()
  if (!value) return
  const copied = await copyToClipboard(value)
  if (copied) app.success(`已复制${label}`)
  else app.error(CLIPBOARD_DENIED_MESSAGE)
}

</script>

<template>
  <Modal
    :model-value="modelValue"
    title="Oracle 连接"
    size="lg"
    @update:model-value="(value) => (value ? emit('update:modelValue', true) : close())"
  >
    <template v-if="asset && guide">
      <div class="mode-switch" role="tablist" aria-label="Oracle 连接方式">
        <button
          class="btn"
          :class="{ 'btn-primary': mode === ORACLE_LOCAL_MODE }"
          type="button"
          role="tab"
          :aria-selected="mode === ORACLE_LOCAL_MODE"
          @click="mode = ORACLE_LOCAL_MODE"
        >
          {{ ORACLE_LOCAL_MODE_LABEL }}
        </button>
        <button
          class="btn"
          :class="{ 'btn-primary': mode === ORACLE_CONSOLE_MODE }"
          type="button"
          role="tab"
          :aria-selected="mode === ORACLE_CONSOLE_MODE"
          @click="mode = ORACLE_CONSOLE_MODE"
        >
          {{ ORACLE_CONSOLE_MODE_LABEL }}
        </button>
      </div>
      <p v-if="!mode" class="form-hint mode-hint">请先选择一种连接方式。</p>

      <div class="asset-line mb-12">
        <span class="text-secondary">资产</span>
        <span>{{ asset.asset_name }}</span>
      </div>

      <template v-if="mode === ORACLE_LOCAL_MODE">
        <div class="result-panel guide-warn mb-16">
          浏览器无法启动本机程序。请复制下面的命令，在已安装的 Windows cmd / sqlplus 或 DBeaver 里连接网关。
          列表中的资产地址
          <span class="text-mono">{{ listedEndpoint }}</span>
          是堡垒机转发用的目标（常见为 127.0.0.1:1522），工作站不要直连。
          本页不打开 SSH 终端，也不代填口令。
        </div>

        <div class="field-row">
          <div class="field-label">网关地址</div>
          <div>
            <div class="field-value text-mono">{{ guide.gateway }}</div>
            <div class="form-hint">SQL 客户端填写此地址。端口为堡垒机 Oracle 监听，默认 1521。需要 BASTION_ORACLE_ENABLE。</div>
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
            <span class="form-label">sqlplus（Windows cmd）</span>
            <button class="btn btn-sm" type="button" @click="copyText(guide.sqlplus, 'sqlplus 命令')">
              复制
            </button>
          </div>
          <pre class="snippet text-mono">{{ guide.sqlplus }}</pre>
          <div class="form-hint">
            运维工作站默认用 Windows Instant Client。把这一行粘贴到 cmd.exe。整段连接串放在双引号里，圆括号会原样交给 sqlplus。
            请把
            <span class="text-mono">{{ DB_PASSWORD_PLACEHOLDER }}</span>
            换成数据库口令，把
            <span class="text-mono">{{ BASTION_PASSWORD_PLACEHOLDER }}</span>
            换成堡垒机登录口令。
          </div>
        </div>

        <div class="snippet-block">
          <div class="snippet-head">
            <span class="form-label">sqlplus（bash）</span>
            <button class="btn btn-sm" type="button" @click="copyText(guide.sqlplusBash, 'bash sqlplus 命令')">
              复制
            </button>
          </div>
          <pre class="snippet text-mono">{{ guide.sqlplusBash }}</pre>
          <div class="form-hint">
            Linux 或 macOS 终端用这一行。整段连接串放在单引号里，服务名中的 # 会原样保留。占位符替换方式与上面相同。
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

      <template v-else-if="mode === ORACLE_CONSOLE_MODE">
        <div class="result-panel guide-warn mb-16">
          将进入独立的 SQL 工作台。语句在堡垒机上执行：先校验当前登录的授权，再用托管账号连接资产库，并写入会话审计。
          这不是 SSH 终端，浏览器也不会直接连接
          <span class="text-mono">{{ listedEndpoint }}</span>
          。未托管口令的账号请改用「{{ ORACLE_LOCAL_MODE_LABEL }}」。这里不显示、不代填口令。
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
            <div class="form-hint">选定账号后进入工作台。切到其它页面再回来，会话仍会留着。</div>
          </div>
          <button
            class="btn btn-primary btn-sm"
            type="button"
            :disabled="!selectedAccount"
            @click="enterWorkbench"
          >
            进入 SQL 工作台
          </button>
        </div>
      </template>
    </template>

    <template #footer>
      <button class="btn" type="button" @click="close">关闭</button>
    </template>
  </Modal>
</template>

<style scoped>
.mode-switch {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}

.mode-hint {
  margin-top: 0;
}

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

.sql-box {
  width: 100%;
  resize: vertical;
  min-height: 96px;
}

.sql-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 8px 0;
}

.sql-table-wrap {
  overflow: auto;
  max-height: 280px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius);
}

.sql-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
}

.sql-table th,
.sql-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--color-border);
  text-align: left;
  white-space: pre-wrap;
  word-break: break-all;
}

.form-error {
  margin: 8px 0;
  color: var(--color-danger);
  font-size: 13px;
}
</style>
