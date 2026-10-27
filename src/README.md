# src —— 堡垒机产品源码

三个独立项目 + 一个共享模块，用 Go workspace（`go.work`）联合。

## 项目划分

| 目录 | 职责 | 技术栈 | 对应文档章节 |
| --- | --- | --- | --- |
| `manager/` | **管理服务**：初始化、认证、用户与角色、资产与账号、策略、工单、报表、租户治理 | Go 1.27 + PostgreSQL | 详设 6.1~6.4、6.9、6.12、6.13 |
| `server/` | **服务端**：协议代理、会话编排、命令管控、审计采集、录像封存 | Go 1.27 | 详设 6.5~6.8 |
| `client/` | **客户端**：Web（Vue 3）、Windows / Linux 桌面（Wails v2） | Vue 3 + Go + Wails | 详设 9 |
| `shared/` | **共享模块**：错误码、统一响应、公共模型、密码服务工厂 | Go 1.27 | 详设 7.6、11 |
| `deploy/` | **部署交付物**：一键安装脚本、systemd 单元、Linux 部署指南（含国产 OS 实测记录） | Shell / systemd | 详设 12 |

> 三个业务项目是独立的 Go module，通过 `replace` 指令引用 `shared`；部署时各自编译为独立二进制。
>
> 桌面客户端选用 **Wails v2**（而非早期规划的 Tauri）：实测银河麒麟 V10 的软件源只提供
>
> `libwebkit2gtk-4.0`（包名 `webkit2gtk3`），而 Tauri 2 要求 `webkit2gtk-4.1`，在麒麟上无法运行；
>
> Wails v2 依赖 4.0，与麒麟自带版本一致。同时 Wails 与后端同用 Go，可直接复用国密运算、
>
> 准入签名与接口客户端代码。详见 `TODO.md` 与 `client/README.md`。

## 快速开始

```powershell
# 构建（Windows）
.\build.ps1

# 构建到 Linux（amd64）
.\build.ps1 linux

# 运行测试
.\build.ps1 test

# 启动服务（开发模式）
$env:BASTION_DEBUG = 'true'
.\bin\manager.exe     # 管理服务，默认 :8080
.\bin\server.exe      # 服务端，默认 :8081
```

## 数据库

PostgreSQL 16，库名 `bastion`，用户 `bastion`。

### 迁移与初始化

迁移脚本位于 `manager/migrations/`，以**嵌入方式**打进二进制（部署只需交付可执行文件，无需附带 SQL 目录）。

```powershell
.\build.ps1 db-init      # 首次部署：建表 + 基础数据
.\build.ps1 db-status    # 查看迁移状态
.\build.ps1 db-reset     # 清空并重建（测试重建 / 恢复出厂设置）
```

也可直接调用迁移工具：

```powershell
.\bin\migrate.exe init              # 初始化
.\bin\migrate.exe up                # 增量迁移（版本升级）
.\bin\migrate.exe status            # 状态
.\bin\migrate.exe reset --yes       # 清空重建
.\bin\migrate.exe reset --dry-run   # 预览将执行的操作
```

### 迁移脚本来源（两种模式）

程序支持两种加载方式，**默认使用内置脚本**，按需切换到外部目录：

| 模式 | 来源 | 适用场景 | 是否需重新编译 |
| --- | --- | --- | --- |
| **内置**（默认） | 编译期嵌入二进制（`go:embed`） | 生产部署：单文件即可完成初始化，无外部依赖 | 改 SQL 需重新编译 |
| **外部目录** | `--dir` 参数或 `BASTION_MIGRATIONS_DIR` | 开发调试 / 运维热更新 SQL | **不需要** |

优先级：`--dir` 参数 > `BASTION_MIGRATIONS_DIR` 环境变量 > 内置脚本。

**运维定制流程**（无需重新编译程序）：

```bash
# 1. 导出内置脚本到目录
./bin/migrate export --dir ./migrations

# 2. 按需修改或新增 SQL
vi ./migrations/0001_core.sql

# 3. 以外部目录模式执行
./bin/migrate up --dir ./migrations

# 或通过环境变量固定
export BASTION_MIGRATIONS_DIR=/etc/bastion/migrations
./bin/migrate up
```

> 健壮性：脚本读取时会自动剥离 UTF-8 BOM —— Windows 记事本、PowerShell `Set-Content -Encoding UTF8`、
>
> VS Code 带 BOM 保存都会写入 BOM，直接送进 PostgreSQL 会报语法错误。

### 迁移文件

| 文件 | 内容 | 表数 |
| --- | --- | --- |
| `0001_core.sql` | 租户、组织、用户、角色、权限、字典、配置、许可、初始化记录、运行日志、配额 | 14 |
| `0002_asset.sql` | 资产、资产分组、标签、托管账号、账号历史 | 5 |
| `0003_acl.sql` | 授权、授权规则、授权变更日志 | 3 |
| `0004_session_audit.sql` | 会话、命令、文件、参与者、密钥使用 + 登录/操作/SQL/告警/告警规则/导出日志 | 11 |
| `0005_ticket.sql` | 工单、工单明细、审批流程、审批记录 | 4 |
| `0006_key_cert.sql` | 密钥元数据、证书 | 2 |
| `0007_device_ops.sql` | 备份任务与记录、自检记录、资源指标、证书链、身份源配置、租户访问策略、运维许可 | 8 |
| `0008_seed.sql` | 基础数据：系统租户、6 个内置角色、60 个权限点、13 个字典（59 项）、29 项系统配置 | — |
| `0009_role_permissions.sql` | 角色→权限点授予（三权分立 + 运维 + 审批），含权限断链防护 | — |
| `0010_command_policy.sql` | 命令策略表 + **19 条内置危险命令规则** + 工单号序列 + 策略/工单默认配置 | 1 |
| `0011_policy_rm_fix.sql` | **修正 rm 类策略**：修复「删根目录」正则误报，新增删绝对路径 / 通配符批量删 / 删家目录 / 删备份文件四条二次确认规则 | — |
| `0012_credential_manual.sql` | 凭据不托管模式：`ast_account.credential_mode` + CHECK 约束兜底 + 口令索取配置 | — |
| `0013_sms_code.sql` | 短信验证码表 + 有效期/位数/校验次数/手机号与 IP 双维度限流等 10 项配置 | 1 |
| `0014_mfa.sql` | 登录挑战表（`sys_login_challenge`）+ `sys_user.mfa_type` + 图形验证码配置 | 1 |
| `0015_super_admin_scope.sql` | 超级管理员权限收敛：只保留"管理管理员"的能力，移除 ops/asset/acl/policy/audit | — |
| `0016_captcha_mode.sql` | 图形验证码模式（always / after_failures / off） | — |
| `0017_role_level.sql` | 角色等级与授权上限（只能授予等级严格低于自己的角色，修复提权漏洞） | — |
| `0018_selftest.sql` | 自检记录表（四种触发时机：init / boot / periodic / manual）+ 保留期配置 | 1 |
| `0019_node_cluster.sql` | 集群节点注册表（`sys_node`）+ 心跳间隔/离线阈值/会话上限配置 | 1 |
| `0020_cluster_groups.sql` | 集群定义与成员表（运维手动录入的设备分组，IP 全局唯一）+ 探测周期配置 | 2 |
| `0021_cluster_vip.sql` | 集群 VIP 与成员 VIP 持有标记（主备模式的可见性） | — |
| `0022_cluster_failover.sql` | 主备切换事件表（首次选出 / 切换 / 丢主 / 脑裂） | 1 |
| `0023_sec_admin_role_list.sql` | 给安全管理员补 `system:role:list`（修复权限断链，见下） | — |
| `0024_grant_approval.sql` | 授权的 `grant_kind`（binding 绑定 / pass 通行证）与 `require_approval`（使用资源是否需要审批）；唯一索引下沉到 `grant_kind` 维度 | — |
| `0025_sys_admin_grant_scope.sql` | 系统管理员获得访问授权能力（`acl:grant:list/create/revoke`，业务方要求） | — |
| `0026_operator_no_asset_ledger.sql` | 收回运维人员的资产台账权限（`asset:asset:list` / `asset:account:list`）：没绑定的资源不该出现在运维视野里 | — |

合计 **55 张业务表 + 1 张迁移版本表（`schema_migrations`）= 56**，187 个索引。

> 与设计文档的差异：文档列出 48 张业务表，实际实现为 55 张。
>
> 其中 `km_key_usage` 与 `ops_key_usage` 职责重复，合并为 `ops_key_usage` 一张表
>
> （避免同一事实两处记录）；其余增量来自文档之后的功能迭代：
>
> 短信验证码、MFA 登录挑战、自检记录、集群节点与集群定义（3 张）、主备切换事件。

### 约定

- 主键统一 `BIGSERIAL`，时间统一 `TIMESTAMPTZ`，半结构化数据用 `JSONB`；
- **不建数据库级外键**（避免级联删除风险与写入开销），由应用层保证一致性，关联字段均建索引；
- 除 `sys_tenant` 外所有业务表含 `tenant_id`（系统级数据为 0）；
- 已发布的迁移**不可修改**（校验和会不一致），变更请新增迁移文件；
- 迁移脚本支持内置与外部目录两种来源，均会自动剥离 UTF-8 BOM。

### 跨平台编码约定

- 文件路径统一使用 `filepath` 包，自动适配各平台分隔符；
- 文件权限（`0o600` / `0o700`）在 Windows 上被忽略但不会报错，Linux 上生效；
- 源码与 SQL 一律使用 **UTF-8**；PowerShell 脚本使用 **UTF-8 with BOM**（Windows PowerShell 5.1 识别中文所需）。

## 跨平台支持

三个程序均为**纯 Go 实现、无 CGO 依赖**，可交叉编译为静态二进制。

### 已验证的目标平台

| 平台 | manager | server | migrate | 产物格式 |
| --- | --- | --- | --- | --- |
| windows/amd64 | ✓ | ✓ | ✓ | PE |
| linux/amd64 | ✓ | ✓ | ✓ | ELF（静态） |
| linux/arm64 | ✓ | ✓ | ✓ | ELF（静态） |
| darwin/amd64 | ✓ | ✓ | ✓ | Mach-O |
| darwin/arm64 | ✓ | ✓ | ✓ | Mach-O |

**Linux 产物为静态链接、不含 GLIBC 符号依赖** —— 可直接运行于任意发行版（含 Kylin、UOS、openEuler、Alpine），无需安装运行时依赖。

### 构建入口

Windows 使用 `build.ps1`，Linux / macOS 使用 `Makefile`，两者能力一致：

| 操作 | Windows | Linux / macOS |
| --- | --- | --- |
| 构建全部 | `.\build.ps1` | `make` |
| 运行测试 | `.\build.ps1 test` | `make test` |
| 静态检查 | `.\build.ps1 vet` | `make vet` |
| 交叉编译全部平台 | `.\build.ps1 cross` | `make cross` |
| 初始化数据库 | `.\build.ps1 db-init` | `make db-init` |
| 查看迁移状态 | `.\build.ps1 db-status` | `make db-status` |
| 重建数据库 | `.\build.ps1 db-reset` | `make db-reset` |

产物输出到 `bin/`（当前平台）与 `dist/<os>-<arch>/`（交叉编译）。

### 部署流程（Linux）

```bash
# 1. 在开发机交叉编译
make cross

# 2. 拷贝到目标机（单文件，无需其他依赖）
scp dist/linux-amd64/* root@target:/opt/bastion/bin/

# 3. 在目标机初始化数据库
cd /opt/bastion/bin
BASTION_DB_HOST=127.0.0.1 BASTION_DB_PASSWORD='***' ./migrate init

# 4. 启动服务
BASTION_DB_HOST=127.0.0.1 BASTION_JWT_SECRET='***' ./manager &
BASTION_DB_HOST=127.0.0.1 ./server &
```

## 已实现接口

### 初始化（`/api/v1/setup/*`）

对应需求 IR-01 ~ IR-30。**接口层面**支持"设备证书 → 密码模块 → 超级管理员 → 三员 → 封存超管"的完整流程；

**当前推荐流程**是「设备 + 超级管理员」一步完成，三员由超级管理员登录后在用户管理里创建 ——

超管的职责本就是创建管理员，让一个匿名向导替它把三员建完，等于让匿名流程行使了超管的权力。

`/setup/admins` 与 `/setup/seal` 仍保留，供需要一次性建成三员的部署使用。

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/setup/status` | 否 | 初始化状态与下一步动作 |
| POST | `/api/v1/setup/init` | 否 | 一步完成：生成设备密钥 → 密码模块自检 → 创建超级管理员，返回**初始化临时令牌** |
| POST | `/api/v1/setup/admins` | setup 令牌 | 创建三员（系统/安全/审计管理员），校验三员不得同名 |
| POST | `/api/v1/setup/seal` | setup 令牌 | 封存超级管理员，初始化完成 |
| POST | `/api/v1/setup/reset-super-admin` | 否 | **应急恢复**：重建超级管理员账号（口令丢失或误删），其余数据保持不变 |

**典型流程**：

```bash
# 1. 查询状态
curl http://127.0.0.1:8080/api/v1/setup/status

# 2. 初始化（返回 setup_token）
curl -X POST http://127.0.0.1:8080/api/v1/setup/init \
  -H 'Content-Type: application/json' \
  -d '{"username":"superadmin","password":"Bastion@2025","device_name":"堡垒机-01"}'

# 3. 创建三员（用 setup_token）
curl -X POST http://127.0.0.1:8080/api/v1/setup/admins \
  -H "Authorization: Bearer $SETUP_TOKEN" -H 'Content-Type: application/json' \
  -d '{"admins":[
    {"role_code":"sys_admin","username":"sysadmin","password":"Sys@Bastion2025"},
    {"role_code":"sec_admin","username":"secadmin","password":"Sec@Bastion2025"},
    {"role_code":"audit_admin","username":"auditadmin","password":"Aud@Bastion2025"}]}'

# 4. 封存超管
curl -X POST http://127.0.0.1:8080/api/v1/setup/seal -H "Authorization: Bearer $SETUP_TOKEN"
```

### 认证（`/api/v1/auth/*`）

对应需求 FR-01。口令采用 **SM3 加盐迭代（10000 轮）** 存储；令牌采用 **HMAC-SM3** 签名（不依赖第三方 JWT 库，全程国密）。

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/auth/login` | 否 | 登录（口令 + 可选 TOTP），返回访问令牌与刷新令牌；启用 MFA 时不发令牌，返回挑战 |
| POST | `/api/v1/auth/mfa/send` | 否 | 按登录挑战触发第二因子验证码（短信 / 邮件），需携带 `challenge_id` |
| POST | `/api/v1/auth/mfa/verify` | 否 | 校验第二因子后签发令牌（短信验证码 / TOTP / UKey） |
| GET | `/api/v1/auth/captcha` | 否 | 获取图形验证码（按 `captcha.mode` 决定是否要求） |
| GET | `/api/v1/auth/captcha/required` | 否 | 当前登录是否需要图形验证码（供登录页提前展示） |
| POST | `/api/v1/auth/refresh` | 否 | 用刷新令牌换取新的访问令牌 |
| POST | `/api/v1/auth/logout` | 是 | 登出 |
| GET | `/api/v1/auth/me` | 是 | 当前用户信息、角色与权限列表 |
| POST | `/api/v1/auth/password` | 是 | 修改口令（校验原口令） |
| POST | `/api/v1/auth/totp/start` | 是 | 开始绑定动态令牌，返回 otpauth URI 与手动密钥 |
| POST | `/api/v1/auth/totp/confirm` | 是 | 用验证码确认绑定，返回 10 个一次性恢复码 |
| POST | `/api/v1/auth/totp/unbind` | 是 | 解绑动态令牌 |

**安全特性**：

- 口令 SM3 加盐迭代存储，数据库中不可见明文；
- 登录失败计数 + 阈值锁定（默认 5 次锁定 15 分钟，可配置）；
- 用户不存在与口令错误返回**同一错误码**，避免账号枚举；
- 动态令牌支持 SHA1（兼容主流 App）与 SM3（国密，需配套 App）；
- 容错窗口 ±1 个时间片（±30 秒）；
- **MFA 二次认证**：账号可配置第二因子（`sys_user.mfa_type`：`totp` / `sms` / `ukey`）。
  第一因子校验通过后**不签发令牌**，改为返回登录挑战（`challenge_id`，一次性、短时效、绑定来源 IP），
  由第二因子换取令牌 —— 校验通过前拿不到任何可用凭证；
- **图形验证码**：按 `captcha.mode`（`always` / `after_failures` / `off`）决定是否要求，
  一次性消费；存储在进程内存，多实例不共享（见 `TODO.md`）。

### 资产管理（`/api/v1/assets/*`）

对应需求 FR-02。**账号口令采用 SM4 加密存储，密钥由密码服务托管**。

| 方法 | 路径 | 权限点 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/assets` | `asset:asset:list` | 资产列表（支持 keyword、level、protocol、分页） |
| POST | `/api/v1/assets` | `asset:asset:create` | 创建资产（端口按协议自动填充） |
| GET | `/api/v1/assets/{id}` | `asset:asset:list` | 资产详情 |
| PUT | `/api/v1/assets/{id}` | `asset:asset:update` | 更新资产 |
| DELETE | `/api/v1/assets/{id}` | `asset:asset:delete` | 删除资产（级联删除账号） |
| GET | `/api/v1/asset-groups/tree` | `asset:asset:list` | 分组树（物化路径实现） |
| POST | `/api/v1/asset-groups` | `asset:asset:create` | 创建分组 |
| DELETE | `/api/v1/asset-groups/{id}` | `asset:asset:delete` | 删除分组（有子分组或资产时拒绝） |
| GET | `/api/v1/assets/{id}/accounts` | `asset:account:list` | 账号列表（密文脱敏） |
| POST | `/api/v1/assets/{id}/accounts` | `asset:account:manage` | 创建账号（`credential_mode` 指定托管 / 不托管；托管账号口令加密入库） |
| PUT | `/api/v1/accounts/{id}` | `asset:account:manage` | 更新账号属性，**含托管 ↔ 不托管切换**（切回托管必须同时给凭据） |
| DELETE | `/api/v1/accounts/{id}` | `asset:account:manage` | 删除账号 |
| POST | `/api/v1/accounts/{id}/password` | `asset:account:manage` | 更新口令（保留历史版本） |
| POST | `/api/v1/accounts/{id}/reveal` | **`asset:account:viewpwd`** | **查看明文口令（高敏，强制审计）** |
| GET | `/api/v1/assets/credential-key` | `asset:account:list` | 查询凭据加密密钥 ID |

**口令安全设计**：

```
明文口令
   │
   ├─ SM4 加密（密钥由密码服务托管）→ ast_account.password_cipher（Base64 密文）
   └─ SM3 指纹 → ast_account_history.password_digest（不可逆，用于核对）
```

- **密文与密钥分离**：密钥材料在密码服务密钥库，密钥 ID 记录在 `sys_config.crypto.credential_key_id`；
- **能改不能看**：系统管理员可管理账号（含重置口令），但**无查看明文权限**；查看明文归安全管理员（`asset:account:viewpwd`）；
- **高敏留痕**：每次查看明文口令均写入 `audit_operation_log`；
- **版本历史**：口令变更时旧密文入 `ast_account_history`，支持追溯与回滚。

**凭据模式（托管 / 不托管）** —— `ast_account.credential_mode`，**账号级**开关，同一资产可混用：

| 模式 | 凭据在哪 | 运维怎么用 |
| --- | --- | --- |
| `managed`（默认） | 堡垒机托管：SM4 加密入库 | 授权后**免密登录**（网关自己取密文解密） |
| `manual` | **堡垒机不留存任何凭据材料** | 连接时**现场输入**：SSH 网关在终端提示（不回显），Web 终端弹窗索取 |

- **入口**：「资产管理 → 账号管理 → 新建/编辑账号 → 凭据模式」；也可通过
  `POST /api/v1/assets/{id}/accounts` 与 `PUT /api/v1/accounts/{id}` 的
  `credential_mode` 字段设置。**创建后可以双向切换**；
- **切换的约束不对称**：
  · 切到 `manual`：必须清空全部凭据材料（数据库 CHECK 约束
    `ck_account_manual_no_secret` 兜底），`rotate_days` 归零；
  · 切回 `managed`：**必须同时提供口令或私钥**，否则会造出
    "托管却没有任何凭据"的账号——界面上一切正常，直到运维连接时才失败；
- **不托管不能防"口令爆破"**：手动输入环节必须限次，否则堡垒机就成了
  "带审计的口令爆破入口"。默认 3 次失败断开、单次等待 60 秒，
  由服务端环境变量 `BASTION_CREDENTIAL_MAX_ATTEMPTS` /
  `BASTION_CREDENTIAL_TIMEOUT` 控制；每次尝试都写审计（不含口令本身）；
- **口令不落库、不进录像、不进命令记录**：录像只记录目标输出方向，
  口令是输入；索取发生在会话建立前的独立阶段，不经过命令拦截器。
  `ops_session.credential_mode` 记录本次会话的凭据来源，便于审计区分；
- **已知取舍**：失去免密登录；`manual` 账号不参与口令轮换与复杂度策略
  （口令不在堡垒机手里）。

### 用户与角色管理（`/api/v1/users/*`、`/api/v1/roles`）

对应需求 FR-07。

| 方法 | 路径 | 权限点 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/users` | `system:user:list` | 用户列表（支持 keyword、status、role_code、分页） |
| POST | `/api/v1/users` | `system:user:create` | 创建用户（校验口令强度） |
| GET | `/api/v1/users/{id}` | `system:user:list` | 用户详情 |
| PUT | `/api/v1/users/{id}` | `system:user:update` | 更新用户与角色 |
| DELETE | `/api/v1/users/{id}` | `system:user:delete` | 删除用户（禁止删除自己） |
| POST | `/api/v1/users/{id}/password/reset` | `system:user:update` | 重置口令（返回临时口令，强制下次修改） |
| POST | `/api/v1/users/{id}/unlock` | `system:user:update` | 解锁账号 |
| POST | `/api/v1/users/{id}/roles` | `system:role:manage` | 分配角色 |
| GET | `/api/v1/users/stats` | `system:user:list` | 用户状态统计 |
| GET | `/api/v1/roles` | 登录即可 | 角色列表 |
| GET | `/api/v1/roles/{id}/permissions` | `system:role:list` | 角色权限 |
| GET | `/api/v1/permissions` | `system:role:list` | 权限点列表（按模块分组） |

### 命令策略（`/api/v1/policies/*`）

命令策略引擎位于 `shared/policy`，**由管理服务（配置）与服务端（执行）共用**，保证两侧判定完全一致。

| 方法 | 路径 | 权限点 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/policies/command` | `policy:command:manage` | 策略列表（keyword / action / risk_level / enabled / 分页） |
| POST | `/api/v1/policies/command` | `policy:command:manage` | 创建策略（校验正则合法性，重名拒绝） |
| GET | `/api/v1/policies/command/{id}` | `policy:command:manage` | 策略详情 |
| PUT | `/api/v1/policies/command/{id}` | `policy:command:manage` | 更新策略（**部分更新**：可只改处置动作或启停） |
| DELETE | `/api/v1/policies/command/{id}` | `policy:command:manage` | 删除策略 |
| POST | `/api/v1/policies/command/test` | `policy:command:manage` | **命令判定测试**：配置后立即验证是否生效 |
| GET | `/api/v1/policies/stats` | `policy:command:manage` | 策略统计（动作分布、引擎规则数、默认动作、白名单模式） |

**五种匹配方式**

| 匹配方式 | 作用对象 | 适用场景 |
| --- | --- | --- |
| `regex` | 整条命令文本 | 结构化危险模式（`rm -rf /`、`dd of=/dev/sda`） |
| `keyword` | 整条命令文本 | 简单关键字（`curl`、`nmap`） |
| `prefix` | 整条命令文本 | 特定工具前缀 |
| `exact` | 整条命令文本 | 精确命令 |
| **`command`** | **每条子命令的程序名** | `useradd`、`passwd`、`mysqldump` 等"谁在执行" |

> `command` 类型是为解决真实误报而新增的：`regex` 作用于整行文本，`cat /etc/passwd` 会被 `passwd` 关键字误伤、`grep useradd /var/log/secure` 会被 `useradd` 误伤。`command` 类型先按 `;` / `&&` / `||` / `|` / 换行做**引号感知切分**，再逐段提取程序名（自动跳过 `sudo` / `env` / `nohup` 等包装器与环境变量赋值），因此只匹配"真正被执行的程序"。

**四种处置动作**：`allow` 放行、`audit` 放行并告警、`confirm` 二次确认后放行、`deny` 阻断。

**判定顺序**：规则按 `priority` 升序，同优先级时 `deny` 优先，命中即返回；全部未命中时按默认动作（白名单模式下默认 `deny`）。

**内置规则**：23 条（8 条阻断、9 条二次确认、6 条放行告警），出厂即启用；内置规则**不允许修改匹配模式**，但可调整处置动作或停用。

> 其中 19 条来自 `0010`，`0011` 补充了 4 条二次确认规则（删绝对路径 / 通配符批量删 / 删家目录 / 删备份文件）。

> ⚠️ 命令策略是**启发式防线**而非完备沙箱：不做变量展开、不解析命令替换与 here-doc。真正的强隔离依赖会话录像 + 全量审计 + 网络准入。

### 工单与授权（`/api/v1/tickets/*`）

闭环：**申请 → 审批 → 自动生成授权 → 到期失效 / 撤销**。

| 方法 | 路径 | 权限点 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/tickets` | 登录即可 | 列表；无 `ticket:list:all` 时只返回本人工单 |
| POST | `/api/v1/tickets` | `ticket:apply` | 提交申请；`is_emergency` 需额外持有 `ticket:emergency` |
| GET | `/api/v1/tickets/{id}` | 登录即可 | 详情（含明细与审批记录）；非本人需 `ticket:list:all` |
| POST | `/api/v1/tickets/{id}/approve` | `ticket:approve` | 审批通过并**自动生成授权** |
| POST | `/api/v1/tickets/{id}/reject` | `ticket:approve` | 驳回（必须填写意见） |
| POST | `/api/v1/tickets/{id}/cancel` | `ticket:apply` | 撤销（仅申请人、仅待审批状态） |
| POST | `/api/v1/tickets/{id}/revoke-grants` | `ticket:approve` | 撤销该工单已产生的授权 |
| GET | `/api/v1/tickets/stats` | `ticket:list:all` | 工单统计（状态分布 + 有效授权数） |
| GET | `/api/v1/grants/my` | 登录即可 | 我的有效授权（运维人员查看可访问资产） |

**紧急放行**：持有 `ticket:emergency` 者可"先授权后补单"，工单状态为 `supplement`（待补单），且不可撤销。

### 访问授权（`/api/v1/grants/*`）

对应 `acl:*` 权限点，是**授权配置面**：管理员直接给运维人员分配资产。

> **谁能配置**：`sec_admin`（安全管理员）与 `sys_admin`（系统管理员，迁移 `0025` 起）。
>
> 菜单「访问授权」按 `acl:grant:*` 任一权限显示；超管与审计管理员不可见
>
> （超管只做"管理员的管理者"）。

**绑定与通行证是两类记录**（`acl_grant.grant_kind` 区分），这是理解本模块的关键：

| `grant_kind` | 回答的问题 | 生命周期 | 谁产生 |
| --- | --- | --- | --- |
| `binding` | 这个运维**可以访问（或申请）哪些资源** | 长期，不因工单到期而消失 | 安全管理员在「访问授权」页配置 |
| `pass` | **这一次审批**是否在有效期内 | 有明确起止时间 | 工单审批通过自动生成 |

绑定上还有一个开关 `require_approval`，决定**使用资源时的验证方式**：

| `require_approval` | 运维怎么用这个资源 |
| --- | --- |
| `false`（默认） | **免审批直连**：绑定即可访问，直接连 |
| `true` | **需要审批**：绑定只代表"可以申请"，实际使用前必须提交工单并审批通过；<br>审批产出的 `pass` 到期或被撤销后，**绑定仍然保留**，可再次申请 |

判定顺序（`repo.EvaluateAccess`，服务端建会话前调用）：

```
有效 pass            → 放行（审批已通过）
否则 binding 免审批  → 放行（绑定直连）
否则 binding 需审批  → 拒绝，need_approval=true → "请提交工单申请"
都没有               → 拒绝，"没有可用的授权"
```

> 第 3 种与第 4 种必须区分：前者运维自己提交工单就能解决，后者要找管理员要资源。
>
> 两者都回一句"无权限"，运维只能来回问人 —— 因此 `CheckGrant` 返回三态，
>
> 而不是一个布尔。

> 把绑定与通行证分成两类记录（迁移 `0024`）解决的是一个具体问题：此前
>
> `uk_grant` 只到「人 + 资产 + 账号」，工单到期会把绑定关系一并覆盖掉，
>
> 也表达不了"绑定了但访问要审批"。**唯一索引已下沉到 `grant_kind` 维度**，
>
> 同一维度可以同时存在一条绑定与一条通行证。

> 更早之前只有工单一条路径：`acl:grant:list/create/revoke/rule:manage` 四个权限点
>
> 与三张表从第一版就在，却**没有任何配置入口**，长期固定的访问关系也必须
>
> 每次走"申请 → 审批 → 到期 → 再申请"。

| 方法 | 路径 | 权限点 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/grants` | `acl:grant:list` | 授权列表（keyword / status / source / subject_id / asset_id / 分页），带用户名、资产名、账号名与 `grant_kind` |
| GET | `/api/v1/grants/options` | `acl:grant:list` | 可授权对象：运维人员 + 资产 + 各资产的托管账号（下拉数据源） |
| GET | `/api/v1/grants/{id}/logs` | `acl:grant:list` | 该授权的变更历史（`acl_grant_log`） |
| POST | `/api/v1/grants` | `acl:grant:create` | 创建绑定；同一「用户 + 资产 + 账号」已存在绑定时**原地接管**（含 `require_approval` 的切换） |
| PUT | `/api/v1/grants/{id}` | `acl:grant:create` | 调整权限开关、**审批属性**与有效期 |
| POST | `/api/v1/grants/{id}/revoke` | `acl:grant:revoke` | 回收（软删除 `status=revoked`，记录保留） |
| DELETE | `/api/v1/grants/{id}` | `acl:grant:revoke` | 删除记录（**仅限已回收 / 已过期**，避免误删生效中的授权） |

**授权维度与约束**：

- 维度为「**用户 × 资产（可细化到单个账号）**」；账号留空表示该资产下的所有账号；
- 权限开关：`can_login`（登录）/ `can_file`（文件传输）/ `can_copy` / `can_paste`，前两项至少勾一个；
- 有效期可留空（长期有效），也可指定区间；已过期的记录由后台巡检置为 `expired`，
  判定层同时用 `valid_to` 兜底，二者不会出现"看着有效却连不上"；
- 唯一索引 `uk_grant` 为「人 + 资产 + 账号 + `grant_kind`」：同一维度上绑定只有一条、
  通行证只有一条，二者互不覆盖；绑定接管时 `ticket_id` 保留，
  事后仍能追溯"这条访问权最早是哪张工单批的"；
- 每次创建 / 变更 / 回收 / 删除都写 `acl_grant_log`（含前后值）与 `audit_operation_log`；
- **需审批的绑定同样出现在运维侧**：`/api/v1/ops/assets` 会返回它并带上
  `need_approval=true`（前端提示"需申请"），否则运维会以为没有这个资源。

> **可授权对象接口为什么不复用 `/users` 与 `/assets`**：安全管理员按最小权限设计
>
> 没有 `system:user:list` 与 `asset:asset:list`，若复用它就会出现"唯一能配授权的人
>
> 打不开配置页"。`/grants/options` 只暴露授权所必需的最小信息，不含任何凭据。

> **尚未实现**：`acl_grant_rule`（ABAC 条件规则）的配置界面与判定、
>
> `acl_grant` 的 `time_ranges`（时间段）/ `source_cidrs`（来源网段）判定
>
> —— 字段已备但判定未接线，因此界面上不暴露，避免"配了不生效"。

### 系统管理（`/api/v1/system/*`）

除健康探针外，**本前缀下所有接口均要求登录**；带"权限"列的要求对应权限点

（此前该前缀整体未鉴权，实测未登录即可调用网络变更接口，已修复）。

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/system/info` | 登录 | 版本、运行平台、运行时长、软件完整性、服务状态 |
| GET | `/api/v1/system/metrics` | 登录 | 运行指标（CPU / 内存 / 网络吞吐），仪表盘图表与系统信息页共用 |
| POST | `/api/v1/system/selftest` | 登录 | 触发一次手动自检（密码服务 + 软件完整性） |
| GET | `/api/v1/system/selftest/latest` | 登录 | 最近一次自检结论 |
| GET | `/api/v1/system/selftest/history` | 登录 | 自检历史（含检查项明细） |
| GET | `/api/v1/system/config` | **`system:config:read`** | 系统配置项（支持 `?category=` 过滤）。含密码服务实现、凭据密钥 ID 与登录安全参数，因此收敛到权限点；运行指标与网口状态仍对登录用户开放（排障需要） |
| GET | `/api/v1/system/migrations` | 登录 | 数据库迁移状态 |
| GET | `/api/v1/system/crypto/selftest` | 登录 | 密码服务自检（保留的旧接口，5 项） |

### 设备网络配置（`/api/v1/system/network*`）

对应需求 FR-10（设备运维）。**以下接口均要求登录，写接口额外要求 `system:config:write`** ——

改网络配置（尤其是管理口、聚合、网桥）足以让设备失联、只能进机房恢复，

不属于"任意登录用户"能做的事（该前缀一度整体未鉴权，实测未登录即可调用

`/system/network/bond` 并进入业务逻辑，已修复）。

**每一项都遵循同一套"改错能回来"的安全模型**：

- **管理口标记**：读取网口时按"该网口网段是否包含本次请求来源"标记 `is_management`，改动它必须显式传 `force`;
- **立即生效 + 倒计时确认**：配置写入后立刻生效，但进入 `pending` 状态；超时未确认则**自动恢复原配置**；
- **整体备份与整体回滚**：高级配置一次改动多个 ifcfg（聚合本体 + 各成员），备份与回滚都以**一组文件**为单位，
  不会留下"成员已改、聚合不存在"的中间状态。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/system/network` | 网口 / 路由 / DNS 快照（含回环、虚拟、管理口标记） |
| POST | `/api/v1/system/network/apply` | 单网口配置（静态 / DHCP、网关、DNS、MTU、开机启用） |
| POST | `/api/v1/system/network/confirm` | 确认变更，取消自动回滚 |
| POST | `/api/v1/system/network/cancel` | 立即回滚 |
| GET | `/api/v1/system/network/pending` | 待确认的单网口变更（刷新页面后恢复倒计时） |
| POST | `/api/v1/system/network/bond` | 创建链路聚合：`active-backup` 主备 / `802.3ad` LACP，至少两个成员 |
| POST | `/api/v1/system/network/vlan` | 创建 VLAN 子接口（`<父接口>.<VLANID>`，ID 1~4094） |
| POST | `/api/v1/system/network/bridge` | 创建网桥（串联部署的可选能力，默认关闭 STP、`DELAY=0`） |
| POST | `/api/v1/system/network/advanced/members` | **修改聚合 / 网桥成员**：新增写 slave 配置，移除写回最小独立配置 |
| POST | `/api/v1/system/network/advanced/delete` | **删除**聚合 / VLAN / 网桥：成员一并脱离，整组文件一起回滚 |
| POST | `/api/v1/system/network/advanced/confirm` | 确认高级变更 |
| POST | `/api/v1/system/network/advanced/cancel` | 回滚高级变更（整组文件一起恢复） |
| GET | `/api/v1/system/network/advanced/pending` | 待确认的高级变更 |

**写配置时的取舍**：

- 只接管自己认识的键（`TYPE` / `BOOTPROTO` / `IPADDR` / `ONBOOT` …），其余键（`UUID`、`MASTER`、`BONDING_OPTS` 等）原样保留，
  避免把发行版特有字段写丢；
- 不配网关时**显式写入 `DEFROUTE=no`**，防止新接口抢走管理通道的默认路由；
- 链路聚合默认加 `miimon=100`（不加则主备模式感知不到链路断开），LACP 额外加 `lacp_rate=fast` 与 `xmit_hash_policy=layer3+4`；
- VLAN 子接口的 MTU 留空时**继承父接口**（子接口 MTU 不能超过父接口）；
- 成员网口承载管理连接时，加入聚合/网桥会清掉它的地址，必须显式确认后才放行；
- **删除或改成员同样按"一组文件"处理**：删掉 `ifcfg-bond0` 而不管成员，会留下写着
  `MASTER=bond0` 的网口 —— 聚合已不存在，它们既不属于任何聚合，也不会自己变回独立网口。
  因此成员会一并写回一份最小独立配置（开机启用、暂不分配地址）；
- 快照会解析 `MASTER=` / `BRIDGE=` / `PHYSDEV=` 并**反向聚合**出 `members`：
  ifcfg 只在成员一侧声明归属，上层接口不记录成员，不回查就画不出"bond0 由谁组成"；
- 上述校验与 ifcfg 生成都有单元测试固定（`manager/internal/netcfg/advanced_test.go`，20 项）。

### 集群定义与成员（`/api/v1/clusters/*`）

**集群由运维手动录入**：填一个集群名称 + 一串设备 IP，管理服务主动去探测这些设备。

这套设计与"节点自注册"（下一条）的分工是：

|  | 回答的问题 | 数据来源 |
| --- | --- | --- |
| **集群成员**（本节） | "哪几台设备应该在一起" | 运维录入，管理意图 |
| **节点自注册**（下一节） | "谁在跑、有多忙" | 进程上报，运行事实 |

为什么不让自注册直接成团：自注册只能说明"这个进程在跑"，说明不了"它属于哪个集群"。

同一个管理服务下面往往还挂着测试机、备用机，自动成团等于替运维做了判断，

而运维真正想表达的是"我这次只把 A、B 两台组成双机"。

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/clusters` | 登录 | 集群列表（含成员数 / 在线数 / 模式判定） |
| POST | `/api/v1/clusters` | `system:config:write` | **新建集群**：名称 + 设备 IP 列表 |
| GET | `/api/v1/clusters/{id}` | 登录 | 集群详情（成员设备与状态） |
| PUT | `/api/v1/clusters/{id}` | `system:config:write` | 修改集群名称与说明 |
| DELETE | `/api/v1/clusters/{id}` | `system:config:write` | 删除集群（成员记录一并删除，不影响设备本身） |
| POST | `/api/v1/clusters/{id}/members` | `system:config:write` | 追加设备 |
| DELETE | `/api/v1/clusters/{id}/members/{memberId}` | `system:config:write` | 移除设备 |
| PUT | `/api/v1/clusters/{id}/members/{memberId}` | `system:config:write` | 设备别名 / 权重 / 会话上限 |
| POST | `/api/v1/clusters/{id}/members/{memberId}/state` | `system:config:write` | 置为维护 / 恢复接单 / 摘除 |
| POST | `/api/v1/clusters/{id}/probe` | `system:config:write` | 立即探测（不等下一个周期） |

**录入规则**：

- 设备地址支持 `10.0.0.11` 或 `10.0.0.11:8082`；不写端口时用 `cluster.member.default_port`（默认 8081）；
- 批量录入支持换行、逗号、空格分隔；
- **一台设备只能属于一个集群**，由数据库唯一索引 `uk_cluster_member_ip` 保证 ——
  放在数据库而不是只做应用层校验：并发录入时"先查再插"挡不住两个请求同时通过检查；
- 创建是**整体成功或整体失败**：中途某个 IP 冲突，不会只写进去一半设备；
- 模式（单机 / 双机 / 集群多活）由成员数量推定，**不落库** —— 落库就会在删减成员后与实际不一致。

**主备（Keepalived + VIP）的可见性**：

- 集群可填一个 **VIP**（漂移地址）。VIP 在 Keepalived 里是 `ip addr add` 加到网卡上的，
  因此持有 VIP 的设备，其本机 IP 列表里必然出现它；服务端的观测接口上报本机 IP 列表，
  管理服务拿它与集群 VIP 比对 —— **谁持有 VIP，谁就是主**；
- 判定不依赖 Keepalived 回调、不需要额外 agent，也不要求管理服务能访问到 VIP
  （管理服务可能在另一网段，而 VIP 只对客户端所在的网络有意义）；
- 集群列表与成员详情会显示「主节点 / 备节点」，并在 `vip_held_count > 1` 时标红提示**脑裂**、
  在无人持有 VIP 时提示**无主**；
- **主备事件记录**：主节点归属发生变化时写一条（首次选出 / 主备切换 / 主节点丢失 / 脑裂），
  集群列表显示"最近切换 X 分钟前"，详情弹窗可看最近 20 条。
  持续无主或持续脑裂不重复记录 —— 否则一次故障会刷出上千条，把真正的变化淹没；
  这是核对切换耗时、发现 VIP 抖动的唯一依据（当前状态本身看不出这些）；
- VIP 必须是**独立于任何成员**的地址：配成某台设备自己的 IP 会让它"永远持有 VIP"，
  主备判定退化为"永远是同一台是主"、切换永不发生，但界面上看起来一切正常 —— 因此服务端会拒绝这种配置；
- 完整部署方案（健康检查脚本、切换演练、**录像共享**与脑裂防护）见 `deploy/keepalived-ha.md`。

**主动探测**：

- 管理服务每 15 秒（`cluster.probe.interval_seconds`）对每个成员请求一次
  `http://<设备IP>:<端口>/api/v1/sessions/stats`，一次拿到身份（node_id / 版本）与负载（活跃会话数）；
- 探测失败会把原因翻译成人话：`连接被拒绝：该设备上未启动堡垒机服务端（或端口不对）`、
  `网络不可达：请确认两台设备之间可以互通`、`探测超时，设备无响应` ——
  原文 `connection refused` 对排障没有帮助，而"没装服务"与"网络不通"的处理方式完全不同；
- 探测只读取状态，**不会改动对端任何配置**；已摘除的设备不再探测（继续打只会刷日志）。

**维护状态如何生效**（这是"点一下真的有作用"的关键）：

设备上的服务端每 3 秒心跳一次，上报自己的**本机 IPv4 列表**；管理服务用这些 IP 反查集群成员，

把成员记录里的 `admin_state` 作为准入结论回给设备。因此集群页点「置为维护」后，

该设备在 3 秒内就会拒绝新会话（存量会话继续跑完），而不只是界面上换个标签。

按 IP 关联而不是 node_id：node_id 是进程自己起的名字（可以随便改），

运维录入的是设备 IP，只有 IP 是两者共同的锚点。

### 集群节点管理（`/api/v1/cluster/*`、`/internal/v1/nodes/heartbeat`）

对应规划文档 5.3 集群化部署架构、9.1 可用性设计、9.3 扩展性设计；需求 NFR-01-01、NFR-02-02、NFR-07-01。

这一节是**节点自注册**（运行事实），集群归属见上一节。

设计上有三条边界：

1. **管理意图与运行事实分开存**：`sys_node.admin_state`（正常 / 维护 / 摘除）是运维的决定，
   心跳只刷新负载与时间。若共用一个 status 字段，节点每次心跳都会把自己写回"正常"，
   运维刚设置的维护状态会在 3 秒后莫名失效；
2. **"在线"是推导出来的**：按最后心跳时间与阈值实时判断，不落库 ——
   一旦落库就要有人负责改状态，而那个"有人"恰恰是已经挂掉的节点；
3. **维护只影响新会话**：置为维护后新会话不再进入该节点，存量会话继续跑完
   （会话在代理节点之间的热迁移不纳入首版，见 FR-04-07 的能力边界）。

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/cluster/overview` | 登录 | 集群概览：部署模式判定、节点计数、容量占用、设计目标与心跳参数 |
| GET | `/api/v1/cluster/nodes` | 登录 | 节点列表（支持 `?role=proxy`），含健康结论与会话占用 |
| POST | `/api/v1/cluster/nodes/{id}/state` | `system:config:write` | 设置运维状态：`active` / `draining` / `disabled` |
| PUT | `/api/v1/cluster/nodes/{id}` | `system:config:write` | 修改名称、负载权重、并发会话上限 |
| DELETE | `/api/v1/cluster/nodes/{id}` | `system:config:write` | 移除节点记录（**仅允许离线节点**） |
| POST | `/internal/v1/nodes/heartbeat` | HMAC-SM3 | 节点心跳：上报负载，取回"是否允许接收新会话" |

**心跳与准入**：

- 代理节点每 3 秒上报一次（`cluster.heartbeat.interval_seconds`），
  超过 10 秒无心跳判定离线（约 3 次丢失），对齐规划文档"健康检查 3 秒一次、连续 3 次失败摘除"；
- 心跳应答同时下发准入结论：节点被置为 `draining` / `disabled` 后，
  `accept_new_sessions` 为 false，SSH 网关据此拒绝**新会话**并给出提示；
- **管理面不可达时不改变上一次的准入结论** —— 若取不到应答就默认拒接，
  管理服务抖动几秒就等于全集群停止接单，比多接几个会话严重得多；
- 节点身份以**签名头**为准而非请求体：签名覆盖了 `X-Node` 头，请求体没有，
  只信请求体的话，任一持有共享密钥的节点都能冒充别的节点上报负载；
- 管理节点启动时也注册自己（标识为 `manager-<主机名>`），因此界面能看出管理面是单点还是多活；
  选主与仲裁交给部署层，这里不做第二套仲裁；
- 摘除或维护**不中断存量会话**；带外强制断开是会话审计里的另一个动作。

> 集群界面给出的 ≤5 秒 / ≤30 秒 / ≤60 秒是**规划文档的设计目标**，不是实测值。
>
> 实测切换时间需要在目标环境做故障注入演练后填写（见 `TODO.md` 待办）。

### 健康检查

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` | 服务与依赖组件健康状况 |
| GET | `/livez` | 存活探针 |
| GET | `/readyz` | 就绪探针（依赖可用才返回 200） |

### 审计查询（`/api/v1/audit/*`，对应需求 FR-06）

| 方法 | 路径 | 权限点 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/audit/sessions` | `audit:session:list` | 会话列表（keyword / status / user_id / asset_id / 时间范围） |
| GET | `/api/v1/audit/sessions/stats` | `audit:session:list` | 会话与命令统计（含被阻断命令数、高危命令数） |
| GET | `/api/v1/audit/sessions/{id}` | `audit:session:list` | 会话详情 + 命令明细 + 录像回放地址 |
| GET | `/api/v1/audit/commands` | `audit:command:list` | **跨会话命令检索**（按风险等级 / 处置动作 / 关键字） |
| GET | `/api/v1/audit/operation-logs` | `audit:log:list` | 管理操作日志 |
| GET | `/api/v1/ops/sessions/my` | 登录即可 | 运维人员回看自己的会话 |
| GET | `/api/v1/ops/assets` | 登录即可 | 运维视图的"我可访问的资产"（与 `/internal/v1/ops/assets` 同源，供 Web 端选择） |

**录像回放**：`GET http://<server>:8081/api/v1/records/{sessionNo}`（带 `Authorization: Bearer`）。

服务端先校验令牌与 `audit:record:play` 权限，再解密并**校验摘要链**后返回明文；

校验不过一律拒绝 —— 录像不可信时回放内容没有证据价值。

### Web 客户端（`client/web`）

Vue 3 + TypeScript + Vite + Pinia + xterm.js，中文界面。开发与构建：

```bash
cd client/web
npm install
npm run dev      # 开发服务器 :5173，/api 代理到管理服务 :8080
npm run build    # 产物 dist/
```

「系统 → **网络配置**」页包含两张卡片：单网口配置（IP / 网关 / DNS / MTU）与高级网络配置

（链路聚合 / VLAN 子接口 / 网桥）。两者共用同一套安全交互：管理口用红色标记并要求勾选知情确认，

提交后顶部出现**倒计时确认条**（可"确认配置无误"或"立即恢复"），超时未确认由服务端自动回滚。

网口列表**只列硬件网口**：回环（`lo`）与容器虚拟接口（`docker0` / `veth*` / `virbr*` / `br-*`）

不展示 —— 一台跑容器的机器上这类接口可能有几十个，混在列表里会把真正要看的网卡挤到看不见。

网络状态对所有登录用户开放（排障要看网口），写操作按 `system:config:write` 在界面上收敛入口。

构建产物由管理服务直接托管（`BASTION_WEB_DIR`，默认 `./web/dist`）：

把 `client/web/dist` 复制到管理服务工作目录的 `web/dist` 下，打开 `http://<manager>:8080` 即是控制台。

SPA 路由会回落到 `index.html`，而 `/api`、`/internal`、探针前缀仍返回 JSON 404。

### 服务端（`server`，默认 :8081，SSH 网关默认 :2222）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` | 服务、管理服务连通性、策略引擎状态、密码后端 |
| GET | `/livez` | 存活探针 |
| GET | `/readyz` | 就绪探针（管理服务可达 **且** 命令策略已加载才返回 200） |
| GET | `/api/v1/sessions/stats` | 本节点活跃会话数与上限（容量观测 / 负载均衡） |
| GET | `/api/v1/webssh` | **WebSSH WebSocket**：浏览器终端（令牌经管理服务校验） |
| GET | `/api/v1/records/{sessionNo}` | **录像回放**：解密 + 摘要链校验后返回明文 |

**SSH 网关**（第三方客户端接入，对应 FR-08-09）：

```bash
# 登录后交互式选择资产
ssh -p 2222 ops01@bastion-host

# 直接指定资产（-l 支持 `用户名@资产标识`，资产标识可为名称/ID/主机地址）
ssh -p 2222 -l ops01@web-prod-01 bastion-host
```

会话建立即开始录像；危险命令被阻断、需确认的命令进入 y/n 确认流程，全部命令（含被阻断的）写入 `ops_session_command`。

**会话录像**：分片加密落盘（SM4-CBC，密文自带 IV），哈希链保证任意分片被删除、重排或篡改都能被发现：

```
文件头 BSR1 | 分片[index|plainLen|cipherLen|prevDigest|digest|cipher]... | 文件尾 BSRE|chunkCount|fileDigest
```

```bash
# 校验录像完整性与可解密性（篡改任意一字节即报错）
.\bin\record-verify.exe -file .\data\records\2026\09\18\Sxxx.bsr -show 2
```

### 服务间接口（`/internal/v1/*`，仅服务端调用）

以 **HMAC-SM3 签名 + 时间戳窗口**认证，签名覆盖 `方法|完整RequestURI`（含 query，防止 `user_id` 被篡改越权）。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/internal/v1/ops/verify` | 校验运维人员口令与动态令牌 |
| POST | `/internal/v1/ops/verify-token` | 用访问令牌换取运维身份（WebSSH 等场景） |
| GET | `/internal/v1/ops/assets` | 该用户可运维的资产与账号 |
| GET | `/internal/v1/ops/grant` | 授权校验（资产 + 账号维度） |
| POST | `/internal/v1/ops/credential` | 解密下发连接凭据（**每次强制审计**） |
| GET | `/internal/v1/policies/commands` | 下发命令策略规则 |
| POST | `/internal/v1/sessions` | 会话开始 |
| POST | `/internal/v1/sessions/{id}/commands` | 命令留痕 |
| POST | `/internal/v1/sessions/{id}/finish` | 会话结束与录像归档 |
| POST | `/internal/v1/nodes/heartbeat` | 节点心跳：上报负载，取回"是否允许接收新会话"（身份以签名头为准） |

> **为什么服务端不直连数据库**：用户口令散列与凭据密钥只存在于管理面。代理节点因此可以部署在 DMZ 并横向扩展，而不扩散密钥；它本地只持有**录像加密密钥**（`record_encrypt`），泄漏也只影响录像机密性。

## 权限模型

内置 6 个角色，**60 个权限点**。下表权限数为运行库实际统计（2026-09 核对）：

| 角色 | 权限数 | 职责范围 | 明确排除 |
| --- | --- | --- | --- |
| `super_admin` | 6 | **只做"管理员的管理者"**：创建/删除/修改用户、授予角色权限、查看系统配置（迁移 `0015` 收敛） | **全部业务能力**：资产、授权、策略、审计、运维 |
| `sys_admin` | 29 | 用户、组织、资产、配置、设备运维（含网络配置与集群管理）、**访问授权**（迁移 `0025`） | 审计、安全策略、密钥、**查看明文口令** |
| `sec_admin` | 27 | 授权（含**访问授权配置**）、策略、密钥、角色权限授予、准入模式 | 用户管理、审计；系统配置**只读** |
| `audit_admin` | 8 | 审计查询、录像回放、报表、取证导出 | 任何管理与授权 |
| `operator` | 6 | 发起运维会话、文件传输、**只查看绑定给自己的资产**（迁移 `0026` 收回资产台账权限） | 管理与审计 |
| `approver` | 6 | 工单审批、紧急放行、查看全部工单 | 管理与审计 |

边界已通过数据库校验：系统管理员无审计权限、安全管理员无用户管理/审计权限、

审计管理员无管理/授权权限（均为 0）。

> **迁移 `0025` 起，系统管理员也持有 `acl:grant:list/create/revoke`**（业务方要求）：
>
> "录入资产"与"分配资源"本是同一批人在做，拆给两个账号反而多一道交接。
>
> 影响是 `sec_admin` 不再独占授权能力 —— 审计上区分责任时需注意，
>
> `acl_grant_log` 与 `audit_operation_log` 都保留了操作人快照。
>
> `acl:rule:manage`（ABAC 规则）仍未授予任何人以外的角色：该功能尚未实现，
>
> 授出去是空权限。

> **迁移 `0026` 起，`operator` 不再持有 `asset:asset:list` / `asset:account:list`**：
>
> 那两条权限让它能读**资产台账**（全部资产与账号），于是"没有绑定给他的资源
>
> 也能在资产页看到" —— 既是信息越权，也让"绑定关系"失去意义（看到 ≠ 能连）。
>
> 运维的正确视图是 `/api/v1/ops/assets`（按授权过滤，并标记 `need_approval`），
>
> Web 终端与工单申请页已改用它。
>
> `approver` **保留** `asset:asset:list`：紧急放行需要为他人指定任意资产；
>
> 前端通过 `hideForRoles` 让它不作为日常页面出现（菜单与路由双重拦截）。

> `super_admin` 的权限数从 60 收敛为 6 是**刻意**的：超管只负责"把管理员建出来"，
>
> 不具备资产、授权、审计与运维能力 —— 否则它就是一个绕过三权分立的超级后门。
>
> 它保留了 `system:config:read`，因此仍可**查看**系统信息、网络状态与集群状态；
>
> 但没有 `system:config:write`，界面上不给出任何写操作的入口。

## 配置

全部通过环境变量配置（前缀 `BASTION_`），均有默认值，本地开发可零配置启动。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `BASTION_DEBUG` | false | 调试模式（日志级别、默认密钥） |
| `BASTION_MANAGER_ADDR` | :8080 | 管理服务监听地址 |
| `BASTION_SERVER_ADDR` | :8081 | 服务端监听地址 |
| `BASTION_DB_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_NAME` | 127.0.0.1 / 5432 / bastion / bastion / bastion | PostgreSQL 连接 |
| `BASTION_CRYPTO_PROVIDER` | soft | 密码服务实现：`soft`（软件）/ `pkcs11`（密码卡、密码机） |
| `BASTION_CRYPTO_BACKEND` | go | 软件实现的后端：`gmssl` / `openssl` / `go` |
| `BASTION_JWT_SECRET` | （调试模式自动填充） | 令牌签名密钥，生产必须显式配置 |
| `BASTION_SSH_LISTEN` | :2222 | 服务端 SSH 网关监听地址（第三方客户端接入） |
| `BASTION_IDLE_TIMEOUT` | 30m | SSH / WebSSH 运维会话空闲上限（无用户输入；`0` 关闭）。到点后掐断连接，会话状态与结束原因都记为 `timeout`（审计显示「超时结束」）。系统配置项 `security.session.idle_minutes` 只展示同一默认值（30 分钟），代理不读取 |
| `BASTION_RECORD_DIR` | ./data/records | 录像存储目录 |
| `BASTION_KEYSTORE_DIR` | ./data/keys | 密钥库目录 |
| `BASTION_ALERT_WEBHOOK_URL` | （空） | 高危命令告警 Webhook。空则只落库 |

### 高危命令告警通知

策略阻断，或风险级别为 `danger` / `critical` 的命令，会写入 `audit_alert`（`alert_code=cmd_high_risk`）。审计管理员在「告警中心」确认、处理。

是否外推由规则 `cmd_high_risk` 的 `notify_channels` 决定（迁移 `0035` 在种子值 `["db"]` 上追加 `webhook`，已改过的通道不动）：

- `db`：只落库。
- `webhook`：向 `BASTION_ALERT_WEBHOOK_URL` POST JSON。地址只从该环境变量读取，不写进 `notify_targets`，日志也不打印 URL。未配置、或地址不是 http/https 时告警仍在，通知状态为 `skipped` / `failed`，不影响命令入库。
- 邮件、钉钉没有现成客户端，不内置 SDK。钉钉自定义机器人本身是 Webhook，把机器人地址配到上面的变量即可。

## 开发进度

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| 0 | 环境准备（Go 1.27、PostgreSQL 16） | **已完成** |
| 1 | 工程骨架（三项目 + 共享模块） | **已完成** |
| 3 | 密码服务工厂（SM2 / SM3 / SM4） | **已完成**（22 个测试通过） |
| 2 | 数据库表结构与迁移 | **已完成**（55 张业务表 + `migrate` 工具） |
| 4 | 管理服务业务 API | **已完成**：初始化、认证、TOTP、权限模型、用户角色、资产管理（含口令加密）、**命令策略（当时 19 条内置规则，现 23 条；含判定测试、优先级与白名单）**、**工单闭环（申请→审批→自动授权→撤销、紧急放行）**（44 + 52 + 112 项端到端测试通过） |
| 5 | 服务端 SSH 代理与审计 | **已完成**：SSH 网关（第三方客户端可直连）、命令截获与策略判定（放行 / 二次确认 / 阻断）、会话与命令留痕、录像（SM4 分片加密 + SM3 摘要链）、`record-verify` 篡改校验（22 项 SSH 端到端测试通过） |
| 6 | Web 客户端 | **已完成**：Vue 3 + TypeScript + Vite + Pinia + xterm.js，16 条路由（登录/初始化向导/仪表盘/资产/Web 终端/我的会话/会话审计/命令审计/操作日志/策略/工单/**访问授权**/用户/系统信息/**网络配置**/**集群管理**），菜单按权限过滤；由管理服务托管构建产物（43 项 WebSSH + 审计端到端测试通过） |
| 7 | 端到端集成与 Linux 部署验证 | **已完成**：银河麒麟 V10 真机部署（纯静态二进制零依赖、PostgreSQL 16 容器、manager / server / SSH 网关 / Web 控制台全部运行），管理面端到端 44 项、真实主机运维 15 项通过，含命令阻断、审计留痕与录像完整性校验 |
| 8 | 桌面客户端（Wails v2） | **Windows 版已完成**：设备指纹采集（SM3 归一化）、登录、可运维资产、SSH 终端（直连网关 :2222）、我的会话、设备与准入页；实测通过客户端运维银河麒麟主机；12.37 MB 单文件，进程内存约 30 MB。Linux 版待编译验证 |
| 9 | 设备网络配置 | **已完成**：单网口配置（静态 / DHCP、管理口显式确认、超时自动回滚）+ **链路聚合 / VLAN 子接口 / 网桥**（创建、改成员、删除；多文件整体备份与整体回滚、成员管理口拦截、桥接强制确认）+ 成员关系反向聚合展示；独立为「系统 → 网络配置」页（与系统信息、集群管理同级），列表只显示硬件网口；两张卡片均显示待确认倒计时条；20 项单元测试固定校验规则、ifcfg 生成与失败回滚 |
| 10 | 集群管理（双机 / 集群多活） | **代码已完成**：**集群由运维手动录入**（集群名 + 设备 IP 列表，一台设备只能属于一个集群，数据库唯一索引保证）+ 管理服务主动探测成员状态（默认 15 秒，失败原因翻译成人话）；节点自注册与心跳保留，按本机 IP 关联集群成员下发准入 —— 「置为维护」3 秒内真正拒绝新会话、存量继续；在线状态按心跳窗口实时推导，管理意图与运行事实分离；管理面不可达时保持上一次准入结论。**单元测试与故障注入演练待补**（见 `TODO.md`） |

设计依据：`../docs/` 下的四份文档。
