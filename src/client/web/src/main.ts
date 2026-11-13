import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { configureHttp, endedByOtherLogin } from './api/http'
import { discardOracleWorkbench } from './stores/oracleWorkbench'
import { useAppStore } from './stores/app'
import { useAuthStore } from './stores/auth'
import './styles/base.css'
import './styles/components.css'

const app = createApp(App)
const pinia = createPinia()

app.use(pinia)

const auth = useAuthStore(pinia)
const appStore = useAppStore(pinia)

// 先把认证状态与全局提示注入 API 层，再安装路由，
// 保证首个路由守卫触发的 /auth/me 请求就已带上访问令牌。
configureHttp({
  getToken: () => auth.accessToken || null,
  onUnauthorized: (message: string) => {
    if (!auth.isAuthenticated) return
    // 被另一端顶掉时令牌已经作废，关闭接口不会成功。工作台状态留在模块里的话，
    // 下次登录还会显示一条服务端已经拆掉的会话。普通过期不丢会话号，重新登录后仍可关闭。
    if (endedByOtherLogin(message)) discardOracleWorkbench()
    auth.clear()
    const text = message || '登录状态已失效，请重新登录'
    appStore.error(text)
    try {
      window.sessionStorage.setItem('bastion.auth.ended', text)
    } catch {
      /* 隐私模式写不进去时，toast 仍然在 */
    }
    const current = router.currentRoute.value
    if (current.path !== '/login') {
      void router.replace({ path: '/login', query: { redirect: current.fullPath } })
    }
  },
  onPasswordExpired: (message: string) => {
    // 令牌仍然有效。清会话会把「刚用临时口令登录成功」变成「立刻被踢出」。
    const first = auth.notePasswordExpired()
    auth.openAccountDialog()
    if (first) appStore.error(message || '口令已过期，请先修改口令')
    if (router.currentRoute.value.path !== '/account') {
      void router.replace('/account')
    }
  },
  onForbidden: (message: string) => appStore.error(message || '无权执行该操作'),
  onError: (message: string) => appStore.error(message),
})

app.use(router)

app.mount('#app')
