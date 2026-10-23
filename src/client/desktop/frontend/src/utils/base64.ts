/**
 * 二进制与 base64 互转。
 *
 * 为什么需要它：Wails 的方法绑定走 JSON，二进制数据（终端输入输出）
 * 必须先编码成字符串。这里不用 `String.fromCharCode(...bytes)` 的展开写法 ——
 * 终端一次可能回传几十 KB，展开会触发「Maximum call stack size exceeded」。
 */
export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode.apply(null, Array.from(bytes.subarray(i, i + chunk)))
  }
  return btoa(binary)
}

export function base64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    out[i] = binary.charCodeAt(i)
  }
  return out
}

export function strToBase64(s: string): string {
  return bytesToBase64(new TextEncoder().encode(s))
}
