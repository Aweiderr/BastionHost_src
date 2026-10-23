/**
 * 桌面客户端的轻量状态：跨视图共享登录态、设备指纹与资产列表。
 *
 * 不引入 Pinia：桌面端只有一个窗口、状态量很小，
 * 多一个依赖就多一份需要跟随升级的东西。
 */
import { reactive } from 'vue'

export interface Profile {
  user_id: number
  username: string
  display_name: string
  roles: string[]
  permissions: string[]
  mfa_enabled: boolean
  must_change_password: boolean
  password_expired?: boolean
  cert_expiry_hint?: string
  mfa_required?: boolean
  mfa_type?: string
  challenge_id?: string
  phone_masked?: string
  mfa_hint?: string
}

export interface DeviceInfo {
  hostname: string
  os: string
  arch: string
  mac: string
  disk_serial: string
  board_serial: string
  cpu: string
  device_id: string
  collected_at: string
  warnings?: string[]
}

export interface Account {
  account_id: number
  account_name: string
  account_type: string
  privileged: boolean
}

export interface Asset {
  asset_id: number
  asset_name: string
  host: string
  port: number
  protocol: string
  level: string
  os_type: string
  remark: string
  accounts: Account[]
  grant_source: string
  valid_to: string | null
  can_file: boolean
}

export interface SessionRecord {
  session_id: number
  session_no: string
  asset_name: string
  account_name: string
  client_type: string
  status: string
  start_time: string
  command_count: number
  block_count: number
}

export type ViewName = 'workbench' | 'sessions' | 'device'

export const state = reactive({
  loggedIn: false,
  profile: null as Profile | null,
  device: null as DeviceInfo | null,
  coreURL: '',
  view: 'workbench' as ViewName,
  assets: [] as Asset[],
  /** 全局提示（右上角） */
  toast: { text: '', kind: 'info' as 'info' | 'ok' | 'err', seq: 0 },
  /** 当前活动会话 key（终端页签） */
  activeKey: '' as string,
})

export function notify(text: string, kind: 'info' | 'ok' | 'err' = 'info') {
  state.toast = { text, kind, seq: state.toast.seq + 1 }
}
