/**
 * Oracle 建库用户的界面默认值。
 * 勾选只出现在 Oracle 新建托管账号上，默认开启；SSH 不带这个字段。
 */

export function defaultProvisionDbUser(protocol: string | undefined, editing: boolean): boolean {
  return !editing && protocol === 'oracle'
}

export function showProvisionDbUser(
  protocol: string | undefined,
  editing: boolean,
  credentialMode: string,
): boolean {
  return protocol === 'oracle' && !editing && credentialMode === 'managed'
}

/**
 * 只在 Oracle 新建时提交。取消勾选传 false，勾选传 true。
 * 不托管账号没有口令可设，固定传 false。
 * SSH 与编辑不带这个字段，服务端就保持原来的「只登记」行为。
 */
export function provisionDbUserField(
  protocol: string | undefined,
  editing: boolean,
  credentialMode: string,
  checked: boolean,
): boolean | undefined {
  if (protocol !== 'oracle' || editing) return undefined
  return credentialMode === 'managed' && checked
}

export function defaultSyncDbPassword(
  protocol: string | undefined,
  credentialMode: string | undefined,
): boolean {
  return protocol === 'oracle' && credentialMode !== 'manual'
}

/** 非 Oracle 不提交，避免 SSH 重置口令被当成改库口令。 */
export function syncDbPasswordField(
  protocol: string | undefined,
  checked: boolean,
): boolean | undefined {
  if (protocol !== 'oracle') return undefined
  return checked
}
