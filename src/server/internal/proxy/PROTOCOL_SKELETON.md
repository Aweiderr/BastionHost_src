# 协议通道骨架说明（2026-10-21）

## 已接通

| 协议 | 配置项 | 默认监听 | 代码 | 状态 |
|------|--------|----------|------|------|
| SSH / WebSSH | `BASTION_SSH_LISTEN` | `:2222` | `server.go` / `webssh.go` | 完整运维闭环 |
| RDP | `BASTION_RDP_LISTEN` + `BASTION_RDP_ENABLE` | `:3389` | `rdp.go` | **骨架**：TCP 监听 + 提示，无协议解析/授权/录像 |
| VNC | `BASTION_VNC_LISTEN` + `BASTION_VNC_ENABLE` | `:5900` | `vnc.go` | **骨架**：同上，无 RFB |
| Oracle | `BASTION_ORACLE_LISTEN` + `BASTION_ORACLE_ENABLE` | `:1521` | `oracle.go` | **骨架**：同上，无 TNS/SQL 审计 |

资产台账 `ast_asset.protocol` 枚举已含 `rdp` / `vnc` / `oracle`（见迁移 0008）。

## 未完成边界（勿当作已交付）

1. **画面通道**：RDP/VNC 需完整握手、剪贴板/磁盘映射策略、录像帧封装。
2. **SQL 通道**：Oracle 需 TNS 代理、语句截获、高危 SQL 策略与 `ops_session_command` 留痕。
3. **授权与凭据**：骨架未调用 `OpsService`；启用开关默认关闭，避免误开端口。

## 启用方式

```bash
export BASTION_RDP_ENABLE=true
export BASTION_VNC_ENABLE=true
export BASTION_ORACLE_ENABLE=true
```
