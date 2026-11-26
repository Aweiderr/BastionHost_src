<script setup lang="ts">
/**
 * 安全管理员设置用户二次认证方式。
 *
 * 只改 mfa_type 和短信手机号；不碰角色、口令、状态。系统管理员没有
 * security:mfa:manage，因此看不到这个入口。
 */
import { computed, ref, watch } from 'vue'
import { ApiError } from '@/api/http'
import type { UserItem } from '@/api/types'
import { assignUserMfa } from '@/api/users'
import Modal from '@/components/Modal.vue'
import { requestStepUpIfNeeded } from '@/stepup'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { factorChoices, isTargetFactor, type TargetFactor } from '@/views/userAdminMfa'

const props = defineProps<{
  modelValue: boolean
  user: UserItem | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'saved'): void
}>()

const app = useAppStore()
const auth = useAuthStore()
const factor = ref<TargetFactor>('totp')
const phone = ref('')
const busy = ref(false)
const errorText = ref('')

const choices = computed(() =>
  factorChoices({
    roles: props.user?.roles ?? [],
    editing: true,
    previousRoles: props.user?.roles ?? [],
    previousFactor: props.user?.mfa_type ?? '',
  }),
)

const currentText = computed(() => {
  const type = props.user?.mfa_type
  if (type === 'totp') return '动态口令'
  if (type === 'sms') return '短信验证码'
  if (type === 'ukey') return 'UKey'
  return '未指定'
})

watch(
  () => [props.modelValue, props.user] as const,
  () => {
    if (!props.modelValue) return
    const previous = props.user?.mfa_type
    const initial = isTargetFactor(previous ?? '') ? (previous as TargetFactor) : 'totp'
    const enabled = choices.value.filter((opt) => !opt.disabled)
    factor.value = enabled.some((opt) => opt.value === initial) ? initial : (enabled[0]?.value ?? 'totp')
    phone.value = props.user?.phone ?? ''
    errorText.value = ''
  },
  { immediate: true },
)

function close(): void {
  if (busy.value) return
  emit('update:modelValue', false)
}

async function submit(): Promise<void> {
  if (!props.user) return
  if (!isTargetFactor(factor.value)) {
    errorText.value = '请选择二次认证方式'
    return
  }
  if (factor.value === 'sms' && !phone.value.trim()) {
    errorText.value = '选择短信二次认证时必须填写手机号'
    return
  }
  busy.value = true
  errorText.value = ''
  try {
    const proof = await requestStepUpIfNeeded(
      {
        action: 'mfa_assign',
        targetUserId: props.user.user_id,
        newFactor: factor.value,
        title: '确认是您本人在设置二次认证',
      },
      auth.user,
    )
    await assignUserMfa(props.user.user_id, {
      mfa_type: factor.value,
      phone: factor.value === 'sms' ? phone.value.trim() : undefined,
      step_up_proof: proof,
    })
    app.success('二次认证方式已更新')
    emit('saved')
    emit('update:modelValue', false)
  } catch (err) {
    const message = err instanceof ApiError ? err.message : err instanceof Error ? err.message : '设置失败'
    errorText.value = message
    if (!(err instanceof ApiError)) app.error(message)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :model-value="modelValue" title="设置二次认证" @update:model-value="close">
    <div v-if="user" class="form-grid single">
      <div class="form-item">
        <label class="form-label">用户</label>
        <div>{{ user.display_name || user.username }}（{{ user.username }}）</div>
      </div>
      <div class="form-item">
        <label class="form-label">当前方式</label>
        <div class="text-secondary">{{ currentText }}</div>
      </div>
      <div class="form-item">
        <label class="form-label">新的二次认证方式</label>
        <select v-model="factor" class="input">
          <option v-for="opt in choices" :key="opt.value" :value="opt.value" :disabled="opt.disabled">
            {{ opt.label }}
          </option>
        </select>
        <span class="form-hint">{{ choices.find((opt) => opt.value === factor)?.hint }}</span>
      </div>
      <div v-if="factor === 'sms'" class="form-item">
        <label class="form-label">手机号</label>
        <input v-model.trim="phone" class="input" placeholder="用于接收短信验证码" />
      </div>
      <p v-if="factor === 'totp'" class="form-hint">
        指定动态口令后，用户下次在 Web 登录时必须完成绑定，绑完才能进入系统。桌面客户端会提示改用 Web。
      </p>
      <p v-if="factor === 'ukey'" class="form-hint">
        UKey 需要用户先在 Web 端完成证书绑定；绑定前该账号无法使用 UKey 登录。
      </p>
      <div v-if="errorText" class="text-danger">{{ errorText }}</div>
    </div>
    <template #footer>
      <button class="btn" type="button" :disabled="busy" @click="close">取消</button>
      <button class="btn btn-primary" type="button" :disabled="busy" @click="submit">
        <span v-if="busy" class="spinner light" />
        保存
      </button>
    </template>
  </Modal>
</template>
