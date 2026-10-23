<script setup lang="ts">
/**
 * UKey 二次认证弹窗（账密已通过，等待第二因子）。
 *
 * 与短信 MFA 弹窗（MfaDialog.vue）同构：账密通过时服务端**不签发令牌**，
 * 只下发一次性挑战，第二因子校验通过后才换取令牌。区别在于第二因子的
 * 校验方式不是"输入 6 位验证码"，而是"用 UKey 私钥对服务端原文签名"：
 *
 *   1. 服务端下发 sign_data（原文）与 src_data（= base64(sign_data)）；
 *   2. 客户端按序调用本机中间件：checkComSetup → InitProvider → VerifyPin
 *      → getCertByContName(iKeyType=2) → sign(src_data, AlgType=3)；
 *   3. 把 证书 + src_data + signature 提交 /auth/ukey/login 换令牌。
 *
 * 两个"必须原样"的点，改错就会得到"签名校验失败"这种没有信息量的报错：
 *   - src_data 必须原样交给中间件并原样回传服务端（中间件把它当数据本身签名，
 *     不做 base64 解码；服务端验签对象也是这串字符的字节）；
 *   - 取证书必须用 iKeyType=2（容器里另一张证书虽然也含 clientAuth，
 *     但其公钥与签名私钥不配对）。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { ukeyLogin } from '@/api/ukey'
import type { LoginResult } from '@/api/types'
import Modal from '@/components/Modal.vue'
import {
  DEFAULT_CONTAINER,
  DEFAULT_CSP_NAME,
  algTypeForCert,
  base64ToPem,
  certAlgorithmLabel,
  checkComSetup,
  getCertByContName,
  initProvider,
  isSecurePage,
  middlewareUrl,
  sign,
  toUKeyError,
  verifyPin,
} from '@/utils/ukeyAgent'

const props = defineProps<{
  modelValue: boolean
  challengeId: string
  /** 服务端签名原文 */
  signData: string
  /** base64(签名原文)；中间件签的就是这串字符本身 */
  srcData: string
  /** 挑战到期时间（ISO），用于界面倒计时 */
  expireTime?: string
  /** 挑战有效期（秒）；没有 expireTime 时用它兜底 */
  ttlSeconds?: number
  /** 当前登录用户显示名，仅用于文案；父组件可能拿不到，故允许空串 */
  username?: string
  hint?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'success', result: LoginResult, authMethod: string): void
  (e: 'cancel'): void
}>()

/** 与服务端 ukey.challenge.ttl_seconds 默认值保持一致 */
const DEFAULT_TTL_SECONDS = 120

const cspName = ref(DEFAULT_CSP_NAME)
const container = ref(DEFAULT_CONTAINER)
const pin = ref('')
const busy = ref(false)
const stepText = ref('')
const errorText = ref('')
/** 展开的排障指引；由「查看帮助」按钮切换 */
const showHelp = ref(false)
/**
 * 本次从 UKey 读到的证书算法（SM2 / RSA / 未知）。
 *
 * 展示它有两个作用：让用户知道系统识别到的算法是否符合预期，
 * 以及在 RSA 时给出弱算法告警（中间件只支持 RSA-SHA1）。
 */
const certAlgorithm = ref('')
/** 通道自检结论（中间件地址等），放在界面底部便于排障时对照 */
const channelHint = computed(
  () => `本机中间件地址：${middlewareUrl()}${isSecurePage() ? '（HTTPS 页面固定走 8199 端口）' : ''}`,
)

/* ------------------------------------------------------------------ 倒计时 */

const remaining = ref(0)
let timer: number | undefined

function stopTimer(): void {
  if (timer !== undefined) {
    window.clearInterval(timer)
    timer = undefined
  }
}

function startTimer(): void {
  stopTimer()
  const expireMs = props.expireTime ? new Date(props.expireTime).getTime() : NaN
  let seconds: number
  if (!Number.isNaN(expireMs)) {
    seconds = Math.floor((expireMs - Date.now()) / 1000)
  } else {
    seconds = props.ttlSeconds && props.ttlSeconds > 0 ? props.ttlSeconds : DEFAULT_TTL_SECONDS
  }
  // 允许 -10 秒的余量：客户端与服务端时钟略有偏差时不至于一打开就显示"已过期"
  remaining.value = Math.max(seconds, -10)
  timer = window.setInterval(() => {
    remaining.value -= 1
    if (remaining.value <= -10) stopTimer()
  }, 1000)
}

const expired = computed(() => remaining.value <= 0)
const remainingText = computed(() => (remaining.value > 0 ? `${remaining.value} 秒` : '已过期'))

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      errorText.value = ''
      stepText.value = ''
      busy.value = false
      pin.value = ''
      showHelp.value = false
      startTimer()
      void nextTick(() => {
        const el = document.getElementById('ukey-pin-input') as HTMLInputElement | null
        el?.focus()
      })
    } else {
      stopTimer()
      // PIN 是硬件凭据：关窗即清空，不留在内存里
      pin.value = ''
      stepText.value = ''
      // 换一张 Key / 换一张证书后算法可能不同，重新打开时重新识别
      certAlgorithm.value = ''
    }
  },
  { immediate: true },
)

onBeforeUnmount(stopTimer)

/* --------------------------------------------------------------- 签名并登录 */

function close(): void {
  emit('update:modelValue', false)
}

function onCancel(): void {
  emit('cancel')
  close()
}

/**
 * 走完整条「读证书 → 签名 → 换令牌」链路。
 *
 * 做成一次点击跑完而不是分成两步：读证书与签名之间要求 PIN 已验证过，
 * 拆开会让用户在一个"看起来什么都没发生"的界面上多点一次；
 * 而每一步的进度通过 stepText 展示，卡在哪里依然可见。
 */
async function authenticate(): Promise<void> {
  errorText.value = ''
  if (!pin.value.trim()) {
    errorText.value = '请输入 UKey PIN'
    return
  }
  if (!props.challengeId) {
    errorText.value = '登录挑战已失效，请返回重新输入账号口令'
    return
  }
  busy.value = true
  try {
    stepText.value = '正在检测本机 UKey 中间件…'
    await checkComSetup()

    stepText.value = '正在初始化 UKey 提供者…'
    await initProvider(cspName.value.trim() || DEFAULT_CSP_NAME, container.value.trim() || DEFAULT_CONTAINER)

    stepText.value = '正在校验 PIN…'
    await verifyPin(pin.value)

    stepText.value = '正在读取证书…'
    // iKeyType 固定为 2（持有签名私钥的那张），理由见文件头
    const certBase64 = await getCertByContName(container.value.trim() || DEFAULT_CONTAINER)
    // 算法必须从证书推导：SM2 → AlgType=3，RSA → 只能用 2（中间件没有 RSA-SHA256）。
    // 识别不出算法时 algTypeForCert 会直接报错，而不是硬用 3 签出一个验不过的值
    certAlgorithm.value = certAlgorithmLabel(certBase64)

    stepText.value = '正在签名…'
    // 必须签服务端下发的 src_data 原样字符串，不做任何解码
    const signature = await sign(props.srcData, algTypeForCert(certBase64))

    stepText.value = '正在向服务端提交验证…'
    // 中间件给的是 base64(DER)，而 /auth/ukey/login 的 cert 字段按 PEM 文本约定，
    // 这里在提交前转换，顺带验证一下证书数据本身是否可解析
    const certPem = base64ToPem(certBase64)

    const res = await ukeyLogin({
      challenge_id: props.challengeId,
      cert: certPem,
      src_data: props.srcData,
      signature,
    })
    stepText.value = ''
    emit('success', res.login, res.auth_method)
    close()
  } catch (err) {
    stepText.value = ''
    errorText.value = toUKeyError(err).message
    // PIN 错误后清空输入，让用户直接重新输入而不是先手动删掉旧值
    if (toUKeyError(err).code === 'PIN_ERROR') pin.value = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal
    :model-value="props.modelValue"
    title="UKey 二次身份验证"
    size="sm"
    :close-on-mask="false"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="text-secondary mb-12" style="font-size: 13px; line-height: 1.6">
      账号密码已通过验证。请插入
      <strong style="color: var(--color-primary, #2563eb)">{{ props.username || '本人' }}</strong>
      的 UKey 并输入 PIN，系统将用 Key 中的私钥完成签名验证。
    </div>

    <div v-if="props.hint" class="text-secondary mb-8" style="font-size: 12px">{{ props.hint }}</div>

    <div class="form-grid">
      <div class="form-item">
        <label class="form-label">CSP 名称</label>
        <input v-model.trim="cspName" class="input" :disabled="busy" />
      </div>
      <div class="form-item">
        <label class="form-label">证书容器名</label>
        <input v-model.trim="container" class="input text-mono" :disabled="busy" />
      </div>
      <div class="form-item span-2">
        <label class="form-label">UKey PIN</label>
        <input
          id="ukey-pin-input"
          v-model="pin"
          class="input"
          type="password"
          autocomplete="off"
          placeholder="请输入 UKey 的 PIN 码"
          :disabled="busy"
          @keyup.enter="authenticate"
        />
      </div>
    </div>

    <div class="flex-between mb-8" style="align-items: center">
      <span class="text-secondary" style="font-size: 12px">
        本次挑战剩余
        <b :class="expired ? 'text-danger' : 'text-success'">{{ remainingText }}</b>
        ，过期后需重新输入账号口令。
      </span>
      <span v-if="certAlgorithm" class="text-secondary" style="font-size: 12px">
        证书算法：<b>{{ certAlgorithm }}</b>
      </span>
    </div>

    <!-- RSA 是硬件能力限制下的妥协：中间件只有 RSA-SHA1，没有 SHA-256。
         这是安全事实，必须在用户签名之前就讲明白 -->
    <div
      v-if="certAlgorithm === 'RSA'"
      class="mb-8"
      style="font-size: 12px; line-height: 1.7; color: var(--color-warning)"
    >
      当前 UKey 证书为 <b>RSA</b> 类型：受 UKey 中间件算法能力限制，签名只能使用
      <b>RSA-SHA1</b>（中间件不支持 RSA-SHA256）。该方式仅用于兼容存量 RSA 密钥，
      安全性弱于 SM2，建议条件允许时更换为 SM2 类型的 UKey。
    </div>

    <!-- 过期是"必须重新走第一因子"的状态，提示要给出下一步动作而不只是报错 -->
    <div v-if="expired && !busy" class="text-danger mb-8" style="font-size: 12.5px; line-height: 1.6">
      本次登录挑战已过期（挑战一次性、短时效，且绑定来源 IP）。
      请关闭本弹窗后重新输入账号与口令以获取新的挑战。
    </div>

    <div v-if="stepText" class="text-secondary mb-8" style="font-size: 12px">
      <span class="spinner" />
      <span style="margin-left: 6px">{{ stepText }}</span>
    </div>

    <div v-if="errorText" class="text-danger mb-8" style="font-size: 12.5px; line-height: 1.6">
      {{ errorText }}
    </div>

    <!-- 排障指引：跨域/中间件这条链路的失败原因在浏览器里天然不可观测，
         把"该查什么"直接写在页面上，比让用户去翻文档有用得多 -->
    <div v-if="showHelp" class="mb-8" style="font-size: 12px; line-height: 1.7">
      <div class="form-label">UKey 认证排障指引</div>
      <div class="text-secondary">
        1. 先确认已安装 <b>UKey 中间件</b>（见客户端下载页），且其服务正在运行；
        可打开 <span class="text-mono">/bastion/ukey-test.html</span> 做一次链路自检。<br />
        2. 本页面当前走 <span class="text-mono">{{ middlewareUrl() }}</span>
        （HTTPS 页面只能连 8199 端口，HTTP 页面连 8198；连错端口浏览器会直接拦掉）。<br />
        3. 确认 <b>Key 已插入</b>且 CSP 名称、证书容器名与实际一致（可用中间件枚举提供者核对）。<br />
        4. PIN 连续输错会<b>锁定 Key</b>，锁死后需要在厂商工具里解锁或重置 PIN 后重试。<br />
        5. 提示"证书未绑定/已吊销/链不受信任"属于服务端判定：请联系管理员在
        「用户管理 → 证书链」或「证书链管理」中处理，客户端无法绕过。<br />
        6. 提示挑战过期：挑战一次性且默认仅 2 分钟有效，请返回登录页重新输入账号口令领取新挑战。
      </div>
    </div>

    <div class="text-muted" style="font-size: 11.5px; line-height: 1.6">
      {{ channelHint }}
    </div>

    <template #footer>
      <button class="btn" type="button" :disabled="busy" @click="onCancel">取消</button>
      <button class="btn" type="button" @click="showHelp = !showHelp">
        {{ showHelp ? '收起帮助' : '查看帮助' }}
      </button>
      <!-- 与"验证"是同一个动作，但语义上是"重新开始"：PIN 输错后用户第一反应是重新读 Key -->
      <button class="btn" type="button" :disabled="busy || expired" @click="authenticate">
        重新读取 UKey
      </button>
      <button
        class="btn btn-primary"
        type="button"
        :disabled="busy || expired"
        @click="authenticate"
      >
        <span v-if="busy" class="spinner light" />
        {{ busy ? '验证中…' : '签名并登录' }}
      </button>
    </template>
  </Modal>
</template>
