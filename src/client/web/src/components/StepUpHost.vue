<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { beginStepUp } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import {
  algTypeForCert,
  base64ToPem,
  checkComSetup,
  DEFAULT_CONTAINER,
  DEFAULT_CSP_NAME,
  getCertByContName,
  initProvider,
  sign,
  toUKeyError,
  verifyPin,
} from '@/utils/ukeyAgent'
import { cancelStepUp, currentStepUp, finishStepUp, onStepUpChange, type StepUpAsk } from '@/stepup'

const auth = useAuthStore()
const ask = ref<StepUpAsk | null>(null)
const password = ref('')
const totpCode = ref('')
const smsCode = ref('')
const pin = ref('')
const challengeId = ref('')
const srcData = ref('')
const errorText = ref('')
const busy = ref(false)

function sync(): void {
  ask.value = currentStepUp()
  if (!ask.value) {
    password.value = ''
    totpCode.value = ''
    smsCode.value = ''
    pin.value = ''
    challengeId.value = ''
    srcData.value = ''
    errorText.value = ''
  }
}

let stop = () => {}
onMounted(() => {
  stop = onStepUpChange(sync)
})
onUnmounted(() => stop())

const factor = () => auth.user?.mfa_type || 'none'

async function submit(): Promise<void> {
  if (!ask.value) return
  if (!password.value) {
    errorText.value = '请输入当前口令'
    return
  }
  busy.value = true
  errorText.value = ''
  try {
    const first = await beginStepUp({
      password: password.value,
      totp_code: totpCode.value || undefined,
      sms_code: smsCode.value || undefined,
      challenge_id: challengeId.value || undefined,
      action: ask.value.action,
      target_user_id: ask.value.targetUserId,
      new_factor: ask.value.newFactor || '',
    })
    if (first.step_up_proof) {
      finishStepUp(first.step_up_proof)
      return
    }
    if (first.mfa_type === 'sms' && first.challenge_id) {
      challengeId.value = first.challenge_id
      errorText.value = first.mfa_hint || '请输入短信验证码'
      return
    }
    if (first.mfa_type === 'ukey' && first.challenge_id && first.src_data) {
      challengeId.value = first.challenge_id
      srcData.value = first.src_data
      if (!pin.value.trim()) {
        errorText.value = '请输入 UKey PIN 后再次确认'
        return
      }
      await checkComSetup()
      await initProvider(DEFAULT_CSP_NAME, DEFAULT_CONTAINER)
      await verifyPin(pin.value)
      const certBase64 = await getCertByContName(DEFAULT_CONTAINER)
      const signature = await sign(first.src_data, algTypeForCert(certBase64))
      const done = await beginStepUp({
        password: password.value,
        challenge_id: first.challenge_id,
        cert: base64ToPem(certBase64),
        src_data: first.src_data,
        signature,
        action: ask.value.action,
        target_user_id: ask.value.targetUserId,
        new_factor: ask.value.newFactor || '',
      })
      if (!done.step_up_proof) throw new Error(done.mfa_hint || 'UKey 核验未完成')
      finishStepUp(done.step_up_proof)
      return
    }
    throw new Error(first.mfa_hint || '身份核验未完成')
  } catch (err) {
    errorText.value = toUKeyError(err).message || (err instanceof Error ? err.message : '身份核验失败')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <!--
    挂到 body，并压过账户设置、用户编辑等弹窗（.overlay 是 1000）。
    否则在已经打开的账户对话框里发起核验时，这一层会留在弹窗下面，无法操作。
  -->
  <Teleport to="body">
  <div v-if="ask" class="stepup-mask" role="dialog" aria-modal="true" :aria-label="ask.title">
    <div class="card stepup-card">
      <div class="card-header"><span class="card-title">{{ ask.title }}</span></div>
      <div class="card-body form-grid single">
        <p class="form-hint">
          需要重新验证您自己的口令和当前第二因子。不会要求输入被修改账号的验证码或 UKey。
        </p>
        <div class="form-item">
          <label class="form-label">当前口令<span class="required">*</span></label>
          <input v-model="password" class="input" type="password" autocomplete="current-password" />
        </div>
        <div v-if="factor() === 'totp'" class="form-item">
          <label class="form-label">动态口令<span class="required">*</span></label>
          <input v-model="totpCode" class="input" maxlength="6" placeholder="6 位数字" />
        </div>
        <div v-if="factor() === 'sms'" class="form-item">
          <label class="form-label">短信验证码</label>
          <input v-model="smsCode" class="input" maxlength="8" placeholder="发往您自己的手机" />
        </div>
        <div v-if="factor() === 'ukey'" class="form-item">
          <label class="form-label">UKey PIN</label>
          <input v-model="pin" class="input" type="password" placeholder="当前账号的 UKey" />
        </div>
        <p v-if="errorText" class="text-danger" style="font-size: 13px">{{ errorText }}</p>
      </div>
      <div class="card-body" style="display: flex; justify-content: flex-end; gap: 8px">
        <button class="btn" type="button" :disabled="busy" @click="cancelStepUp">取消</button>
        <button class="btn btn-primary" type="button" :disabled="busy" @click="submit">
          <span v-if="busy" class="spinner light" />
          确认
        </button>
      </div>
    </div>
  </div>
  </Teleport>
</template>

<style scoped>
.stepup-mask {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1600;
}
.stepup-card { width: min(440px, calc(100vw - 32px)); }
</style>
