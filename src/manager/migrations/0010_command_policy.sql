-- ============================================================================
-- 0010 命令策略与内置危险命令规则
--
-- 对应需求 FR-03（访问控制）与 FR-04（命令管控）。
--
-- 设计说明：
--   * 策略表由管理服务维护（配置面），策略引擎位于 shared/policy 由两侧共用；
--   * 服务端（协议代理）在会话中截获命令后调用引擎判定，实现阻断/确认/告警；
--   * 规则按 priority 升序匹配，**命中即返回**（deny 类规则应配置更小 priority）。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 命令策略
-- ---------------------------------------------------------------------------
CREATE TABLE policy_command (
    policy_id       BIGSERIAL    PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL DEFAULT 0,
    policy_name     VARCHAR(128) NOT NULL,
    policy_type     VARCHAR(16)  NOT NULL DEFAULT 'blacklist',
    match_type      VARCHAR(16)  NOT NULL DEFAULT 'regex',
    pattern         TEXT         NOT NULL,
    risk_level      VARCHAR(16)  NOT NULL DEFAULT 'warn',
    action          VARCHAR(16)  NOT NULL DEFAULT 'audit',
    scope           JSONB        NOT NULL DEFAULT '{}'::jsonb,
    priority        INTEGER      NOT NULL DEFAULT 100,
    enabled         BOOLEAN      NOT NULL DEFAULT true,
    is_builtin      BOOLEAN      NOT NULL DEFAULT false,
    timeout_seconds INTEGER      NOT NULL DEFAULT 30,
    remark          VARCHAR(512),
    create_by       BIGINT,
    create_time     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE policy_command IS '命令策略表（命令级管控规则）';
COMMENT ON COLUMN policy_command.policy_type IS '策略类型：blacklist（黑名单，命中拦截）/ whitelist（白名单，未命中拦截）';
COMMENT ON COLUMN policy_command.match_type IS '匹配方式：regex（正则）/ keyword（关键字）/ prefix（前缀）/ exact（精确）';
COMMENT ON COLUMN policy_command.action IS '处置动作：allow（放行）/ audit（放行并告警）/ confirm（二次确认）/ deny（阻断）';
COMMENT ON COLUMN policy_command.risk_level IS '风险等级：normal / warn / danger / critical';
COMMENT ON COLUMN policy_command.scope IS '适用范围：{"asset_ids":[],"asset_levels":[],"user_ids":[],"protocols":[]}，为空表示全部适用';
COMMENT ON COLUMN policy_command.timeout_seconds IS 'confirm 动作的等待超时（秒）';
CREATE UNIQUE INDEX uk_policy_name ON policy_command (tenant_id, policy_name);
CREATE INDEX idx_policy_priority ON policy_command (tenant_id, priority) WHERE enabled = true;
CREATE INDEX idx_policy_action ON policy_command (action) WHERE enabled = true;

-- ---------------------------------------------------------------------------
-- 内置危险命令规则
--
-- 说明：这些是业界通用的高危命令，出厂即启用；管理员可停用或调整处置动作。
-- priority 越小越先匹配；deny 类规则排在最前，避免被后续规则放行。
-- ---------------------------------------------------------------------------
INSERT INTO policy_command
    (tenant_id, policy_name, policy_type, match_type, pattern, risk_level, action, priority, enabled, is_builtin, remark)
VALUES
 -- ============ 灾难性删除与磁盘操作（阻断）============
 (0, '禁止删除根目录', 'blacklist', 'regex', 'rm\s+(-[a-zA-Z]*\s+)*(/|/\*|/\s|--no-preserve-root)',
  'critical', 'deny', 10, true, true, '删除根目录将导致系统不可用'),
 (0, '禁止通配符删除系统目录', 'blacklist', 'regex', 'rm\s+-[a-zA-Z]*[rf][a-zA-Z]*\s+/(bin|boot|dev|etc|lib|lib64|proc|root|sbin|sys|usr|var)\b',
  'critical', 'deny', 11, true, true, '删除系统关键目录'),
 (0, '禁止格式化文件系统', 'blacklist', 'regex', '\bmkfs(\.\w+)?\b',
  'critical', 'deny', 12, true, true, '格式化操作会清空磁盘数据'),
 (0, '禁止直接写裸设备', 'blacklist', 'regex', '(dd\s+[^|;]*of=/dev/(sd|hd|vd|nvme|disk))|(>\s*/dev/(sd|hd|vd|nvme)[a-z0-9]*)',
  'critical', 'deny', 13, true, true, '直接写裸设备会破坏磁盘'),
 (0, '禁止 fork 炸弹', 'blacklist', 'regex', ':\(\)\s*\{\s*:\|:&\s*\}\s*;\s*:',
  'critical', 'deny', 14, true, true, 'fork 炸弹会导致系统资源耗尽'),
 (0, '禁止清空防火墙规则', 'blacklist', 'regex', '\b(iptables|nft)\s+(-F|--flush)\b',
  'danger', 'deny', 15, true, true, '清空防火墙可能暴露内网'),
 (0, '禁止修改根目录权限', 'blacklist', 'regex', 'chmod\s+(-[a-zA-Z]+\s+)*777\s+/(\s|$)',
  'critical', 'deny', 16, true, true, '开放根目录权限是严重安全风险'),
 (0, '禁止清空命令历史', 'blacklist', 'regex', '(\bhistory\s+-c\b|>\s*~?/?\.?(bash_)?history\b|unset\s+HISTFILE\b|HISTFILE=/dev/null)',
  'danger', 'deny', 17, true, true, '清除历史痕迹属于规避审计行为'),

 -- ============ 系统与服务操作（二次确认）============
 (0, '关机与重启需确认', 'blacklist', 'regex', '\b(shutdown|reboot|halt|poweroff|init\s+0|init\s+6)\b',
  'danger', 'confirm', 30, true, true, '关机重启会中断业务'),
 (0, '停止关键服务需确认', 'blacklist', 'regex', '\bsystemctl\s+(stop|disable|mask)\s+(sshd|network|NetworkManager|firewalld|docker)\b',
  'danger', 'confirm', 31, true, true, '停止关键服务可能影响运维通道'),
 (0, '强制杀进程需确认', 'blacklist', 'regex', '\bkill\s+-9\s+-1\b|\bkillall\s+-9\b',
  'danger', 'confirm', 32, true, true, '批量强杀进程风险高'),
 (0, '磁盘配额与挂载变更需确认', 'blacklist', 'regex', '\b(umount|mount)\s+/',
  'warn', 'confirm', 33, true, true, '挂载变更可能影响在线业务'),
 (0, '清理日志需确认', 'blacklist', 'regex', '>\s*/var/log/|truncate\s+-s\s*0\s+/var/log/|find\s+/var/log\s+-delete',
  'danger', 'confirm', 34, true, true, '清理系统日志影响故障排查与审计'),

 -- ============ 账号与权限变更（放行并告警）============
 -- 说明：match_type='command' 表示匹配"命令名"（逗号分隔），
 -- 只看每条子命令实际执行的程序，不会命中 `cat /etc/passwd`
 -- 或 `grep useradd /var/log/x` 这类只读命令中的同名参数。
 (0, '账号变更告警', 'blacklist', 'command', 'useradd,userdel,usermod,groupadd,groupdel,chpasswd,gpasswd,passwd',
  'warn', 'audit', 50, true, true, '账号与口令变更需留痕'),
 (0, '提权配置变更告警', 'blacklist', 'regex', '\b(visudo|/etc/sudoers)\b|\becho\s+[^|;]*>>?\s*/etc/sudoers',
  'danger', 'audit', 51, true, true, 'sudo 配置变更属高敏操作'),
 (0, 'SSH 免密配置变更告警', 'blacklist', 'regex', '(>>?\s*~?/?\.ssh/authorized_keys)|(ssh-keygen\b)|(chmod\s+600\s+~?/?\.ssh/)',
  'warn', 'audit', 52, true, true, 'SSH 密钥变更可能引入后门'),

 -- ============ 数据外发与下载（放行并告警）============
 (0, '数据外发告警', 'blacklist', 'regex', '\b(scp|rsync|sftp|nc|ncat|telnet)\s+[^|;]*(/etc/(passwd|shadow)|/root/|\.ssh/)',
  'danger', 'audit', 60, true, true, '敏感文件外发需审计'),
 (0, '下载并执行告警', 'blacklist', 'regex', '(curl|wget)\s+[^|;]*\|\s*(ba|z|k)?sh\b',
  'danger', 'audit', 61, true, true, '下载即执行存在供应链风险'),
 (0, '数据库导出告警', 'blacklist', 'command', 'mysqldump,pg_dump,exp,expdp',
  'warn', 'audit', 62, true, true, '数据库导出需留痕')
ON CONFLICT (tenant_id, policy_name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 系统配置补充：策略默认行为
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'policy.command.default_action', 'allow', 'string', 'security',
  '命令未命中任何策略时的默认动作：allow / audit / confirm / deny'),
 (0, 'policy.command.whitelist_mode', 'false', 'bool', 'security',
  '是否启用命令白名单模式（启用后仅放行白名单内命令，其余全部阻断）'),
 (0, 'policy.command.confirm_timeout', '30', 'int', 'security',
  '二次确认的默认等待超时（秒）')
ON CONFLICT (tenant_id, config_key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 工单号序列（保证并发下编号唯一）
-- ---------------------------------------------------------------------------
CREATE SEQUENCE IF NOT EXISTS wf_ticket_no_seq START 1;

-- ---------------------------------------------------------------------------
-- 工单相关默认配置
-- ---------------------------------------------------------------------------
INSERT INTO sys_config (tenant_id, config_key, config_value, value_type, category, description) VALUES
 (0, 'ticket.default_valid_hours', '4', 'int', 'ticket',
  '工单审批通过后的默认授权时长（小时）'),
 (0, 'ticket.emergency.enabled', 'true', 'bool', 'ticket',
  '是否启用紧急放行（事后必须补单）'),
 (0, 'ticket.emergency.max_per_month', '2', 'int', 'ticket',
  '每人每月紧急放行次数上限'),
 (0, 'ticket.emergency.supplement_hours', '24', 'int', 'ticket',
  '紧急放行后补单时限（小时）')
ON CONFLICT (tenant_id, config_key) DO NOTHING;
