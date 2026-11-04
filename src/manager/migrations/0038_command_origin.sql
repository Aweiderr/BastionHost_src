-- ============================================================================
-- 0038 命令来源：用户输入 / 客户端自动
--
-- Oracle 客户端（SQL*Plus、JDBC、OCI）连上后会自己发身份探测等语句。
-- 这些语句以前和手工 SQL 记在同一张命令审计里，看起来像是人敲的。
--
-- 本迁移只增加来源字段，不删除、不隐藏任何历史语句。
-- 已有行默认仍是 user。只有与 JDBC/OCI 身份探测整句一致的历史文本回填为
-- client_auto；其它语句没有可靠信号，不靠模糊文本回填，避免把人工 SQL 标错。
-- 新的 Oracle 流量由代理在入库前写入 client_auto 或 user。
-- ============================================================================

ALTER TABLE ops_session_command
    ADD COLUMN IF NOT EXISTS command_origin VARCHAR(16) NOT NULL DEFAULT 'user';

COMMENT ON COLUMN ops_session_command.command_origin IS
    '命令来源：user（用户输入，缺省）/ client_auto（客户端自动发出）。只用于区分展示，不改变策略处置。';

ALTER TABLE ops_session_command
    DROP CONSTRAINT IF EXISTS ck_command_origin;

ALTER TABLE ops_session_command
    ADD CONSTRAINT ck_command_origin CHECK (command_origin IN ('user', 'client_auto'));

-- 筛「仅客户端自动」时用。绝大多数行是 user，不给这列建全量索引。
CREATE INDEX IF NOT EXISTS idx_cmd_client_auto
    ON ops_session_command (tenant_id, command_time DESC)
    WHERE command_origin = 'client_auto';

-- 已经落库的 JDBC/OCI 身份探测。只认整句去掉空白和末尾分号后与驱动文本一致的行。
-- SELECT USER FROM DUAL 以及带 WHERE 的变体都对不上，不会被改标。
UPDATE ops_session_command
   SET command_origin = 'client_auto'
 WHERE command_origin = 'user'
   AND upper(regexp_replace(rtrim(btrim(command_text), ';'), '[[:space:]]', '', 'g')) IN (
        'SELECTDECODE(USER,''XS$NULL'',XS_SYS_CONTEXT(''XS$SESSION'',''USERNAME''),USER)FROMSYS.DUAL',
        'SELECTDECODE(USER,''XS$NULL'',XS_SYS_CONTEXT(''XS$SESSION'',''USERNAME''),USER)FROMDUAL'
   );
