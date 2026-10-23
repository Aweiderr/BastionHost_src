<script setup lang="ts">
import { computed } from 'vue'

/**
 * 轻量指标卡片：当前值 + 迷你趋势折线。
 *
 * 为什么手写 SVG 而不是用 ECharts：
 * 这里只需要"一个数字 + 一条折线"，为三个小卡片引入 1MB 的图表库
 * 不划算（前端总包目前才 120KB）。手写 SVG 还能完全控制渲染，
 * 不依赖图表库的生命周期与响应式行为。
 */
const props = withDefaults(
  defineProps<{
    title: string
    /** 当前值 */
    value: number
    /** 历史点（含当前值），按时间从旧到新 */
    points?: number[]
    /** 数值单位，用于展示 */
    unit?: string
    /** 折线颜色 */
    color?: string
    /** Y 轴上限；百分比类传 100，速率类不传则按峰值自适应 */
    max?: number
    /** 数值格式化函数（如字节转 KB/MB） */
    format?: (v: number) => string
    /** 数据是否已就绪；false 时显示"采集中"而不是 0 */
    ready?: boolean
  }>(),
  {
    points: () => [],
    unit: '',
    color: '#2563eb',
    ready: true,
  },
)

const displayValue = computed(() => {
  if (!props.ready) return '采集中…'
  const text = props.format ? props.format(props.value) : props.value.toFixed(2)
  return props.unit ? `${text} ${props.unit}` : text
})

/** 折线的 SVG 坐标串（viewBox 100x30，preserveAspectRatio=none 拉伸铺满） */
const polyline = computed(() => {
  const list = props.points
  if (list.length < 2) return ''

  // Y 轴上限：百分比固定 100，其余按数据的峰值上浮 20%，
  // 保证曲线不会顶到边框，也避免恒为 0 时被放大成噪声
  const peak = Math.max(...list)
  let top = props.max ?? peak * 1.2
  if (!top || top <= 0) top = 1

  const stepX = 100 / (list.length - 1)
  return list
    .map((v, i) => {
      const x = (i * stepX).toFixed(2)
      const clamped = Math.min(Math.max(v, 0), top)
      const y = (30 - (clamped / top) * 30).toFixed(2)
      return `${x},${y}`
    })
    .join(' ')
})

/** 面积填充，让趋势更易读 */
const areaPath = computed(() => {
  const line = polyline.value
  if (!line) return ''
  return `M0,30 L${line.replace(/ /g, ' L')} L100,30 Z`
})
</script>

<template>
  <div class="metric-card">
    <div class="metric-head">
      <span class="metric-title">{{ title }}</span>
      <span class="metric-value">{{ displayValue }}</span>
    </div>

    <svg
      class="metric-spark"
      viewBox="0 0 100 30"
      preserveAspectRatio="none"
      role="img"
      :aria-label="`${title} 趋势`"
    >
      <path v-if="areaPath" :d="areaPath" :fill="color" opacity="0.12" />
      <polyline
        v-if="polyline"
        :points="polyline"
        fill="none"
        :stroke="color"
        stroke-width="1"
        stroke-linejoin="round"
        stroke-linecap="round"
        vector-effect="non-scaling-stroke"
      />
      <!-- 还没有足够数据点时给一条基线，避免卡片看起来是坏的 -->
      <line
        v-else
        x1="0"
        y1="29"
        x2="100"
        y2="29"
        :stroke="color"
        stroke-width="1"
        stroke-dasharray="3 3"
        opacity="0.4"
        vector-effect="non-scaling-stroke"
      />
    </svg>
  </div>
</template>

<style scoped>
.metric-card {
  padding: 14px 16px;
  border: 1px solid var(--border, #e5e7eb);
  border-radius: 8px;
  background: var(--card-bg, #fff);
}

.metric-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}

.metric-title {
  font-size: 13px;
  color: var(--text-secondary, #6b7280);
}

.metric-value {
  font-size: 18px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.metric-spark {
  display: block;
  width: 100%;
  height: 44px;
}
</style>
