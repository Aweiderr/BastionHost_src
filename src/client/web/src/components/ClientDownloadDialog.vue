<script setup lang="ts">
import { computed } from 'vue'
import Modal from '@/components/Modal.vue'

/**
 * 客户端下载弹窗。
 *
 * 为什么做成弹窗而不是直接在登录页铺链接：客户端会随平台增多
 * （Windows、麒麟/统信、后续可能还有 ARM 与 macOS），
 * 全部平铺会把登录页挤得很长；收进弹窗后登录页保持简洁，
 * 平台清单也能独立扩展。
 *
 * 平台清单里**保留尚未构建的条目并明确标注**，而不是隐藏：
 * 用户问"有没有 Linux 版"时，能直接看到"编译中"，
 * 比看不到任何 Linux 字样要好 —— 后者会让人以为这个产品不支持。
 */
const props = defineProps<{ modelValue: boolean }>()
defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

interface PlatformItem {
  key: string
  name: string
  hint: string
  /** 下载地址；未提供时为空串 */
  url?: string
  size?: string
  available: boolean
  /** 未提供时的说明文案 */
  note?: string
}

const platforms = computed<PlatformItem[]>(() => [
  {
    key: 'windows',
    name: 'Windows 客户端',
    hint: 'Windows 10 / 11 · 64 位',
    url: '/bastion/downloads/bastion-desktop-windows-amd64.exe',
    size: '12.4 MB',
    available: true,
  },
  {
    key: 'linux',
    name: 'Linux 客户端',
    hint: '银河麒麟 V10 / 统信 UOS · x86_64',
    available: false,
    note: '编译中，暂未开放',
  },
])

const hasPending = computed(() => platforms.value.some((p) => !p.available))
</script>

<template>
  <Modal
    :model-value="props.modelValue"
    title="下载客户端"
    size="md"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="text-secondary mb-12" style="font-size: 13px">
      桌面客户端提供与 Web 控制台一致的受控会话能力，所有操作同样被录像与审计。
    </div>

    <div
      v-for="item in platforms"
      :key="item.key"
      class="result-panel mb-12"
      :style="item.available ? '' : 'opacity: .6'"
    >
      <div class="flex-between gap-8">
        <div>
          <div style="font-size: 14px; font-weight: 500">{{ item.name }}</div>
          <div class="text-secondary" style="font-size: 12px; margin-top: 2px">
            {{ item.hint }}
          </div>
          <div
            v-if="item.note"
            class="text-secondary"
            style="font-size: 12px; margin-top: 4px; color: var(--warning, #d97706)"
          >
            {{ item.note }}
          </div>
        </div>

        <a
          v-if="item.available"
          class="btn btn-sm btn-primary"
          :href="item.url"
          download
        >
          下载{{ item.size ? `（${item.size}）` : '' }}
        </a>
        <button v-else class="btn btn-sm" type="button" disabled>暂未开放</button>
      </div>
    </div>

    <div v-if="hasPending" class="text-secondary" style="font-size: 12px; margin-top: 4px">
      Linux 版需在目标系统上编译（依赖系统 WebView），完成后会在此处开放。
    </div>

    <template #footer>
      <button
        class="btn"
        type="button"
        @click="$emit('update:modelValue', false)"
      >
        关闭
      </button>
    </template>
  </Modal>
</template>
