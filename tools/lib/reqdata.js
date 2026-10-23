'use strict'
// 【自动生成，请勿手工编辑】需求单一数据源
// 由 gen-reqdata.js 从 content-req-*.js 正文提取，供表 2-4、附录 A、追踪矩阵统一引用
module.exports = {
  "domains": [
    {
      "code": "FR-01",
      "name": "身份认证",
      "design": "6.2（含 6.2.5 联邦认证）",
      "plan": "3.1.2、8.2",
      "test": "功能测试 + 渗透测试",
      "accept": "功能验收",
      "total": 11,
      "p0": 7,
      "p1": 3,
      "p2": 1,
      "items": [
        {
          "id": "FR-01-01",
          "name": "静态口令认证",
          "priority": "P0"
        },
        {
          "id": "FR-01-02",
          "name": "OTP 动态令牌认证（系统内置）",
          "priority": "P0"
        },
        {
          "id": "FR-01-03",
          "name": "短信验证码认证（外部依赖）",
          "priority": "P1"
        },
        {
          "id": "FR-01-04",
          "name": "SM2 数字证书认证",
          "priority": "P0"
        },
        {
          "id": "FR-01-05",
          "name": "生物特征认证",
          "priority": "P2"
        },
        {
          "id": "FR-01-06",
          "name": "扫码确认登录",
          "priority": "P1"
        },
        {
          "id": "FR-01-07",
          "name": "本地身份源对接（LDAP / AD）",
          "priority": "P0"
        },
        {
          "id": "FR-01-08",
          "name": "多因素组合策略",
          "priority": "P0"
        },
        {
          "id": "FR-01-09",
          "name": "登录安全策略",
          "priority": "P0"
        },
        {
          "id": "FR-01-10",
          "name": "会话与令牌管理",
          "priority": "P0"
        },
        {
          "id": "FR-01-11",
          "name": "联邦认证（OIDC / SAML 2.0）",
          "priority": "P1"
        }
      ]
    },
    {
      "code": "FR-02",
      "name": "资产与账号管理",
      "design": "6.3",
      "plan": "3.1.3、12.1",
      "test": "功能测试 + 数据一致性",
      "accept": "功能验收",
      "total": 10,
      "p0": 6,
      "p1": 4,
      "p2": 0,
      "items": [
        {
          "id": "FR-02-01",
          "name": "资产台账管理",
          "priority": "P0"
        },
        {
          "id": "FR-02-02",
          "name": "资产分组与标签",
          "priority": "P0"
        },
        {
          "id": "FR-02-03",
          "name": "资产账号托管",
          "priority": "P0"
        },
        {
          "id": "FR-02-04",
          "name": "账号口令自动轮换",
          "priority": "P0"
        },
        {
          "id": "FR-02-05",
          "name": "资产自动发现",
          "priority": "P1"
        },
        {
          "id": "FR-02-06",
          "name": "资产连通性巡检",
          "priority": "P1"
        },
        {
          "id": "FR-02-07",
          "name": "资产责任人管理",
          "priority": "P0"
        },
        {
          "id": "FR-02-08",
          "name": "资产信息维护与同步",
          "priority": "P1"
        },
        {
          "id": "FR-02-09",
          "name": "资产分组授权模板",
          "priority": "P1"
        },
        {
          "id": "FR-02-10",
          "name": "资产删除与归档",
          "priority": "P0"
        }
      ]
    },
    {
      "code": "FR-03",
      "name": "访问控制",
      "design": "6.4",
      "plan": "8.2.2",
      "test": "功能测试 + 越权测试",
      "accept": "功能验收",
      "total": 10,
      "p0": 7,
      "p1": 3,
      "p2": 0,
      "items": [
        {
          "id": "FR-03-01",
          "name": "RBAC 角色授权",
          "priority": "P0"
        },
        {
          "id": "FR-03-02",
          "name": "ABAC 属性策略",
          "priority": "P0"
        },
        {
          "id": "FR-03-03",
          "name": "默认拒绝",
          "priority": "P0"
        },
        {
          "id": "FR-03-04",
          "name": "临时授权",
          "priority": "P0"
        },
        {
          "id": "FR-03-05",
          "name": "工单驱动授权",
          "priority": "P0"
        },
        {
          "id": "FR-03-06",
          "name": "权限复核",
          "priority": "P0"
        },
        {
          "id": "FR-03-07",
          "name": "权限冲突检测",
          "priority": "P1"
        },
        {
          "id": "FR-03-08",
          "name": "账号映射策略",
          "priority": "P0"
        },
        {
          "id": "FR-03-09",
          "name": "操作类型级授权",
          "priority": "P1"
        },
        {
          "id": "FR-03-10",
          "name": "授权变更审批",
          "priority": "P1"
        }
      ]
    },
    {
      "code": "FR-04",
      "name": "运维会话（含 SSH / SFTP）",
      "design": "6.5、6.6",
      "plan": "12.2、12.3",
      "test": "协议矩阵测试 + 压力测试",
      "accept": "功能与性能验收",
      "total": 22,
      "p0": 17,
      "p1": 5,
      "p2": 0,
      "items": [
        {
          "id": "FR-04-01",
          "name": "SSH / Telnet 字符会话",
          "priority": "P0"
        },
        {
          "id": "FR-04-02",
          "name": "图形桌面会话（RDP / VNC）",
          "priority": "P0"
        },
        {
          "id": "FR-04-03",
          "name": "SFTP / FTP 文件传输",
          "priority": "P0"
        },
        {
          "id": "FR-04-04",
          "name": "数据库运维会话",
          "priority": "P0"
        },
        {
          "id": "FR-04-05",
          "name": "Web 应用与云平台运维",
          "priority": "P1"
        },
        {
          "id": "FR-04-06",
          "name": "批量运维与脚本",
          "priority": "P1"
        },
        {
          "id": "FR-04-07",
          "name": "会话共享与旁观",
          "priority": "P1"
        },
        {
          "id": "FR-04-08",
          "name": "命令策略与实时阻断",
          "priority": "P0"
        },
        {
          "id": "FR-04-09",
          "name": "会话创建与票据校验",
          "priority": "P0"
        },
        {
          "id": "FR-04-10",
          "name": "操作体验优化",
          "priority": "P0"
        },
        {
          "id": "FR-04-11",
          "name": "剪贴板与本地盘管控",
          "priority": "P0"
        },
        {
          "id": "FR-04-12",
          "name": "会话异常处理",
          "priority": "P0"
        },
        {
          "id": "FR-04-13",
          "name": "SSH 密钥托管与代签（两类能力）",
          "priority": "P0"
        },
        {
          "id": "FR-04-14",
          "name": "SSH 端口转发与隧道管控",
          "priority": "P0"
        },
        {
          "id": "FR-04-15",
          "name": "后门密钥检测",
          "priority": "P1"
        },
        {
          "id": "FR-04-16",
          "name": "提权操作审计",
          "priority": "P0"
        },
        {
          "id": "FR-04-17",
          "name": "跳板转发链路记录",
          "priority": "P1"
        },
        {
          "id": "FR-04-18",
          "name": "命令策略配置",
          "priority": "P0"
        },
        {
          "id": "FR-04-19",
          "name": "命令组合处理与能力边界",
          "priority": "P0"
        },
        {
          "id": "FR-04-20",
          "name": "命令二次确认",
          "priority": "P0"
        },
        {
          "id": "FR-04-21",
          "name": "按键级记录与回放",
          "priority": "P0"
        },
        {
          "id": "FR-04-22",
          "name": "SSH / SFTP 审计记录",
          "priority": "P0"
        }
      ]
    },
    {
      "code": "FR-05",
      "name": "审计与录像",
      "design": "6.8",
      "plan": "8.4",
      "test": "功能测试 + 篡改检测",
      "accept": "功能验收",
      "total": 12,
      "p0": 10,
      "p1": 2,
      "p2": 0,
      "items": [
        {
          "id": "FR-05-01",
          "name": "字符会话完整记录",
          "priority": "P0"
        },
        {
          "id": "FR-05-02",
          "name": "图形会话录屏",
          "priority": "P0"
        },
        {
          "id": "FR-05-03",
          "name": "SQL 语句级审计",
          "priority": "P0"
        },
        {
          "id": "FR-05-04",
          "name": "审计检索",
          "priority": "P0"
        },
        {
          "id": "FR-05-05",
          "name": "回放与关键帧定位",
          "priority": "P0"
        },
        {
          "id": "FR-05-06",
          "name": "录像完整性校验",
          "priority": "P0"
        },
        {
          "id": "FR-05-07",
          "name": "录像与日志加密存储",
          "priority": "P0"
        },
        {
          "id": "FR-05-08",
          "name": "取证包导出",
          "priority": "P0"
        },
        {
          "id": "FR-05-09",
          "name": "审计数据权限分离",
          "priority": "P0"
        },
        {
          "id": "FR-05-10",
          "name": "日志留存策略",
          "priority": "P0"
        },
        {
          "id": "FR-05-11",
          "name": "敏感信息脱敏",
          "priority": "P1"
        },
        {
          "id": "FR-05-12",
          "name": "审计数据外发",
          "priority": "P1"
        }
      ]
    },
    {
      "code": "FR-06",
      "name": "告警与合规报表",
      "design": "6.9",
      "plan": "10.3、14.1",
      "test": "功能测试 + 数据核对",
      "accept": "功能验收",
      "total": 10,
      "p0": 6,
      "p1": 4,
      "p2": 0,
      "items": [
        {
          "id": "FR-06-01",
          "name": "告警规则配置",
          "priority": "P0"
        },
        {
          "id": "FR-06-02",
          "name": "告警通知渠道",
          "priority": "P0"
        },
        {
          "id": "FR-06-03",
          "name": "告警处置闭环",
          "priority": "P0"
        },
        {
          "id": "FR-06-04",
          "name": "操作统计报表",
          "priority": "P0"
        },
        {
          "id": "FR-06-05",
          "name": "合规报表模板",
          "priority": "P0"
        },
        {
          "id": "FR-06-06",
          "name": "行为画像",
          "priority": "P1"
        },
        {
          "id": "FR-06-07",
          "name": "报表订阅与推送",
          "priority": "P1"
        },
        {
          "id": "FR-06-08",
          "name": "告警与报表权限",
          "priority": "P0"
        },
        {
          "id": "FR-06-09",
          "name": "大屏可视化",
          "priority": "P1"
        },
        {
          "id": "FR-06-10",
          "name": "合规差距提示",
          "priority": "P1"
        }
      ]
    },
    {
      "code": "FR-07",
      "name": "系统管理（含设备运维）",
      "design": "6.12、6.13",
      "plan": "3.1.3、10.5、12.1",
      "test": "功能测试 + 权限测试 + 设备运维演练",
      "accept": "功能验收",
      "total": 19,
      "p0": 14,
      "p1": 5,
      "p2": 0,
      "items": [
        {
          "id": "FR-07-01",
          "name": "组织与用户管理",
          "priority": "P0"
        },
        {
          "id": "FR-07-02",
          "name": "角色与权限管理",
          "priority": "P0"
        },
        {
          "id": "FR-07-03",
          "name": "系统参数配置",
          "priority": "P0"
        },
        {
          "id": "FR-07-04",
          "name": "许可与授权管理",
          "priority": "P0"
        },
        {
          "id": "FR-07-05",
          "name": "消息与公告",
          "priority": "P1"
        },
        {
          "id": "FR-07-06",
          "name": "系统操作日志",
          "priority": "P0"
        },
        {
          "id": "FR-07-07",
          "name": "数据字典与枚举",
          "priority": "P1"
        },
        {
          "id": "FR-07-08",
          "name": "界面与个性化配置",
          "priority": "P1"
        },
        {
          "id": "FR-07-09",
          "name": "系统备份与还原",
          "priority": "P0"
        },
        {
          "id": "FR-07-10",
          "name": "系统健康检查",
          "priority": "P0"
        },
        {
          "id": "FR-07-11",
          "name": "设备网络与时间配置",
          "priority": "P0"
        },
        {
          "id": "FR-07-12",
          "name": "恢复出厂设置",
          "priority": "P0"
        },
        {
          "id": "FR-07-13",
          "name": "SNMPv3 监控与 Trap",
          "priority": "P1"
        },
        {
          "id": "FR-07-14",
          "name": "日志存储与日志服务器管理",
          "priority": "P0"
        },
        {
          "id": "FR-07-15",
          "name": "网络高级配置",
          "priority": "P1"
        },
        {
          "id": "FR-07-16",
          "name": "本机资源监控与展示",
          "priority": "P0"
        },
        {
          "id": "FR-07-17",
          "name": "设备系统自检",
          "priority": "P0"
        },
        {
          "id": "FR-07-18",
          "name": "备份策略与任务管理",
          "priority": "P0"
        },
        {
          "id": "FR-07-19",
          "name": "时间与时间源管理",
          "priority": "P0"
        }
      ]
    },
    {
      "code": "FR-08",
      "name": "多租户与集成",
      "design": "6.6.5、6.12.1、7.6",
      "plan": "3.1.3、12.1",
      "test": "隔离测试 + 接口联调",
      "accept": "功能与集成验收",
      "total": 13,
      "p0": 6,
      "p1": 7,
      "p2": 0,
      "items": [
        {
          "id": "FR-08-01",
          "name": "租户隔离",
          "priority": "P0"
        },
        {
          "id": "FR-08-02",
          "name": "租户配额管理",
          "priority": "P0"
        },
        {
          "id": "FR-08-03",
          "name": "租户自助管理",
          "priority": "P1"
        },
        {
          "id": "FR-08-04",
          "name": "开放 API",
          "priority": "P0"
        },
        {
          "id": "FR-08-05",
          "name": "Webhook 与事件订阅",
          "priority": "P1"
        },
        {
          "id": "FR-08-06",
          "name": "身份源集成",
          "priority": "P0"
        },
        {
          "id": "FR-08-07",
          "name": "审计数据外发集成",
          "priority": "P1"
        },
        {
          "id": "FR-08-08",
          "name": "单点登出与会话联动",
          "priority": "P1"
        },
        {
          "id": "FR-08-09",
          "name": "第三方客户端接入",
          "priority": "P1"
        },
        {
          "id": "FR-08-10",
          "name": "数据批量导入导出",
          "priority": "P0"
        },
        {
          "id": "FR-08-11",
          "name": "租户资源访问免审批策略",
          "priority": "P1"
        },
        {
          "id": "FR-08-13",
          "name": "运维接入准入模式",
          "priority": "P1"
        },
        {
          "id": "FR-08-12",
          "name": "API 接入的操作人归属与审计",
          "priority": "P0"
        }
      ]
    },
    {
      "code": "FR-09",
      "name": "国密与安全",
      "design": "6.10、6.10.4、6.13、8.2",
      "plan": "规划文档第 7 章",
      "test": "算法验证 + 密评 + 安全测试",
      "accept": "合规验收",
      "total": 14,
      "p0": 13,
      "p1": 1,
      "p2": 0,
      "items": [
        {
          "id": "FR-09-01",
          "name": "国密算法支持",
          "priority": "P0"
        },
        {
          "id": "FR-09-02",
          "name": "国密 TLS 安全通道",
          "priority": "P0"
        },
        {
          "id": "FR-09-03",
          "name": "SM2 数字证书体系",
          "priority": "P0"
        },
        {
          "id": "FR-09-04",
          "name": "密钥全生命周期管理",
          "priority": "P0"
        },
        {
          "id": "FR-09-05",
          "name": "密码机 / 密码卡对接",
          "priority": "P0"
        },
        {
          "id": "FR-09-06",
          "name": "敏感数据加密存储",
          "priority": "P0"
        },
        {
          "id": "FR-09-07",
          "name": "完整性保护与防篡改",
          "priority": "P0"
        },
        {
          "id": "FR-09-08",
          "name": "操作行为可追溯",
          "priority": "P0"
        },
        {
          "id": "FR-09-09",
          "name": "密码算法合规声明",
          "priority": "P0"
        },
        {
          "id": "FR-09-10",
          "name": "安全审计留痕",
          "priority": "P0"
        },
        {
          "id": "FR-09-11",
          "name": "弱算法与不安全配置检测",
          "priority": "P0"
        },
        {
          "id": "FR-09-12",
          "name": "密码资源与性能监控",
          "priority": "P1"
        },
        {
          "id": "FR-09-13",
          "name": "密钥备份与还原",
          "priority": "P0"
        },
        {
          "id": "FR-09-14",
          "name": "证书链管理",
          "priority": "P0"
        }
      ]
    }
  ],
  "irDomain": {
    "code": "IR",
    "name": "系统初始化与管理员体系",
    "design": "6.1",
    "plan": "规划文档第 4 章",
    "test": "初始化演练 + 权限分立验证",
    "accept": "交付验收",
    "total": 30,
    "p0": 26,
    "p1": 4,
    "p2": 0,
    "items": [
      {
        "id": "IR-01",
        "name": "初始化入口与本地可信访问",
        "priority": "P0"
      },
      {
        "id": "IR-02",
        "name": "出厂初始化凭证",
        "priority": "P0"
      },
      {
        "id": "IR-03",
        "name": "设备完整性校验",
        "priority": "P0"
      },
      {
        "id": "IR-04",
        "name": "基础网络与时间配置",
        "priority": "P0"
      },
      {
        "id": "IR-05",
        "name": "初始化可重入",
        "priority": "P0"
      },
      {
        "id": "IR-06",
        "name": "设备密钥对生成",
        "priority": "P0"
      },
      {
        "id": "IR-07",
        "name": "设备证书申请与导入",
        "priority": "P0"
      },
      {
        "id": "IR-08",
        "name": "证书用途与有效期校验",
        "priority": "P0"
      },
      {
        "id": "IR-09",
        "name": "国密 TLS 服务启用",
        "priority": "P0"
      },
      {
        "id": "IR-10",
        "name": "证书更新与轮换",
        "priority": "P1"
      },
      {
        "id": "IR-11",
        "name": "密码卡检测与自检（双模式）",
        "priority": "P0"
      },
      {
        "id": "IR-12",
        "name": "密码卡管理口令设置",
        "priority": "P0"
      },
      {
        "id": "IR-13",
        "name": "主密钥生成与备份",
        "priority": "P0"
      },
      {
        "id": "IR-14",
        "name": "密钥库初始化",
        "priority": "P0"
      },
      {
        "id": "IR-15",
        "name": "密码卡双机配对",
        "priority": "P1"
      },
      {
        "id": "IR-16",
        "name": "超级管理员账号创建",
        "priority": "P0"
      },
      {
        "id": "IR-17",
        "name": "超管双因素认证",
        "priority": "P0"
      },
      {
        "id": "IR-18",
        "name": "首次登录强制改密",
        "priority": "P0"
      },
      {
        "id": "IR-19",
        "name": "应急恢复凭证",
        "priority": "P1"
      },
      {
        "id": "IR-20",
        "name": "初始化确认与报告",
        "priority": "P0"
      },
      {
        "id": "IR-21",
        "name": "超级管理员创建三员",
        "priority": "P0"
      },
      {
        "id": "IR-22",
        "name": "三权分立约束",
        "priority": "P0"
      },
      {
        "id": "IR-23",
        "name": "超管账号封存",
        "priority": "P0"
      },
      {
        "id": "IR-24",
        "name": "管理功能整合到三员",
        "priority": "P0"
      },
      {
        "id": "IR-25",
        "name": "租户管理员创建",
        "priority": "P1"
      },
      {
        "id": "IR-26",
        "name": "业务角色创建与授权",
        "priority": "P0"
      },
      {
        "id": "IR-27",
        "name": "本地可信环境约束",
        "priority": "P0"
      },
      {
        "id": "IR-28",
        "name": "初始化过程留痕",
        "priority": "P0"
      },
      {
        "id": "IR-29",
        "name": "未初始化状态封闭",
        "priority": "P0"
      },
      {
        "id": "IR-30",
        "name": "恢复出厂与密钥销毁",
        "priority": "P0"
      }
    ]
  },
  "nfrDomains": [
    {
      "code": "NFR-01",
      "name": "性能需求",
      "design": "10.1",
      "total": 12,
      "items": [
        {
          "id": "NFR-01-01",
          "name": "并发会话能力"
        },
        {
          "id": "NFR-01-02",
          "name": "会话建立时延"
        },
        {
          "id": "NFR-01-03",
          "name": "字符会话转发时延"
        },
        {
          "id": "NFR-01-04",
          "name": "图形会话时延"
        },
        {
          "id": "NFR-01-05",
          "name": "命令审计与控制时延"
        },
        {
          "id": "NFR-01-11",
          "name": "SFTP 传输性能"
        },
        {
          "id": "NFR-01-12",
          "name": "命令记录完整性"
        },
        {
          "id": "NFR-01-06",
          "name": "页面响应时间"
        },
        {
          "id": "NFR-01-07",
          "name": "审计检索性能"
        },
        {
          "id": "NFR-01-08",
          "name": "登录处理能力"
        },
        {
          "id": "NFR-01-09",
          "name": "录像回放性能"
        },
        {
          "id": "NFR-01-10",
          "name": "资源占用"
        }
      ]
    },
    {
      "code": "NFR-02",
      "name": "可靠性需求",
      "design": "10.2",
      "total": 7,
      "items": [
        {
          "id": "NFR-02-01",
          "name": "系统可用性"
        },
        {
          "id": "NFR-02-02",
          "name": "组件故障切换"
        },
        {
          "id": "NFR-02-03",
          "name": "数据可靠性"
        },
        {
          "id": "NFR-02-04",
          "name": "长会话稳定性"
        },
        {
          "id": "NFR-02-05",
          "name": "异常自恢复"
        },
        {
          "id": "NFR-02-06",
          "name": "降级能力"
        },
        {
          "id": "NFR-02-07",
          "name": "数据一致性"
        }
      ]
    },
    {
      "code": "NFR-03",
      "name": "安全需求",
      "design": "10.3",
      "total": 11,
      "items": [
        {
          "id": "NFR-03-01",
          "name": "传输加密"
        },
        {
          "id": "NFR-03-02",
          "name": "存储加密"
        },
        {
          "id": "NFR-03-03",
          "name": "身份鉴别强度"
        },
        {
          "id": "NFR-03-04",
          "name": "权限最小化"
        },
        {
          "id": "NFR-03-05",
          "name": "防越权"
        },
        {
          "id": "NFR-03-06",
          "name": "防注入与常见 Web 漏洞"
        },
        {
          "id": "NFR-03-07",
          "name": "会话安全"
        },
        {
          "id": "NFR-03-08",
          "name": "审计不可篡改"
        },
        {
          "id": "NFR-03-09",
          "name": "漏洞管理"
        },
        {
          "id": "NFR-03-10",
          "name": "供应链安全"
        },
        {
          "id": "NFR-03-11",
          "name": "安全配置基线"
        }
      ]
    },
    {
      "code": "NFR-04",
      "name": "易用性需求",
      "design": "3、9.5",
      "total": 7,
      "items": [
        {
          "id": "NFR-04-01",
          "name": "操作效率"
        },
        {
          "id": "NFR-04-02",
          "name": "界面一致性"
        },
        {
          "id": "NFR-04-03",
          "name": "提示可读性"
        },
        {
          "id": "NFR-04-04",
          "name": "引导与帮助"
        },
        {
          "id": "NFR-04-05",
          "name": "中文支持"
        },
        {
          "id": "NFR-04-06",
          "name": "响应式与适配"
        },
        {
          "id": "NFR-04-07",
          "name": "用户满意度"
        }
      ]
    },
    {
      "code": "NFR-05",
      "name": "兼容性需求",
      "design": "10.5",
      "total": 9,
      "items": [
        {
          "id": "NFR-05-01",
          "name": "服务端操作系统兼容"
        },
        {
          "id": "NFR-05-02",
          "name": "芯片架构兼容"
        },
        {
          "id": "NFR-05-03",
          "name": "浏览器兼容"
        },
        {
          "id": "NFR-05-04",
          "name": "被管资产兼容"
        },
        {
          "id": "NFR-05-05",
          "name": "数据库兼容"
        },
        {
          "id": "NFR-05-06",
          "name": "虚拟化与云平台"
        },
        {
          "id": "NFR-05-07",
          "name": "身份源兼容"
        },
        {
          "id": "NFR-05-08",
          "name": "密码设备兼容"
        },
        {
          "id": "NFR-05-09",
          "name": "既有工具习惯兼容"
        }
      ]
    },
    {
      "code": "NFR-06",
      "name": "可维护性需求",
      "design": "10.4",
      "total": 7,
      "items": [
        {
          "id": "NFR-06-01",
          "name": "在线升级"
        },
        {
          "id": "NFR-06-02",
          "name": "配置版本管理"
        },
        {
          "id": "NFR-06-03",
          "name": "日志完备性"
        },
        {
          "id": "NFR-06-04",
          "name": "诊断能力"
        },
        {
          "id": "NFR-06-05",
          "name": "监控指标暴露"
        },
        {
          "id": "NFR-06-06",
          "name": "数据迁移工具"
        },
        {
          "id": "NFR-06-07",
          "name": "部署自动化"
        }
      ]
    },
    {
      "code": "NFR-07",
      "name": "可扩展性需求",
      "design": "8.5、10.1",
      "total": 6,
      "items": [
        {
          "id": "NFR-07-01",
          "name": "横向扩展"
        },
        {
          "id": "NFR-07-02",
          "name": "存储扩展"
        },
        {
          "id": "NFR-07-03",
          "name": "租户规模"
        },
        {
          "id": "NFR-07-04",
          "name": "策略规模"
        },
        {
          "id": "NFR-07-05",
          "name": "资产规模"
        },
        {
          "id": "NFR-07-06",
          "name": "接口扩展"
        }
      ]
    },
    {
      "code": "NFR-08",
      "name": "合规性需求",
      "design": "数据库设计文档第 7 章",
      "total": 7,
      "items": [
        {
          "id": "NFR-08-01",
          "name": "等保 2.0 三级符合"
        },
        {
          "id": "NFR-08-02",
          "name": "密码应用合规材料"
        },
        {
          "id": "NFR-08-03",
          "name": "日志留存"
        },
        {
          "id": "NFR-08-04",
          "name": "个人信息保护"
        },
        {
          "id": "NFR-08-05",
          "name": "审计独立性"
        },
        {
          "id": "NFR-08-06",
          "name": "密码产品合规"
        },
        {
          "id": "NFR-08-07",
          "name": "合规证据输出"
        }
      ]
    }
  ],
  "totals": {
    "fr": 121,
    "ir": 30,
    "nfr": 66,
    "frP0": 86,
    "frP1": 34,
    "frP2": 1,
    "irP0": 26,
    "irP1": 4
  }
}
