/**
 * SQL 工作台的存活状态。
 *
 * 放在页面组件外面：即使路由把页面卸掉再装回来，编辑器、结果、历史和对象树还在，
 * 也不会去调用关闭接口。退出登录时由布局主动关掉后端会话。
 */
import { reactive } from 'vue'
import { closeOracleConsole } from '@/api/ops'
import { createWorkbenchState, takeAllSessionIds } from '@/utils/oracleWorkbench'

export const oracleBench = reactive(createWorkbenchState())

export async function shutdownOracleWorkbench(): Promise<void> {
  const ids = takeAllSessionIds(oracleBench)
  await Promise.all(ids.map((id) => closeOracleConsole(id).catch(() => undefined)))
}

/** 丢掉本地工作台状态，不再调用关闭接口。用于令牌已经作废、服务端会自行收口的情况。 */
export function discardOracleWorkbench(): void {
  takeAllSessionIds(oracleBench)
}
