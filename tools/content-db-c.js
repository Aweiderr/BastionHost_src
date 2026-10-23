'use strict'
// 《堡垒机数据库设计文档》内容（第 4 章余下域 + 第 5 ~ 10 章）
const path = require('path')
const IMG = (n) => path.join(__dirname, 'assets', n + '.png')
const F = ['字段名', '类型', '空', '默认值', '说明']
const W = [20, 20, 6, 16, 38]

const domains = [
  {
    no: '4.6', title: '工单与审批域',
    intro: '工单是权限授予的主要驱动方式。工单与授权、会话、审计记录通过 ticket_id 强关联，形成「申请—审批—授权—运维—审计」的完整证据链。',
    tables: [
      {
        no: '4.6.1', name: 'wf_ticket', cn: '工单主表',
        desc: '运维申请单主表。记录申请人、申请事由、时间窗与审批结果，审批通过后据此生成临时授权。',
        fields: [
          ['ticket_id', 'BIGINT', 'N', '序列', '主键'],
          ['ticket_no', 'VARCHAR(64)', 'N', '—', '工单编号，格式 TKT-yyyyMMdd-nnnn'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['user_id', 'BIGINT', 'N', '—', '申请人'],
          ['ticket_type', 'VARCHAR(16)', 'N', "'ops'", '类型：ops（运维）/ temp（临时授权）/ emergency（紧急）'],
          ['title', 'VARCHAR(256)', 'N', '—', '申请标题'],
          ['reason', 'VARCHAR(1000)', 'N', '—', '申请事由'],
          ['valid_from', 'TIMESTAMP', 'N', '—', '申请生效时间'],
          ['valid_to', 'TIMESTAMP', 'N', '—', '申请失效时间'],
          ['flow_id', 'BIGINT', 'N', '—', '使用的审批流 ID'],
          ['current_node', 'VARCHAR(64)', 'Y', 'NULL', '当前审批节点'],
          ['status', 'VARCHAR(16)', 'N', "'pending'", '状态：pending / approving / approved / rejected / cancelled / expired'],
          ['approve_result', 'VARCHAR(16)', 'Y', 'NULL', '审批结果：pass / reject'],
          ['granted', 'BOOLEAN', 'N', 'false', '是否已生成授权'],
          ['create_time', 'TIMESTAMP', 'N', 'now()', '提交时间'],
          ['finish_time', 'TIMESTAMP', 'Y', 'NULL', '结束时间'],
        ],
        indexes: ['唯一索引 uk_ticket_no(ticket_no)', '普通索引 idx_ticket_user(tenant_id, user_id, create_time DESC)', '普通索引 idx_ticket_status(tenant_id, status, create_time DESC)'],
      },
      {
        no: '4.6.2', name: 'wf_ticket_item', cn: '工单明细表',
        desc: '工单申请的资产、账号与操作范围明细。授权范围严格等于申请范围，不允许审批环节扩大。',
        fields: [
          ['item_id', 'BIGINT', 'N', '序列', '主键'],
          ['ticket_id', 'BIGINT', 'N', '—', '所属工单'],
          ['asset_id', 'BIGINT', 'N', '—', '资产 ID'],
          ['asset_name_snap', 'VARCHAR(128)', 'N', '—', '资产名快照'],
          ['account_id', 'BIGINT', 'Y', 'NULL', '目标账号 ID'],
          ['action_scope', 'VARCHAR(256)', 'N', '—', '申请的操作范围'],
          ['cmd_scope', 'VARCHAR(512)', 'Y', 'NULL', '申请的命令范围（白名单）'],
        ],
        indexes: ['普通索引 idx_ticket_item(ticket_id)', '普通索引 idx_ticket_item_asset(asset_id)'],
      },
      {
        no: '4.6.3', name: 'wf_approval_flow', cn: '审批流定义表',
        desc: '审批流模板，定义审批节点顺序、审批人来源与超时策略。支持按资产重要级别匹配不同流程。',
        fields: [
          ['flow_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['flow_code', 'VARCHAR(64)', 'N', '—', '流程编码'],
          ['flow_name', 'VARCHAR(128)', 'N', '—', '流程名称'],
          ['match_rule', 'JSONB', 'Y', 'NULL', '匹配规则（资产级别、申请时长等）'],
          ['node_config', 'JSONB', 'N', '—', '节点配置：顺序、审批人来源、是否允许加签'],
          ['timeout_hours', 'INTEGER', 'N', '4', '审批超时小时数'],
          ['timeout_action', 'VARCHAR(16)', 'N', "'remind'", '超时动作：remind / escalate / auto_pass'],
          ['is_builtin', 'BOOLEAN', 'N', 'false', '是否内置流程'],
          ['status', 'VARCHAR(16)', 'N', "'active'", '状态'],
          ['create_time', 'TIMESTAMP', 'N', 'now()', '创建时间'],
        ],
        indexes: ['唯一索引 uk_flow_code(tenant_id, flow_code)'],
      },
      {
        no: '4.6.4', name: 'wf_approval_record', cn: '审批记录表',
        desc: '审批操作记录，含审批人、动作、意见与时间。审批记录不可删除，是合规审计与责任认定的依据。',
        fields: [
          ['record_id', 'BIGINT', 'N', '序列', '主键'],
          ['ticket_id', 'BIGINT', 'N', '—', '工单 ID'],
          ['node_name', 'VARCHAR(64)', 'N', '—', '审批节点名称'],
          ['approver_id', 'BIGINT', 'N', '—', '审批人'],
          ['approver_name_snap', 'VARCHAR(64)', 'N', '—', '审批人姓名快照'],
          ['action', 'VARCHAR(16)', 'N', '—', '动作：approve / reject / transfer / add_sign / comment'],
          ['opinion', 'VARCHAR(1000)', 'Y', 'NULL', '审批意见'],
          ['target_user_id', 'BIGINT', 'Y', 'NULL', '转办或加签目标人'],
          ['operate_time', 'TIMESTAMP', 'N', 'now()', '操作时间'],
          ['cost_minutes', 'INTEGER', 'Y', 'NULL', '该节点耗时（分钟）'],
        ],
        indexes: ['普通索引 idx_approval_ticket(ticket_id, operate_time)', '普通索引 idx_approval_approver(approver_id, operate_time DESC)'],
      },
    ],
  },
  {
    no: '4.7', title: '审计与告警域',
    intro: '审计域承载合规证据。审计表禁用逻辑删除，通过摘要链保证不可篡改，按月分区并按留存策略归档。',
    tables: [
      {
        no: '4.7.1', name: 'audit_login_log', cn: '登录日志表',
        desc: '记录全部登录尝试（成功与失败），用于异常登录分析、账号锁定与合规审计。留存不少于 6 个月。',
        fields: [
          ['log_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['user_id', 'BIGINT', 'Y', 'NULL', '用户 ID（用户名不存在时为空）'],
          ['username', 'VARCHAR(64)', 'N', '—', '登录名（含失败尝试）'],
          ['login_time', 'TIMESTAMP', 'N', 'now()', '登录时间（分区键）'],
          ['client_ip', 'VARCHAR(45)', 'N', '—', '来源 IP'],
          ['client_type', 'VARCHAR(16)', 'Y', 'NULL', '客户端类型'],
          ['auth_type', 'VARCHAR(64)', 'N', '—', '认证方式'],
          ['result', 'VARCHAR(16)', 'N', '—', '结果：success / failed'],
          ['fail_reason', 'VARCHAR(256)', 'Y', 'NULL', '失败原因'],
          ['user_agent', 'VARCHAR(512)', 'Y', 'NULL', '客户端标识'],
          ['session_id', 'BIGINT', 'Y', 'NULL', '成功登录后关联的会话（若有）'],
          ['record_hash', 'VARCHAR(128)', 'N', '—', '本条摘要'],
        ],
        indexes: ['普通索引 idx_login_user(user_id, login_time DESC)', '普通索引 idx_login_ip(client_ip, login_time DESC)', '普通索引 idx_login_fail(tenant_id, result, login_time DESC) WHERE result = \'failed\'', '分区：按 login_time 月度分区'],
      },
      {
        no: '4.7.2', name: 'audit_operation_log', cn: '管理操作日志表',
        desc: '记录管理侧全部写操作（配置变更、策略调整、授权、密钥操作等），支持回答「谁在何时改了什么」。',
        fields: [
          ['log_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['operator_id', 'BIGINT', 'N', '—', '操作人'],
          ['operator_name_snap', 'VARCHAR(64)', 'N', '—', '操作人姓名快照'],
          ['module', 'VARCHAR(32)', 'N', '—', '模块：asset / policy / user / key / system'],
          ['operation', 'VARCHAR(64)', 'N', '—', '操作：create / update / delete / enable / rotate'],
          ['target_type', 'VARCHAR(32)', 'N', '—', '对象类型'],
          ['target_id', 'VARCHAR(64)', 'Y', 'NULL', '对象 ID'],
          ['target_name', 'VARCHAR(256)', 'Y', 'NULL', '对象名称'],
          ['before_value', 'JSONB', 'Y', 'NULL', '变更前快照'],
          ['after_value', 'JSONB', 'Y', 'NULL', '变更后快照'],
          ['client_ip', 'VARCHAR(45)', 'Y', 'NULL', '来源 IP'],
          ['result', 'VARCHAR(16)', 'N', "'success'", '结果'],
          ['operate_time', 'TIMESTAMP', 'N', 'now()', '操作时间（分区键）'],
          ['record_hash', 'VARCHAR(128)', 'N', '—', '本条摘要'],
          ['prev_hash', 'VARCHAR(128)', 'Y', 'NULL', '前一条摘要'],
        ],
        indexes: ['普通索引 idx_oplog_operator(operator_id, operate_time DESC)', '普通索引 idx_oplog_target(target_type, target_id, operate_time DESC)', '普通索引 idx_oplog_module(tenant_id, module, operate_time DESC)', '分区：按 operate_time 月度分区'],
      },
      {
        no: '4.7.3', name: 'audit_sql_log', cn: 'SQL 审计日志表',
        desc: '数据库运维会话的 SQL 语句级审计，记录完整 SQL 原文、执行计划摘要与影响行数，支持按库表与关键字检索。',
        fields: [
          ['log_id', 'BIGINT', 'N', '序列', '主键'],
          ['session_id', 'BIGINT', 'N', '—', '会话 ID'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['db_name', 'VARCHAR(128)', 'N', '—', '数据库名'],
          ['sql_type', 'VARCHAR(16)', 'N', '—', '语句类型：select / insert / update / delete / ddl / dcl'],
          ['sql_text', 'TEXT', 'N', '—', 'SQL 原文'],
          ['sql_brief', 'VARCHAR(512)', 'Y', 'NULL', 'SQL 摘要'],
          ['table_names', 'VARCHAR(512)', 'Y', 'NULL', '涉及的表名（逗号分隔）'],
          ['affected_rows', 'BIGINT', 'Y', 'NULL', '影响行数'],
          ['exec_time', 'TIMESTAMP', 'N', '—', '执行时间（分区键）'],
          ['duration_ms', 'INTEGER', 'Y', 'NULL', '执行耗时'],
          ['policy_action', 'VARCHAR(16)', 'N', "'allow'", '策略动作'],
          ['result', 'VARCHAR(16)', 'N', '—', '结果：success / failed / blocked'],
          ['error_msg', 'VARCHAR(1000)', 'Y', 'NULL', '错误信息'],
        ],
        indexes: ['普通索引 idx_sqllog_session(session_id, exec_time)', '普通索引 idx_sqllog_type(tenant_id, sql_type, exec_time DESC)', 'GIN 索引 idx_sqllog_text(to_tsvector(\'simple\', sql_brief))（关键字检索）', '分区：按 exec_time 月度分区'],
      },
      {
        no: '4.7.4', name: 'audit_alert', cn: '告警记录表',
        desc: '安全告警记录，来源包括策略命中、认证异常、命令阻断与系统异常。支持确认、指派、处置与关闭的闭环管理。',
        fields: [
          ['alert_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['rule_id', 'BIGINT', 'Y', 'NULL', '触发的规则 ID'],
          ['alert_no', 'VARCHAR(64)', 'N', '—', '告警编号'],
          ['level', 'VARCHAR(16)', 'N', '—', '级别：critical / major / minor / info'],
          ['alert_type', 'VARCHAR(32)', 'N', '—', '类型：cmd_block / login_abnormal / policy_hit / device_error'],
          ['alert_title', 'VARCHAR(256)', 'N', '—', '告警标题'],
          ['alert_detail', 'TEXT', 'Y', 'NULL', '告警详情'],
          ['session_id', 'BIGINT', 'Y', 'NULL', '关联会话'],
          ['user_id', 'BIGINT', 'Y', 'NULL', '关联用户'],
          ['asset_id', 'BIGINT', 'Y', 'NULL', '关联资产'],
          ['trigger_time', 'TIMESTAMP', 'N', 'now()', '触发时间（分区键）'],
          ['notify_status', 'VARCHAR(16)', 'N', "'pending'", '通知状态：pending / sent / failed'],
          ['status', 'VARCHAR(16)', 'N', "'open'", '处置状态：open / acked / handling / closed / ignored'],
          ['handler_id', 'BIGINT', 'Y', 'NULL', '处置人'],
          ['handle_time', 'TIMESTAMP', 'Y', 'NULL', '处置时间'],
          ['handle_opinion', 'VARCHAR(1000)', 'Y', 'NULL', '处置意见'],
        ],
        indexes: ['唯一索引 uk_alert_no(alert_no)', '普通索引 idx_alert_status(tenant_id, status, level, trigger_time DESC)', '普通索引 idx_alert_session(session_id)', '分区：按 trigger_time 月度分区'],
      },
      {
        no: '4.7.5', name: 'audit_alert_rule', cn: '告警规则表',
        desc: '告警规则定义，条件以 JSONB 表达，支持事件类型、频率、关键字与用户/资产维度的组合。',
        fields: [
          ['rule_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['rule_code', 'VARCHAR(64)', 'N', '—', '规则编码'],
          ['rule_name', 'VARCHAR(128)', 'N', '—', '规则名称'],
          ['event_type', 'VARCHAR(32)', 'N', '—', '事件类型'],
          ['condition_json', 'JSONB', 'N', '—', '条件定义'],
          ['level', 'VARCHAR(16)', 'N', '—', '告警级别'],
          ['notify_channels', 'VARCHAR(128)', 'N', "'station'", '通知渠道：station,mail,sms,webhook'],
          ['notify_receivers', 'VARCHAR(512)', 'Y', 'NULL', '接收人（用户 ID 或角色编码）'],
          ['silence_minutes', 'INTEGER', 'N', '0', '静默期（分钟），避免重复告警'],
          ['agg_window_sec', 'INTEGER', 'N', '60', '聚合窗口（秒），窗口内相同事件合并'],
          ['status', 'VARCHAR(16)', 'N', "'active'", '状态：active / disabled'],
          ['create_time', 'TIMESTAMP', 'N', 'now()', '创建时间'],
        ],
        indexes: ['唯一索引 uk_alert_rule_code(tenant_id, rule_code)', '普通索引 idx_alert_rule_type(tenant_id, event_type, status)'],
      },
      {
        no: '4.7.6', name: 'audit_export_log', cn: '审计导出日志表',
        desc: '记录审计数据的导出行为（导出范围、行数、用途、审批单号），导出行为本身也是被审计对象，形成「审计的审计」。',
        fields: [
          ['export_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['operator_id', 'BIGINT', 'N', '—', '导出人'],
          ['export_type', 'VARCHAR(32)', 'N', '—', '导出类型：session_record / command_list / evidence_package / report'],
          ['query_condition', 'JSONB', 'Y', 'NULL', '导出使用的检索条件'],
          ['record_count', 'INTEGER', 'N', '0', '导出记录数'],
          ['file_size', 'BIGINT', 'Y', 'NULL', '导出文件大小'],
          ['file_digest', 'VARCHAR(128)', 'Y', 'NULL', '导出文件 SM3 摘要'],
          ['watermark', 'VARCHAR(256)', 'Y', 'NULL', '水印标识'],
          ['approve_ticket', 'VARCHAR(64)', 'Y', 'NULL', '审批单号'],
          ['purpose', 'VARCHAR(500)', 'Y', 'NULL', '导出用途'],
          ['export_time', 'TIMESTAMP', 'N', 'now()', '导出时间'],
          ['result', 'VARCHAR(16)', 'N', '—', '结果：success / failed'],
        ],
        indexes: ['普通索引 idx_export_log(tenant_id, export_time DESC)', '普通索引 idx_export_operator(operator_id, export_time DESC)'],
      },
    ],
  },
  {
    no: '4.8', title: '密钥与证书域',
    intro: '密钥域仅存密钥元数据与索引，密钥本体始终在密码机内。密钥台账是密评与合规审计的核心证据。',
    tables: [
      {
        no: '4.8.1', name: 'km_key', cn: '密钥台账表',
        desc: '密钥元数据台账。key_index 为密码机中的密钥索引，本表不存储任何密钥材料。密钥全生命周期状态在此维护。',
        fields: [
          ['key_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户，0 表示系统级密钥'],
          ['key_index', 'VARCHAR(64)', 'N', '—', '密码机密钥索引，唯一'],
          ['key_alias', 'VARCHAR(128)', 'N', '—', '密钥别名，便于识别'],
          ['key_type', 'VARCHAR(16)', 'N', '—', '类型：SM2 / SM4 / SM9 / KEK'],
          ['key_usage', 'VARCHAR(32)', 'N', '—', '用途：sign / encrypt / dek_protect / session'],
          ['device_id', 'BIGINT', 'Y', 'NULL', '所在密码设备 ID'],
          ['backup_shards', 'SMALLINT', 'Y', 'NULL', '备份分片数'],
          ['shard_threshold', 'SMALLINT', 'Y', 'NULL', '恢复门限（如 3 取 2）'],
          ['valid_from', 'TIMESTAMP', 'N', 'now()', '生效时间'],
          ['expire_time', 'TIMESTAMP', 'Y', 'NULL', '计划轮换时间'],
          ['rotate_cycle_days', 'INTEGER', 'N', '365', '轮换周期（天）'],
          ['status', 'VARCHAR(16)', 'N', "'active'", '状态：active / rotating / archived / destroyed'],
          ['destroy_time', 'TIMESTAMP', 'Y', 'NULL', '销毁时间'],
          ['destroy_approver', 'BIGINT', 'Y', 'NULL', '销毁审批人'],
          ['create_time', 'TIMESTAMP', 'N', 'now()', '创建时间'],
        ],
        indexes: ['唯一索引 uk_key_index(key_index)', '普通索引 idx_key_status(tenant_id, status, expire_time)'],
      },
      {
        no: '4.8.2', name: 'km_key_usage', cn: '密钥调用统计表',
        desc: '按日聚合的密钥调用统计，用于容量分析与异常检测。明细记录见 ops_key_usage。',
        fields: [
          ['id', 'BIGINT', 'N', '序列', '主键'],
          ['key_id', 'BIGINT', 'N', '—', '密钥 ID'],
          ['stat_date', 'DATE', 'N', '—', '统计日期'],
          ['call_count', 'BIGINT', 'N', '0', '调用次数'],
          ['success_count', 'BIGINT', 'N', '0', '成功次数'],
          ['fail_count', 'BIGINT', 'N', '0', '失败次数'],
          ['avg_duration_ms', 'INTEGER', 'Y', 'NULL', '平均耗时'],
        ],
        indexes: ['唯一索引 uk_key_usage_stat(key_id, stat_date)'],
      },
      {
        no: '4.8.3', name: 'km_cert', cn: '证书表',
        desc: '设备证书、客户端证书与 CA 证书的管理台账。记录证书指纹、有效期与吊销状态，到期前分级提醒。',
        fields: [
          ['cert_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
          ['cert_type', 'VARCHAR(16)', 'N', '—', '类型：device / client / ca'],
          ['subject_dn', 'VARCHAR(512)', 'N', '—', '证书主题'],
          ['issuer_dn', 'VARCHAR(512)', 'N', '—', '签发者'],
          ['serial_no', 'VARCHAR(128)', 'N', '—', '序列号，唯一'],
          ['fingerprint', 'VARCHAR(128)', 'N', '—', 'SM3 指纹'],
          ['key_index', 'VARCHAR(64)', 'Y', 'NULL', '对应私钥索引'],
          ['not_before', 'TIMESTAMP', 'N', '—', '生效时间'],
          ['not_after', 'TIMESTAMP', 'N', '—', '失效时间'],
          ['bind_user_id', 'BIGINT', 'Y', 'NULL', '绑定的用户（客户端证书）'],
          ['bind_device', 'VARCHAR(128)', 'Y', 'NULL', '绑定的设备标识'],
          ['status', 'VARCHAR(16)', 'N', "'valid'", '状态：valid / expiring / expired / revoked'],
          ['revoke_time', 'TIMESTAMP', 'Y', 'NULL', '吊销时间'],
          ['revoke_reason', 'VARCHAR(256)', 'Y', 'NULL', '吊销原因'],
        ],
        indexes: ['唯一索引 uk_cert_serial(serial_no)', '普通索引 idx_cert_expire(not_after, status)', '普通索引 idx_cert_user(bind_user_id)'],
      },
    ],
  },
  {
    no: '4.9', title: '系统与初始化域',
    intro: '承载系统配置、许可、初始化记录与运维日志。初始化记录是设备交付验收的关键证据，长期保存且不可删除。',
    tables: [
      {
        no: '4.9.1', name: 'sys_config', cn: '系统配置表',
        desc: '系统级与租户级参数配置。敏感配置项（如对接凭据）加密存储；配置变更启用乐观锁并记录版本。',
        fields: [
          ['config_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '0', '所属租户，0 表示系统级'],
          ['config_key', 'VARCHAR(128)', 'N', '—', '配置键，如 session.timeout'],
          ['config_value', 'TEXT', 'Y', 'NULL', '配置值'],
          ['value_type', 'VARCHAR(16)', 'N', "'string'", '值类型：string / int / bool / json'],
          ['is_encrypted', 'BOOLEAN', 'N', 'false', '是否加密存储'],
          ['is_system', 'BOOLEAN', 'N', 'false', '是否系统内置（不可删除）'],
          ['remark', 'VARCHAR(500)', 'Y', 'NULL', '说明'],
          ['version', 'INTEGER', 'N', '0', '乐观锁版本号'],
          ['update_by', 'BIGINT', 'Y', 'NULL', '最后修改人'],
          ['update_time', 'TIMESTAMP', 'N', 'now()', '最后修改时间'],
        ],
        indexes: ['唯一索引 uk_config_key(tenant_id, config_key)'],
      },
      {
        no: '4.9.2', name: 'sys_license', cn: '许可信息表',
        desc: '许可授权信息，记录授权容量、有效期与授权对象，用于容量校验与到期提醒。',
        fields: [
          ['license_id', 'BIGINT', 'N', '序列', '主键'],
          ['license_key', 'VARCHAR(128)', 'N', '—', '许可标识'],
          ['license_type', 'VARCHAR(32)', 'N', '—', '类型：trial / standard / enterprise'],
          ['max_asset', 'INTEGER', 'N', '0', '资产容量上限'],
          ['max_user', 'INTEGER', 'N', '0', '用户容量上限'],
          ['max_session', 'INTEGER', 'N', '0', '并发会话上限'],
          ['issue_time', 'TIMESTAMP', 'N', '—', '签发时间'],
          ['expire_time', 'TIMESTAMP', 'N', '—', '到期时间'],
          ['license_file', 'TEXT', 'Y', 'NULL', '许可文件内容（含签名）'],
          ['status', 'VARCHAR(16)', 'N', "'active'", '状态：active / expiring / expired'],
          ['import_time', 'TIMESTAMP', 'N', 'now()', '导入时间'],
        ],
        indexes: ['唯一索引 uk_license_key(license_key)', '普通索引 idx_license_expire(expire_time)'],
      },
      {
        no: '4.9.3', name: 'sys_init_record', cn: '初始化记录表',
        desc: '记录设备初始化的完整过程，包括设备信息、三项初始化结果、操作人与证书指纹。作为交付验收与责任认定依据，长期保存不可删除。',
        fields: [
          ['record_id', 'BIGINT', 'N', '序列', '主键'],
          ['device_sn', 'VARCHAR(64)', 'N', '—', '设备序列号'],
          ['device_model', 'VARCHAR(64)', 'N', '—', '设备型号'],
          ['firmware_version', 'VARCHAR(32)', 'N', '—', '固件版本'],
          ['mgmt_ip', 'VARCHAR(45)', 'N', '—', '管理地址'],
          ['integrity_check', 'VARCHAR(16)', 'N', '—', '完整性校验结果：pass / fail'],
          ['cert_result', 'VARCHAR(16)', 'N', '—', '设备证书初始化结果'],
          ['cert_fingerprint', 'VARCHAR(128)', 'Y', 'NULL', '设备证书指纹'],
          ['cert_issuer', 'VARCHAR(512)', 'Y', 'NULL', '证书签发者'],
          ['keycard_result', 'VARCHAR(16)', 'N', '—', '密码卡初始化结果'],
          ['keycard_self_test', 'VARCHAR(128)', 'Y', 'NULL', '算法自检结果'],
          ['superadmin_id', 'BIGINT', 'Y', 'NULL', '创建的超级管理员 ID'],
          ['operator', 'VARCHAR(64)', 'N', '—', '实施人员（姓名 / 工号）'],
          ['start_time', 'TIMESTAMP', 'N', '—', '初始化开始时间'],
          ['finish_time', 'TIMESTAMP', 'Y', 'NULL', '初始化完成时间'],
          ['report_digest', 'VARCHAR(128)', 'Y', 'NULL', '初始化报告摘要'],
          ['status', 'VARCHAR(16)', 'N', "'completed'", '状态：completed / interrupted / failed'],
        ],
        indexes: ['唯一索引 uk_init_device(device_sn, start_time)', '普通索引 idx_init_time(start_time DESC)'],
      },
      {
        no: '4.9.4', name: 'sys_log', cn: '系统运行日志表',
        desc: '系统运行与接口调用日志，用于问题排查与性能分析。与审计日志分离存储，可配置留存周期。',
        fields: [
          ['log_id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'Y', 'NULL', '所属租户'],
          ['trace_id', 'VARCHAR(64)', 'Y', 'NULL', '链路追踪 ID'],
          ['service_name', 'VARCHAR(64)', 'N', '—', '服务名'],
          ['log_level', 'VARCHAR(16)', 'N', '—', '级别：DEBUG / INFO / WARN / ERROR'],
          ['message', 'TEXT', 'N', '—', '日志内容'],
          ['context_json', 'JSONB', 'Y', 'NULL', '上下文信息'],
          ['log_time', 'TIMESTAMP', 'N', 'now()', '日志时间（分区键）'],
        ],
        indexes: ['普通索引 idx_syslog_level(log_level, log_time DESC)', '普通索引 idx_syslog_trace(trace_id)', '分区：按 log_time 日度分区（保留 30 天）'],
      },
      {
        no: '4.9.5', name: 'sys_tenant_quota', cn: '租户配额使用表',
        desc: '租户配额的实际使用量统计，用于超限校验与容量趋势分析。按日聚合更新。',
        fields: [
          ['id', 'BIGINT', 'N', '序列', '主键'],
          ['tenant_id', 'BIGINT', 'N', '—', '租户 ID'],
          ['stat_date', 'DATE', 'N', '—', '统计日期'],
          ['asset_used', 'INTEGER', 'N', '0', '已用资产数'],
          ['user_used', 'INTEGER', 'N', '0', '已用用户数'],
          ['session_peak', 'INTEGER', 'N', '0', '当日并发会话峰值'],
          ['storage_used', 'BIGINT', 'N', '0', '已用存储（字节）'],
          ['update_time', 'TIMESTAMP', 'N', 'now()', '更新时间'],
        ],
        indexes: ['唯一索引 uk_quota_stat(tenant_id, stat_date)'],
      },
    ],
  },
]

function renderDomains(d) {
  domains.forEach((dm) => {
    d.h(2, `${dm.no}  ${dm.title}`)
    d.p(dm.intro)
    dm.tables.forEach((t) => {
      d.h(3, `${t.no}  ${t.name}（${t.cn}）`)
      d.p(t.desc)
      d.table({
        caption: `表 ${t.no.split('.').join('-')}  ${t.name} 字段定义`,
        headers: F,
        widths: W,
        rows: t.fields,
        size: 17,
        headFill: '2E5B9A',
      })
      d.p('索引与约束：' + t.indexes.join('；') + '。', { size: 19, indent: false, after: 200 })
    })
  })
}

module.exports = function build(d) {
  renderDomains(d)

  // ================= 4.10 设备运维与信任配置域 =================
  d.h(2, '4.10  设备运维与信任配置域')
  d.p('本域承载堡垒机设备自身的运维数据与信任配置，对应需求文档 FR-07-09、FR-07-11 ~ FR-07-19、FR-08-11、FR-09-13、FR-09-14。这些数据此前分散在配置表与日志表中，现按功能独立建表，便于独立查询与保留策略管理。')

  d.h(3, '4.10.1  dev_backup_task（备份任务与策略表）')
  d.p('保存备份策略定义与最近执行状态。备份内容的三类划分（配置 / 系统 / 录像）由 backup_type 区分。备份包不含密码卡主密钥材料，密钥备份见 FR-09-13 的独立流程。')
  d.table({
    caption: '表 4-10-1  dev_backup_task 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['task_id', 'BIGINT', 'N', '序列', '主键'],
      ['tenant_id', 'BIGINT', 'N', '0', '所属租户，0 表示系统级任务'],
      ['task_name', 'VARCHAR(128)', 'N', '—', '任务名称'],
      ['backup_type', 'VARCHAR(16)', 'N', '—', '类型：config / system / record'],
      ['cron_expr', 'VARCHAR(64)', 'Y', 'NULL', '调度表达式，NULL 表示仅手动触发'],
      ['target_type', 'VARCHAR(16)', 'N', "'local'", '目标类型：local / sftp / nfs'],
      ['target_config', 'JSONB', 'Y', 'NULL', '目标参数（地址、路径、凭据引用，凭据加密）'],
      ['keep_copies', 'INTEGER', 'N', '7', '保留份数'],
      ['keep_days', 'INTEGER', 'Y', 'NULL', '保留天数，与份数二选一'],
      ['encrypt_enabled', 'BOOLEAN', 'N', 'true', '是否加密备份包'],
      ['last_run_time', 'TIMESTAMP', 'Y', 'NULL', '最近执行时间'],
      ['last_result', 'VARCHAR(16)', 'Y', 'NULL', '最近结果：success / failed / running'],
      ['next_run_time', 'TIMESTAMP', 'Y', 'NULL', '下次执行时间'],
      ['status', 'VARCHAR(16)', 'N', "'active'", '状态：active / paused'],
      ['create_by', 'BIGINT', 'Y', 'NULL', '创建人'],
      ['create_time', 'TIMESTAMP', 'N', 'now()', '创建时间'],
    ],
  })
  d.p('索引与约束：唯一索引 uk_backup_task(tenant_id, task_name)；普通索引 idx_backup_task_next(next_run_time) WHERE status = \'active\'（调度扫描）。', { size: 19, indent: false, after: 200 })

  d.h(3, '4.10.2  dev_backup_record（备份执行记录表）')
  d.p('每次备份执行的明细记录，含文件位置、大小与 SM3 摘要，用于校验备份可用性与追溯。')
  d.table({
    caption: '表 4-10-2  dev_backup_record 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['record_id', 'BIGINT', 'N', '序列', '主键'],
      ['task_id', 'BIGINT', 'N', '—', '所属任务'],
      ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
      ['run_type', 'VARCHAR(16)', 'N', '—', '触发方式：schedule / manual'],
      ['start_time', 'TIMESTAMP', 'N', '—', '开始时间（分区键）'],
      ['end_time', 'TIMESTAMP', 'Y', 'NULL', '结束时间'],
      ['duration_sec', 'INTEGER', 'Y', 'NULL', '耗时（秒）'],
      ['file_name', 'VARCHAR(512)', 'Y', 'NULL', '备份文件名'],
      ['file_size', 'BIGINT', 'Y', 'NULL', '文件大小（字节）'],
      ['file_digest', 'VARCHAR(128)', 'Y', 'NULL', '备份包 SM3 摘要'],
      ['target_path', 'VARCHAR(1024)', 'Y', 'NULL', '目标路径'],
      ['result', 'VARCHAR(16)', 'N', '—', '结果：success / failed'],
      ['fail_reason', 'VARCHAR(500)', 'Y', 'NULL', '失败原因'],
      ['operator_id', 'BIGINT', 'Y', 'NULL', '操作人（手动触发时）'],
    ],
  })
  d.p('索引与约束：普通索引 idx_backup_rec(task_id, start_time DESC)；分区：按 start_time 月度分区，保留 12 个月。', { size: 19, indent: false, after: 200 })

  d.h(3, '4.10.3  dev_selfcheck_record（系统自检记录表）')
  d.p('记录设备系统自检结果，覆盖软件完整性、密码卡功能与密钥完整性三方面，是密评与等保核查的证据之一。')
  d.table({
    caption: '表 4-10-3  dev_selfcheck_record 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['record_id', 'BIGINT', 'N', '序列', '主键'],
      ['check_type', 'VARCHAR(16)', 'N', '—', '触发方式：manual / schedule'],
      ['start_time', 'TIMESTAMP', 'N', '—', '开始时间（分区键）'],
      ['end_time', 'TIMESTAMP', 'Y', 'NULL', '结束时间'],
      ['software_result', 'VARCHAR(16)', 'N', '—', '软件完整性：pass / fail'],
      ['software_diff', 'JSONB', 'Y', 'NULL', '差异文件清单（路径、期望摘要、实际摘要）'],
      ['keycard_result', 'VARCHAR(16)', 'N', '—', '密码卡功能：pass / fail / absent'],
      ['keycard_detail', 'VARCHAR(500)', 'Y', 'NULL', '自检明细（SM2 / SM3 / SM4 结果）'],
      ['key_result', 'VARCHAR(16)', 'N', '—', '密钥完整性：pass / fail'],
      ['key_detail', 'VARCHAR(500)', 'Y', 'NULL', '密钥一致性、分片可读性、证书链校验结果'],
      ['overall_result', 'VARCHAR(16)', 'N', '—', '总体结论：pass / fail'],
      ['report_digest', 'VARCHAR(128)', 'Y', 'NULL', '自检报告 SM3 摘要'],
      ['operator_id', 'BIGINT', 'Y', 'NULL', '操作人（手动触发时）'],
    ],
  })
  d.p('索引与约束：普通索引 idx_selfcheck(start_time DESC)；普通索引 idx_selfcheck_result(overall_result) WHERE overall_result = \'fail\'；分区：按 start_time 月度分区。', { size: 19, indent: false, after: 200 })

  d.h(3, '4.10.4  dev_resource_metric（本机资源指标表）')
  d.p('采集设备自身 CPU、内存、磁盘与网络指标。原始数据保留 7 天、小时聚合保留 30 天，通过 granularity 区分粒度，查询时按时间范围选择。')
  d.table({
    caption: '表 4-10-4  dev_resource_metric 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['metric_id', 'BIGINT', 'N', '序列', '主键'],
      ['node_id', 'VARCHAR(64)', 'N', '—', '节点标识（主机名或节点 ID）'],
      ['metric_time', 'TIMESTAMP', 'N', '—', '采样时间（分区键）'],
      ['granularity', 'VARCHAR(8)', 'N', "'raw'", '粒度：raw（分钟）/ hour / day'],
      ['cpu_usage', 'NUMERIC(5,2)', 'Y', 'NULL', 'CPU 利用率（%）'],
      ['load_avg', 'NUMERIC(6,2)', 'Y', 'NULL', '1 分钟负载均值'],
      ['mem_total', 'BIGINT', 'Y', 'NULL', '内存总量（字节）'],
      ['mem_usage', 'NUMERIC(5,2)', 'Y', 'NULL', '内存使用率（%）'],
      ['disk_usage', 'NUMERIC(5,2)', 'Y', 'NULL', '根分区使用率（%）'],
      ['disk_detail', 'JSONB', 'Y', 'NULL', '各挂载点明细（挂载点、容量、使用率、IO）'],
      ['net_rx_bytes', 'BIGINT', 'Y', 'NULL', '网络接收字节速率'],
      ['net_tx_bytes', 'BIGINT', 'Y', 'NULL', '网络发送字节速率'],
      ['conn_count', 'INTEGER', 'Y', 'NULL', '当前连接数'],
    ],
  })
  d.p('索引与约束：唯一索引 uk_metric(node_id, metric_time, granularity)；普通索引 idx_metric_time(metric_time DESC)；分区：按 metric_time 日度分区，原粒度保留 7 天、聚合粒度保留 30 天。', { size: 19, indent: false, after: 200 })

  d.h(3, '4.10.5  km_cert_chain（证书链表）')
  d.p('维护证书链的层级结构，证书本体存于 km_cert（cert_type = ca / intermediate）。链按 issuer / subject 建立父子关系，支持多条链共存与租户绑定。')
  d.table({
    caption: '表 4-10-5  km_cert_chain 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['chain_id', 'BIGINT', 'N', '序列', '主键'],
      ['tenant_id', 'BIGINT', 'N', '0', '绑定租户，0 表示平台默认链'],
      ['chain_name', 'VARCHAR(128)', 'N', '—', '证书链名称'],
      ['cert_id', 'BIGINT', 'N', '—', '证书 ID（指向 km_cert）'],
      ['parent_cert_id', 'BIGINT', 'Y', 'NULL', '父证书 ID，NULL 表示链的顶点'],
      ['chain_level', 'SMALLINT', 'N', '1', '层级（1 为根）'],
      ['is_trusted_root', 'BOOLEAN', 'N', 'false', '是否受信任根'],
      ['is_default', 'BOOLEAN', 'N', 'false', '是否为租户默认链'],
      ['status', 'VARCHAR(16)', 'N', "'active'", '状态：active / disabled'],
      ['create_by', 'BIGINT', 'Y', 'NULL', '导入人'],
      ['create_time', 'TIMESTAMP', 'N', 'now()', '导入时间'],
    ],
  })
  d.p('索引与约束：唯一索引 uk_chain_cert(tenant_id, cert_id)；普通索引 idx_chain_parent(parent_cert_id)；普通索引 idx_chain_default(tenant_id, is_default) WHERE is_default = true。', { size: 19, indent: false, after: 200 })

  d.h(3, '4.10.6  sys_idp_config（身份源与联邦配置表）')
  d.p('保存 LDAP / AD / OIDC / SAML / Kerberos 的身份源配置与属性映射。绑定凭据以引用方式存储（凭据本体加密），避免明文落库。')
  d.table({
    caption: '表 4-10-6  sys_idp_config 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['idp_id', 'BIGINT', 'N', '序列', '主键'],
      ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
      ['idp_type', 'VARCHAR(16)', 'N', '—', '类型：ldap / ad / oidc / saml / kerberos'],
      ['idp_name', 'VARCHAR(128)', 'N', '—', '身份源名称'],
      ['config_json', 'JSONB', 'N', '—', '连接参数（地址、端口、端点、绑定 DN、凭据引用）'],
      ['attr_mapping', 'JSONB', 'Y', 'NULL', '属性映射（用户名、姓名、组织、角色、工号）'],
      ['auto_create_user', 'BOOLEAN', 'N', 'false', '是否允许自动创建用户（默认关闭）'],
      ['default_org_id', 'BIGINT', 'Y', 'NULL', '自动创建用户时的默认组织'],
      ['default_role_id', 'BIGINT', 'Y', 'NULL', '自动创建用户时的默认角色'],
      ['sync_enabled', 'BOOLEAN', 'N', 'false', '是否启用定时同步'],
      ['sync_cron', 'VARCHAR(64)', 'Y', 'NULL', '同步调度表达式'],
      ['last_sync_time', 'TIMESTAMP', 'Y', 'NULL', '最近同步时间'],
      ['last_sync_result', 'VARCHAR(16)', 'Y', 'NULL', '最近同步结果：success / failed'],
      ['status', 'VARCHAR(16)', 'N', "'active'", '状态：active / disabled'],
      ['create_time', 'TIMESTAMP', 'N', 'now()', '创建时间'],
    ],
  })
  d.p('索引与约束：唯一索引 uk_idp_name(tenant_id, idp_name)；普通索引 idx_idp_type(tenant_id, idp_type, status)。', { size: 19, indent: false, after: 200 })

  d.h(3, '4.10.7  tenant_access_policy（租户访问策略表）')
  d.p('保存租户级的资源访问审批策略。平台级开关（强制审批 / 允许租户自决）存于 sys_config 的 platform.approval.mode，本表仅在允许自决时生效。')
  d.table({
    caption: '表 4-10-7  tenant_access_policy 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['policy_id', 'BIGINT', 'N', '序列', '主键'],
      ['tenant_id', 'BIGINT', 'N', '—', '租户 ID（唯一）'],
      ['access_mode', 'VARCHAR(16)', 'N', "'approval'", '访问模式：approval（需工单）/ binding（绑定即授权）'],
      ['approval_flow_id', 'BIGINT', 'Y', 'NULL', '默认审批流（access_mode = approval 时生效）'],
      ['core_asset_force_approval', 'BOOLEAN', 'N', 'true', '核心资产是否强制审批（不受免审批影响）'],
      ['session_concurrent_limit', 'INTEGER', 'N', '0', '免审批会话并发上限，0 表示不限制'],
      ['alert_threshold', 'INTEGER', 'N', '0', '免审批会话告警阈值（次/小时），0 表示不告警'],
      ['update_by', 'BIGINT', 'Y', 'NULL', '最后修改人'],
      ['update_time', 'TIMESTAMP', 'N', 'now()', '最后修改时间'],
    ],
  })
  d.p('索引与约束：唯一索引 uk_tenant_policy(tenant_id)；约束：access_mode 取值受 CHECK 约束；变更写入 audit_operation_log（含前后值）。', { size: 19, indent: false, after: 200 })

  d.h(3, '4.10.8  sys_access_permit（运维许可凭据表）')
  d.p('记录准入模式签发的「运维许可」。许可本体在服务端，派生凭据（如 SSH 短时证书）在客户端保存，服务端通过证书中的 KeyId 反查本表校验许可状态；撤销许可即同时使全部派生凭据失效（对应需求 FR-08-13）。')
  d.table({
    caption: '表 4-10-8  sys_access_permit 字段定义',
    headers: ['字段名', '类型', '空', '默认值', '说明'],
    widths: [22, 20, 6, 14, 38],
    size: 17,
    headFill: '2E5B9A',
    rows: [
      ['permit_id', 'BIGINT', 'N', '序列', '主键'],
      ['tenant_id', 'BIGINT', 'N', '—', '所属租户'],
      ['permit_no', 'VARCHAR(64)', 'N', '—', '许可编号，格式 PMT-yyyyMMdd-nnnn，全局唯一'],
      ['user_id', 'BIGINT', 'N', '—', '许可持有用户'],
      ['device_fingerprint', 'VARCHAR(128)', 'N', '—', '设备指纹（客户端证书 + 硬件标识派生），换机即失效'],
      ['client_type', 'VARCHAR(16)', 'N', '—', '登录客户端类型：web / windows / linux'],
      ['client_version', 'VARCHAR(32)', 'Y', 'NULL', '客户端版本'],
      ['src_ip', 'VARCHAR(45)', 'N', '—', '客户端登录来源 IP'],
      ['access_mode', 'VARCHAR(16)', 'N', "'standard'", '签发时的准入模式：loose / standard / strict'],
      ['issue_time', 'TIMESTAMP', 'N', 'now()', '签发时间'],
      ['expire_time', 'TIMESTAMP', 'N', '—', '失效时间（默认签发 + 8 小时）'],
      ['renew_count', 'INTEGER', 'N', '0', '已续期次数'],
      ['derived_types', 'VARCHAR(256)', 'Y', 'NULL', '已派生的凭据类型：ssh_cert / rdp_ticket / api_token / net_permit'],
      ['status', 'VARCHAR(16)', 'N', "'active'", '状态：active / expiring / expired / revoked / logged_out'],
      ['revoke_time', 'TIMESTAMP', 'Y', 'NULL', '撤销时间'],
      ['revoke_by', 'BIGINT', 'Y', 'NULL', '撤销人（管理员撤销时）'],
      ['revoke_reason', 'VARCHAR(256)', 'Y', 'NULL', '撤销原因'],
      ['create_time', 'TIMESTAMP', 'N', 'now()', '创建时间'],
    ],
  })
  d.p('索引与约束：唯一索引 uk_permit_no(permit_no)；普通索引 idx_permit_user(tenant_id, user_id, status)；普通索引 idx_permit_expire(expire_time) WHERE status IN (\'active\', \'expiring\')（到期扫描与续期提醒）；约束：access_mode 取值受 CHECK 约束；撤销与过期事件同时写入 audit_operation_log。', { size: 19, indent: false, after: 200 })

  d.note('本域设计要点', [
    '一、备份包与密钥分离：dev_backup_record 仅记录备份包信息，密码卡主密钥材料不入备份包（走 km_key 的独立备份流程）；',
    '二、资源指标与自检记录均为时序数据，按时分区并设置自动清理策略，避免长期占用在线存储；',
    '三、证书链采用「本体 + 链结构」分离设计：证书本体复用 km_cert，km_cert_chain 只维护层级与绑定关系，避免重复存储；',
    '四、身份源配置中的凭据以引用方式存储（凭据本体加密在密钥服务侧），配置表不落明文；',
    '五、租户访问策略变更属高敏操作，必须记录前后值与操作人，并可在审计报表中按「免审批直连」筛选会话。',
  ], 'info')

  // ================= 5 索引与查询优化 =================
  d.h(1, '5  索引与查询优化')

  d.h(2, '5.1  索引设计原则')
  d.ol([
    '按查询模式建索引：以高频查询的 WHERE、JOIN、ORDER BY 字段组合为依据，而非按字段重要性；',
    '遵循最左前缀：复合索引字段顺序与查询条件顺序一致，将选择性高的字段放在前面；',
    '控制索引数量：单表普通索引不超过 6 个，过多索引会拖慢写入（审计表写入量大尤其敏感）；',
    '使用部分索引：对状态类查询使用 WHERE 条件的部分索引（如仅索引活跃会话），减少索引体积；',
    '避免冗余索引：已有 (a, b, c) 复合索引时不再单独建 (a) 与 (a, b)；',
    '大表分区后在各分区内建本地索引，避免全局索引成为写入瓶颈；',
    '文本检索使用 GIN 索引或交由 Elasticsearch 承担，不在主库做 LIKE \'%...%\' 全表扫描。',
  ], { prefix: '原则 ', sep: '：' })

  d.h(2, '5.2  关键索引清单')
  d.table({
    caption: '表 5-1  关键查询与索引设计',
    headers: ['查询场景', '查询条件', '使用索引', '预期性能'],
    widths: [22, 34, 26, 18],
    rows: [
      ['我的资产列表', 'tenant_id + user_id（经授权联表）', 'idx_grant_subject + uk_asset_code', '≤ 200 ms'],
      ['按用户查会话', 'tenant_id + user_id + start_time 区间', 'idx_session_user_time', '≤ 500 ms'],
      ['按资产查会话', 'tenant_id + asset_id + start_time 区间', 'idx_session_asset_time', '≤ 500 ms'],
      ['会话命令明细', 'session_id + seq_no', 'uk_cmd_seq', '≤ 100 ms'],
      ['命令关键字检索', 'cmd_brief 全文匹配', 'idx_cmd_brief（GIN）或 ES 检索', '≤ 3 s（百万级）'],
      ['阻断命令统计', 'action = block + exec_time 区间', 'idx_cmd_action（部分索引）', '≤ 1 s'],
      ['文件传输查询', 'session_id 或 file_digest', 'idx_file_session / idx_file_digest', '≤ 300 ms'],
      ['审计告警列表', 'tenant_id + status + level + trigger_time', 'idx_alert_status', '≤ 500 ms'],
      ['待办工单', 'tenant_id + status + create_time', 'idx_ticket_status', '≤ 300 ms'],
      ['账号改密扫描', 'next_rotate_time ≤ now()', 'idx_account_rotate（部分索引）', '≤ 200 ms'],
      ['证书到期扫描', 'not_after ≤ now() + 30d', 'idx_cert_expire', '≤ 200 ms'],
      ['授权到期扫描', 'valid_to ≤ now() 且 status = active', 'idx_grant_valid（部分索引）', '≤ 200 ms'],
    ],
  })

  d.h(2, '5.3  慢查询与优化策略')
  d.table({
    caption: '表 5-2  常见慢查询与优化手段',
    headers: ['慢查询场景', '原因分析', '优化手段'],
    widths: [24, 34, 42],
    rows: [
      ['审计列表深分页（offset 很大）', 'OFFSET 逐行跳过，代价随页数线性增长', '改用游标分页（start_time + session_id 作为游标），或限制最大页数'],
      ['按命令关键字模糊查询', 'LIKE \'%kw%\' 无法使用 B-Tree 索引', '使用 GIN 全文索引或转 ES 检索，主库仅存热数据'],
      ['跨分区统计报表', '查询跨越多个历史分区，扫描量大', '走只读副本 + 预聚合统计表（按日汇总），报表读汇总表'],
      ['大表 JOIN 资产与账号', '多表关联在大数据量下代价高', '在明细表冗余资产名与账号名快照，避免 JOIN'],
      ['会话列表带录像状态筛选', '低选择性字段导致全表扫描', '建立部分索引，或将会话状态物化到冗余列'],
      ['实时并发会话统计', '每次聚合计算，压力集中', '使用 Redis 计数器 + 定时落库，避免实时聚合'],
    ],
  })
  d.ul([
    '慢查询日志阈值 500 ms，每日汇总 Top 20 慢查询并纳入周度优化清单；',
    '上线前对核心查询执行 EXPLAIN ANALYZE，确认走索引且无 Seq Scan（小表除外）；',
    '禁止在生产库执行未经审核的 DDL 与大批量 UPDATE / DELETE；',
    '统计信息定期 ANALYZE，避免执行计划因统计过期而退化。',
  ])

  // ================= 6 分区、归档与容量 =================
  d.h(1, '6  分区、归档与容量')

  d.h(2, '6.1  分区策略')
  d.table({
    caption: '表 6-1  分区表与分区策略',
    headers: ['表名', '分区键', '分区粒度', '保留策略', '说明'],
    widths: [26, 18, 14, 22, 20],
    rows: [
      ['ops_session', 'start_time', '月度', '在线 6 个月后归档', '核心大表，按月范围分区'],
      ['ops_session_command', 'exec_time', '月度', '在线 6 个月后归档', '与会话分区对齐，避免跨分区关联'],
      ['ops_session_file', 'transfer_time', '月度', '在线 6 个月后归档', '数据量较小，可季度分区'],
      ['ops_key_usage', 'call_time', '月度', '在线 12 个月', '密钥调用明细'],
      ['audit_login_log', 'login_time', '月度', '在线 6 个月后归档', '合规要求 ≥ 6 个月'],
      ['audit_operation_log', 'operate_time', '月度', '在线 6 个月后归档', '管理操作留痕'],
      ['audit_sql_log', 'exec_time', '月度', '在线 6 个月后归档', 'SQL 审计明细'],
      ['audit_alert', 'trigger_time', '月度', '在线 12 个月后归档', '告警记录'],
      ['acl_grant_log', 'operate_time', '月度', '在线 12 个月', '授权变更历史'],
      ['ast_account_history', 'rotate_time', '月度', '在线 12 个月', '改密历史'],
      ['sys_log', 'log_time', '日度', '在线 30 天', '运行日志，量大但价值周期短'],
    ],
  })
  d.note('分区管理要求', [
    '一、分区由定时任务提前创建（提前 3 个月），避免写入时因分区缺失而失败；',
    '二、分区创建与归档脚本纳入版本管理，执行结果记录到 sys_log 并告警失败；',
    '三、归档前校验目标存储可写且校验和一致，归档完成后才允许 DETACH 分区；',
    '四、历史分区设为只读，禁止在历史分区上执行 UPDATE / DELETE。',
  ], 'warn')

  d.h(2, '6.2  归档与清理')
  d.table({
    caption: '表 6-2  归档流程与校验',
    headers: ['阶段', '操作内容', '校验点', '失败处理'],
    widths: [18, 40, 24, 18],
    rows: [
      ['准备', '创建目标归档表 / 外部存储路径', '目标可写、空间充足', '中止并告警'],
      ['导出', '按分区导出为压缩文件', '行数与源分区一致', '重试，最多 3 次'],
      ['校验', '比对记录数与关键字段摘要', '摘要一致、抽样可读', '不一致则中止并保留源分区'],
      ['入库', '导入归档库并建索引', '可正常查询', '回滚导入'],
      ['切换', '应用侧查询路由到归档库', '查询结果与归档前一致', '回退路由'],
      ['清理', 'DETACH 并删除源分区', '归档确认完成后执行', '禁止提前删除'],
    ],
  })

  d.h(2, '6.3  数据分布与加密策略')
  d.p('不同数据类别按敏感级别选择存储位置、加密方式与留存周期，兼顾安全与成本。')
  d.image(IMG('db-datamap'), { maxWidth: 490, alt: '数据分布与加密策略', caption: '图 4  数据分布、加密与留存策略' })

  d.h(2, '6.4  容量估算')
  d.p('以 2000 资产、500 并发会话的中型规模为基准，数据库容量估算如下（不含对象存储中的录像文件）：')
  d.table({
    caption: '表 6-3  数据库容量估算（3 年）',
    headers: ['数据类别', '单行大小（估）', '日增量', '3 年总量', '说明'],
    widths: [22, 16, 18, 18, 26],
    rows: [
      ['ops_session', '≈ 1.2 KB', '≈ 3,000 行', '≈ 3.3 百万行 / 4 GB', '按 500 并发 × 6 次/日估算'],
      ['ops_session_command', '≈ 0.8 KB', '≈ 120,000 行', '≈ 1.3 亿行 / 104 GB', '按每会话 40 条命令估算'],
      ['ops_session_file', '≈ 0.6 KB', '≈ 6,000 行', '≈ 6.6 百万行 / 4 GB', '文件传输记录'],
      ['audit_login_log', '≈ 0.5 KB', '≈ 2,000 行', '≈ 2.2 百万行 / 1.1 GB', '含失败尝试'],
      ['audit_operation_log', '≈ 1.5 KB', '≈ 1,000 行', '≈ 1.1 百万行 / 1.6 GB', '含变更前后快照'],
      ['audit_sql_log', '≈ 1.5 KB', '≈ 20,000 行', '≈ 2.2 千万行 / 33 GB', '数据库运维场景'],
      ['audit_alert', '≈ 1 KB', '≈ 500 行', '≈ 55 万行 / 0.5 GB', '告警记录'],
      ['主数据与配置', '—', '少量增长', '≈ 2 GB', '用户、资产、策略等'],
      ['索引与索引膨胀', '约为数据量 30%', '—', '≈ 45 GB', '含分区本地索引'],
      ['合计（在线 6 个月）', '—', '—', '≈ 60 GB 可用容量', '建议配置 100 GB 并预留余量'],
      ['合计（含 3 年归档）', '—', '—', '≈ 200 GB', '归档库可使用低成本存储'],
    ],
  })
  d.note('容量规划建议', [
    '一、上表为数据库容量，录像文件不计入（录像存对象存储，3 年约 90 TB，见规划文档第 9 章）；',
    '二、命令明细表是数据库容量主要增长点，若会话量与命令数高于估算，优先通过缩短在线保留期缓解；',
    '三、建议按 3 年预估并预留 30% 余量，避免上线一年内即需扩容；',
    '四、分区数量控制在合理范围（单表 ≤ 60 个分区），过多分区会影响查询计划生成效率。',
  ], 'info')

  // ================= 7 数据安全设计 =================
  d.h(1, '7  数据安全设计')

  d.h(2, '7.1  加密策略')
  d.table({
    caption: '表 7-1  字段级加密策略',
    headers: ['字段', '加密算法', '密钥管理', '说明'],
    widths: [24, 18, 30, 28],
    rows: [
      ['sys_user.password_hash', 'SM3 加盐摘要', '不涉及密钥', '不可逆，仅用于校验'],
      ['sys_user.otp_secret_cipher', 'SM4-CBC', '密钥在密码机，索引存 km_key', 'OTP 种子密钥'],
      ['sys_user.mobile_cipher', 'SM4-CBC', '同上', '手机号，展示时脱敏'],
      ['ast_account.secret_cipher', 'SM4-CBC', '密钥在密码机，一次一密 + KEK 保护', '资产托管口令与私钥'],
      ['ops_session_command.cmd_text', 'SM4-CBC（可选）', '同上', '命令原文，高敏环境启用'],
      ['audit_sql_log.sql_text', 'SM4-CBC（可选）', '同上', 'SQL 原文，含业务数据时启用'],
      ['sys_config.config_value', 'SM4-CBC', '同上', 'is_encrypted = true 的配置项'],
      ['km_key 全表', '不存储密钥材料', '—', '仅存索引与元数据'],
    ],
  })

  d.h(2, '7.2  完整性保护')
  d.p('审计类表通过摘要链保证不可篡改：每条记录写入时计算 record_hash，并将前一条记录的摘要存入 prev_hash，形成链式结构。定期校验任务从链首重算，任意篡改都会导致后续摘要不匹配。')
  d.table({
    caption: '表 7-2  审计表完整性字段',
    headers: ['表名', '摘要字段', '链式字段', '校验频率'],
    widths: [26, 20, 20, 34],
    rows: [
      ['audit_login_log', 'record_hash', '—（独立校验）', '每日增量校验'],
      ['audit_operation_log', 'record_hash', 'prev_hash', '每日增量 + 每周全量'],
      ['ops_session_command', 'record_hash', 'prev_hash', '每日增量 + 每周全量'],
      ['ops_session', 'record_digest', '—（录像整体摘要）', '会话结束时写入，每日校验'],
    ],
  })

  d.h(2, '7.3  数据脱敏')
  d.ul([
    '手机号：展示为 138****8888，仅安全管理员与本人可见完整号码；',
    '身份证号：展示为 110***********1234，导出时默认脱敏；',
    '资产口令：任何界面均不展示明文，查看口令需二次授权并全程留痕；',
    '命令中的敏感参数：支持配置正则规则对命令原文中的口令参数脱敏后再落库与展示；',
    '个人信息最小化：仅在必要时采集，人员离职后按策略脱敏或删除，操作留痕。',
  ])

  d.h(2, '7.4  权限隔离')
  d.p('数据库层与应用层双重隔离：')
  d.table({
    caption: '表 7-3  隔离层次与措施',
    headers: ['层次', '措施', '防止的问题'],
    widths: [20, 46, 34],
    rows: [
      ['数据库账号', '按用途划分读写、只读、审计只读、备份账号，最小权限', '越权访问与误操作'],
      ['行级隔离', '业务查询强制带 tenant_id 条件，可选启用数据库 RLS', '跨租户数据泄露'],
      ['应用层', '数据访问层统一注入租户条件，禁止业务代码手写 SQL', '漏加隔离条件'],
      ['审计只读', '审计账号无任何写权限，且不可访问密钥与口令字段', '审计数据被篡改'],
      ['敏感操作', '口令查看、密钥轮换、数据导出需二次授权与双人复核', '内部人员滥用'],
    ],
  })

  d.h(2, '7.5  合规要求落实')
  d.table({
    caption: '表 7-4  合规要求与数据库实现对照',
    headers: ['合规要求', '来源', '数据库实现'],
    widths: [26, 24, 50],
    rows: [
      ['日志留存不少于六个月', '网络安全法第二十一条', '审计表在线 ≥ 6 个月，分区归档 ≥ 3 年'],
      ['审计记录不可删除修改', '等保 2.0 安全审计', '审计表禁用逻辑删除；摘要链防篡改；权限隔离'],
      ['数据完整性保护', 'GB/T 39786', '关键字段 SM3 摘要 + 摘要链 + 定期校验'],
      ['数据保密性保护', 'GB/T 39786', '敏感字段 SM4 加密，密钥在密码机内'],
      ['鉴别信息复杂度与更换', '等保 2.0 身份鉴别', 'password_update_time 支持有效期校验'],
      ['个人信息保护', '个人信息保护法', '最小化采集 + 脱敏展示 + 离职清理策略'],
      ['备份与恢复', '等保 2.0 数据备份恢复', '全量 + 增量 + WAL 归档，季度恢复演练'],
    ],
  })

  // ================= 8 备份与恢复 =================
  d.h(1, '8  备份与恢复')

  d.h(2, '8.1  备份策略')
  d.table({
    caption: '表 8-1  备份策略',
    headers: ['备份类型', '频率', '保留', '存储位置', '加密'],
    widths: [18, 18, 18, 26, 20],
    rows: [
      ['全量备份', '每日 02:00', '本地 7 份', '本地备份存储', 'SM4 加密'],
      ['增量备份', '每小时', '本地 24 份', '本地备份存储', 'SM4 加密'],
      ['WAL 归档', '实时', '直至全量覆盖', '本地 + 异地', 'SM4 加密'],
      ['异地备份', '每日同步', '90 天', '异地对象存储', 'SM4 加密'],
      ['归档库备份', '每周', '12 份', '异地对象存储', 'SM4 加密'],
    ],
  })

  d.h(2, '8.2  恢复流程')
  d.ol([
    '确认恢复目标：明确恢复时间点（PITR）或恢复范围（单表 / 全库）；',
    '评估影响：通知相关方，确认恢复期间的业务降级方案；',
    '准备环境：在隔离环境执行恢复演练，验证备份可用性；',
    '执行恢复：恢复全量备份 → 应用增量 → 回放 WAL 到目标时间点；',
    '数据校验：比对关键表行数、摘要与业务抽样数据；',
    '审批与切换：恢复结果经双人复核与审批后切换业务流量；',
    '记录归档：记录恢复原因、范围、耗时与结果，纳入运维档案。',
  ], { prefix: '第 ', sep: ' 步：' })
  d.note('恢复演练要求', [
    '一、每季度执行一次恢复演练，覆盖「单表误删」「全库恢复到指定时间点」两类场景；',
    '二、演练在隔离环境进行，禁止直接在生产库演练；',
    '三、演练报告需包含恢复耗时、数据校验结果与发现的问题及改进措施；',
    '四、备份文件每半年做一次可解密性与完整性全量校验，防止备份本身失效。',
  ], 'ok')

  // ================= 9 数据库运维规范 =================
  d.h(1, '9  数据库运维规范')

  d.h(2, '9.1  变更管理')
  d.table({
    caption: '表 9-1  数据库变更管理要求',
    headers: ['变更类型', '审批要求', '执行要求', '回滚方案'],
    widths: [20, 24, 32, 24],
    rows: [
      ['新增表 / 索引', '技术负责人审批', '业务低峰期执行，加索引使用 CONCURRENTLY', 'DROP 新增对象'],
      ['字段变更', '技术负责人 + 安全负责人审批', '先加列后写数据，禁止直接改类型', '保留原列并回退应用'],
      ['数据订正', '技术负责人 + 业务方审批', '限定条件与行数，先备份受影响数据', '用备份数据回滚'],
      ['分区创建 / 归档', '运维负责人审批', '自动化脚本执行，结果告警', '保留源分区直至校验通过'],
      ['权限变更', '安全管理员审批', '双人复核，操作留痕', '恢复原权限配置'],
    ],
  })

  d.h(2, '9.2  监控指标')
  d.table({
    caption: '表 9-2  数据库监控指标与阈值',
    headers: ['指标', '告警阈值', '级别', '处置建议'],
    widths: [26, 22, 14, 38],
    rows: [
      ['连接数使用率', '≥ 80% 持续 5 分钟', '重要', '排查连接泄漏，必要时扩容连接池'],
      ['主备复制延迟', '≥ 30 秒', '紧急', '检查网络与主库压力，必要时暂停大批量写入'],
      ['慢查询数量', '≥ 50 条/小时', '一般', '分析慢查询日志，优化索引或 SQL'],
      ['磁盘使用率', '≥ 80%', '重要', '清理过期分区或扩容'],
      ['事务回滚率', '≥ 5%', '一般', '排查应用异常与锁冲突'],
      ['锁等待时长', '≥ 10 秒', '重要', '定位阻塞会话并处理'],
      ['备份任务结果', '失败', '紧急', '立即重试并人工确认'],
      ['分区剩余月份', '≤ 3 个月', '一般', '提前创建分区'],
      ['表膨胀率', '≥ 50%', '提示', '安排 VACUUM / 重建'],
      ['审计摘要链校验', '校验失败', '紧急', '立即排查篡改或数据损坏，启动调查'],
    ],
  })

  d.h(2, '9.3  常见故障处理')
  d.table({
    caption: '表 9-3  常见故障与处理',
    headers: ['故障现象', '可能原因', '处理步骤', '预防措施'],
    widths: [22, 24, 34, 20],
    rows: [
      ['主库不可写', '磁盘满 / 进程崩溃 / 脑裂', '确认状态并切换备库，排查根因后修复', '磁盘水位告警、健康检查'],
      ['复制中断', '网络抖动、主库 WAL 被清理', '重建复制槽或从备份重建备库', '监控复制延迟与槽位'],
      ['连接池耗尽', '慢查询堆积、连接未释放', '定位长事务与阻塞会话并终止，扩容连接池', '连接超时、慢查询治理'],
      ['写入变慢', '索引过多、分区缺失、锁等待', '检查锁与执行计划，补齐分区', '索引评审、分区预创建'],
      ['查询超时', '统计信息过期、计划退化', 'ANALYZE 后复测，必要时固化计划', '定期 ANALYZE 与计划评审'],
      ['备份失败', '空间不足、权限变更', '清理空间、恢复权限后重跑', '备份空间与权限监控'],
    ],
  })

  // ================= 10 附录 =================
  d.h(1, '附录 A  建表规范速查')
  d.table({
    caption: '表 A-1  建表强制规范',
    headers: ['编号', '规范要求'],
    widths: [12, 88],
    rows: [
      ['R-01', '所有业务表必须有主键，主键为 BIGINT + 序列，禁止使用业务字段作主键'],
      ['R-02', '除租户表外，所有业务表必须包含 tenant_id 且非空，参与所有唯一索引'],
      ['R-03', '枚举类字段使用 VARCHAR + CHECK 约束，禁止使用无注释的数字编码'],
      ['R-04', '时间字段统一 TIMESTAMP（无时区），命名以 _time 结尾'],
      ['R-05', '审计类表禁止 is_deleted 字段，禁止物理删除'],
      ['R-06', '金额与比率使用 NUMERIC，禁止 FLOAT / DOUBLE'],
      ['R-07', '大文本字段使用 TEXT，禁止 VARCHAR(4000) 以上的定长定义'],
      ['R-08', '外键约束在应用层保证，数据库层按需启用（大表可不建物理外键以降低写入开销）'],
      ['R-09', '表与字段必须填写注释（COMMENT），确保数据字典可自动生成'],
      ['R-10', '索引命名规则：uk_（唯一）、idx_（普通），名称包含表名与字段缩写'],
      ['R-11', '分区表必须提前创建分区，禁止依赖自动创建'],
      ['R-12', '所有 DDL 变更通过迁移脚本执行并纳入版本库，禁止手工改表'],
    ],
  })

  d.h(1, '附录 B  常用查询示例')
  d.code([
    '-- 1. 查询某用户近 7 天的会话（走 idx_session_user_time）',
    'SELECT session_no, asset_name_snap, protocol, start_time, end_time,',
    '       command_count, block_count, record_status',
    'FROM ops_session',
    "WHERE tenant_id = :tenantId AND user_id = :userId",
    "  AND start_time >= now() - interval '7 days'",
    'ORDER BY start_time DESC LIMIT 100;',
    '',
    '-- 2. 查询某会话的全部阻断命令（走部分索引 idx_cmd_action）',
    'SELECT seq_no, cmd_brief, exec_time, policy_id',
    'FROM ops_session_command',
    "WHERE session_id = :sessionId AND action <> 'allow'",
    'ORDER BY seq_no;',
    '',
    '-- 3. 统计近 30 天各用户的高危操作次数',
    'SELECT s.user_id, s.username_snap, count(*) AS block_cnt',
    'FROM ops_session s JOIN ops_session_command c ON c.session_id = s.session_id',
    "WHERE s.tenant_id = :tenantId AND c.action = 'block'",
    "  AND c.exec_time >= now() - interval '30 days'",
    'GROUP BY s.user_id, s.username_snap ORDER BY block_cnt DESC LIMIT 20;',
    '',
    '-- 4. 扫描即将到期的托管账号口令',
    'SELECT a.account_id, ast.asset_name, a.account_name, a.next_rotate_time',
    'FROM ast_account a JOIN ast_asset ast ON ast.asset_id = a.asset_id',
    'WHERE a.is_managed = true AND a.next_rotate_time <= now() + interval \'7 days\'',
    'ORDER BY a.next_rotate_time;',
    '',
    '-- 5. 租户配额超限检查',
    'SELECT t.tenant_id, t.tenant_name, t.quota_asset,',
    '       (SELECT count(*) FROM ast_asset a WHERE a.tenant_id = t.tenant_id AND a.is_deleted = false) AS used',
    'FROM sys_tenant t WHERE t.status = \'active\';',
  ])

  d.h(1, '附录 C  初始化脚本清单')
  d.table({
    caption: '表 C-1  数据库初始化脚本',
    headers: ['序号', '脚本文件', '内容', '执行时机'],
    widths: [10, 30, 40, 20],
    rows: [
      ['1', 'V1.0__create_database.sql', '创建数据库、字符集、时区与基础账号', '首次部署'],
      ['2', 'V1.0__create_tables.sql', '创建 48 张业务表与约束', '首次部署'],
      ['3', 'V1.0__create_indexes.sql', '创建全部索引', '建表后'],
      ['4', 'V1.0__create_partitions.sql', '创建初始分区与分区维护函数', '建表后'],
      ['5', 'V1.0__init_dict.sql', '初始化数据字典（协议、类型、级别等）', '首次部署'],
      ['6', 'V1.0__init_permission.sql', '初始化权限项与三员内置角色', '首次部署'],
      ['7', 'V1.0__init_alert_rule.sql', '初始化内置告警规则', '首次部署'],
      ['8', 'V1.0__init_config.sql', '初始化系统参数默认值', '首次部署'],
      ['9', 'V1.0__create_partition_job.sql', '创建分区自动维护任务', '首次部署'],
      ['10', 'V1.0__create_verify_job.sql', '创建摘要链校验任务', '首次部署'],
      ['11', 'V1.1__add_xxx.sql', '后续版本变更脚本（示例）', '版本升级'],
    ],
  })
  d.note('执行要求', [
    '一、脚本按版本号顺序执行，每步执行后可重复执行（幂等），避免中断后无法续跑；',
    '二、初始化脚本执行前必须完成数据库备份，执行结果记录到 sys_init_record 与 sys_log；',
    '三、脚本纳入版本库并通过迁移工具执行，禁止手工修改生产库结构；',
    '四、初始化完成后执行校验脚本，确认表、索引、分区与字典数据齐全。',
  ], 'ok')
}
