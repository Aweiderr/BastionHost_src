# 堡垒机 Linux 部署指南

本指南基于**真机实测**编写，不是纸面推演。

## 目录

| 文件 | 用途 |
| --- | --- |
| `install.sh` | 一键部署脚本（装 PostgreSQL 容器、初始化库、装 systemd 服务） |
| `bastion-manager.service` | 管理服务 systemd 单元 |
| `bastion-server.service` | 服务端 systemd 单元 |
| `keepalived-ha.md` | **双机主备（Keepalived + VIP）部署方案**：前置条件、健康检查、切换演练、录像共享与脑裂防护 |

## 一、部署架构

单机最小部署（本次实测的形态）：

```
                    ┌──────────────────────────────────────┐
  浏览器 ──:8080──▶ │  manager  管理面                      │
                    │  认证 / 资产 / 策略 / 工单 / 审计 / Web │
  第三方 SSH 客户端  │                                      │
   ──────:2222────▶ │  server   数据面                      │
                    │  SSH 网关 / 命令管控 / 录像            │
                    │        │                              │
                    │        ▼                              │
                    │  PostgreSQL 16（仅监听 127.0.0.1）     │
                    └──────────────────────────────────────┘
```

**关键设计**：服务端不直连数据库。用户口令散列与资产凭据密钥只存在于管理面，
服务端经服务间接口（HMAC-SM3 签名）回源；代理节点因此可以放到 DMZ 横向扩展，
本地只持有**录像加密密钥**。

## 二、准备

### 2.1 构件

| 构件 | 来源 |
| --- | --- |
| `bin/manager`、`bin/server`、`bin/migrate` | `.\build.ps1 linux`（Windows）或 `make linux`（Linux/macOS） |
| 前端 `client/web/dist/` | `cd client/web && npm install && npm run build` |
| `record-verify`（可选） | `go build -o bin/record-verify ./cmd/record-verify` |

二进制是**纯静态**（`CGO_ENABLED=0`），不依赖 glibc 版本：

```
$ ldd ./bin/manager
        不是动态可执行文件
```

### 2.2 端口

| 端口 | 用途 | 建议暴露范围 |
| --- | --- | --- |
| 8080 | 管理控制台 + API | 运维网段 |
| 8081 | 服务端观测接口 + WebSSH + 录像回放 | 运维网段 |
| 2222 | SSH 网关（第三方客户端接入） | 运维网段 |
| 5432 | PostgreSQL | **仅 127.0.0.1** |

## 三、部署

```bash
# 1. 拷贝构件
scp -r dist-linux/* root@<服务器>:/opt/bastion/bin/
scp -r client/web/dist root@<服务器>:/opt/bastion/client/web/
scp -r deploy root@<服务器>:/opt/bastion/deploy/

# 2. 一键安装
cd /opt/bastion/deploy && chmod +x install.sh && ./install.sh
```

访问 `http://<服务器IP>:8080` 进入**初始化向导**（设备命名 → 超级管理员 → 三员 → 封存）。

## 四、真机实测记录（银河麒麟 V10）

### 4.1 测试环境

| 项 | 值 |
| --- | --- |
| 操作系统 | Kylin Linux Advanced Server V10 (Lance) |
| 内核 | 4.19.90-52.22.v2207.ky10.x86_64 |
| glibc | 2.28 |
| CPU / 内存 | 8 核 / 14 GB |
| Docker | 18.09.0 |
| 数据库 | PostgreSQL 16.15（alpine 容器） |

### 4.2 结果

| 验证项 | 结果 |
| --- | --- |
| 静态二进制运行 | ✅ `ldd` 报"不是动态可执行文件"，零依赖 |
| 数据库初始化 | ✅ 10 条迁移约 2 秒 |
| 管理服务启动 | ✅ `/healthz` → database ok / crypto ok |
| 服务端 + SSH 网关 | ✅ 策略 19 条已加载，录像密钥已生成 |
| Web 控制台 | ✅ 浏览器正常渲染，菜单按权限过滤 |
| 端到端（管理面） | ✅ `e2e-manager` 44/44 |
| 端到端（真实主机运维） | ✅ `e2e-real` 15/15 |
| 命令管控 | ✅ `rm -rf /` 与多空格变体均被阻断且**未下发目标** |
| 二次确认 | ✅ `reboot` 进入确认，取消后主机未被重启 |
| 审计留痕 | ✅ 会话 8 条命令、2 条阻断全部入库 |
| 录像 | ✅ 加密落盘、摘要链校验通过、回放返回明文 |

> 上表是**阶段 7 的历史快照**，数字为当时的规模（迁移 10 条、内置策略 19 条）。
> 当前状态：迁移 **26 条**、内置命令策略 **23 条**、业务表 **55 张**。
> 保留原值是为了如实记录"当时验证的到底是什么"，不做追改。

### 4.3 踩过的坑（重要）

**① 老版本 Docker 的 iptables 链缺失**

```
Error response from daemon: driver failed programming external connectivity
... iptables: No chain/target/match by that name.
```

Kylin V10 + Docker 18.09 上端口映射会失败（`DOCKER` 链被清空）。

**解法**：用 `--network=host` 绕开 iptables，再用 `-c listen_addresses=127.0.0.1`
限定只监听本机。既不改宿主防火墙，也不用重启 docker。`install.sh` 已采用该方案。

**② 内存紧张时的自我保护**

本次目标的宿主机可用内存一度只剩 153 MB。因此：

- 容器显式限制 `--memory=384m --memory-swap=384m`（不允许额外吃宿主 swap）
- systemd 单元设置 `MemoryMax=1G`
- PostgreSQL 参数压到 `shared_buffers=48MB / max_connections=20`

**③ 录像回放地址不能用配置里的 127.0.0.1**

录像文件在代理节点本地，回放地址由管理服务下发。若直接下发配置值
（默认 `http://127.0.0.1:8081`），浏览器拿到后根本连不上。
实现上已改为**按当前请求的 Host 推断主机名、端口取配置**，
于是从 `192.168.106.114:8080` 访问时会下发 `http://192.168.106.114:8081/...`。

**④ 跨域：管理端与录像服务不同端口，必须处理 CORS 预检**

管理服务在 `:8080`、录像回放与 WebSSH 在 `:8081`，浏览器把回放请求视为**跨域**。
由于请求带 `Authorization` 头，会先发 `OPTIONS` 预检；服务端若只注册了 GET，
预检返回 405，`fetch` 在**拿到响应之前**就被浏览器拒绝。

这个故障的表象极具误导性：前端只显示"录像不可回放"，
而**服务端日志里连一条请求记录都没有**（预检根本没通过），
很容易被误判成"录像文件损坏"或"解密失败"——而用 curl 直接测接口却是 200，
因为 curl 不走浏览器的跨域检查。

服务端已内置 CORS 中间件（回显 Origin + `Vary`，并短路预检）。
若用 Nginx 做同源反向代理（`/api` → 管理服务、`/records` → 服务端），
则不存在跨域，这一段可以省掉——**这也是更推荐的部署形态**。

## 五、运维

### 5.1 服务管理

```bash
systemctl status bastion-manager bastion-server
systemctl restart bastion-server
journalctl -u bastion-server -f
```

### 5.2 升级

> **必须先停 server、再停 manager；启动时相反。**
> `bastion-server.service` 里有 `Requires=bastion-manager.service`，
> 单独执行 `systemctl stop bastion-manager` 会**连带停掉 server**，
> 而随后的 `systemctl start bastion-manager` 不会把 server 带回来 ——
> 结果是 Web 控制台还在、SSH 网关（:2222）与 WebSSH 静默下线，
> 从界面上一时看不出来。2026-09-19 的一次实际部署踩到过这个坑。

```bash
# 1. 前端可热更新（管理服务按 mtime 感知 index.html）
#    先清空再解压：assets 是内容哈希命名，覆盖式解压会不断累积历史 chunk
rm -rf /opt/bastion/client/web/dist && mkdir -p /opt/bastion/client/web/dist
tar -xzf web-dist.tar.gz -C /opt/bastion/client/web/dist

# 2. 数据库迁移（有新增迁移文件时必须执行，放在重启之前）
cd /opt/bastion && set -a && . etc/manager.env && set +a
./bin/migrate up

# 3. 后端：成对停 → 替换 → 成对起
#    （不能覆盖正在运行的二进制，会 Text file busy；
#      本次部署用"上传为 *.new → 停服 → mv 覆盖"）
systemctl stop bastion-server bastion-manager
install -m 0755 manager server migrate /opt/bastion/bin/
systemctl start bastion-manager bastion-server

# 4. 确认
systemctl is-active bastion-manager bastion-server
curl -s http://127.0.0.1:8080/healthz          # 需数据库 ok
curl -s -o /dev/null -w '%{http_code}\n' \
  http://127.0.0.1:8080/api/v1/cluster/overview   # 未认证应为 401
```

### 5.3 数据库备份

```bash
docker exec bastion-pg pg_dump -U bastion bastion | gzip > bastion-$(date +%F).sql.gz
```

### 5.4 录像完整性巡检

```bash
find /opt/bastion/data/records -name '*.bsr' -newermt '-1 day' | while read -r f; do
  /opt/bastion/bin/record-verify -file "$f" \
    -keystore /opt/bastion/data/server-keys \
    -master /opt/bastion/data/server-master.key >/dev/null || echo "校验失败: $f"
done
```

摘要链能发现任意分片被删除、重排或篡改；一旦断裂，回放接口会拒绝返回明文。

## 六、安全加固建议

1. **主机指纹校验**：SSH 网关当前对目标主机使用 `InsecureIgnoreHostKey`（代码中已标注），
   生产应接入资产主机指纹库做 TOFU/强校验。
2. **内部接口网络隔离**：`/internal/v1/*` 仅有 HMAC-SM3 签名保护，
   应在网络层限制为代理节点网段可访问。
3. **前端来源限制**：WebSSH 的 WebSocket 目前放开 Origin 校验以适配开发服务器，
   生产应在反向代理层限定 `Origin`。
4. **密钥备份**：`/opt/bastion/etc/*.env`、`data/keys`、`data/server-keys`、
   `data/server-master.key` 必须单独备份；丢失后凭据与录像都无法解密。
