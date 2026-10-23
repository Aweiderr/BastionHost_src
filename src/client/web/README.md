# 堡垒机 Web 客户端

Vue 3 + TypeScript + Vite 单页应用，中文界面，用于生产部署。

## 快速开始

```bash
npm install
npm run dev      # 开发服务器 http://localhost:5173
npm run build    # 类型检查 + 生产构建，产物在 dist/
npm run preview  # 预览构建产物
```

- 开发代理：`/api` → `http://127.0.0.1:8080`（管理服务），见 `vite.config.ts`
- WebSSH 直连：`ws://<当前页面主机名>:8081/api/v1/webssh`，不走代理（见 `src/utils/constants.ts` 的 `buildWebsshUrl`）

## 生产部署

1. 将 `dist/` 作为静态资源目录交由 Nginx / Caddy 托管，并配置 SPA 回退到 `index.html`。
2. 反向代理 `/api` 到管理服务 `127.0.0.1:8080`。
3. 放通页面主机名的 `8081` 端口（WebSSH WebSocket 直连服务端）。
4. HTTPS 部署时 `buildWebsshUrl` 会自动使用 `wss://`。

## 目录结构

```
src/
├── api/          接口封装（原生 fetch，统一响应信封与错误归一化）
├── components/   通用组件（表格、分页、弹窗、抽屉、标签、提示、终端回放等）
├── router/       路由表、权限元信息与侧边栏菜单定义
├── stores/       Pinia：auth（令牌/用户/权限，持久化）、app（全局提示）
├── styles/       主题变量与统一组件样式（无 UI 组件库）
├── types/        通用 UI 类型
├── utils/        字典常量（协议、等级、状态、风险、处置动作）与格式化工具
└── views/        页面
```

## 路由与权限

菜单与路由按当前用户 `permissions` 过滤，无权限的菜单不显示，直接访问 URL 会提示并回到仪表盘。

| 路由 | 页面 | 关键权限 |
| --- | --- | --- |
| `/login` | 登录（含 TOTP） | 无需登录 |
| `/setup` | 初始化向导（设备+超管 → 三员 → 封存） | 无需登录 |
| `/dashboard` | 仪表盘统计卡片 | — |
| `/assets` | 资产管理（分组树 + 账号抽屉） | `asset:asset:list` |
| `/terminal` | Web SSH 终端 | `ops:session:connect` |
| `/my-sessions` | 我的会话 | `ops:session:list` |
| `/audit/sessions` | 会话审计（详情 + 录像回放） | `audit:session:list` |
| `/audit/commands` | 命令审计 | `audit:command:list` |
| `/audit/logs` | 操作日志 | `audit:log:list` |
| `/policies` | 命令策略（列表 + 命令测试面板） | `policy:command:manage` |
| `/tickets` | 工单与授权（申请 / 审批 / 我的授权） | `ticket:*` |
| `/users` | 用户管理（含角色分配、角色权限说明） | `system:user:list` |
| `/system` | 系统信息（版本、密码服务自检、配置项） | — |

## 与接口文档的差异说明（已做兼容）

实现时以文档契约为主，同时兼容后端实际返回，差异如下：

| 位置 | 文档契约 | 后端实际 | 处理方式 |
| --- | --- | --- | --- |
| 资产创建/更新 | `os_type` | 请求体为 `ostype` | 提交时同时带两个字段（`src/api/assets.ts`） |
| 资产列表 | `tags` / `group_id` | 未返回 | 类型改为可选，界面降级展示“未分组” |
| 资产列表 | `reachable: bool` | 返回 `"unknown"` 等字符串 | 按可达 / 不可达 / 未探测三态展示 |
| 系统配置项 | `config_key` / `config_value` | `key` / `value` | 两者都兼容（`SystemView.vue`） |
| 权限点 | `perm_id` | 无该字段 | 以 `perm_code` 作为渲染 key |
| 密码服务自检项 | `detail` | `cost_ms` | 优先 `detail`，缺失时展示耗时 |
| 系统信息 | `runtime` | `os` / `arch` | 缺失 `runtime` 时用 `os / arch` 组合 |
| 操作日志 | `target_name` 等 | 返回 `target_id` / `after_value` | 目标为空时回退展示 `#target_id` 与变更摘要 |
| 初始化状态 | `step` / `sealed` / `has_super_admin` | `current_step` / `super_admin_sealed` / `admin_count` | 两套字段都读取，并提示“部分步骤已完成” |

## 依赖说明

- 终端使用 xterm.js 的现行 npm 包名 `@xterm/xterm`（旧包 `xterm` 已停止维护，且与 `@xterm/addon-*` 存在 peer 依赖冲突），配套 `@xterm/addon-fit`、`@xterm/addon-web-links`。
- 未引入任何 UI 组件库，样式为手写（深色侧边栏 + 浅色内容区）。
- 未引入 axios，`src/api/http.ts` 基于原生 `fetch` 实现统一信封、鉴权头、401/403 处理。
