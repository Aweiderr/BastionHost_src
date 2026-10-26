<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { captchaRequired, fetchCaptcha } from '@/api/auth'
import type { LoginResult } from '@/api/types'
import logo2x from '@/assets/logo@2x.png'
import logoUrl from '@/assets/logo.png'
import ClientDownloadDialog from '@/components/ClientDownloadDialog.vue'
import MfaDialog from '@/components/MfaDialog.vue'
import UkeyMfaDialog from '@/components/UkeyMfaDialog.vue'
import { canAccessPath, firstAllowedPath } from '@/router'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const app = useAppStore()

const form = ref({ username: '', password: '', totp_code: '' })
const showTotp = ref(false)
const submitting = ref(false)
const errorText = ref('')
/** 客户端下载弹窗：平台清单收在弹窗内，登录页保持简洁 */
const showDownload = ref(false)

// ---------------------------------------------------------------------------
// 图形验证码
//
// 按**连续失败次数**触发（默认 3 次），不是每次登录都要求 ——
// 正常用户几乎感知不到，而脚本在试错几次后就会被挡住。
// 触发后即使输入正确也不再显示？不：只要达到阈值就一直要求，
// 直到登录成功（成功后服务端会清零失败计数）。
// ---------------------------------------------------------------------------
const captchaNeeded = ref(false)
const captchaId = ref('')
const captchaImage = ref('')
const captchaCode = ref('')

/** 拉取一张新验证码；旧的 objectURL 必须释放，否则会持续占用内存 */
async function refreshCaptcha(): Promise<void> {
  try {
    if (captchaImage.value) URL.revokeObjectURL(captchaImage.value)
    const { id, objectUrl } = await fetchCaptcha()
    captchaId.value = id
    captchaImage.value = objectUrl
    captchaCode.value = ''
  } catch {
    captchaNeeded.value = false
  }
}

onBeforeUnmount(() => {
  if (captchaImage.value) URL.revokeObjectURL(captchaImage.value)
})

// 进入登录页就判断是否需要验证码。
//
// 默认模式是 always（每次登录都要求），因此正常情况下这里会立即加载一张图；
// 若部署把模式改成 after_failures，则此处不会展示，直到该账号失败达到阈值。
// 不传用户名：always 模式下的判定与账号无关，传空即可。
onMounted(async () => {
  try {
    const r = await captchaRequired('')
    captchaNeeded.value = r.required
    if (captchaNeeded.value) await refreshCaptcha()
  } catch {
    /* 探测失败不展示；登录时若服务端要求，会在错误处理里补显示 */
  }
})

// ---------------------------------------------------------------------------
// MFA 二次认证
//
// 流程：账密 + TOTP（如启用）校验通过后，若账号配置了短信 MFA，
// 服务端返回 mfa_required 与 challenge_id，此时**尚未签发令牌**；
// 弹窗校验通过后才真正登录。
//
// 首次验证码由服务端在账密通过时自动发出（配置 mfa.sms.auto_send_on_open），
// 因此弹窗打开时只是展示与计时，不重复请求。
// ---------------------------------------------------------------------------
const showMfa = ref(false)
const mfaChallengeId = ref('')
const mfaPhoneMasked = ref('')
const mfaHint = ref('')
const mfaDialog = ref<InstanceType<typeof MfaDialog> | null>(null)

// ---------------------------------------------------------------------------
// UKey 二次认证
//
// 后端在账密通过、且该账号 mfa_type=ukey 时，直接在登录响应里带回
// challenge_id / sign_data / src_data，因此这里**不需要**再调
// /auth/ukey/challenge 取一次 —— 少一次往返，也少一处可能不一致的来源。
// ---------------------------------------------------------------------------
const showUkeyMfa = ref(false)
const ukeyChallengeId = ref('')
const ukeySignData = ref('')
const ukeySrcData = ref('')
const ukeyExpireTime = ref('')
const ukeyUsername = ref('')
const ukeyHint = ref('')

/** UKey 签名校验通过：与短信 MFA 成功后走同一条落地路径 */
async function onUkeySuccess(result: LoginResult, authMethod: string): Promise<void> {
  showUkeyMfa.value = false
  // 令牌由 store 落地：这一步与 verifyMfa 完全对称，漏掉就会出现
  // "提示登录成功但页面还是未登录"的现象
  auth.applyUKeyLogin(result)
  if (authMethod === 'password+ukey_bypass') {
    // 保留与短信 bypass 同样的提示：测试模式下并未真正校验签名
    app.info('当前为测试模式，UKey 签名未经真实校验')
  }
  await afterLoginSuccess(result)
}

/** 取消 UKey 二次验证：清掉挑战相关状态，要求重新走账密 */
function onUkeyCancel(): void {
  ukeyChallengeId.value = ''
  ukeySignData.value = ''
  ukeySrcData.value = ''
  ukeyExpireTime.value = ''
  ukeyHint.value = ''
  errorText.value = '已取消 UKey 二次验证，请重新登录'
}

/**
 * 登录成功后的落点。
 *
 * 须改密时不能进业务页。临时口令登录本身是成功的（令牌已签发），
 * 但仪表盘等页面一挂载就会请求被拦截的接口，返回 AUTH_4008。
 * 若这时再把用户带进那些页面，等于立刻触发一串必然失败的请求。
 * 改密页只使用白名单接口（查看自身 / 修改口令 / 登出）。
 *
 * 口令可用时，两处都必须防越权往返：
 *   1. 没有 ?redirect= 时不再固定跳 /dashboard —— 运维（operator）与审批人
 *      被路由 meta 的 hideForRoles 挡在仪表盘之外，跳过去只会被守卫弹回，
 *      用户看到的是"欢迎回来"紧跟着一条权限提示；
 *   2. ?redirect= 指向的目标也要先判一次权限，没权限就直接落到首个可用页面，
 *      避免"先跳过去、被弹回"期间把该页面的数据请求也打出去。
 */
async function redirectAfterLogin(): Promise<void> {
  const raw = typeof route.query.redirect === 'string' ? route.query.redirect : ''
  // 只接受站内绝对路径：防止被构造成 //evil.com 这类协议相对地址
  const target = raw.startsWith('/') && !raw.startsWith('//') ? raw : ''
  const dest = target && canAccessPath(target) ? target : firstAllowedPath()
  await router.replace(dest)
}

async function afterLoginSuccess(result: LoginResult): Promise<void> {
  if (result.must_change_password || result.password_expired) {
    // 先闩住，避免紧接着的 AUTH_4008 再弹一次同样的话
    auth.notePasswordExpired()
    app.info('口令已过期，请先修改口令')
    await router.replace('/account')
    return
  }
  app.success(`欢迎回来，${result.display_name || result.username}`)
  await redirectAfterLogin()
}

async function submit(): Promise<void> {
  errorText.value = ''
  if (!form.value.username || !form.value.password) {
    errorText.value = '请输入用户名与口令'
    return
  }
  if (captchaNeeded.value && !captchaCode.value.trim()) {
    errorText.value = '请输入图形验证码'
    return
  }
  submitting.value = true
  try {
    const result = await auth.login(
      form.value.username,
      form.value.password,
      form.value.totp_code,
      captchaNeeded.value ? { id: captchaId.value, code: captchaCode.value.trim() } : undefined,
    )

    // 需要第二因子：打开弹窗，此时还没有令牌
    if (result.mfa_required) {
      // UKey 与短信、TOTP 是同一层级的第二因子，但校验方式完全不同：
      // 短信要用户输入收到的验证码，UKey 要用钥匙里的私钥对服务端原文签名。
      // 两者共用"账密通过 → 弹窗 → 换取令牌"这条骨架，弹窗各自实现。
      if (result.mfa_type === 'ukey') {
        ukeyChallengeId.value = result.challenge_id || ''
        ukeySignData.value = result.sign_data || ''
        ukeySrcData.value = result.src_data || ''
        ukeyExpireTime.value = result.challenge_expire_time || ''
        ukeyUsername.value = result.display_name || result.username || form.value.username
        ukeyHint.value = result.mfa_hint || ''
        showUkeyMfa.value = true
        return
      }
      mfaChallengeId.value = result.challenge_id || ''
      mfaPhoneMasked.value = result.phone_masked || ''
      mfaHint.value = result.mfa_hint || ''
      showMfa.value = true
      return
    }

    await afterLoginSuccess(result)
  } catch (error) {
    const message = error instanceof Error ? error.message : '登录失败'
    errorText.value = message
    // 后端提示缺少动态口令时自动展开输入框
    if (message.includes('动态口令') || message.includes('TOTP') || message.includes('二次验证')) {
      showTotp.value = true
    }
    // 服务端要求图形验证码（或验证码输错）：显示并刷新一张
    if (message.includes('图形验证码')) {
      captchaNeeded.value = true
      await refreshCaptcha()
      // 主动问一次服务端，避免把"验证码错误"误判成"达到了阈值"
      try {
        const r = await captchaRequired(form.value.username)
        captchaNeeded.value = r.required
      } catch {
        /* 探测失败时保持当前状态，不影响用户重试 */
      }
    }
  } finally {
    submitting.value = false
  }
}

/** 提交第二因子验证码 */
async function onMfaSubmit(code: string): Promise<void> {
  try {
    const { result, authMethod } = await auth.verifyMfa(mfaChallengeId.value, code)
    showMfa.value = false
    if (authMethod === 'password+sms_bypass') {
      app.info('当前为测试模式，验证码未经真实校验')
    }
    await afterLoginSuccess(result)
  } catch (error) {
    // 失败时把原因交回弹窗展示，让用户可直接重试而不必重新输入账密
    const message = error instanceof Error ? error.message : '验证失败'
    mfaDialog.value?.fail(message)
  }
}

/** 取消第二因子：放弃本次登录，清理服务端挑战对应的前端状态 */
function onMfaCancel(): void {
  mfaChallengeId.value = ''
  mfaPhoneMasked.value = ''
  mfaHint.value = ''
  errorText.value = '已取消二次验证，请重新登录'
}
</script>

<template>
  <div class="auth-page">
    <div class="auth-card">
      <div class="auth-brand">
        <!--
          只取原 logo 里的公司图标，文字部分裁掉了。
          原因：原图右侧写的是「服务器密码机」，而这是堡垒机系统；
          把文字留在图片里既改不动、在高分屏下也发虚。
          裁掉后文字用 HTML 渲染 —— 清晰、可改、能跟随主题与语言。
        -->
        <img
          :src="logoUrl"
          :srcset="`${logoUrl} 1x, ${logo2x} 2x`"
          alt="北京创原天地科技有限公司"
          style="height: 38px; width: auto; display: block"
        />
        <span>堡垒机 · 运维审计系统</span>
      </div>

      <!-- 副标题已移除；原来由它提供的上下间距在这里显式补回，避免品牌与表单贴死 -->
      <form class="form-grid single" style="margin-top: 20px" @submit.prevent="submit">
        <div class="form-item">
          <label class="form-label">用户名</label>
          <input v-model.trim="form.username" class="input" autocomplete="username" placeholder="请输入用户名" />
        </div>
        <div class="form-item">
          <label class="form-label">口令</label>
          <input
            v-model="form.password"
            class="input"
            type="password"
            autocomplete="current-password"
            placeholder="请输入口令"
          />
        </div>

        <div v-if="showTotp" class="form-item">
          <label class="form-label">动态口令</label>
          <input
            v-model.trim="form.totp_code"
            class="input text-mono"
            maxlength="6"
            inputmode="numeric"
            placeholder="认证器中的 6 位数字"
          />
        </div>

        <!-- 图形验证码：仅在连续失败达到阈值后出现 -->
        <div v-if="captchaNeeded" class="form-item">
          <label class="form-label">图形验证码</label>
          <div style="display: flex; gap: 8px; align-items: center">
            <input
              v-model.trim="captchaCode"
              class="input text-mono"
              maxlength="6"
              autocomplete="off"
              placeholder="请输入图中字符"
              style="flex: 1"
            />
            <img
              v-if="captchaImage"
              :src="captchaImage"
              alt="点击刷新验证码"
              title="看不清？点击刷新"
              style="height: 38px; width: auto; cursor: pointer; border-radius: 4px"
              @click="refreshCaptcha"
            />
          </div>
          <div class="text-secondary" style="font-size: 12px; margin-top: 4px">
            连续登录失败较多，请输入图形验证码以继续。看不清可点击图片刷新。
          </div>
        </div>

        <div v-if="errorText" class="text-danger" style="font-size: 13px">{{ errorText }}</div>

        <button class="btn btn-primary btn-lg btn-block mt-8" type="submit" :disabled="submitting">
          <span v-if="submitting" class="spinner light" />
          {{ submitting ? '登录中…' : '登 录' }}
        </button>

        <div class="flex-between">
          <button class="btn-link" type="button" @click="showTotp = !showTotp">
            {{ showTotp ? '隐藏动态口令' : '使用动态口令（TOTP）' }}
          </button>
        </div>
      </form>

      <button class="btn btn-block mt-16" type="button" @click="showDownload = true">
        下载客户端
      </button>

      <ClientDownloadDialog v-model="showDownload" />

      <!-- 第二因子弹窗：账密通过后才出现 -->
      <MfaDialog
        ref="mfaDialog"
        v-model="showMfa"
        :challenge-id="mfaChallengeId"
        :phone-masked="mfaPhoneMasked"
        :hint="mfaHint"
        @submit="onMfaSubmit"
        @cancel="onMfaCancel"
      />

      <!-- UKey 第二因子：与短信弹窗互斥，由后端返回的 mfa_type 决定走哪一个 -->
      <UkeyMfaDialog
        v-model="showUkeyMfa"
        :challenge-id="ukeyChallengeId"
        :sign-data="ukeySignData"
        :src-data="ukeySrcData"
        :expire-time="ukeyExpireTime"
        :username="ukeyUsername"
        :hint="ukeyHint"
        @success="onUkeySuccess"
        @cancel="onUkeyCancel"
      />

      <div
        style="
          margin-top: 18px;
          font-size: 11px;
          line-height: 1.5;
          color: var(--text-secondary, #9aa3b2);
          text-align: center;
        "
      >
        Copyright 2001-至今 北京创原天地科技有限公司版权所有
      </div>
    </div>
  </div>
</template>
