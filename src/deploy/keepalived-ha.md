# 堡垒机双机主备（Keepalived + VIP）部署方案

> 形态说明：这是**主备（Active-Standby）**，不是负载均衡。
> 同一时刻只有一台设备持有 VIP 并对外服务，另一台待命。
> 需要容量叠加（多活）请走"负载均衡 + 会话调度 + 共享录像"路线，
> 见 `TODO.md`「部署配置：双机负载 / 集群高可用」中的待办 1、3。
>
> 验收依据：需求 NFR-02-02「代理节点故障切换 ≤ 5 秒」；
> 规划文档表 5-4「代理节点：健康检查 3 秒一次，连续 3 次失败摘除」。
>
> 注意这两个数字不是一回事：按表 5-4 的"3 次判定"，**检测本身就要约 9 秒**。
> 本方案默认与代码里的心跳节奏（3 秒）对齐，切换约 10~12 秒；
> 若按 NFR-02-02 的 5 秒验收，只需把 keepalived 的 `interval` 改为 1 ——
> 时间构成见 6.1 节的表格。

本文以两台设备为例，按实际环境替换 IP：

| 角色 | 主机名 | 物理 IP | 说明 |
| --- | --- | --- | --- |
| 节点 A（初始主） | bastion-01 | `192.168.106.114` | 已部署 |
| 节点 B（初始备） | bastion-02 | `192.168.106.115` | 待部署 |
| VIP | — | `192.168.106.200` | 空闲地址，客户端只认它 |

---

## 一、前置条件（缺一不可）

1. **两台设备在同一二层网络**，能互相访问；VRRP 建议用**单播**模式（见下），
   因为多数交换机 / 防火墙默认丢弃 VRRP 组播（`224.0.0.18`）。
2. **两台都部署完整的堡垒机**（manager + server + migrate），见 `install.sh` 与 `README.md`。
3. **两台共用同一个 PostgreSQL**。会话记录、授权、集群成员都在这里，
   两台各连各的库会出现"这台看得到会话、那台看不到"。
   > 数据库自身的高可用（流复制 / 主备）不在本文范围，但它是这套方案的单点：
   > VIP 只解决应用层切换，数据库挂了 VIP 也救不了。
4. **密钥必须一致**：`BASTION_JWT_SECRET`、`BASTION_INTERNAL_SECRET`。
   不一致的表现是：切到备机后登录有效但服务端连不上管理服务（签名校验失败）。
5. **时间同步**（chrony / ntp）。VRRP 状态机与审计时间都依赖时钟。
6. **一台设备只能属于一个集群**：在「系统 → 集群管理」里把两台建为一个集群，
   并填入 VIP（见第五节）。

---

## 二、客户端入口

客户端只配置 VIP，不配置任何一台物理 IP：

```
Web 控制台   http://192.168.106.200:8080/bastion/
WebSSH/回放  192.168.106.200:8081   ← 前端按 window.location.hostname 自动指向 VIP，无需配置
SSH 网关     ssh -p 2222 ops01@192.168.106.200
```

> WebSSH 与录像回放的前端代码用的是 `window.location.hostname` + 固定端口 8081，
> 所以只要从 VIP 打开控制台，WebSocket 与回放请求就会自动落到 VIP，**不需要改前端**。

VIP 需要同时承载 **8080（管理服务）、8081（服务端）、2222（SSH 网关）** 三个端口 ——
Keepalived 管理的是地址，端口天然都跟着走，无需额外配置。

---

## 三、部署节点 B

按 `README.md` 的三、部署章节在第二台设备上执行 `install.sh`，然后核对：

```bash
# 1. 密钥与生产环境一致（两台逐字相同）
grep -E 'JWT_SECRET|INTERNAL_SECRET' /opt/bastion/etc/manager.env /opt/bastion/etc/server.env

# 2. 连的是同一个数据库
grep BASTION_DB_HOST /opt/bastion/etc/manager.env

# 3. 两台都能起来
systemctl is-active bastion-manager bastion-server
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8081/healthz
```

注意：**节点 B 上的 manager 不要暴露到外网**，`8080` 只需要 VIP 与内网可达。

---

## 四、安装与配置 Keepalived

### 4.1 安装

```bash
# Kylin V10 / openEuler / CentOS 通用
yum install -y keepalived curl
systemctl enable keepalived
```

### 4.2 健康检查脚本

`/etc/keepalived/check_bastion.sh`（两台相同，权限 `0755`）：

```bash
#!/bin/bash
# 两个组件都要检查：
#   manager 挂了 → 控制台不可用（页面打不开）
#   server  挂了 → 运维入口不可用（SSH 网关与 WebSSH）
# 任何一个不健康，就放弃 VIP，让对端顶上。
curl -fsS --max-time 2 http://127.0.0.1:8080/healthz >/dev/null || exit 1
curl -fsS --max-time 2 http://127.0.0.1:8081/healthz >/dev/null || exit 1
exit 0
```

```bash
chmod 0755 /etc/keepalived/check_bastion.sh
```

> 为什么用本地 `127.0.0.1` 而不是 VIP：健康检查要回答"**本机**还能不能干活"。
> 检查 VIP 会在 VIP 漂走之后必然失败，从而形成"两台都认为自己不该持有 VIP"的死锁。

### 4.3 状态变化日志（可选）

`/etc/keepalived/notify.sh`（`0755`）：

```bash
#!/bin/bash
echo "$(date '+%F %T') keepalived -> $1" >> /var/log/keepalived-bastion.log
```

> 界面上"谁是主节点"由管理服务**探测 VIP 所在位置**自动判定（见第五节），
> 不依赖这个脚本；它只用于事后查切换时间点。

### 4.4 节点 A 的配置

`/etc/keepalived/keepalived.conf`：

```conf
global_defs {
    router_id bastion-01
    enable_script_security
    script_user root
}

vrrp_script chk_bastion {
    script "/etc/keepalived/check_bastion.sh"
    interval 3      # 健康检查间隔：与服务端心跳（3 秒）同节奏；
                    # 要对齐 NFR-02-02 的 ≤5 秒切换，把这里改成 1
    timeout 2
    fall 3          # 连续 3 次失败才判定故障（对应"连续 3 次失败摘除"）
    rise 2
}

vrrp_instance VI_BASTION {
    state MASTER
    interface ens33              # ← 改成实际承载 VIP 的网卡名（ip -br addr 查看）
    virtual_router_id 51
    priority 150                 # 主 150 / 备 100
    advert_int 1
    nopreempt                    # 主机恢复后不抢回 VIP，避免二次中断

    authentication {
        auth_type PASS
        auth_pass BastionVrrp2025   # ← 改成自己的口令，两台一致
    }

    # 单播模式：不依赖交换机放行 VRRP 组播，跨网段也能用
    unicast_src_ip 192.168.106.114
    unicast_peer {
        192.168.106.115
    }

    virtual_ipaddress {
        192.168.106.200/24 dev ens33
    }

    track_script {
        chk_bastion
    }

    notify_master "/etc/keepalived/notify.sh MASTER"
    notify_backup "/etc/keepalived/notify.sh BACKUP"
    notify_fault  "/etc/keepalived/notify.sh FAULT"
}
```

### 4.5 节点 B 的配置

与 A 相同，只改四处：

```conf
    router_id bastion-02
    state BACKUP
    priority 100
    unicast_src_ip 192.168.106.115
    unicast_peer { 192.168.106.114 }
```

### 4.6 启动与验证

```bash
systemctl restart keepalived

# 主节点上应能看到 VIP
ip -br addr show ens33 | grep 192.168.106.200

# 备节点上应没有 VIP（有的话说明两边都认为自己是主 → 见第六节脑裂）
ip -br addr show ens33 | grep 192.168.106.200 || echo "备节点：无 VIP（正常）"

# 通过 VIP 访问
curl -s http://192.168.106.200:8080/healthz
curl -s http://192.168.106.200:8081/healthz
```

---

## 五、在管理界面上看主备

「系统 → 集群管理」→ 新建集群：

- **集群名称**：生产双机
- **设备 IP**：`192.168.106.114`、`192.168.106.115`（一行一台）
- **VIP 地址**：`192.168.106.200`

保存后立即探测一次，之后每 15 秒刷新。界面上会显示：

| 位置 | 显示内容 |
| --- | --- |
| 集群列表「主节点 / VIP」列 | `主 192.168.106.114`、`VIP 192.168.106.200 · 备 1 台` |
| 集群列表「最近切换」 | `最近主备切换 2 分钟前`（悬停可看详细说明） |
| 成员表「角色」列 | 主节点（持有 VIP）/ 备节点 / 未知（设备不可达） |
| 集群列表标红 | `无主`（VIP 无人持有）或 `脑裂`（多台同时持有） |
| 详情弹窗「主备事件」 | 时间、事件类型（首次选出 / 主备切换 / 主节点丢失 / 脑裂）、说明，最多 20 条 |

**关于主备事件记录**：只在**主节点归属发生变化**时写一条，持续无主或持续脑裂不会重复刷记录
（否则一次故障会刷出上千条同样的条目，把"什么时候开始、什么时候恢复"淹没掉）。
它是核对切换耗时、以及发现"VIP 来回抖动"的唯一依据 —— 当前状态本身看不出这些。

**判定原理**：VIP 在 Keepalived 里是 `ip addr add` 加到网卡上的，所以持有 VIP 的设备，
它的本机 IPv4 列表里就会出现这个地址。服务端的观测接口上报本机 IP 列表，
管理服务拿它与集群配置的 VIP 比对 —— **谁持有 VIP，谁就是主**。

这样做不依赖 Keepalived 回调、不需要额外 agent，也不要求管理服务能访问到 VIP
（管理服务可能在另一个网段，而 VIP 只对客户端所在网络有意义）。

---

## 六、切换演练（建议在业务低峰执行）

### 6.1 主动切换（模拟主机故障）

```bash
# 在节点 A（当前主）上停掉堡垒机，Keepalived 会在健康检查失败后放弃 VIP
systemctl stop bastion-server bastion-manager

# 观察：约 9 秒检测（3 秒 × 3 次）+ 1~3 秒漂移，合计 10~12 秒后 VIP 到 B
# 在节点 B 上确认
watch -n1 'ip -br addr show ens33 | grep 192.168.106.200'
```

记录两个**不同**的耗时，别混为一谈：

| 指标 | 含义 | 本方案取值 |
| --- | --- | --- |
| VIP 漂移完成时间 | `ip addr` 上 VIP 出现/消失的时刻 | ≈ 10 秒 |
| 新会话可用时间 | 从故障发生到能通过 VIP 建立新会话 | ≈ 10~12 秒 |

**这两段时间的构成（与代码里的参数对齐）**：

| 环节 | 取值 | 依据 |
| --- | --- | --- |
| Keepalived 健康检查间隔 | 3 秒 | `keepalived.conf` 的 `interval 3`，**与服务端心跳同节奏** |
| 连续失败次数 | 3 次 | `fall 3`，对应规划表 5-4「连续 3 次失败摘除」 |
| → 故障检测耗时 | ≈ 9 秒 | 3 × 3 |
| VRRP 通告与地址漂移 | 1~3 秒 | `advert_int 1` + 地址增删 |
| → **合计** | **≈ 10~12 秒** | |

> **与 NFR-02-02（≤ 5 秒）的关系**：默认取值 ≈10 秒，与代码里
> "心跳 3 秒 / 离线判定 10 秒"（`cluster.heartbeat.*`）**同一节奏**，
> 排查时两边的数字能对上，不会出现"文档说 5 秒、日志看着像 10 秒"的困惑。
>
> 若验收严格按 ≤5 秒执行，只改 **keepalived 的部署参数**即可：
> `interval 1` + `fall 3` → 检测 ≈3 秒 + 漂移 1~3 秒 ≈ **4~6 秒**，代码侧无需改动。
> 代码里的心跳与探测周期是**管理面观测**的口径（界面多久刷新一次状态），
> 不参与 VIP 的切换决策——切换完全由 Keepalived 自己判定。

### 6.2 恢复与回切

```bash
# 节点 A 恢复服务：nopreempt 下它不会抢回 VIP，仍是 B 为主
systemctl start bastion-manager bastion-server

# 需要回切时，在 B 上临时停 keepalived
systemctl stop keepalived       # B 释放 VIP → A 接管（A 的 priority 更高）
# 再把 B 的 keepalived 起回来
systemctl start keepalived
```

### 6.3 观察点

1. **存量会话会中断**：首版不做会话热迁移（需求 FR-04-07 的能力边界），
   切换时正在进行的运维会话断开，需要重新连接。录像已采集部分不丢。
2. **界面上的主节点应在 15 秒内跟着切换**（探测周期），不会出现"VIP 已经漂走、界面还写着旧主"。
3. **对照「主备事件」核对耗时**：切换完成后详情里应出现一条「主备切换」记录，
   它的时间戳就是检测到归属变化的时刻。与你在 6.1 里手工记录的故障时刻相减，
   即可得到实际切换耗时（写入验收记录）。
3. **验证码与网络配置的待确认状态**存在进程内存，多实例不共享：
   切换后图形验证码计数重置、网络配置的倒计时条丢失（配置已落盘）。
4. `journalctl -u keepalived -f` 与 `/var/log/keepalived-bastion.log` 能看到 `MASTER/BACKUP/FAULT` 流转。

---

## 七、录像：这套方案最容易踩的坑

**这是必须先想清楚的一件事。** 录像默认落在**设备本地磁盘**
（`BASTION_RECORD_DIR=/opt/bastion/data/records`），而回放接口也由服务端提供
（`http://<VIP>:8081/api/v1/records/<会话号>`）。于是：

```
会话在 A 上建立 → 录像写在 A 的本地盘
切换到 B 之后   → 客户端访问 VIP 落到 B → B 本地没有这个文件 → 回放 404
```

**三种处理方式**：

| 方案 | 做法 | 代价 |
| --- | --- | --- |
| **A. 共享目录（推荐）** | 用 NFS 挂载同一个录像目录到两台，`BASTION_RECORD_DIR` 指向它 | 需要一台 NFS 服务；NFS 本身成为单点（可用双机 NFS 或分布式存储） |
| **B. 对象存储驱动** | 录像改存对象存储（规划文档的形态：多副本 / 纠删码） | 当前只实现了 `local` 驱动，需要新开发（已记入 `TODO.md`） |
| **C. 接受限制** | 历史录像通过**产生它的那台设备**的固定 IP 回放 | 切换期间历史回放不可用；运维要知道去哪台上找，体验差 |

共享目录的最小做法（示例，按实际存储调整）：

```bash
# NFS 服务端（可以是第三台，也可以先放在节点 A）
yum install -y nfs-utils
echo "/srv/bastion-records 192.168.106.0/24(rw,sync,no_subtree_check,no_root_squash)" >> /etc/exports
systemctl enable --now nfs-server

# 两台节点上挂载
mkdir -p /srv/bastion-records
echo "nfs-server:/srv/bastion-records /opt/bastion/data/records nfs defaults,_netdev 0 0" >> /etc/fstab
mount -a
systemctl restart bastion-server
```

> 录像按会话分文件写入（`S<会话号>.bsr`），两台不会并发写同一个文件，
> 因此共享目录不存在写冲突；`_netdev` 保证开机时等网络就绪再挂载。

---

## 八、脑裂防护

VRRP 脑裂（两台同时持有 VIP）的后果：客户端可能连到任意一台，会话与录像分散，
而界面上只会显示"脑裂"两个字。

| 措施 | 说明 |
| --- | --- |
| `nopreempt` | 主机恢复后不抢回 VIP，避免来回抖动 |
| 单播 `unicast_peer` | 不依赖交换机放行 VRRP 组播，也避免跨网段误判 |
| 防火墙放行 VRRP | 若必须用组播模式：`firewall-cmd --add-protocol=vrrp --permanent` |
| `authentication` | 防止别的 VRRP 实例混入同一 `virtual_router_id` |
| 管理界面标红 | 集群列表在 `vip_held_count > 1` 时直接标「脑裂」，这是最后一道人工兜底 |

出现脑裂时按序处理：先确认哪台真正在服务（看 `active_sessions`），
停掉另一台的 keepalived，再排查为什么两边都认为自己该持有 VIP
（常见原因：健康检查脚本只检查了 VIP 而不是本机、或两台互相看不到对方的 VRRP 报文）。

---

## 九、验收清单

- [ ] 两台 `systemctl is-active bastion-manager bastion-server` 均为 active
- [ ] 主节点 `ip addr` 有 VIP，备节点没有
- [ ] `curl http://<VIP>:8080/healthz` 与 `http://<VIP>:8081/healthz` 返回 `"status":"ok"`
- [ ] 通过 VIP 能登录控制台，能打开 Web 终端（WebSocket 走 VIP:8081）
- [ ] 管理界面集群页显示正确的主节点 IP 与「备 N 台」
- [ ] 主动停主机 → VIP 漂移 → 通过 VIP 能建立新会话
      （分别记录"VIP 漂移完成"与"新会话可用"两个耗时，对照 6.1 的时间构成表）
- [ ] 主机恢复后不抢回 VIP（`nopreempt` 生效）
- [ ] 切换后审计里的历史录像能否回放（取决于第七节选了哪个方案）
- [ ] 脑裂演练：人为让两台都持有 VIP，界面应标红提示
