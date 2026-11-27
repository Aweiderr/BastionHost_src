<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  DEFAULT_WATERMARK,
  renderWatermark,
  watermarkCopies,
  type WatermarkSpec,
} from '@/utils/watermark'

const props = defineProps<{
  spec?: WatermarkSpec | null
  vars?: Record<string, string>
}>()

const nowText = ref(formatNow())
let timer: number | null = null

function formatNow(): string {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

onMounted(() => {
  timer = window.setInterval(() => {
    nowText.value = formatNow()
  }, 30000)
})

onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})

const spec = computed(() => props.spec ?? DEFAULT_WATERMARK)
const styleName = computed(() => {
  const style = spec.value.style
  if (style === 'tile' || style === 'corner' || style === 'diagonal') return style
  return 'diagonal'
})
const text = computed(() =>
  renderWatermark(spec.value.template || DEFAULT_WATERMARK.template, {
    ...(props.vars ?? {}),
    time: nowText.value,
  }),
)
const copies = computed(() => watermarkCopies(styleName.value))
const layerStyle = computed(() => ({
  opacity: String(spec.value.opacity > 0 ? spec.value.opacity : DEFAULT_WATERMARK.opacity),
  fontSize: `${spec.value.font_size || DEFAULT_WATERMARK.font_size}px`,
  '--wm-angle': `${spec.value.angle ?? DEFAULT_WATERMARK.angle}deg`,
}))
</script>

<template>
  <div v-if="spec.enabled !== false" class="wm-layer" :class="`wm-${styleName}`" :style="layerStyle" aria-hidden="true">
    <span v-for="n in copies" :key="n" class="wm-item">{{ text }}</span>
  </div>
</template>

<style scoped>
.wm-layer {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
  user-select: none;
  z-index: 3;
  color: #f8fafc;
}

.wm-diagonal,
.wm-tile {
  display: grid;
  grid-template-columns: 1fr 1fr;
  align-content: space-evenly;
  justify-items: center;
  transform: rotate(var(--wm-angle, -24deg));
  width: 140%;
  height: 140%;
  left: -20%;
  top: -20%;
}

.wm-tile {
  grid-template-columns: 1fr 1fr 1fr;
}

.wm-corner {
  position: absolute;
}

.wm-corner .wm-item:nth-child(1) {
  position: absolute;
  top: 8px;
  left: 8px;
}
.wm-corner .wm-item:nth-child(2) {
  position: absolute;
  top: 8px;
  right: 8px;
}
.wm-corner .wm-item:nth-child(3) {
  position: absolute;
  bottom: 8px;
  left: 8px;
}
.wm-corner .wm-item:nth-child(4) {
  position: absolute;
  bottom: 8px;
  right: 8px;
}

.wm-item {
  white-space: nowrap;
  font-weight: 600;
  letter-spacing: 0.04em;
}
</style>
