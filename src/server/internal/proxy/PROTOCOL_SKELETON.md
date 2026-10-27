# 协议通道说明

## 已接通

| 协议 | 配置项 | 默认监听 | 代码 | 状态 |
|------|--------|----------|------|------|
| SSH / WebSSH | `BASTION_SSH_LISTEN` | `:2222` | `server.go` / `webssh.go` | 完整运维闭环 |
| RDP | `BASTION_RDP_LISTEN` + `BASTION_RDP_ENABLE` | `:3389` | `rdp.go` | **骨架**：TCP 监听 + 提示，无协议解析/授权/录像 |
| VNC | `BASTION_VNC_LISTEN` + `BASTION_VNC_ENABLE` | `:5900` | `vnc.go` | **骨架**：同上，无 RFB |
| Oracle | `BASTION_ORACLE_LISTEN` + `BASTION_ORACLE_ENABLE` | `:1521` | `oracle.go` | **授权 TNS 隧道**：见下文。默认关闭 |

资产台账 `ast_asset.protocol` 枚举已含 `rdp` / `vnc` / `oracle`（见迁移 0008）。
Oracle 的服务名 / SID 在 `ast_asset.oracle_service_name`、`ast_asset.oracle_sid`（迁移 0036）。

## Oracle 网关

开启 `BASTION_ORACLE_ENABLE=true` 之后，SQL*Plus、Instant Client、JDBC 等把堡垒机当监听来连。
命令策略还没拉到时不监听，避免没有规则就放 SQL。

### 连接约定

运维身份和 SSH 一样，写在连接描述符里，不写在数据库用户名上：

```text
(DESCRIPTION=
  (ADDRESS=(PROTOCOL=TCP)(HOST=堡垒机)(PORT=1521))
  (CONNECT_DATA=
    (SERVICE_NAME=运维用户@资产[#账号])
    (BASTION_PASSWORD=堡垒机口令)))
```

- `运维用户@资产` 支持资产 ID、名称、地址，匹配规则与 SSH 的 `parseLoginName` / `matchAsset` 相同。
- 多个托管账号时必须写 `#账号`。只有一个账号时可以省略。
- 口令末尾的 `:六位数字` 视为动态令牌，与 SSH 相同。口令本身不能含圆括号。
- 也可以改用 `BASTION_USER` / `BASTION_ASSET` / `BASTION_ACCOUNT` 这三个键，不必把身份塞进服务名。

网关校验顺序：运维口令 → `ops:session:connect` → 资产授权与账号 → 建 `ops_session` → 取连接参数。
未授权或身份不对时回 TNS REFUSE，不会拨目标库。

校验通过后，描述符里的主机、端口、`SERVICE_NAME` 或 `SID` 改写成资产台账上的值（两者都填时只用服务名），`BASTION_*` 键全部删掉，再把后续 TNS 包双向转发。
监听若回 REDIRECT，网关改写回连地址，不把客户端直接放到数据库上。监听绑在 `0.0.0.0` 时需要 `BASTION_ORACLE_ADVERTISE`（客户端能访问到的 IP 或主机名），否则拒绝这次重定向。

示例（库用户名和库口令是 SQL 客户端自己交给 Oracle 的，见边界）：

```bash
sqlplus 'app/库口令@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=堡垒机)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=堡垒机口令)))'
```

资产账号为手动（不托管）时，库口令同样由 SQL 客户端提交。托管账号必须已经存了口令，否则会话直接拒绝；这次取凭据会记审计。

### 语句审计与策略

DATA 包里用 UTF-8 / UTF-16BE 尽力抠出 SQL 文本，写入 `ops_session_command`（`command_type=sql`）。
`IDENTIFIED BY` 和 `PASSWORD '...'` 会打码后再入库。日志不打印语句，也不打印口令。

命中 `deny`，或命中 `confirm`，都不把这个包转发出去，并结束会话（`end_reason=policy_deny`）。
TNS 上没有 SSH 那种 y/n，确认动作没法交互，所以按阻断处理；审计行里的 `policy_action` 仍是原来的 `confirm`。
`audit` 只留痕，语句照常转发。判定上下文的协议是 `oracle`。

迁移 0036 加了三条只作用于 `protocols: ["oracle"]` 的出厂规则：`DROP` 对象、`TRUNCATE TABLE` 阻断，`CREATE/ALTER USER`、`GRANT`、`REVOKE`、`IDENTIFIED BY` 记为高危审计。范围为空的 shell 规则仍然对 Oracle 文本生效，只是那些正则很少碰上 SQL。

空闲断开与 SSH 共用 `BASTION_IDLE_TIMEOUT`。会话登记进同一张在途表，管理员强制中断和节点心跳能看见它。

### 未完成边界（勿当作已交付）

1. **画面通道**：RDP/VNC 需完整握手、剪贴板/磁盘映射策略、录像帧封装。
2. **Oracle 口令注入**：11g 及以后的 O5LOGON / O7LOGON 不在线上给出可还原的库口令。本版本**不改写认证包**，SQL 客户端仍使用资产账号和库口令向 Oracle 认证。托管口令只用于“账号已配置口令”的核对和凭据下发审计，不写入连接串。
3. **SQL 提取是旁路启发式**，不是完整 TTC。绑定变量里的原文、分片在多个包里的语句、非 UTF-8/UTF-16 的编码可能漏记。阻断以“这个包里识别出的文本”为准，识别不到就不会拦。
4. **重定向回连**会短时占用一个额外端口，只接受原客户端 IP 的一次连接。防火墙需要放行这个端口，或者让监听不要重定向（专用服务器直连）。
5. 不录像。手动账号没有交互式口令提示。

## 启用方式

```bash
# RDP / VNC 仍是骨架
export BASTION_RDP_ENABLE=true
export BASTION_VNC_ENABLE=true

# Oracle 网关。默认 false，不监听
export BASTION_ORACLE_ENABLE=true
export BASTION_ORACLE_LISTEN=:1521
# 监听地址是 0.0.0.0 / :: 且数据库会 REDIRECT 时必填
export BASTION_ORACLE_ADVERTISE=10.0.0.8
```

真机联调没有放进自动化测试。本地用伪造的 TNS 对端覆盖了：错误口令不拨号、未授权拒绝、描述符改写且口令不出现在上行字节、SQL 放行留痕、DROP 阻断、REDIRECT 不把真实库地址交还给客户端。
