<script lang="ts" setup>
/**
 * 登录页。
 *
 * 桌面端与 Web 端的登录差异：这里会把**设备指纹**一并展示出来 ——
 * 它是客户端前置准入的主体标识，运维人员需要知道自己这台设备是
 * 以什么身份登记的（后续许可绑定、换机重登记都以它为准）。
 */
import { onMounted, ref } from 'vue'
import { state, notify, type Profile } from '../store'
import { Login, Ping, SetCoreURL, VerifyMFA, ResendMFA, ChangePassword } from '../../wailsjs/go/main/App'

const emit = defineEmits<{ (e: 'logged-in', p: Profile): void }>()

const coreInput = ref('')
const username = ref('')
const password = ref('')
const totp = ref('')
const showTOTP = ref(false)
const mfaChallenge = ref('')
const mfaCode = ref('')
const mfaHint = ref('')
const needChangePwd = ref(false)
const oldPwd = ref('')
const newPwd = ref('')
const newPwd2 = ref('')
const busy = ref(false)
const probing = ref(false)
const probeResult = ref<{ ok: boolean; text: string } | null>(null)

onMounted(() => {
  coreInput.value = state.coreURL || 'http://127.0.0.1:8080'
  probe()
})

async function probe() {
  probing.value = true
  probeResult.value = null
  try {
    const url = await SetCoreURL(coreInput.value)
    coreInput.value = url
    state.coreURL = url
    const res: any = await Ping()
    if (res && res.ok) {
      probeResult.value = { ok: true, text: '管理服务可达' }
    } else {
      probeResult.value = { ok: false, text: res?.error || '不可达' }
    }
  } catch (e: any) {
    probeResult.value = { ok: false, text: String(e?.message || e) }
  } finally {
    probing.value = false
  }
}

async function finishLogin(p: any) {
  if (p?.cert_expiry_hint) {
    notify(p.cert_expiry_hint, 'err')
  }
  if (p?.must_change_password || p?.password_expired) {
    needChangePwd.value = true
    oldPwd.value = password.value
    notify('口令已过期或需强制修改，请先改密', 'err')
    return
  }
  notify(`欢迎，${p.display_name || p.username}`, 'ok')
  emit('logged-in', p as Profile)
}

async function submit() {
  if (!username.value || !password.value) {
    notify('请输入用户名与口令', 'err')
    return
  }
  busy.value = true
  try {
    await SetCoreURL(coreInput.value)
    const p: any = await Login(username.value, password.value, totp.value)
    if (p?.mfa_required && p?.challenge_id) {
      mfaChallenge.value = p.challenge_id
      mfaHint.value = p.mfa_hint || `请输入发往 ${p.phone_masked || '手机'} 的验证码`
      notify(mfaHint.value, 'info')
      return
    }
    await finishLogin(p)
  } catch (e: any) {
    const msg = String(e?.message || e)
    if (msg.includes('多因素') || msg.includes('验证码') || msg.includes('动态')) {
      showTOTP.value = true
    }
    notify(msg, 'err')
  } finally {
    busy.value = false
  }
}

async function submitMFA() {
  if (!mfaChallenge.value || !mfaCode.value) {
    notify('请输入短信验证码', 'err')
    return
  }
  busy.value = true
  try {
    const p: any = await VerifyMFA(mfaChallenge.value, mfaCode.value)
    mfaChallenge.value = ''
    await finishLogin(p)
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  } finally {
    busy.value = false
  }
}

async function resend() {
  if (!mfaChallenge.value) return
  try {
    await ResendMFA(mfaChallenge.value)
    notify('验证码已重新发送', 'ok')
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  }
}

async function submitChangePwd() {
  if (!newPwd.value || newPwd.value !== newPwd2.value) {
    notify('两次新口令不一致', 'err')
    return
  }
  busy.value = true
  try {
    await ChangePassword(oldPwd.value || password.value, newPwd.value)
    notify('口令已修改，请使用新口令重新登录', 'ok')
    needChangePwd.value = false
    password.value = ''
    mfaChallenge.value = ''
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  } finally {
    busy.value = false
  }
}

function copyDeviceId() {
  const id = state.device?.device_id || ''
  if (!id) return
  navigator.clipboard?.writeText(id)
  notify('设备标识已复制', 'ok')
}
</script>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-head">
        <div class="logo">堡</div>
        <div>
          <h1>堡垒机 · 运维客户端</h1>
          <p>本机设备指纹将用于运维准入登记，所有操作全程审计。</p>
        </div>
      </div>

      <label class="field">
        <span>管理服务地址</span>
        <div class="row">
          <input v-model="coreInput" placeholder="http://127.0.0.1:8080" @keyup.enter="probe" />
          <button class="ghost" style="flex: 0 0 auto" :disabled="probing" @click="probe">
            {{ probing ? '检测中…' : '检测' }}
          </button>
        </div>
      </label>
      <div v-if="probeResult" style="margin: -4px 0 12px">
        <span class="tag" :class="probeResult.ok ? 'ok' : 'err'">{{ probeResult.text }}</span>
      </div>

      <div class="device-chip">
        <div>
          设备标识
          <code style="margin-left: 6px">{{ (state.device?.device_id || '').slice(0, 24) }}…</code>
        </div>
        <button class="ghost sm" @click="copyDeviceId">复制</button>
      </div>

      <label class="field">
        <span>用户名</span>
        <input v-model="username" placeholder="堡垒机账号" autocomplete="off" />
      </label>
      <label class="field">
        <span>口令</span>
        <input v-model="password" type="password" placeholder="登录口令" @keyup.enter="submit" />
      </label>

      <label v-if="showTOTP" class="field">
        <span>动态令牌（6 位）</span>
        <input v-model="totp" maxlength="6" placeholder="例如 123456" @keyup.enter="submit" />
      </label>

      
      <div v-if="mfaChallenge" class="mfa-box">
        <label class="field">
          <span>短信验证码</span>
          <p class="hint">{{ mfaHint }}</p>
          <input v-model="mfaCode" maxlength="8" placeholder="6 位验证码" @keyup.enter="submitMFA" />
        </label>
        <div class="row" style="gap:8px;margin-top:8px">
          <button type="button" :disabled="busy" @click="resend">重新发送</button>
          <button type="button" class="primary" :disabled="busy" @click="submitMFA">验证并登录</button>
        </div>
      </div>
      <div v-if="needChangePwd" class="mfa-box">
        <label class="field"><span>原口令</span><input v-model="oldPwd" type="password" /></label>
        <label class="field"><span>新口令</span><input v-model="newPwd" type="password" /></label>
        <label class="field"><span>确认新口令</span><input v-model="newPwd2" type="password" @keyup.enter="submitChangePwd" /></label>
        <button type="button" class="primary" style="width:100%;margin-top:8px" :disabled="busy" @click="submitChangePwd">提交新口令</button>
      </div>
      <button class="primary" style="width: 100%" :disabled="busy" @click="submit">
        {{ busy ? '登录中…' : '登 录' }}
      </button>

      <div class="row" style="margin-top: 12px; justify-content: space-between">
        <a class="hint" style="cursor: pointer" @click="showTOTP = !showTOTP">
          {{ showTOTP ? '隐藏动态令牌' : '使用动态令牌（TOTP）' }}
        </a>
        <span class="hint">v0.1.0</span>
      </div>
    </div>
  </div>
</template>
