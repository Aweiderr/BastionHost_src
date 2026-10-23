/** 通用 UI 类型（供组件与页面共享，避免在 <script setup> 中导出类型） */

export interface TableColumn {
  key: string
  title: string
  width?: string
  align?: 'left' | 'center' | 'right'
}

export interface SelectOption {
  value: string | number
  label: string
}
