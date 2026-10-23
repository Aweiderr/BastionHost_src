# client —— 客户端

产品客户端保留三种形态（评审结论）：**Web 客户端**、**Windows 客户端**、**Linux 客户端**。
macOS 客户端与移动端不纳入范围。

## 目录

| 目录 | 内容 | 技术栈 | 状态 |
| --- | --- | --- | --- |
| `web/` | Web 客户端与管理界面 | Vue 3 + TypeScript + Vite + xterm.js | **已完成**（15 条路由，含网络配置与集群管理；构建产物由管理服务托管） |
| `desktop/` | Windows / Linux 桌面客户端 | **Wails v2（Go + 系统 WebView）** | **Windows 版已完成**（登录、设备指纹、资产、SSH 终端、会话回看；Linux 版待编译验证） |

## 桌面客户端技术选型（Wails v2）

早期文档规划为 Tauri，**阶段 7 真机实测后改为 Wails v2**，依据如下。

### 决定性因素：目标操作系统的 WebView 依赖

三种主流方案都要用系统 WebView（Electron 除外），而 WebView 的 ABI 版本是硬约束：

| 方案 | Linux 端依赖 | 银河麒麟 V10 实测 |
| --- | --- | --- |
| **Tauri 2** | `webkit2gtk-4.1`（WebKitGTK 2.36+ 的新 ABI） | ❌ 源里**没有** 4.1，无法运行 |
| **Wails v2** | `webkit2gtk-4.0` | ✅ 麒麟提供 `webkit2gtk3-2.22.2`，内含 `/usr/lib64/libwebkit2gtk-4.0.so.37` |
| Electron | 自带 Chromium，不依赖系统 WebView | ✅ 可用，但安装包约 150MB |

实测命令与结果：

```bash
$ yum provides "*/libwebkit2gtk-4.0.so*"
webkit2gtk3-2.22.2-11.ky10.x86_64 : GTK+ Web content engine library
    文件名：/usr/lib64/libwebkit2gtk-4.0.so.37

$ yum provides "*/libwebkit2gtk-4.1.so*"
（无结果）
```

### 次要因素

1. **代码复用**：客户端核心是**前置准入**（设备指纹 → 国密签名 → 申请运维许可 → 校验绑定）。
   这些能力在 Go 侧已经具备，Wails 可直接复用；Tauri 需用 Rust 重写。
2. **工具链成本**：Go 已就绪、编译秒级；Rust 需额外安装且首次编译耗时较长。
3. **交叉编译**：Go 用 `GOOS=linux` 即可产出 Linux 构件；Rust 交叉编译需额外配置。

### 必须知道的约束

- Wails 的 **Linux 构件必须在 Linux 上编译**（CGO 链接 webkit2gtk）。
  Windows 版本可在本机编译；Linux 版本建议在麒麟/WSL/Docker 中构建。
  麒麟源里已有 `webkit2gtk3-devel`，具备编译条件。
- Windows 端依赖 WebView2 运行时（Win10 及以上通常自带，本机已确认存在）。

## Web 客户端功能范围（阶段 6）

- 登录页（口令 + TOTP，对接管理服务认证）
- 初始化向导（首次部署时创建超级管理员与三员）
- 资产列表与资产树
- Web SSH 终端（xterm.js，经服务端代理建立会话）
- 会话列表与实时状态
- 审计查询与会话录像回放
- 系统管理（用户、角色、策略、设备运维）

## 开发前置

```bash
cd web
npm install
npm run dev
```

Vite 开发服务器需配置代理，将 `/api` 转发到管理服务（默认 `http://127.0.0.1:8080`）。
