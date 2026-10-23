'use strict'
// 《堡垒机详细设计文档》内容（第 8 ~ 11 章）
module.exports = function build(d) {
  // ================= 8 关键算法与机制设计 =================
  d.h(1, '8  关键算法与机制设计')
  d.p('本章说明系统中若干关键算法与机制的设计细节，这些部分是系统安全性与性能的关键控制点，也是单元测试与代码评审的重点。')

  d.h(2, '8.1  策略匹配算法')
  d.p('策略匹配需要在 30 ms 内完成对单条命令的多维匹配，因此采用「两级匹配」结构：先用 AC 自动机做关键字预筛（O(n) 复杂度，与规则数量无关），再用正则与条件求值做精确判定。')
  d.code([
    '// 两级匹配：AC 自动机预筛 + 精确判定',
    'function matchPolicy(cmd: ParsedCommand, policies: Policy[], ctx: AuthContext): Verdict {',
    '  // 第一级：AC 自动机快速筛选可能命中的规则（构建一次，多会话共享）',
    '  const candidateIds = acAutomaton.match(cmd.raw);   // 返回包含任一关键字的规则 ID',
    '  if (candidateIds.length === 0) return VERDICT_ALLOW_FAST;  // 快速放行路径',
    '',
    '  // 第二级：对候选规则做精确判定',
    '  let verdict = VERDICT_ALLOW;',
    '  for (const id of candidateIds) {',
    '    const p = policies[id];',
    '    if (!matchScope(p, cmd, ctx)) continue;            // 主体 / 客体 / 账号 / 时间 / IP',
    '    if (!matchPattern(p, cmd)) continue;               // 正则或精确匹配',
    '    if (!evalConditions(p.rules, ctx)) continue;       // ABAC 条件求值',
    '    verdict = strictest(verdict, p.action, p);         // 最严格优先',
    '    if (verdict.action === "block") break;             // 已判定阻断，可提前退出',
    '  }',
    '  return verdict;   // 携带命中规则编号，写入审计记录',
    '}',
  ])
  d.table({
    caption: '表 8-1  策略匹配性能设计',
    headers: ['环节', '算法/结构', '复杂度', '性能手段'],
    widths: [22, 28, 20, 30],
    rows: [
      ['关键字预筛', 'AC 自动机（Aho-Corasick）', 'O(文本长度)', '构建一次全局共享，支持热更新'],
      ['正则匹配', '预编译正则 + 超时保护', '与模式复杂度相关', '限制正则长度，单次匹配超时 10 ms'],
      ['条件求值', '条件编译为判定函数', 'O(条件数)', '避免反射与字符串解析'],
      ['策略加载', '本地 LRU 缓存 + 版本号', 'O(1)', '版本号失效，消息广播通知'],
      ['最严格优先', '动作优先级比较', 'O(1)', '阻断可直接短路提前返回'],
    ],
  })

  d.h(2, '8.2  国密加密与签名机制')
  d.table({
    caption: '表 8-2  密码算法应用设计',
    headers: ['场景', '算法', '密钥来源', '实现要点'],
    widths: [24, 18, 26, 32],
    rows: [
      ['通信加密', '国密 TLS（GM/T 0024 密码套件，SM2 证书 + SM4 加密）', '设备证书、客户端证书', '仅启用国密套件，禁用弱算法与低版本协议'],
      ['数字签名', 'SM2 签名', '密码机内签名密钥', '签名在密码机内完成，私钥不出卡'],
      ['口令存储', 'SM3 加盐摘要', '无需密钥', 'salt 16 字节随机，摘要 32 字节'],
      ['敏感字段加密', 'SM4-CBC', 'DEK（由 KEK 保护）', '每条记录独立 IV，IV 随密文存储'],
      ['会话密钥', 'SM4 会话密钥', '一次一密，随会话生成', '会话结束即销毁，密钥索引记入录像'],
      ['完整性校验', 'SM3 摘要 + 摘要链', '无需密钥', '分片摘要链接，整体签名'],
      ['标识认证（可选）', 'SM9 标识密码', 'KGC 派发标识密钥', '用于跨域免证书认证场景'],
    ],
  })
  d.code([
    '// SM4-CBC 加密流程（敏感字段）',
    'function encryptField(plaintext: string, keyIndex: string): CipherResult {',
    '  const iv = randomBytes(16);                       // 每条记录独立 IV',
    '  const cipher = gm.cipher("SM4-CBC", getKeyHandle(keyIndex), iv);',
    '  const ct = cipher.update(plaintext, "utf8") + cipher.final();',
    '  return {',
    '    ciphertext: ct,',
    '    iv: hex(iv),                 // IV 不保密，随密文存储',
    '    keyIndex: keyIndex,          // 仅存索引，密钥材料在密码机内',
    '    algorithm: "SM4-CBC",',
    '  };',
    '}',
    '',
    '// 口令摘要（恒定时间比对，防时序攻击）',
    'function hashPassword(password: string, salt: Buffer): string {',
    '  return "SM3$" + hex(salt) + "$" + hex(sm3(concat(salt, utf8(password))));',
    '}',
    'function verifyPassword(input: string, stored: string): boolean {',
    '  const [algo, saltHex] = stored.split("$");',
    '  const computed = hashPassword(input, fromHex(saltHex));',
    '  return timingSafeEqual(computed, stored);        // 恒定时间比较',
    '}',
  ])

  d.h(2, '8.3  摘要链与完整性校验算法')
  d.p('摘要链用于保证审计记录不可篡改：每条记录的 hash 与前一条记录的 hash 关联，形成 H(n) = SM3(content(n) || H(n-1))。任何一条被修改都会导致其后所有记录的校验失败。')
  d.code([
    '// 写入时的摘要链计算',
    'function calcRecordHash(record: AuditRecord, prevHash: string): string {',
    '  const canonical = canonicalize({          // 规范化字段顺序，避免因字段顺序变化导致摘要不一致',
    '    id: record.id, ts: record.time, user: record.userId,',
    '    action: record.action, target: record.target, detail: record.detail,',
    '  });',
    '  return hex(sm3(utf8(canonical + "|" + (prevHash || GENESIS_HASH))));',
    '}',
    '',
    '// 校验任务：按分区顺序重算摘要链',
    'function verifyChain(table: string, from: Date, to: Date): VerifyReport {',
    '  let prev = lastHashBefore(table, from);           // 上一分区最后一条的 hash',
    '  const broken: number[] = [];',
    '  for (const rec of streamRecords(table, from, to)) {',
    '    const expect = calcRecordHash(rec, prev);',
    '    if (expect !== rec.recordHash) broken.push(rec.id);',
    '    prev = rec.recordHash;',
    '  }',
    '  return { total: count, broken, ok: broken.length === 0 };',
    '}',
  ])
  d.table({
    caption: '表 8-3  校验任务设计',
    headers: ['任务', '频率', '范围', '失败处理'],
    widths: [24, 18, 26, 32],
    rows: [
      ['增量校验', '每日 03:00', '前一日新增记录', '标记异常区间，触发紧急告警并启动调查'],
      ['全量校验', '每周日 02:00', '当前在线全部数据', '输出校验报告，异常记录进入人工核查'],
      ['录像摘要校验', '每日', '前一日封存的录像', '标记录像异常，评估是否需要从备份恢复'],
      ['归档校验', '归档时', '即将归档的分区', '校验失败则不执行清理，保留源数据'],
    ],
  })

  d.h(2, '8.4  会话票据与防重放机制')
  d.code([
    '// 票据结构与生命周期',
    'interface SessionTicket {',
    '  ticket: string;          // 256 位随机字符串（非 JWT，不自解释）',
    '  userId: number;          // 绑定用户',
    '  assetId: number;         // 绑定资产',
    '  grantId: number;         // 绑定授权',
    '  clientIp: string;        // 绑定来源 IP',
    '  protocol: string;        // 目标协议',
    '  issuedAt: number;        // 签发时间',
    '  ttlSec: 60;              // 有效期 ≤ 60 秒',
    '}',
    '',
    '// 消费票据（原子操作，防重放）',
    'async function consumeTicket(ticket: string, clientIp: string): Promise<SessionTicket> {',
    '  // 使用 Redis GETDEL 原子获取并删除，保证一次性',
    '  const raw = await redis.getdel("ticket:" + ticket);',
    '  if (!raw) throw new BizError("SESS_2003", "票据无效或已被使用");',
    '  const t = JSON.parse(raw);',
    '  if (t.clientIp !== clientIp) {                    // 校验来源绑定',
    '    throw new BizError("SESS_2008", "票据来源不匹配");',
    '  }',
    '  return t;',
    '}',
  ])
  d.table({
    caption: '表 8-4  防重放与防篡改设计',
    headers: ['攻击手法', '防护措施'],
    widths: [30, 70],
    rows: [
      ['票据重放', '票据一次性，消费即删除（GETDEL 原子操作）'],
      ['票据转移使用', '绑定 client_ip 与 user_id，来源不一致则拒绝'],
      ['票据长期利用', 'TTL ≤ 60 秒，过期自动失效'],
      ['令牌伪造', '令牌由服务端签发并校验签名，关键操作回源校验状态'],
      ['中间人窃取', '国密 TLS 双向认证 + 通道加密'],
      ['会话固定', '登录成功后重新签发令牌，失效旧令牌'],
      ['CSRF', '敏感操作使用自定义头 + 二次授权令牌，不使用纯 Cookie 鉴权'],
    ],
  })

  d.h(2, '8.5  分布式会话路由设计')
  d.p('代理节点采用一致性哈希路由，保证「同一资产的会话尽量落在同一节点」，减少密钥拉取与连接建立开销；节点故障时通过虚拟节点机制平滑迁移。')
  d.code([
    '// 一致性哈希（带虚拟节点，降低节点变动影响）',
    'class ConsistentHash {',
    '  constructor(nodes: string[], virtualReplicas = 150) {',
    '    this.ring = new SortedMap();',
    '    for (const n of nodes) {',
    '      for (let i = 0; i < virtualReplicas; i++) {',
    '        this.ring.set(sm3(n + "#" + i), n);      // 使用 SM3 作为哈希函数',
    '      }',
    '    }',
    '  }',
    '  locate(assetKey: string): string {',
    '    if (this.ring.isEmpty()) throw new Error("no available proxy node");',
    '    const h = sm3(assetKey);',
    '    const node = this.ring.ceilingEntry(h) ?? this.ring.firstEntry();',
    '    return node.value;',
    '  }',
    '}',
    '',
    '// 节点选择：优先一致性哈希结果，节点不健康时顺延',
    'function selectNode(assetId: number, healthMap: Map<string, boolean>): string {',
    '  let key = "asset:" + assetId;',
    '  for (let attempt = 0; attempt < 3; attempt++) {',
    '    const node = hashRing.locate(key);',
    '    if (healthMap.get(node)) return node;',
    '    key = key + "#retry" + attempt;               // 顺延到下一个节点',
    '  }',
    '  throw new BizError("SESS_2009", "无可用代理节点");',
    '}',
  ])

  d.h(2, '8.6  限流与熔断机制')
  d.table({
    caption: '表 8-5  限流策略设计',
    headers: ['对象', '算法', '阈值（默认）', '超限处理'],
    widths: [22, 22, 24, 32],
    rows: [
      ['登录接口（按 IP）', '令牌桶', '10 次/分钟', '返回 429 并锁定 5 分钟'],
      ['登录接口（按账号）', '滑动窗口', '5 次失败/15 分钟', '锁定账号 15 分钟'],
      ['开放 API（按令牌）', '令牌桶', '600 次/分钟', '返回 429 + Retry-After'],
      ['审计检索（按用户）', '并发数限制', '20 并发', '排队或拒绝，提示稍后重试'],
      ['导出接口（按用户）', '令牌桶', '5 次/小时', '拒绝并提示'],
      ['会话建立（按用户）', '并发限制', '20 会话', '拒绝并提示并发超限'],
      ['gRPC 内部调用', '熔断器', '错误率 ≥ 50% 持续 10 秒', '熔断 30 秒后半开探测'],
    ],
  })
  d.code([
    '// 熔断器状态机（简化实现）',
    'class CircuitBreaker {',
    '  private state: "CLOSED" | "OPEN" | "HALF_OPEN" = "CLOSED";',
    '  private failures = 0;',
    '  private openedAt = 0;',
    '',
    '  async call<T>(fn: () => Promise<T>, fallback?: () => T): Promise<T> {',
    '    if (this.state === "OPEN") {',
    '      if (Date.now() - this.openedAt > this.openMs) this.state = "HALF_OPEN";',
    '      else return fallback ? fallback() : Promise.reject(new Error("circuit open"));',
    '    }',
    '    try {',
    '      const r = await fn();',
    '      this.onSuccess();                 // 成功则重置计数，HALF_OPEN → CLOSED',
    '      return r;',
    '    } catch (e) {',
    '      this.onFailure();                 // 失败率超阈值则 OPEN',
    '      if (fallback) return fallback();',
    '      throw e;',
    '    }',
    '  }',
    '}',
  ])

  d.h(2, '8.7  敏感数据加解密机制')
  d.table({
    caption: '表 8-6  加解密调用链设计',
    headers: ['层次', '职责', '关键约束'],
    widths: [24, 38, 38],
    rows: [
      ['业务层', '声明字段为敏感（注解），不感知加解密细节', '禁止在业务代码中直接调用密码接口'],
      ['数据访问层', '拦截读写作，自动加解密与密钥索引维护', '批量查询时合并密钥请求，减少密码机调用'],
      ['密钥服务', '提供加解密接口，管理密钥索引与句柄', '密钥材料不出密码机，调用全程审计'],
      ['密码机', '执行实际密码运算', '双机热备，故障自动切换'],
    ],
  })
  d.p('性能优化：单条查询的加解密会带来密码机调用开销，因此对批量查询采用「批量解密」接口（一次请求含多个密文），并通过本地短时缓存（仅缓存句柄，不缓存明文）降低开销。')

  // ================= 9 前端与客户端设计 =================
  d.h(1, '9  前端与客户端设计')

  d.h(2, '9.1  前端架构')
  d.table({
    caption: '表 9-1  前端工程结构',
    headers: ['目录', '内容', '说明'],
    widths: [22, 34, 44],
    rows: [
      ['src/api', '接口封装（按模块分文件）', '统一请求拦截、错误处理、令牌刷新'],
      ['src/assets', '静态资源', '图标、字体、图片'],
      ['src/components', '通用组件', '终端组件、资产树、录像播放器、表格封装'],
      ['src/views', '页面视图（按模块分目录）', '与菜单结构对应'],
      ['src/store', '状态管理（Pinia）', '用户、权限、字典、会话状态'],
      ['src/router', '路由与权限守卫', '动态路由按权限生成'],
      ['src/directives', '权限指令', 'v-perm 控制元素级权限'],
      ['src/utils', '工具函数', '加解密封装、格式化、校验'],
      ['src/plugins', '插件', '国密组件、埋点、错误上报'],
    ],
  })

  d.h(2, '9.2  关键组件设计')
  d.table({
    caption: '表 9-2  核心前端组件',
    headers: ['组件', '职责', '关键设计'],
    widths: [24, 30, 46],
    rows: [
      ['TerminalView', '字符终端渲染', '基于 xterm.js；WebSocket 双向流；支持复制粘贴策略、搜索、字体缩放'],
      ['GraphicView', '图形会话渲染', 'Canvas 渲染画面帧；支持剪贴板受控、分辨率自适应、全屏'],
      ['AssetTree', '资产树', '虚拟滚动支持万级节点；懒加载子节点；支持搜索定位与收藏'],
      ['SessionTabs', '多会话标签', '标签切换保持会话上下文；支持关闭确认、拖拽排序'],
      ['ReplayPlayer', '录像回放', '时间轴 + 关键帧标记；支持倍速、跳转、命令联动定位'],
      ['AuditQueryForm', '审计检索条件', '条件持久化；时间范围校验（≤ 31 天）；检索耗时提示'],
      ['SecretViewer', '口令查看', '二次授权弹窗；查看后自动隐藏倒计时；复制受限并留痕'],
      ['PermButton', '权限按钮', '按权限码渲染或禁用；无权限时提示原因而非静默隐藏'],
    ],
  })

  d.h(2, '9.3  状态管理与权限控制')
  d.code([
    '// 前端权限校验（与后端权限码一致）',
    'export function hasPermission(code: string): boolean {',
    '  const perms = useUserStore().permissions;      // 登录时由后端下发',
    '  return perms.includes("*") || perms.includes(code);',
    '}',
    '',
    '// 权限指令：无权限时不渲染，配合 tooltip 说明原因',
    'export const vPerm = {',
    '  mounted(el: HTMLElement, binding: { value: string }) {',
    '    if (!hasPermission(binding.value)) {',
    '      el.parentNode?.removeChild(el);            // 移除而非隐藏，避免绕过',
    '    }',
    '  },',
    '};',
    '',
    '// 路由守卫：动态路由按权限过滤，未授权路由不可达',
    'router.beforeEach(async (to) => {',
    '  if (to.meta.public) return true;',
    '  if (!useUserStore().token) return { path: "/login" };',
    '  if (!useUserStore().routesLoaded) {',
    '    await useUserStore().loadRoutes();           // 按权限生成可访问路由',
    '    return { ...to, replace: true };',
    '  }',
    '  return true;',
    '});',
  ])
  d.p('前端权限仅为体验优化，不是安全边界：所有权限判定以后端为准，前端隐藏的接口仍会被后端拦截。')

  d.h(2, '9.4  客户端安全设计')
  d.ul([
    '本地不存储资产口令与业务敏感数据，会话结束后清理本地缓存与临时文件；',
    '客户端与服务端双向证书认证，证书绑定设备指纹，异常设备拒绝接入；',
    'Windows 与 Linux 客户端安装包与升级包使用国密签名，启动时校验签名，校验失败拒绝运行；',
    '敏感界面（口令查看、取证导出）启用防截屏与防复制（按策略）；',
    '剪贴板内容按策略受控，禁止将服务端剪贴板内容带出到本地或其他应用；',
    '客户端日志仅记录必要信息（会话 ID、时间、结果），不记录会话内容；',
    '定期上报合规状态（补丁、杀毒、可信环境），不合规终端按策略限制接入。',
  ])

  d.h(2, '9.5  前端性能优化')
  d.table({
    caption: '表 9-3  前端性能优化措施',
    headers: ['优化点', '措施', '目标'],
    widths: [26, 44, 30],
    rows: [
      ['首屏加载', '路由懒加载 + 资源预取 + 关键 CSS 内联', '首屏 ≤ 2 秒'],
      ['大列表渲染', '虚拟滚动 + 分页加载', '万级数据流畅滚动'],
      ['终端渲染', '增量写入 + 批量刷新（16 ms 合并）', '输入回显 ≤ 50 ms'],
      ['图形渲染', 'WebGL 或 Canvas 硬件加速 + 帧丢弃策略', '画面时延 ≤ 200 ms'],
      ['录像回放', '分片预加载 + 关键帧索引', '定位响应 ≤ 1 秒'],
      ['请求优化', '接口合并 + 防抖节流 + 结果缓存', '减少 40% 请求数'],
      ['长连接', '单 WebSocket 复用多会话（按会话 ID 分流）', '降低连接开销'],
    ],
  })

  // ================= 10 非功能设计 =================
  d.h(1, '10  非功能设计')

  d.h(2, '10.1  性能设计')
  d.table({
    caption: '表 10-1  关键路径性能预算',
    headers: ['路径', '端到端目标', '分解预算'],
    widths: [26, 20, 54],
    rows: [
      ['用户登录', '≤ 2 秒', '前端 300 ms + 网络 200 ms + 认证 600 ms + 密码运算 400 ms + 令牌签发 200 ms'],
      ['会话建立', '≤ 3 秒', '票据校验 200 ms + 授权 300 ms + 节点分配 200 ms + 密钥获取 300 ms + 资产认证 1500 ms'],
      ['命令判定', '≤ 50 ms', '捕获 5 ms + 解析 10 ms + 匹配 30 ms + 裁决 5 ms'],
      ['命令回显', '≤ 50 ms', '代理转发 20 ms + 网络往返 30 ms（不含资产执行时间）'],
      ['审计检索', '≤ 3 秒（百万级）', '查询解析 100 ms + 索引检索 1.5 s + 结果组装 1 s + 传输 400 ms'],
      ['录像回放启动', '≤ 2 秒', '索引定位 300 ms + 分片拉取 800 ms + 解密解码 600 ms + 首帧渲染 300 ms'],
      ['报表生成', '≤ 5 秒', '预聚合查询 2 s + 渲染 2 s + 传输 1 s'],
    ],
  })
  d.table({
    caption: '表 10-2  性能优化手段',
    headers: ['层次', '手段'],
    widths: [24, 76],
    rows: [
      ['接入层', '连接复用、心跳保活、数据压缩（图形画面使用增量编码）'],
      ['服务层', '无状态设计横向扩展、协程池、连接池复用、异步非阻塞 IO'],
      ['数据层', '读写分离、缓存热点数据、批量写入、索引优化、分区裁剪'],
      ['密码层', '句柄复用、批量加解密、会话密钥缓存（仅句柄）、硬件加速'],
      ['前端', '虚拟滚动、懒加载、请求合并、结果缓存、增量渲染'],
    ],
  })

  d.h(2, '10.2  可靠性与容错设计')
  d.table({
    caption: '表 10-3  容错策略设计',
    headers: ['场景', '策略', '用户影响'],
    widths: [26, 44, 30],
    rows: [
      ['依赖服务超时', '熔断 + 快速失败 + 降级返回', '功能受限但系统可用'],
      ['数据库主库故障', '自动切换备库（≤ 60 秒），切换期间写失败由应用重试', '短暂写入失败后自动恢复'],
      ['缓存不可用', '降级直查数据库，限制并发保护数据库', '响应变慢但可用'],
      ['消息队列积压', '审计写入转本地队列，恢复后补写', '审计延迟但不丢失'],
      ['密码机故障', '双机切换（≤ 5 秒）；双机均故障拒绝新会话', '新会话不可用，存量会话继续'],
      ['对象存储不可用', '录像本地缓冲，恢复后上传；缓冲满则按策略告警', '审计延迟'],
      ['代理节点故障', '新会话切换节点；存量会话标记异常中断', '会话中断，可重新发起'],
      ['网络抖动', '客户端断线重连（最多 3 次）+ 服务端上下文保留 30 秒', '短暂中断后自动恢复'],
    ],
  })

  d.h(2, '10.3  安全设计')
  d.table({
    caption: '表 10-4  安全防护措施清单',
    headers: ['威胁类别', '防护措施', '验证方式'],
    widths: [22, 48, 30],
    rows: [
      ['身份冒用', '多因素认证、设备绑定、异常登录检测', '渗透测试'],
      ['越权访问', '服务端强制鉴权、租户隔离、对象级权限校验', '越权测试'],
      ['SQL 注入', '参数化查询、ORM 白名单、禁止拼接 SQL', '代码审计 + 扫描'],
      ['XSS', '输出编码、CSP 策略、富文本白名单过滤', '安全测试'],
      ['CSRF', '自定义头鉴权、二次授权令牌、SameSite Cookie', '安全测试'],
      ['命令注入', '命令参数白名单、转义处理、禁止 shell 拼接', '代码审计'],
      ['会话劫持', '国密 TLS、令牌绑定来源、短时票据', '抓包分析'],
      ['重放攻击', '一次性票据、时间戳校验、幂等键', '重放测试'],
      ['敏感信息泄露', '加密存储、脱敏展示、错误信息不泄露内部细节', '配置核查'],
      ['拒绝服务', '限流、熔断、连接数限制、请求体大小限制', '压测'],
      ['供应链风险', '依赖锁定、SBOM、镜像签名校验', '交付物核查'],
    ],
  })

  d.h(2, '10.4  可观测性设计')
  d.table({
    caption: '表 10-5  日志、指标与追踪规范',
    headers: ['类型', '内容', '规范要求'],
    widths: [18, 38, 44],
    rows: [
      ['日志', '业务日志（含 trace_id、user_id、tenant_id）', '结构化 JSON；分级 ERROR/WARN/INFO/DEBUG；禁止记录口令与密钥'],
      ['日志', '审计日志（独立通道）', '与业务日志分离存储；国密签名；不可删除'],
      ['指标', 'RED 指标（请求速率、错误率、耗时）', '按服务与接口维度暴露 Prometheus 指标'],
      ['指标', '业务指标（在线用户、并发会话、命令量）', '实时采集 + 定时落库用于趋势分析'],
      ['追踪', '分布式链路追踪', 'OpenTelemetry；采样率可调；关键路径 100% 采样'],
      ['告警', '基于指标与日志的告警', '分级路由；告警与处置闭环'],
    ],
  })
  d.code([
    '// 结构化日志示例（JSON）',
    '{',
    '  "ts": "2025-02-10T09:41:07.123+08:00",',
    '  "level": "WARN",',
    '  "service": "proxy-node-02",',
    '  "trace_id": "b7f3a1c9e2d4",',
    '  "tenant_id": 1001,',
    '  "user_id": 20305,',
    '  "session_id": 881726354,',
    '  "event": "command_blocked",',
    '  "detail": { "policy_id": 7, "rule_code": "CMD-0007", "cmd_hash": "9f2c...", "latency_ms": 38 },',
    '  "msg": "高危命令已阻断"',
    '}',
  ])

  d.h(2, '10.5  兼容性设计')
  d.table({
    caption: '表 10-6  兼容性设计要点',
    headers: ['兼容维度', '设计措施'],
    widths: [24, 76],
    rows: [
      ['多架构', '代码避免架构相关假设；使用多架构镜像构建；字节序与对齐统一处理'],
      ['多操作系统', '服务端不依赖特定发行版特性；路径与权限按 POSIX 处理；systemd 与 init 脚本兼容'],
      ['多数据库', 'SQL 使用标准语法；JSONB 等特性通过适配层封装；分区语法差异由迁移脚本适配'],
      ['多浏览器', '避免使用非标准 API；CSS 使用渐进增强；终端与图形渲染提供降级方案'],
      ['多终端类型', '终端类型与编码自适应；行编辑行为兼容常见 shell；全屏程序支持'],
      ['协议版本兼容', 'SSH 支持 2.0；RDP 支持主流版本；数据库协议按版本特性适配'],
      ['既有工具习惯', '终端快捷键兼容 PuTTY / Xshell 常用组合，降低迁移成本'],
    ],
  })

  // ================= 11 附录 =================
  // ================= 11 协议与技术详解 =================
  d.h(1, '11  协议与技术详解')
  d.p('本章对产品涉及的全部协议、技术与软件组件做出解释，目的是让开发、测试、实施与售前在同一套术语上沟通，避免把「协议」与「软件实现」混为一谈（例如 RDP 是协议，XRDP 是软件；RFB 是协议，VNC 是软件家族）。')
  d.table({
    caption: '表 11-1  本章条目的统一说明格式',
    headers: ['字段', '含义'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', '中文名与英文全称，缩略语的展开'],
      ['类别', '区分「协议标准」「软件实现」「技术方法」「开发框架」四类'],
      ['标准出处', 'RFC / ISO / ITU-T / 国标 / 厂商规范等；无标准的标注为「事实标准」或「专有」'],
      ['作用', '该技术解决什么问题'],
      ['本产品中的角色', '在产品里承担什么职能、对应哪个需求编号、处于什么优先级'],
      ['相关软件', '业界常见实现，便于选型与兼容性测试'],
    ],
  })
  d.note('阅读提示', [
    '一、**协议与实现要分清**：RDP 是协议、mstsc 是客户端软件、xrdp 是服务端软件；RFB 是协议、VNC 是基于该协议的软件家族；',
    '二、**标准与专有要分清**：SSH 有 RFC 标准，RDP 是 Microsoft 专有协议，SSH 证书是 OpenSSH 事实标准；',
    '三、本章与 6.6.7（图形桌面运维设计）、6.6.5（第三方客户端接入）配合阅读，前者讲「是什么」，后者讲「怎么用」。',
  ], 'info')

  d.h(2, '11.1  运维与远程桌面协议')
  d.h(3, '11.1.1  RDP（远程桌面协议）')
  d.table({
    caption: '表 11-2  RDP 明细',
    headers: ['项目', '内容'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', 'RDP，Remote Desktop Protocol（远程桌面协议）'],
      ['类别', '**协议**（Microsoft 专有，图形部分源自 ITU-T T.128 / T.share）'],
      ['标准出处', 'Microsoft 专有规范（MS-RDPBCGR 等 Open Specifications 文档公开）；图形基础为 ITU-T T.128'],
      ['版本', 'RDP 4.0 ~ RDP 10.x；Windows 10 / 11 与 Server 2016+ 使用 RDP 10.x。本产品支持 **RDP 8.0 及以上**'],
      ['端口', 'TCP 3389（默认）；RDP 8+ 可选 UDP 3389 传输（UDP Transport）'],
      ['安全层', '① Standard RDP Security（RC4，弱，不推荐）；② Enhanced RDP Security（TLS）；③ **CredSSP / NLA**（网络级认证，主流）'],
      ['通道机制', '静态虚拟通道（SVC）+ 动态虚拟通道（DVC），承载剪贴板、驱动器、打印机、音频、智能卡等重定向'],
      ['图形更新', 'Bitmap Update（位图更新）、Surface Command（RDP 8+ 的 GFX 管线，含 RemoteFX Progressive Codec）'],
      ['作用', '把远端 Windows 桌面的画面与输入输出在客户端之间传输'],
      ['本产品中的角色', '**P0 核心协议**（FR-04-02）。两种用法：透传代理（服务端在安全层中间人解密后旁路采集）与服务端渲染（重建帧缓冲后重编码推送 Web 客户端）'],
      ['相关软件', '客户端：Microsoft mstsc、FreeRDP、rdesktop；服务端：Windows 远程桌面服务、xrdp'],
      ['兼容性要点', 'NLA 开启时需正确协商；GFX 管线需按版本降级；多显示器与 4K 需验证'],
    ],
  })

  d.h(3, '11.1.2  XRDP（Linux 上的 RDP 服务端软件）')
  d.table({
    caption: '表 11-3  XRDP 明细',
    headers: ['项目', '内容'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', 'XRDP（也写作 xrdp），**不是协议，是开源软件**'],
      ['类别', '**软件实现**（在 Linux / Unix 上提供 RDP 服务端能力）'],
      ['标准出处', '无独立标准；实现 Microsoft RDP 协议。开源许可 GPL v2+'],
      ['版本', 'xrdp 0.9.x / 0.10.x（配套 xorgxrdp 模块）'],
      ['架构', 'xrdp 服务进程（监听 3389）→ sesman 会话管理 → 后端为 Xorg（xorgxrdp 驱动）或 Xvnc'],
      ['作用', '让 Linux 图形桌面（GNOME / KDE / XFCE）能被标准 RDP 客户端访问'],
      ['本产品中的角色', '**Linux 图形桌面的首选接入方式**（FR-04-02 验收第 ⑧ 项）。因为在堡垒机看来它就是一台标准 RDP 资产，可**完全复用现有 RDP 代理链路，无需额外开发**；剪贴板、驱动器映射、打印重定向等管控能力同样可用'],
      ['对比 VNC 的优势', '复用 RDP 链路（零开发）；管控能力完整（VNC 不支持驱动器与打印重定向）；编码效率更高'],
      ['相关软件', 'xrdp、xorgxrdp、sesman；客户端用 mstsc 或 FreeRDP 即可'],
      ['实施要点', '在 Linux 资产侧安装配置；需注意桌面会话与 systemd 服务的启动方式；建议在实施手册中给出标准配置模板'],
    ],
  })

  d.h(3, '11.1.3  RFB（远程帧缓冲协议）')
  d.table({
    caption: '表 11-4  RFB 明细',
    headers: ['项目', '内容'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', 'RFB，Remote Framebuffer Protocol（远程帧缓冲协议）'],
      ['类别', '**协议**（VNC 体系的底层协议）'],
      ['标准出处', '**RFC 6143**《The Remote Framebuffer Protocol》'],
      ['版本', '3.3 / 3.7 / 3.8（3.7+ 支持安全类型协商；3.8 支持 ServerInit 扩展）'],
      ['端口', 'TCP 5900 + 显示号（5901、5902 ……）；HTTP 方式为 5800 + 显示号'],
      ['消息类型', '客户端→服务端：SetPixelFormat、SetEncodings、FramebufferUpdateRequest、KeyEvent、PointerEvent、ClientCutText；服务端→客户端：FramebufferUpdate、SetColourMapEntries、Bell、ServerCutText'],
      ['核心流程', '版本握手 → 安全握手（认证）→ ClientInit / ServerInit → 客户端请求区域更新 → 服务端按编码推送 FramebufferUpdate（一组矩形区域）'],
      ['作用', '以「帧缓冲矩形更新」的方式传输远端桌面画面，并回传键盘 / 鼠标事件'],
      ['本产品中的角色', 'V1.1 支持（协议矩阵 P1）。代理层需解析 RFB 消息流、重建帧缓冲用于录像；**隧道内流量由代理层强制加密**（RFB 自身安全类型较弱）'],
      ['相关软件', '服务端：TigerVNC、RealVNC、UltraVNC、x11vnc、LibVNCServer；客户端：TigerVNC Viewer、RealVNC Viewer'],
    ],
  })

  d.h(3, '11.1.4  VNC（基于 RFB 的软件家族）')
  d.table({
    caption: '表 11-5  VNC 明细',
    headers: ['项目', '内容'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', 'VNC，Virtual Network Computing（虚拟网络计算）'],
      ['类别', '**软件家族 / 技术体系**（不是协议；其协议是 RFB）'],
      ['标准出处', '无独立标准；由 AT&T 实验室提出，现由 RealVNC 等维护；协议部分见 RFC 6143'],
      ['组成', 'VNC Server（共享本地桌面）+ VNC Viewer（远程查看与操作）'],
      ['主要实现', 'TigerVNC（社区主流）、RealVNC（商业）、UltraVNC（Windows）、TightVNC、x11vnc（共享现有 X 会话）、LibVNCServer（库）'],
      ['安全类型', 'None；**VNC Authentication**（DES 挑战-响应，强度弱）；VeNCrypt / TLS（部分实现）；RA2 / RA2ne（RealVNC 扩展）'],
      ['能力', '画面 + 键鼠；剪贴板（ClientCutText / ServerCutText）；文件传输（**非 RFB 标准**，各实现自扩展，能力不一致）'],
      ['能力缺口', '无标准驱动器重定向、无打印重定向、编码效率低于 RDP、安全机制弱'],
      ['本产品中的角色', 'Linux 图形桌面的**备选方案**（V1.1）。因资产已部署 VNC 且不宜变更时使用；代理层强制加密，并向客户明示「本地盘映射与打印重定向不可审计」'],
      ['相关软件', '同上；兼容性测试需按客户实际使用的服务端定向验证'],
    ],
  })

  d.h(3, '11.1.5  X11 与 X11 转发')
  d.table({
    caption: '表 11-6  X11 与 X11 转发明细',
    headers: ['项目', '内容'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', 'X11，X Window System Protocol version 11（X 窗口系统协议第 11 版）'],
      ['类别', '**协议**（Unix / Linux 图形桌面的基础）'],
      ['标准出处', 'X Consortium / X.Org Foundation 规范（1987 年 MIT 起源，非 RFC）'],
      ['架构', 'X Client（应用程序）+ X Server（显示与输入）+ X Protocol（二者之间的 C/S 通信）'],
      ['X11 转发', '通过 SSH 通道转发 X11 协议（ssh -X / -Y），使远端图形应用显示在本地终端'],
      ['安全扩展', 'MIT-MAGIC-COOKIE-1（访问控制）、XSECURITY 扩展'],
      ['作用', '为图形应用提供统一的显示与输入抽象层；X11 转发则让单个图形应用跨主机显示'],
      ['本产品中的角色', '**不支持作为图形运维方式**。原因：① X11 核心协议外有数十种扩展，代理层完整解析并重建画面工程量大；② X11 转发下流量封装在 SSH 通道内，难以做画面级审计与回放'],
      ['本产品的处理方式', '归入**隧道管控**范畴（FR-04-14）：对 X11 转发按策略「允许 / 禁止 / 审计」，**默认禁止**；若启用仅记录连接事件并告警，不具备画面录像能力'],
      ['替代建议', '需要图形运维时改用 **XRDP**（首选）或 VNC（备选），可获得完整录像与管控'],
    ],
  })

  d.h(3, '11.1.6  SSH 家族（SSH / SFTP / SCP / SSH 证书）')
  d.table({
    caption: '表 11-7  SSH 家族明细',
    headers: ['项目', '内容'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', 'SSH，Secure Shell（安全外壳协议）；当前版本 SSH-2.0'],
      ['类别', '**协议**'],
      ['标准出处', '**RFC 4250 ~ 4254**（协议总则、认证、传输层、连接层、架构）；密钥文件格式 RFC 4716 / OpenSSH 格式'],
      ['端口', 'TCP 22（本产品对外可配 2222 等）'],
      ['子协议分层', '① 传输层（密钥交换、加密、完整性）；② 用户认证层（口令 / 公钥 / 证书 / 键盘交互 / GSSAPI）；③ 连接层（通道复用，承载 shell、exec、subsystem、端口转发）'],
      ['SFTP', 'SSH File Transfer Protocol，**基于 SSH 的文件传输子系统**（draft-ietf-secsh-filexfer），复用 22 端口；提供目录浏览、上传下载、属性操作、断点续传'],
      ['SCP', 'Secure Copy，基于 SSH 的传统文件复制；新版 OpenSSH 已改为内部走 SFTP 协议'],
      ['端口转发', '-L 本地转发、-R 远程转发、-D 动态转发（SOCKS）；本产品对三类转发按策略管控'],
      ['SSH 证书', 'OpenSSH 扩展（**事实标准，非 IETF 标准**）；由 CA 签发，字段含 KeyId、Principals、ValidAfter / ValidBefore、Critical Options、Extensions'],
      ['本产品中的角色', '**P0 核心协议**（FR-04-01 ~ FR-04-19）。命令级管控、字符录像、密钥托管与代签、转发管控、SSH 短时证书（准入模式凭据）均基于 SSH'],
      ['相关软件', '服务端 OpenSSH；客户端 OpenSSH、PuTTY、Xshell、SecureCRT；库 Go x/crypto/ssh、libssh、Rust russh'],
    ],
  })

  d.h(3, '11.1.7  其他运维协议')
  d.table({
    caption: '表 11-8  其他运维协议一览',
    headers: ['协议', '全称 / 出处', '类别与作用', '本产品中的角色'],
    widths: [14, 26, 26, 34],
    rows: [
      ['Telnet', 'TELNET Protocol，RFC 854', '明文远程终端协议', '兼容支持（明文，安全性弱，仅存量场景）；命令级管控与录像同 SSH'],
      ['FTP', 'File Transfer Protocol，RFC 959', '明文文件传输', '不主推；协议矩阵标「不主推」，仅兼容存量场景'],
      ['FTPS', 'FTP over TLS，RFC 4217', '加密文件传输', '按需兼容'],
      ['SPICE', 'Simple Protocol for Independent Computing Environments（Red Hat）', '**协议**，面向 KVM 虚拟机的远程桌面', '未纳入主线；如有虚拟化运维场景可专项适配'],
      ['NoMachine NX', 'NoMachine 专有协议', '**专有协议**，高性能远程桌面', '未纳入主线；按项目专项适配'],
      ['Rlogin', 'Berkeley rlogin，RFC 1282', '早期明文远程登录', '不支持（已被 SSH 取代）'],
      ['VNC over WebSocket', 'RFB 封装于 WebSocket', '便于浏览器直连 VNC', '内部实现可参考，对外不单独作为接入方式'],
    ],
  })

  d.h(3, '11.1.8  数据库协议')
  d.table({
    caption: '表 11-9  数据库协议明细',
    headers: ['协议', '全称 / 出处', '端口', '特征', '本产品中的角色'],
    widths: [16, 24, 10, 24, 26],
    rows: [
      ['MySQL Protocol', 'MySQL Client/Server Protocol（Oracle 规范）', '3306', '握手 → 认证 → COM_QUERY 等命令；支持 SSL', 'V1.1 支持（Web SQL）；后续评估协议代理'],
      ['PostgreSQL Wire', 'PostgreSQL Frontend/Backend Protocol', '5432', '消息类型（Query / Parse / Bind / Execute / Sync）；支持 SSL', 'V1.1 支持（Web SQL）'],
      ['Oracle TNS', 'Transparent Network Substrate（Oracle 专有）', '1521', '专有握手与数据表示；协议复杂', '后续支持（先 Web SQL，再评估协议代理）'],
      ['TDS', 'Tabular Data Stream（Sybase / Microsoft）', '1433', 'SQL Server 通信协议；支持 TLS', '按需支持'],
      ['达梦 DM', '达梦数据库自有协议（兼容 Oracle 语法）', '5236', '国产数据库；协议自有', 'V1.2 适配（信创场景）'],
      ['人大金仓 KingBase', '基于 PostgreSQL 协议扩展', '54321', '国产数据库；兼容 PG 协议', 'V1.2 适配（信创场景）'],
      ['Redis', 'RESP，REdis Serialization Protocol', '6379', '文本协议，简单请求-响应', '不作为被管资产协议（Redis 在本产品中作缓存使用）'],
    ],
  })
  d.note('数据库协议的两种接入方式', [
    '一、**Web SQL**：用户在浏览器中通过本产品的 SQL 编辑器操作，服务端作为客户端连库——实现简单，管控强，适合首版；',
    '二、**协议代理**：客户用自己的数据库客户端（Navicat / DBeaver）直连堡垒机端口，代理层解析数据库协议做 SQL 审计——体验好但开发量大，按版本逐步支持。',
  ], 'info')

  d.h(2, '11.2  身份认证与密码技术')
  d.h(3, '11.2.1  TLS / SSL 与国密 TLS')
  d.table({
    caption: '表 11-10  TLS 与国密 TLS 明细',
    headers: ['项目', '内容'],
    widths: [18, 82],
    rows: [
      ['名称 / 全称', 'TLS，Transport Layer Security（传输层安全协议）；前身为 SSL'],
      ['类别', '**协议**'],
      ['标准出处', 'TLS 1.2 = **RFC 5246**；TLS 1.3 = **RFC 8446**；SSL 3.0 = RFC 6101（已废弃）'],
      ['作用', '为应用层协议提供机密性、完整性与双向身份认证；https / wss / ldaps 等均建立在其上'],
      ['握手要点', '密码套件协商、证书交换与校验、密钥派生（TLS 1.3 简化为 1-RTT / 0-RTT）'],
      ['国密 TLS', '**GM/T 0024**《SSL VPN 技术规范》定义国密密码套件（SM2 证书 + SM3 摘要 + SM4 加密）；基于 TLS 1.2 框架的国密改造，**不是 TLS 1.3**'],
      ['本产品中的角色', '全系统内部与外部通信使用国密 TLS：客户端与服务端、服务端各组件之间、第三方客户端接入全部走国密 TLS 双向认证（FR-09-01 ~ FR-09-03）'],
      ['实现要点', '需在服务端同时支持国密套件与（可选）国际套件；浏览器侧国密支持依赖浏览器或插件 / WASM 实现'],
      ['相关软件', 'OpenSSL（含国密分支如 GmSSL / 铜锁 Tongsuo）、Go crypto/tls + 国密扩展、浏览器国密插件'],
      ['常见误用提醒', '不得把「国密 TLS」写成「国密 TLS 1.3」——GM/T 0024 基于 TLS 1.2 框架，TLS 1.3 的国密支持尚在标准化过程中'],
    ],
  })

  d.h(3, '11.2.2  国密算法族（SM2 / SM3 / SM4 / SM9）')
  d.table({
    caption: '表 11-11  国密算法明细',
    headers: ['算法', '类型', '标准', '作用', '本产品中的角色'],
    widths: [10, 14, 16, 26, 34],
    rows: [
      ['SM2', '非对称（椭圆曲线公钥）', 'GB/T 32918、GM/T 0003', '数字签名、密钥交换、公钥加密', '设备证书与客户端证书、SSH 证书签名、密钥交换、数字信封'],
      ['SM3', '哈希（摘要）', 'GB/T 32905、GM/T 0004', '完整性校验、口令摘要、摘要链', '口令加盐存储、录像分片摘要链、审计日志签名、软件完整性基线比对'],
      ['SM4', '对称（分组密码）', 'GB/T 32907、GM/T 0002', '数据加解密', '录像文件加密、账号口令密文、敏感字段加密、备份包加密'],
      ['SM9', '非对称（标识密码 IBC）', 'GM/T 0044', '以标识（如邮箱、工号）为公钥的免证书认证', 'P2 规划，未纳入主线（见 2.7.5 降级清单）'],
      ['ZUC', '对称（序列密码）', 'GM/T 0001', '移动通信与链路加密', '未使用'],
    ],
  })

  d.h(3, '11.2.3  X.509 证书与 PKI（含 CRL / OCSP）')
  d.table({
    caption: '表 11-12  PKI 相关明细',
    headers: ['项目', '内容'],
    widths: [20, 80],
    rows: [
      ['X.509', '**标准**：公钥证书格式标准，RFC 5280 定义证书与 CRL 的结构；v3 扩展含 KeyUsage、ExtendedKeyUsage、SubjectAltName 等'],
      ['PKI', 'Public Key Infrastructure（公钥基础设施）：CA、注册机构、证书库、吊销机制的统称'],
      ['证书链', '终端实体证书 → 中间证书（0 ~ N 级）→ 根证书；校验时需逐级验签至受信任根'],
      ['CRL', 'Certificate Revocation List，证书吊销列表（RFC 5280）；定期发布，本地缓存，时效性差'],
      ['OCSP', 'Online Certificate Status Protocol，在线证书状态协议（**RFC 6960**）；实时查询，时效性好但依赖服务可用性'],
      ['CSR', 'Certificate Signing Request，证书签发请求（PKCS#10，RFC 2986）'],
      ['本产品中的角色', '设备证书、客户端 / UKey 证书、SSH 证书的统一基础；证书链管理见 6.10.4（FR-09-14），吊销校验支持 CRL 与 OCSP 并可配置缓存策略'],
      ['相关实现', 'OpenSSL、Go crypto/x509、CFSSL、企业 CA（如 EJBCA、微软 AD CS）'],
    ],
  })

  d.h(3, '11.2.4  PKCS#11 与密码设备接口')
  d.table({
    caption: '表 11-13  密码设备接口明细',
    headers: ['项目', '内容'],
    widths: [20, 80],
    rows: [
      ['PKCS#11', '**标准**：Cryptoki，定义应用程序访问密码令牌 / HSM 的 C 接口（OASIS 标准）'],
      ['作用', '屏蔽不同密码硬件的差异，使上层应用以统一接口调用签名、加解密、密钥管理能力'],
      ['本产品中的角色', '对接密码卡 / 密码机 / UKey 的主接口（FR-09-05、FR-09-13）：密钥生成、签名验签、加解密、密钥索引管理均通过 PKCS#11 调用'],
      ['相关标准', 'GM/T 0016（智能密码钥匙）、GM/T 0018（密码设备应用接口规范）、GM/T 0030（服务器密码机技术规范）、GM/T 0028（密码模块安全技术要求）'],
      ['相关实现', '厂商 PKCS#11 库（.so / .dll）、OpenSC（智能卡通用实现）、SoftHSM（软件模拟，仅测试用）'],
      ['注意点', '不同厂商 PKCS#11 实现存在差异（如密钥属性、会话模型），需在适配测试中逐项验证'],
    ],
  })

  d.h(3, '11.2.5  一次性口令（TOTP / HOTP / otpauth）')
  d.table({
    caption: '表 11-14  一次性口令明细',
    headers: ['项目', '内容'],
    widths: [20, 80],
    rows: [
      ['HOTP', 'HMAC-based One-Time Password，**RFC 4226**；基于计数器的一次性口令'],
      ['TOTP', 'Time-based One-Time Password，**RFC 6238**；基于时间片的一次性口令（HOTP 的时间变体），标准参数为 6 位、30 秒步长、HMAC-SHA1'],
      ['otpauth URI', '**事实标准**（Google Authenticator 约定，非 IETF 标准）：otpauth://totp/{issuer}:{account}?secret=...&issuer=...&algorithm=...&digits=...&period=...'],
      ['本产品中的角色', '手机令牌的算法与二维码格式（FR-01-02），详见 6.2.6 手机令牌对接设计'],
      ['相关软件', 'Google Authenticator、Microsoft Authenticator、FreeOTP、Aegis、企业微信 / 钉钉内置动态码；服务端实现 Go otp 库、Rust totp-rs'],
    ],
  })

  d.h(3, '11.2.6  企业身份源协议（LDAP / AD / Kerberos）')
  d.table({
    caption: '表 11-15  企业身份源协议明细',
    headers: ['协议', '全称 / 标准', '作用', '本产品中的角色'],
    widths: [16, 24, 26, 34],
    rows: [
      ['LDAP', 'Lightweight Directory Access Protocol，RFC 4511', '目录服务查询与认证', '身份源对接（FR-01-07）：用户与组织同步、免密登录'],
      ['AD', 'Active Directory（Microsoft 目录服务）', '基于 LDAP + Kerberos 的企业目录', '身份源对接（FR-01-07）：LDAP 协议访问 AD，支持与 AD 域集成'],
      ['Kerberos', 'RFC 4120', '网络认证协议（票据机制）', 'AD 环境下的免密登录（FR-01-07）'],
      ['RADIUS', 'RFC 2865 / 2866', '集中式认证、授权、计费', '可选对接（第三方认证服务器）'],
    ],
  })

  d.h(3, '11.2.7  联邦认证协议（SAML / OIDC / OAuth 2.0）')
  d.table({
    caption: '表 11-16  联邦认证协议明细',
    headers: ['协议', '全称 / 标准', '作用', '本产品中的角色'],
    widths: [14, 26, 26, 34],
    rows: [
      ['SAML 2.0', 'Security Assertion Markup Language，OASIS 标准', '基于 XML 断言的跨域单点登录', '联邦认证（FR-01-11，V1.1）：作为 SP 消费 IdP 断言'],
      ['OIDC', 'OpenID Connect，基于 OAuth 2.0 的身份层', '基于 JSON / JWT 的现代单点登录', '联邦认证（FR-01-11，V1.1）：授权码模式接入'],
      ['OAuth 2.0', 'RFC 6749 / 6750', '授权框架（非认证协议）', 'OIDC 的基础；开放 API 的令牌鉴权可参考其模式'],
      ['JWT', 'RFC 7519', 'JSON 格式的令牌', 'API 接入令牌、联邦认证的 ID Token'],
    ],
  })

  d.h(3, '11.2.8  RDP 安全层（CredSSP / NLA）')
  d.table({
    caption: '表 11-17  RDP 安全机制明细',
    headers: ['项目', '内容'],
    widths: [20, 80],
    rows: [
      ['Standard RDP Security', 'RDP 原生加密（RC4 系列），强度弱，已被淘汰；部分老旧资产仍在使用'],
      ['Enhanced RDP Security', '在 RDP 外层套 TLS，提供服务器身份认证与通道加密'],
      ['CredSSP', 'Credential Security Support Provider（**MS-CSSP**）：在 TLS 之上传递用户凭据，实现单点登录式认证'],
      ['NLA', 'Network Level Authentication（网络级认证）：**在建立会话前**完成用户认证，可防资源耗尽型攻击；Windows 默认开启'],
      ['本产品中的角色', '透传模式下需在安全层做中间人，处理 NLA 协商与证书替换（详见 6.6.7.3）；服务端渲染模式下本产品作为合法 RDP 客户端，直接支持 NLA'],
    ],
  })

  d.h(2, '11.3  传输、网络与管理协议')
  d.table({
    caption: '表 11-18  传输与管理协议一览',
    headers: ['协议 / 技术', '全称与标准', '作用', '本产品中的角色'],
    widths: [16, 26, 24, 34],
    rows: [
      ['TCP / UDP', 'RFC 793 / RFC 768', '传输层基础协议', '全部会话的基础；RDP 8+ 可选 UDP 传输'],
      ['WebSocket / WSS', 'RFC 6455', '浏览器与服务端的全双工通道', 'Web 客户端的会话数据、实时状态推送、终端字符流传输（WSS 加密）'],
      ['HTTP / HTTPS', 'RFC 9110 系列 / TLS 之上的 HTTP', '应用层请求-响应', '管理 API、审计检索、文件下载；外部接口统一 HTTPS（国密 TLS）'],
      ['HTTP/2', 'RFC 9113', '多路复用的 HTTP', 'gRPC 的传输基础'],
      ['gRPC', '基于 HTTP/2 的 RPC 框架（Google，CNCF）', '高效的服务间调用', '服务间内部调用与开放 API 的 gRPC 接口（FR-08-04）'],
      ['Syslog', 'RFC 5424（新版）/ RFC 3164（BSD 旧版）', '日志外发协议', '日志服务器外发（FR-07-14）：支持 UDP / TCP / TLS 与两种报文格式'],
      ['SNMP / SNMPv3', 'RFC 3411 ~ 3418', '网络管理协议', '设备监控与 Trap 上报（FR-07-13）：**仅支持 SNMPv3 authPriv、只读**'],
      ['NTP', 'RFC 5905', '时间同步协议', '时间与时间源管理（FR-07-19）；TOTP 与凭据有效期均依赖时间准确'],
      ['DNS', 'RFC 1035', '域名解析', '设备自身网络配置项（FR-07-11）'],
      ['DHCP', 'RFC 2131', '动态主机配置', '管理口可选 DHCP 获取地址（FR-07-11）'],
      ['ICMP', 'RFC 792', '网络诊断', '资产连通性巡检（ping）'],
      ['VLAN', 'IEEE 802.1Q', '二层网络隔离', '网络高级配置：VLAN 子接口实现管理与业务隔离（FR-07-15）'],
      ['LACP / Bonding', 'IEEE 802.3ad', '链路聚合', '网络高级配置：多网口聚合（主备 / 负载分担）（FR-07-15）'],
      ['S3', 'Amazon S3 API（事实标准）', '对象存储访问接口', '录像文件存储（兼容 S3 的对象存储）'],
      ['SMTP', 'RFC 5321', '邮件发送', '告警与通知邮件（IF-07）'],
    ],
  })

  d.h(2, '11.4  图形编码与录制技术')
  d.h(3, '11.4.1  视频编码格式')
  d.table({
    caption: '表 11-19  视频编码格式明细',
    headers: ['编码', '全称 / 标准', '特点', '本产品中的角色'],
    widths: [12, 24, 30, 34],
    rows: [
      ['H.264 / AVC', 'ITU-T H.264 / ISO-IEC 14496-10', '兼容性最好、压缩率高、硬件支持广泛', '**图形录像主编码**；浏览器与桌面客户端均可解码'],
      ['H.265 / HEVC', 'ITU-T H.265 / ISO-IEC 23008-2', '同画质下码率约为 H.264 的 50%，但专利与兼容性复杂', '可选（按客户端能力协商）'],
      ['VP9', 'Google 开放标准', '免专利、压缩率接近 H.265', '可选编码；Web 端优先（浏览器原生支持）'],
      ['MJPEG', 'Motion JPEG（事实标准）', '实现简单、时延低、码率高', '低带宽 / 低算力场景降级使用'],
    ],
  })
  d.h(3, '11.4.2  硬件编码加速')
  d.table({
    caption: '表 11-20  硬件编码技术',
    headers: ['技术', '厂商', '说明', '本产品中的角色'],
    widths: [16, 18, 30, 36],
    rows: [
      ['Intel QSV', 'Intel', 'Quick Sync Video，集成显卡的编解码引擎', '优先启用（服务器带核显时性价比最高）'],
      ['NVIDIA NVENC', 'NVIDIA', '独立显卡的硬件编码器', '高并发图形会话场景启用'],
      ['AMD VCE / VCN', 'AMD', 'Radeon 显卡的编码引擎', '按硬件支持启用'],
      ['软件编码', 'x264 / openh264', '纯 CPU 编码', '无硬件加速时的回退方案；1080p30 约占用 1 ~ 2 个 CPU 核'],
    ],
  })
  d.h(3, '11.4.3  终端录制格式（asciinema cast）')
  d.table({
    caption: '表 11-21  终端录制格式明细',
    headers: ['项目', '内容'],
    widths: [20, 80],
    rows: [
      ['asciinema cast', '终端会话录制格式（**事实标准**，asciinema 项目定义）；v2 为 JSON Lines，每行一个事件（输出 o / 输入 i / 窗口 resize r，带时间戳）'],
      ['对比视频录制', '体积小 1 ~ 2 个数量级（1 小时 1 ~ 5 MB）、可全文检索、可导出文本、重放不受分辨率影响'],
      ['本产品中的角色', '**字符会话录像的数据形态**（6.8.4）：服务端记录 PTY 字节流 + 时间戳 + resize 事件，回放时由终端模拟器重放'],
      ['相关实现', 'asciinema（参考实现）、自研记录格式（可兼容 cast 结构）'],
    ],
  })
  d.h(3, '11.4.4  帧缓冲重建技术')
  d.table({
    caption: '表 11-22  帧缓冲重建相关技术',
    headers: ['技术点', '所属协议', '说明', '本产品中的角色'],
    widths: [18, 14, 34, 34],
    rows: [
      ['Bitmap Update', 'RDP', '服务端推送位图区域更新（位置 + 尺寸 + 压缩位图数据）', '代理层解析并贴回虚拟帧缓冲'],
      ['Surface Command', 'RDP 8+', 'GFX 管线指令（创建 Surface、编码帧、渐进式编码）', '按 RDP 版本解析，重建帧缓冲以支持录像'],
      ['FramebufferUpdate', 'RFB', '服务端按矩形区域推送画面更新，携带编码类型', 'VNC 场景下解析并重建帧缓冲'],
      ['虚拟帧缓冲', '—', '服务端维护的完整画面缓冲区', '编码器输入源；同时用于水印叠加与画面比对'],
      ['输入注入', 'RDP / RFB', '将客户端键鼠事件写回资产', '服务端渲染模式下由代理层注入；透传模式下直接转发'],
    ],
  })
  d.h(3, '11.4.5  VNC 编码算法族')
  d.table({
    caption: '表 11-23  VNC 编码类型',
    headers: ['编码', '类型号', '特点', '支持策略'],
    widths: [16, 12, 36, 36],
    rows: [
      ['Raw', '0', '原始像素，无压缩，带宽占用最大', '必支持（作为兼容回退）'],
      ['CopyRect', '1', '复用已有区域的像素（滚动、拖动场景）', '支持'],
      ['Hextile', '5', '按 16×16 块压缩，实现简单', '支持'],
      ['Tight', '7', 'JPEG / PNG 压缩 + 调色板，压缩率高', '支持'],
      ['ZRLE', '16', 'Zlib + Run-Length，主流实现常用', '支持'],
      ['TRLE', '15', 'Tight 的无 zlib 变体', '支持'],
      ['H.264', '50（非标准，各实现自定）', '部分实现提供的视频编码', '按实现探测，不支持时回退'],
    ],
  })
  d.note('编码协商策略', [
    '一、代理层在与 VNC 服务端握手时声明支持的编码集合，优先选择压缩率高且已被支持的编码；',
    '二、遇到未知编码时回退为 Raw（用带宽换兼容性），并记录兼容性问题供后续适配；',
    '三、编码多样性是 VNC 相比 RDP 的主要实施成本，建议按客户实际使用的 VNC 服务端做定向验证。',
  ], 'info')

  d.h(2, '11.5  数据存储与检索技术')
  d.table({
    caption: '表 11-24  存储与数据技术明细',
    headers: ['技术 / 软件', '类别与出处', '作用', '本产品中的角色'],
    widths: [18, 22, 26, 34],
    rows: [
      ['PostgreSQL', '开源关系数据库', '事务型数据存储', '**主数据库**（基准类型；表结构设计以 PostgreSQL 为基准）'],
      ['达梦 DM', '国产关系数据库', '信创环境替代', 'V1.2 适配（类型对照与 SQL 方言适配）'],
      ['人大金仓 KingBase', '国产关系数据库（基于 PG）', '信创环境替代', 'V1.2 适配'],
      ['Redis', '开源内存数据库', '缓存、会话状态、分布式锁', '会话状态缓存、策略缓存、限流计数、备份任务分布式锁'],
      ['对象存储（S3 兼容）', 'S3 API 事实标准；实现如 MinIO、Ceph RGW', '海量非结构化数据存储', '**录像文件存储**（库中仅存元数据，文件体入对象存储）'],
      ['纠删码（Erasure Coding）', '技术方法（如 4+2 冗余）', '以较低冗余成本获得容错能力', '录像与备份文件的存储冗余策略'],
      ['Elasticsearch / OpenSearch', '开源全文检索', '日志检索与聚合分析', '可选组件（审计日志量极大时可引入；默认用数据库分区 + 索引满足）'],
      ['数据库分区', '技术方法（Range / List 分区）', '大表按时间切片，提升查询与清理效率', '审计日志、资源指标、自检记录、备份记录等时序表按月 / 日分区'],
    ],
  })

  d.h(2, '11.6  开发框架与运行时组件')
  d.table({
    caption: '表 11-25  开发框架与组件明细',
    headers: ['组件', '类别', '作用', '本产品中的角色'],
    widths: [18, 20, 26, 36],
    rows: [
      ['Go', '编程语言（Google）', '高并发、部署简单', '**服务端主语言**（gateway、领域服务、协议代理、定时任务）'],
      ['Rust', '编程语言（Mozilla 起源）', '内存安全、零成本抽象、高性能', '**性能热点**：协议解析（RDP / VNC）、图形编码、加密运算'],
      ['Vue 3', '前端框架', '组件化 SPA 开发', 'Web 客户端与管理界面'],
      ['TypeScript', '语言（JavaScript 超集）', '静态类型', '前端开发语言'],
      ['Vite', '前端构建工具', '快速构建与热更新', '前端工程构建'],
      ['Pinia', 'Vue 状态管理库', '集中式状态管理', '前端状态管理'],
      ['xterm.js', '浏览器终端模拟器', '在浏览器中渲染终端', 'Web 客户端字符终端渲染（SSH / Telnet）'],
      ['Wails v2', '桌面应用框架（Go + 系统 WebView）', '包体小、内存占用低；与后端同语言，可直接复用国密运算与准入代码', '**Windows / Linux 桌面客户端外壳**'],
      ['FreeRDP', '开源 RDP 客户端库', '实现 RDP 协议客户端', 'Linux 客户端的图形会话渲染；服务端渲染模式下的 RDP 客户端能力参考'],
      ['libssh / x/crypto/ssh', 'SSH 协议库', '实现 SSH 客户端与服务端', '协议代理的 SSH 解析与转发'],
      ['WebAssembly', '浏览器字节码标准（W3C）', '在浏览器中运行高性能代码', '浏览器侧国密算法实现（SM2 / SM3 / SM4）'],
      ['Docker / Kubernetes', '容器与编排', '部署与弹性伸缩', '服务端容器化部署与集群编排'],
    ],
  })

  d.h(2, '11.7  标准与合规依据')
  d.p('本节汇总产品设计所依据的标准与合规要求，便于合规审查、投标应答与密评准备时快速定位。')
  d.h(3, '11.7.1  网络安全等级保护 2.0')
  d.table({
    caption: '表 11-26  等保 2.0 相关标准',
    headers: ['标准号', '名称', '与本产品的关系'],
    widths: [22, 34, 44],
    rows: [
      ['GB/T 22239-2019', '信息安全技术 网络安全等级保护基本要求', '安全通用要求与云计算、移动互联扩展要求的落地依据（三级要求为主）'],
      ['GB/T 25070-2019', '网络安全等级保护安全设计技术要求', '安全区域边界、计算环境、管理中心的设计参考'],
      ['GB/T 28448-2019', '网络安全等级保护测评要求', '测评项对应关系，用于准备测评材料'],
      ['GB/T 22240-2020', '网络安全等级保护定级指南', '定级方法参考'],
    ],
  })
  d.h(3, '11.7.2  密码应用标准族')
  d.table({
    caption: '表 11-27  密码应用标准',
    headers: ['标准号', '名称', '与本产品的关系'],
    widths: [24, 34, 42],
    rows: [
      ['GB/T 39786-2021', '信息安全技术 信息系统密码应用基本要求', '密码应用方案的顶层依据（四级要求）；密评的核心对标标准'],
      ['GM/T 0028', '密码模块安全技术要求', '密码模块自检、密钥管理等要求；密码卡 / 密码机选型依据'],
      ['GM/T 0018', '密码设备应用接口规范', '密码设备接口调用规范（与 PKCS#11 配合）'],
      ['GM/T 0030', '服务器密码机技术规范', '密码机功能与性能要求'],
      ['GM/T 0016', '智能密码钥匙密码应用接口规范', 'UKey 对接依据'],
      ['GM/T 0024', 'SSL VPN 技术规范', '**国密 TLS 密码套件**定义（SM2 + SM3 + SM4）'],
      ['GB/T 32918', 'SM2 椭圆曲线公钥密码算法', '数字签名与密钥交换'],
      ['GB/T 32905', 'SM3 密码杂凑算法', '摘要与完整性校验'],
      ['GB/T 32907', 'SM4 分组密码算法', '数据加解密'],
      ['GM/T 0044', 'SM9 标识密码算法', 'P2 规划，未纳入主线'],
    ],
  })
  d.h(3, '11.7.3  商用密码应用安全性评估（密评）')
  d.table({
    caption: '表 11-28  密评相关说明',
    headers: ['项目', '内容'],
    widths: [22, 78],
    rows: [
      ['性质', '依据《密码法》与 GB/T 39786 开展的评估；结论由具备资质的第三方测评机构出具'],
      ['评估维度', '密码应用总体要求 + 四个技术层面（物理与环境、网络与通信、设备与计算、应用与数据）+ 三个管理层面（制度、人员、实施）'],
      ['与本产品的关系', '本产品需提供密码应用方案、算法与产品清单、密钥管理制度、证书体系说明等材料以支撑评估（FR-09-12）'],
      ['重要提醒', '**密评结论不作为软件功能验收的通过条件**——软件验收依据本文档的验收判据，密评结论作为合规验收的输入（详见需求文档 5.8 节 NFR-08-02）'],
    ],
  })
  d.h(3, '11.7.4  其他参考标准')
  d.table({
    caption: '表 11-29  其他标准与规范索引',
    headers: ['领域', '标准 / 规范', '与本产品的关系'],
    widths: [20, 32, 48],
    rows: [
      ['IT 服务管理', 'GB/T 28827 系列（信息技术服务 运行维护）', '运维流程设计的参考'],
      ['信息安全管理', 'ISO/IEC 27001 / 27002', '安全管理体系（ISMS）要求'],
      ['审计要求', '《网络安全法》《数据安全法》《个人信息保护法》', '日志留存（≥ 6 个月）、个人信息最小化采集（NFR-08-04）'],
      ['信创适配', '国产操作系统 / 数据库 / CPU 相关规范', '麒麟、UOS、openEuler、达梦、金仓、飞腾 / 鲲鹏 / 龙芯适配'],
      ['会话审计', '企业内部运维管理制度、SOX / 等保审计要求', '双人复核、录像留存、取证包导出等能力设计依据'],
    ],
  })
  d.note('本章使用说明', [
    '一、**售前与投标**：可依据 11.1 的协议明细应答「支持哪些协议」，依据 11.2、11.7 应答「国密与合规」类问题；',
    '二、**开发与测试**：可依据 11.1、11.4 明确协议版本与编码要求，据此编写兼容性测试用例；',
    '三、**实施与交付**：可依据 11.1.2（XRDP）、11.3（网络协议）编写部署与配置手册；',
    '四、**术语统一**：全项目文档中的协议名称以本章为准，避免出现「RDP 客户端」与「XRDP 协议」这类混用。',
  ], 'ok')

  d.h(1, '附录 A  系统配置项清单')
  d.table({
    caption: '表 A-1  关键配置项',
    headers: ['配置键', '默认值', '说明', '是否需重启'],
    widths: [30, 16, 40, 14],
    rows: [
      ['session.idle_timeout_min', '30', '会话空闲超时（分钟）', '否'],
      ['session.max_per_user', '20', '单用户最大并发会话数', '否'],
      ['session.ticket_ttl_sec', '60', '会话票据有效期（秒）', '否'],
      ['auth.fail_lock_threshold', '5', '登录失败锁定阈值', '否'],
      ['auth.lock_duration_min', '15', '锁定时长（分钟）', '否'],
      ['auth.password.min_length', '12', '口令最小长度', '否'],
      ['auth.password.expire_days', '90', '口令有效期（天）', '否'],
      ['auth.token.ttl_hours', '2', '访问令牌有效期（小时）', '否'],
      ['policy.cache_ttl_sec', '10', '策略缓存有效期（秒）', '否'],
      ['policy.regex_timeout_ms', '10', '正则匹配超时（毫秒）', '否'],
      ['cmd.confirm_timeout_sec', '300', '二次确认超时（秒）', '否'],
      ['record.slice_size_mb', '10', '录像分片大小（MB）', '是'],
      ['record.slice_seconds', '60', '录像分片时长（秒）', '是'],
      ['record.retain_online_days', '180', '录像在线保留（天）', '否'],
      ['audit.retain_archive_years', '3', '审计归档保留（年）', '否'],
      ['audit.query.max_range_days', '31', '审计查询最大时间跨度（天）', '否'],
      ['audit.export.max_records', '5000', '单次导出最大记录数', '否'],
      ['account.rotate.default_days', '90', '账号口令默认轮换周期（天）', '否'],
      ['key.rotate.default_days', '365', '密钥默认轮换周期（天）', '否'],
      ['proxy.max_sessions_per_node', '300', '单代理节点最大会话数', '否'],
      ['proxy.channel.max_mbps', '200', '单会话最大带宽（Mbps）', '否'],
    ],
  })

  d.h(1, '附录 B  状态与枚举定义')
  d.table({
    caption: '表 B-1  核心状态枚举',
    headers: ['对象', '状态', '说明'],
    widths: [18, 26, 56],
    rows: [
      ['会话', 'INIT / AUTHED / ACTIVE / RECORDING / CLOSING / CLOSED', '正常流转见第 4.5 节'],
      ['会话（异常）', 'REJECTED / ABNORMAL / TERMINATED', '拒绝、异常中断、被强制终止'],
      ['录像', 'recording / completed / failed / degraded', '录制中、完成、失败、降级（仅命令记录）'],
      ['工单', 'DRAFT / PENDING / APPROVING / APPROVED / REJECTED / GRANTED / EXPIRED / REVOKED', '见第 4.5 节状态机'],
      ['告警', 'OPEN / ACKED / HANDLING / CLOSED / IGNORED', '处置闭环状态'],
      ['授权', 'active / expired / revoked', '生效、到期、撤销'],
      ['账号', 'active / disabled', '启用、停用'],
      ['密钥', 'active / rotating / archived / destroyed', '密钥生命周期状态'],
      ['证书', 'valid / expiring / expired / revoked', '证书状态'],
      ['租户', 'active / suspended / expired', '租户状态'],
    ],
  })

  d.h(1, '附录 C  开发约定')
  d.table({
    caption: '表 C-1  编码与提交约定',
    headers: ['方面', '约定'],
    widths: [22, 78],
    rows: [
      ['分支模型', 'main（生产）、release/*（发布）、feature/*（功能）、hotfix/*（紧急修复）'],
      ['提交信息', 'type(scope): subject，type 取 feat/fix/docs/refactor/test/chore'],
      ['代码评审', '所有合并需至少 1 人评审；涉及鉴权、密码、审计的变更需 2 人评审'],
      ['单元测试', '核心算法（策略匹配、命令解析、加解密）覆盖率 ≥ 80%'],
      ['静态检查', '提交前执行 lint 与安全扫描，不允许高危问题入库'],
      ['错误处理', '禁止吞异常；对外返回业务错误码，对内保留原始错误与堆栈'],
      ['日志规范', '禁止打印口令、密钥、令牌与完整个人信息'],
      ['依赖管理', '新增依赖需评审（安全、许可证、活跃度），锁定版本'],
      ['接口变更', '先改文档与契约再改实现；破坏性变更需提供版本与迁移期'],
      ['数据库变更', '通过迁移脚本执行，禁止手工改生产库'],
    ],
  })

  d.h(1, '附录 D  设计追踪矩阵')
  d.table({
    caption: '表 D-1  需求与设计章节追踪',
    headers: ['需求域', '需求编号', '对应设计章节', '关键设计产出'],
    widths: [20, 16, 30, 34],
    rows: [
      ['初始化与管理员体系', 'IR-01 ~ IR-30', '6.1、3.4', '初始化状态机、四员创建与三权分立约束'],
      ['身份认证', 'FR-01', '6.2（含 6.2.5 联邦认证）、4.1、3.1', '认证编排器、令牌服务、登录流程；OIDC / SAML 联邦接入'],
      ['资产与账号管理', 'FR-02', '6.3、3.2', '资产台账、口令轮换引擎、巡检'],
      ['访问控制', 'FR-03', '6.4、8.1', '裁决算法、策略匹配、权限复核'],
      ['运维会话', 'FR-04', '6.5、6.6、3.1、4.2', '会话编排、票据、SSH / SFTP 代理'],
      ['审计与录像', 'FR-05', '6.8、3.3、4.4', '录像采集与封存、完整性校验、取证包'],
      ['告警与报表', 'FR-06', '6.9', '告警规则引擎、去重聚合、报表模板'],
      ['系统管理', 'FR-07', '6.12、6.13', '组织用户与配置中心（6.12）；设备自身运维（6.13）'],
      ['多租户与集成', 'FR-08', '6.12（含 6.12.3）、7.6', '租户隔离、租户访问审批策略、开放 API、错误码规范'],
      ['国密与安全', 'FR-09', '6.10（含 6.10.4 证书链）、8.2、8.3', '密钥服务、证书链管理、国密应用、摘要链'],
      ['性能需求', 'NFR-01', '10.1、8.1、8.5', '性能预算、策略缓存、一致性哈希'],
      ['可靠性需求', 'NFR-02', '10.2、8.6', '容错策略、熔断限流'],
      ['安全需求', 'NFR-03', '10.3、8.4、9.4', '安全防护清单、票据防重放、客户端安全'],
      ['易用性需求', 'NFR-04', '第 3 章', '软件原型与交互规则'],
      ['兼容性需求', 'NFR-05', '10.5', '兼容性设计要点'],
      ['数据需求', 'DR-01 ~ DR-10', '第 5 章 + 数据库设计文档', '表结构、索引、分区、加密'],
      ['设备运维管理', 'FR-07-09、FR-07-11 ~ FR-07-19、FR-09-13', '6.13（6.13.1 ~ 6.13.9）', '网络配置与回滚、资源监控、系统自检、备份还原与策略、时间与时间源、日志与日志服务器、SNMP、恢复出厂、密钥备份'],
    ],
  })
  d.p('本文件与《堡垒机数据库设计文档》共同构成堡垒机系统的详细设计基线，后续需求变更需同步评估两份文档的影响并更新版本。')
}
