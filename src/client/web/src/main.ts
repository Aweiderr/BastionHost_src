import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { configureHttp } from './api/http'
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
  onUnauthorized: () => {
    if (!auth.isAuthenticated) return
    auth.clear()
    appStore.error('登录状态已失效，请重新登录')
    const current = router.currentRoute.value
    if (current.path !== '/login') {
      void router.replace({ path: '/login', query: { redirect: current.fullPath } })
    }
  },
  onForbidden: (message: string) => appStore.error(message || '无权执行该操作'),
  onError: (message: string) => appStore.error(message),
})

app.use(router)

app.mount('#app')
