/** 资产协议是否走图形会话（RDP / VNC），而不是 Web 终端。 */
export function isGraphicalProtocol(protocol: string | undefined | null): boolean {
  const value = (protocol ?? '').trim().toLowerCase()
  return value === 'rdp' || value === 'vnc'
}
