-- ============================================================================
-- 0008 基础数据
--
-- 初始化基础数据：系统租户、内置角色与权限、数据字典、系统配置。
-- 全部使用 ON CONFLICT DO NOTHING，保证可重复执行。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 系统租户（tenant_id = 0 表示系统级数据）
-- ---------------------------------------------------------------------------
INSERT INTO sys_tenant (tenant_id, tenant_code, tenant_name, status, remark)
VALUES (0, 'system', '系统', 'active', '系统级数据所属租户，非真实客户')
ON CONFLICT (tenant_code) DO NOTHING;

-- 序列需跃过已手工插入的 0 号租户，避免后续冲突
SELECT setval('sys_tenant_tenant_id_seq', GREATEST((SELECT COALESCE(MAX(tenant_id), 1) FROM sys_tenant), 1));

-- ---------------------------------------------------------------------------
-- 内置角色：超级管理员 + 三员 + 运维人员 + 审批人
-- 对应规划文档"超级管理员 + 三员"四员模型
-- ---------------------------------------------------------------------------
INSERT INTO sys_role (tenant_id, role_code, role_name, role_type, data_scope, is_builtin, description) VALUES
 (0, 'super_admin', '超级管理员', 'builtin', 'all', true, '仅用于设备初始化与紧急恢复，初始化完成后应封存'),
 (0, 'sys_admin',   '系统管理员', 'builtin', 'all', true, '负责用户、组织、资产、配置与设备自身运维，不参与授权与审计'),
 (0, 'sec_admin',   '安全管理员', 'builtin', 'all', true, '负责授权、策略、密钥与审批，不参与用户管理与审计'),
 (0, 'audit_admin', '审计管理员', 'builtin', 'all', true, '负责审计查询、录像回放与报表，不参与管理与授权'),
 (0, 'operator',    '运维人员',   'builtin', 'self', true, '发起并执行运维会话，仅可见被授权的资产'),
 (0, 'approver',    '审批人',     'builtin', 'org',  true, '审批运维工单与临时授权申请')
ON CONFLICT (tenant_id, role_code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 权限点（模块化编码：<模块>:<资源>:<动作>）
-- ---------------------------------------------------------------------------
INSERT INTO sys_permission (perm_code, perm_name, perm_type, module, sort_no) VALUES
 -- 系统管理
 ('system:user:list',      '查看用户',     'api', 'system', 10),
 ('system:user:create',    '创建用户',     'api', 'system', 11),
 ('system:user:update',    '修改用户',     'api', 'system', 12),
 ('system:user:delete',    '删除用户',     'api', 'system', 13),
 ('system:role:list',      '查看角色',     'api', 'system', 20),
 ('system:role:manage',    '管理角色权限', 'api', 'system', 21),
 ('system:org:list',       '查看组织',     'api', 'system', 30),
 ('system:org:manage',     '管理组织',     'api', 'system', 31),
 ('system:config:read',    '查看系统配置', 'api', 'system', 40),
 ('system:config:write',   '修改系统配置', 'api', 'system', 41),
 ('system:license:manage', '许可管理',     'api', 'system', 50),
 ('system:idp:manage',     '身份源管理',   'api', 'system', 60),
 ('system:accessmode:write','准入模式配置','api', 'system', 70),
 -- 资产
 ('asset:asset:list',      '查看资产',     'api', 'asset', 110),
 ('asset:asset:create',    '创建资产',     'api', 'asset', 111),
 ('asset:asset:update',    '修改资产',     'api', 'asset', 112),
 ('asset:asset:delete',    '删除资产',     'api', 'asset', 113),
 ('asset:asset:import',    '批量导入资产', 'api', 'asset', 114),
 ('asset:account:list',    '查看账号',     'api', 'asset', 120),
 ('asset:account:manage',  '管理账号',     'api', 'asset', 121),
 ('asset:account:viewpwd','查看账号口令', 'api', 'asset', 122),
 -- 授权
 ('acl:grant:list',        '查看授权',     'api', 'acl', 210),
 ('acl:grant:create',      '创建授权',     'api', 'acl', 211),
 ('acl:grant:revoke',      '回收授权',     'api', 'acl', 212),
 ('acl:rule:manage',       '管理授权规则', 'api', 'acl', 220),
 -- 运维
 ('ops:session:connect',   '发起运维会话', 'api', 'ops', 310),
 ('ops:session:list',      '查看自己会话', 'api', 'ops', 311),
 ('ops:session:kill',      '强制断开会话', 'api', 'ops', 312),
 ('ops:session:share',     '会话共享与旁观','api','ops', 313),
 ('ops:file:transfer',     '文件传输',     'api', 'ops', 320),
 -- 审计
 ('audit:session:list',    '查看全部会话', 'api', 'audit', 410),
 ('audit:record:play',     '录像回放',     'api', 'audit', 411),
 ('audit:command:list',    '命令审计',     'api', 'audit', 412),
 ('audit:log:list',        '日志查询',     'api', 'audit', 413),
 ('audit:export:evidence', '导出取证包',   'api', 'audit', 414),
 ('audit:report:view',     '查看合规报表', 'api', 'audit', 420),
 -- 策略
 ('policy:command:manage', '命令策略管理', 'api', 'policy', 510),
 ('policy:time:manage',    '时间与来源策略','api','policy', 511),
 ('policy:approval:manage','审批策略管理', 'api', 'policy', 512),
 -- 工单
 ('ticket:apply',          '提交工单',     'api', 'ticket', 610),
 ('ticket:approve',        '审批工单',     'api', 'ticket', 611),
 ('ticket:list:own',       '查看自己工单', 'api', 'ticket', 612),
 ('ticket:list:all',       '查看全部工单', 'api', 'ticket', 613),
 ('ticket:emergency',      '紧急放行',     'api', 'ticket', 614),
 -- 密钥与证书
 ('key:key:list',          '查看密钥',     'api', 'key', 710),
 ('key:key:generate',      '生成密钥',     'api', 'key', 711),
 ('key:key:destroy',       '销毁密钥',     'api', 'key', 712),
 ('key:key:backup',        '密钥备份与还原','api','key', 713),
 ('key:cert:manage',       '证书链管理',   'api', 'key', 720),
 -- 设备运维
 ('device:network:manage', '网络配置',     'api', 'device', 810),
 ('device:time:manage',    '时间与时间源', 'api', 'device', 811),
 ('device:resource:view',  '本机资源查看', 'api', 'device', 812),
 ('device:selfcheck:run',  '执行系统自检', 'api', 'device', 813),
 ('device:backup:manage',  '备份与还原',   'api', 'device', 814),
 ('device:log:manage',     '日志与日志服务器','api','device', 815),
 ('device:snmp:manage',    'SNMP 配置',    'api', 'device', 816),
 ('device:factory:reset',  '恢复出厂设置', 'api', 'device', 817),
 -- 租户
 ('tenant:tenant:list',    '查看租户',     'api', 'tenant', 910),
 ('tenant:tenant:manage',  '管理租户',     'api', 'tenant', 911),
 ('tenant:policy:manage',  '租户访问策略', 'api', 'tenant', 912)
ON CONFLICT (perm_code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 数据字典
-- ---------------------------------------------------------------------------
INSERT INTO sys_dict (dict_code, dict_name, is_system) VALUES
 ('asset_type',      '资产类型',   true),
 ('asset_protocol',  '资产协议',   true),
 ('asset_level',     '资产重要级别', true),
 ('os_type',         '操作系统类型', true),
 ('session_status',  '会话状态',   true),
 ('alert_severity',  '告警级别',   true),
 ('ticket_status',   '工单状态',   true),
 ('account_type',    '账号认证方式', true),
 ('grant_source',    '授权来源',   true),
 ('client_type',     '客户端类型', true),
 ('risk_level',      '命令风险等级', true),
 ('policy_action',   '策略动作',   true),
 ('access_mode',     '准入模式',   true)
ON CONFLICT (dict_code) DO NOTHING;

INSERT INTO sys_dict_item (dict_id, item_value, item_label, sort_no)
SELECT d.dict_id, v.item_value, v.item_label, v.sort_no
FROM sys_dict d
JOIN (VALUES
 ('asset_type',     'host',     '主机',       1),
 ('asset_type',     'network',  '网络设备',   2),
 ('asset_type',     'database', '数据库',     3),
 ('asset_type',     'web',      'Web 应用',   4),
 ('asset_type',     'custom',   '自定义',     9),
 ('asset_protocol', 'ssh',      'SSH',        1),
 ('asset_protocol', 'rdp',      'RDP',        2),
 ('asset_protocol', 'vnc',      'VNC',        3),
 ('asset_protocol', 'telnet',   'Telnet',     4),
 ('asset_protocol', 'sftp',     'SFTP',       5),
 ('asset_protocol', 'mysql',    'MySQL',      6),
 ('asset_protocol', 'postgresql','PostgreSQL',7),
 ('asset_protocol', 'oracle',   'Oracle',     8),
 ('asset_level',    'normal',   '普通',       1),
 ('asset_level',    'important','重要',       2),
 ('asset_level',    'core',     '核心',       3),
 ('os_type',        'linux',    'Linux',      1),
 ('os_type',        'windows',  'Windows',    2),
 ('os_type',        'unix',     'Unix',       3),
 ('os_type',        'network',  '网络设备',   4),
 ('session_status', 'active',   '进行中',     1),
 ('session_status', 'closed',   '已结束',     2),
 ('session_status', 'terminated','已强制断开',3),
 ('session_status', 'timeout',  '超时断开',   4),
 ('alert_severity', 'info',     '提示',       1),
 ('alert_severity', 'warning',  '警告',       2),
 ('alert_severity', 'critical', '严重',       3),
 ('alert_severity', 'emergency','紧急',       4),
 ('ticket_status',  'draft',    '草稿',       1),
 ('ticket_status',  'pending',  '待审批',     2),
 ('ticket_status',  'approved', '已通过',     3),
 ('ticket_status',  'rejected', '已驳回',     4),
 ('ticket_status',  'cancelled','已取消',     5),
 ('ticket_status',  'expired',  '已过期',     6),
 ('ticket_status',  'supplement','待补单',    7),
 ('account_type',   'password', '口令',       1),
 ('account_type',   'key',      '密钥',       2),
 ('account_type',   'both',     '口令+密钥',  3),
 ('grant_source',   'manual',   '手动授权',   1),
 ('grant_source',   'ticket',   '工单授权',   2),
 ('grant_source',   'temp',     '临时授权',   3),
 ('grant_source',   'binding',  '绑定即授权', 4),
 ('client_type',    'web',      'Web 客户端', 1),
 ('client_type',    'windows',  'Windows 客户端', 2),
 ('client_type',    'linux',    'Linux 客户端',   3),
 ('client_type',    'api',      'API 接入',   4),
 ('client_type',    'ssh_client','SSH 客户端',5),
 ('client_type',    'rdp_client','RDP 客户端',6),
 ('risk_level',     'normal',   '普通',       1),
 ('risk_level',     'warn',     '需关注',     2),
 ('risk_level',     'danger',   '危险',       3),
 ('risk_level',     'critical', '高危',       4),
 ('policy_action',  'allow',    '放行',       1),
 ('policy_action',  'audit',    '放行并告警', 2),
 ('policy_action',  'confirm',  '二次确认',   3),
 ('policy_action',  'deny',     '阻断',       4),
 ('access_mode',    'loose',    '宽松模式',   1),
 ('access_mode',    'standard', '标准模式',   2),
 ('access_mode',    'strict',   '严格模式',   3)
) AS v(dict_code, item_value, item_label, sort_no) ON d.dict_code = v.dict_code
ON CONFLICT (dict_id, item_value) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 系统配置（含准入模式等平台级开关）
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'platform.name',                 '堡垒机系统', 'string', 'general',  '产品名称（界面标题）'),
 (0, 'platform.approval.mode',        'tenant_decide', 'string', 'security', '访问审批平台级开关：strict（强制所有租户审批）/ tenant_decide（允许租户自决）'),
 (0, 'platform.access_mode.default',  'standard', 'string', 'security', '准入模式默认值：loose / standard / strict（FR-08-13）'),
 (0, 'platform.access_mode.by_asset', 'false',    'bool',   'security', '是否允许按资产单独配置准入模式'),
 (0, 'platform.operator.required',    'true',     'bool',   'security', 'API 接入是否强制携带实际操作人（FR-08-12），关闭后仅允许纯自动化场景'),
 (0, 'security.password.min_length',  '8',        'int',    'security', '口令最小长度'),
 (0, 'security.password.complexity',  '3',        'int',    'security', '口令复杂度要求（需包含的字符类别数）'),
 (0, 'security.password.expire_days', '90',       'int',    'security', '口令有效期（天），0 表示不过期'),
 (0, 'security.login.max_fail',       '5',        'int',    'security', '连续登录失败锁定阈值'),
 (0, 'security.login.lock_minutes',   '15',       'int',    'security', '锁定时长（分钟）'),
 (0, 'security.session.idle_minutes', '30',       'int',    'security', '会话空闲超时（分钟）'),
 (0, 'security.mfa.required',         'false',    'bool',   'security', '是否强制所有用户启用多因素认证'),
 (0, 'crypto.provider',               'soft',     'string', 'security', '密码服务实现：soft / pkcs11'),
 (0, 'crypto.backend',                'go',       'string', 'security', '软件实现后端：go / gmssl / openssl'),
 (0, 'audit.log.retain_days',         '180',      'int',    'audit',    '审计日志在线保留天数（下限 180）'),
 (0, 'audit.log.archive_years',       '3',        'int',    'audit',    '审计日志归档年限'),
 (0, 'audit.record.retain_days',      '180',      'int',    'audit',    '录像保留天数'),
 (0, 'runlog.retain_days',            '30',       'int',    'audit',    '运行日志保留天数'),
 (0, 'record.chunk_size_mb',          '10',       'int',    'record',   '录像分片大小（MB）'),
 (0, 'record.chunk_secs',             '60',       'int',    'record',   '录像分片时长（秒）'),
 (0, 'record.encrypt_enabled',        'true',     'bool',   'record',   '录像是否加密存储（SM4）'),
 (0, 'session.max_per_node',          '300',      'int',    'ops',      '单代理节点最大并发会话数'),
 (0, 'session.allow_third_party_client','true',   'bool',   'ops',      '是否允许第三方客户端接入（FR-08-09）'),
 (0, 'device.time.ntp_servers',       '[]',       'json',   'device',   'NTP 服务器列表（FR-07-19）'),
 (0, 'device.time.drift_alert_sec',   '1',        'int',    'device',   '时间偏差告警阈值（秒）'),
 (0, 'device.resource.alert_cpu',     '80',       'int',    'device',   'CPU 使用率告警阈值（%）'),
 (0, 'device.resource.alert_disk',    '85',       'int',    'device',   '磁盘使用率告警阈值（%）'),
 (0, 'device.selfcheck.cron',         '30 3 * * *','string','device',   '系统自检定时表达式（默认每日 03:30）'),
 (0, 'device.log.server.enabled',     'false',    'bool',   'device',   '是否启用日志服务器外发（FR-07-14）')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
