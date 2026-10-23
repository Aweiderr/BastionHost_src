import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ToastType = 'success' | 'error' | 'info'

export interface Toast {
  id: number
  type: ToastType
  message: string
}

let seq = 0

/**
 * 全局应用状态：右上角提示（toast）与全屏加载指示。
 * 页面内的一切用户反馈都走这里，禁止使用 alert。
 */
export const useAppStore = defineStore('app', () => {
  const toasts = ref<Toast[]>([])
  const globalLoading = ref(false)

  function dismiss(id: number): void {
    toasts.value = toasts.value.filter((item) => item.id !== id)
  }

  function push(message: string, type: ToastType, duration = 3600): number {
    if (!message) return 0
    seq += 1
    const id = seq
    toasts.value = [...toasts.value, { id, type, message }]
    window.setTimeout(() => dismiss(id), duration)
    return id
  }

  function success(message: string): number {
    return push(message, 'success')
  }

  function error(message: string): number {
    return push(message || '操作失败', 'error', 5000)
  }

  function info(message: string): number {
    return push(message, 'info')
  }

  function setGlobalLoading(value: boolean): void {
    globalLoading.value = value
  }

  return { toasts, globalLoading, push, success, error, info, dismiss, setGlobalLoading }
})
