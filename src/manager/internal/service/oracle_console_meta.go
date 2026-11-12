package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/errcode"
	sharedmodel "bastion.local/shared/model"
	"bastion.local/shared/policy"
)

// 对象树只跑这几条固定的只读字典查询。前端不能把自己的 SQL 送进来枚举对象。
const (
	oracleMetaSchemas = `SELECT USERNAME FROM ALL_USERS ORDER BY USERNAME`
	oracleMetaTables  = `SELECT TABLE_NAME FROM ALL_TABLES WHERE OWNER = :1 ORDER BY TABLE_NAME`
	oracleMetaViews   = `SELECT VIEW_NAME FROM ALL_VIEWS WHERE OWNER = :1 ORDER BY VIEW_NAME`
	oracleMetaColumns = `SELECT COLUMN_NAME, DATA_TYPE, DATA_LENGTH, DATA_PRECISION, DATA_SCALE, NULLABLE FROM ALL_TAB_COLUMNS WHERE OWNER = :1 AND TABLE_NAME = :2 ORDER BY COLUMN_ID`
)

var oracleIdentRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]{0,127}$`)

// OracleMetaInput 是对象树请求。填写地址、口令或 SQL 会被拒绝。
type OracleMetaInput struct {
	Schema          string `json:"schema,omitempty"`
	Name            string `json:"name,omitempty"`
	SQL             string `json:"sql,omitempty"`
	Host            string `json:"host,omitempty"`
	Port            int    `json:"port,omitempty"`
	DSN             string `json:"dsn,omitempty"`
	ServiceName     string `json:"service_name,omitempty"`
	Password        string `json:"password,omitempty"`
	BastionPassword string `json:"bastion_password,omitempty"`
	DBPassword      string `json:"db_password,omitempty"`
	PrivateKey      string `json:"private_key,omitempty"`
}

// OracleSchemaList 是当前账号能看见的用户。
type OracleSchemaList struct {
	Schemas   []string `json:"schemas"`
	Truncated bool     `json:"truncated,omitempty"`
}

// OracleObject 是一张表或一个视图。
type OracleObject struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// OracleObjectList 是某个 schema 下的表和视图。
type OracleObjectList struct {
	Schema    string         `json:"schema"`
	Objects   []OracleObject `json:"objects"`
	Truncated bool           `json:"truncated,omitempty"`
}

// OracleColumn 是一列。Type 已拼好长度或精度，界面不用再解释数据字典。
type OracleColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

// OracleColumnList 是某张表或视图的列。
type OracleColumnList struct {
	Schema    string         `json:"schema"`
	Name      string         `json:"name"`
	Columns   []OracleColumn `json:"columns"`
	Truncated bool           `json:"truncated,omitempty"`
}

// Schemas 列出 ALL_USERS。必须走已经打开的会话连接。
func (c *OracleConsole) Schemas(ctx context.Context, actor OracleActor, sessionID int64, in OracleMetaInput) (*OracleSchemaList, error) {
	if msg := rejectOracleMeta(in); msg != "" {
		return nil, errcode.New(errcode.ErrBadRequest, msg)
	}
	live, unlock, err := c.lockLive(ctx, actor, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	table, err := c.dictionaryQuery(ctx, live, oracleMetaSchemas, oracleMetaSchemas, nil, "ALL_USERS")
	if err != nil {
		return nil, err
	}
	out := &OracleSchemaList{Schemas: make([]string, 0, len(table.Rows)), Truncated: table.Truncated}
	for _, row := range table.Rows {
		name := oracleCell(row, 0)
		if name != "" {
			out.Schemas = append(out.Schemas, name)
		}
	}
	return out, nil
}

// Objects 列出某个用户下的表和视图。
func (c *OracleConsole) Objects(ctx context.Context, actor OracleActor, sessionID int64, in OracleMetaInput) (*OracleObjectList, error) {
	if msg := rejectOracleMeta(in); msg != "" {
		return nil, errcode.New(errcode.ErrBadRequest, msg)
	}
	owner, err := canonicalOracleIdent(in.Schema)
	if err != nil {
		return nil, err
	}
	live, unlock, err := c.lockLive(ctx, actor, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	tables, err := c.dictionaryQuery(ctx, live, oracleMetaLiteral(oracleMetaTables, owner), oracleMetaTables, []any{owner}, "ALL_TABLES")
	if err != nil {
		return nil, err
	}
	views, err := c.dictionaryQuery(ctx, live, oracleMetaLiteral(oracleMetaViews, owner), oracleMetaViews, []any{owner}, "ALL_VIEWS")
	if err != nil {
		return nil, err
	}
	out := &OracleObjectList{
		Schema:    owner,
		Objects:   make([]OracleObject, 0, len(tables.Rows)+len(views.Rows)),
		Truncated: tables.Truncated || views.Truncated,
	}
	for _, row := range tables.Rows {
		if name := oracleCell(row, 0); name != "" {
			out.Objects = append(out.Objects, OracleObject{Name: name, Kind: "table"})
		}
	}
	for _, row := range views.Rows {
		if name := oracleCell(row, 0); name != "" {
			out.Objects = append(out.Objects, OracleObject{Name: name, Kind: "view"})
		}
	}
	return out, nil
}

// Columns 列出某张表或视图的列。表和视图都在 ALL_TAB_COLUMNS 里。
func (c *OracleConsole) Columns(ctx context.Context, actor OracleActor, sessionID int64, in OracleMetaInput) (*OracleColumnList, error) {
	if msg := rejectOracleMeta(in); msg != "" {
		return nil, errcode.New(errcode.ErrBadRequest, msg)
	}
	owner, err := canonicalOracleIdent(in.Schema)
	if err != nil {
		return nil, err
	}
	name, err := canonicalOracleIdent(in.Name)
	if err != nil {
		return nil, err
	}
	live, unlock, err := c.lockLive(ctx, actor, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	auditSQL := fmt.Sprintf(
		"SELECT COLUMN_NAME, DATA_TYPE, DATA_LENGTH, DATA_PRECISION, DATA_SCALE, NULLABLE FROM ALL_TAB_COLUMNS WHERE OWNER = '%s' AND TABLE_NAME = '%s' ORDER BY COLUMN_ID",
		owner, name,
	)
	table, err := c.dictionaryQuery(ctx, live, auditSQL, oracleMetaColumns, []any{owner, name}, "ALL_TAB_COLUMNS")
	if err != nil {
		return nil, err
	}
	out := &OracleColumnList{Schema: owner, Name: name, Columns: make([]OracleColumn, 0, len(table.Rows)), Truncated: table.Truncated}
	for _, row := range table.Rows {
		col := oracleCell(row, 0)
		if col == "" {
			continue
		}
		out.Columns = append(out.Columns, OracleColumn{
			Name:     col,
			Type:     formatOracleDataType(oracleCell(row, 1), oracleCell(row, 2), oracleCell(row, 3), oracleCell(row, 4)),
			Nullable: strings.EqualFold(oracleCell(row, 5), "Y"),
		})
	}
	return out, nil
}

// dictionaryQuery 把固定字典 SQL 先过命令策略再执行，并按 system 写入审计。
// auditSQL 是审计和策略看到的文本（标识符已校验，可以写成字面量）。
// execSQL 用绑定变量执行，避免把名字拼进真正送到数据库的语句。
func (c *OracleConsole) dictionaryQuery(ctx context.Context, live *oracleLive, auditSQL, execSQL string, args []any, dict string) (OracleTable, error) {
	if !strings.Contains(execSQL, dict) || !strings.Contains(auditSQL, dict) {
		return OracleTable{}, errcode.New(errcode.ErrInternal, "元数据查询不是固定的数据字典语句")
	}
	decision, err := c.judge(ctx, live, auditSQL)
	if err != nil {
		return OracleTable{}, err
	}
	blocked := decision != nil && (decision.Action == policy.ActionDeny || decision.Action == policy.ActionConfirm)
	live.seq++
	resultText := "submitted"
	if blocked {
		resultText = "blocked"
		live.blocked++
	}
	cmd := &model.SessionCommand{
		SeqNo: live.seq, CommandText: auditSQL, CommandType: "sql",
		CommandOrigin: sharedmodel.CommandOriginSystem,
		ExecResult:    resultText, PolicyAction: oracleAction(decision), RiskLevel: oracleRisk(decision),
		MatchedRule: truncateRunes(oracleRule(decision), 254),
		CommandTime: time.Now(),
	}
	if decision != nil && decision.MatchedRule != nil && decision.MatchedRule.PolicyID != 0 {
		id := decision.MatchedRule.PolicyID
		cmd.PolicyID = &id
	}
	if _, err := c.sessions.AddCommand(ctx, live.info.SessionID, cmd); err != nil {
		return OracleTable{}, errcode.New(errcode.ErrInternal, "命令审计写入失败，已拒绝读取对象")
	}
	live.last = c.now()
	if blocked {
		msg := "对象树查询已被命令策略阻断"
		if rule := oracleRule(decision); rule != "" {
			msg += "：" + rule
		}
		return OracleTable{}, errcode.New(errcode.ErrForbidden, msg)
	}
	execCtx, cancel := context.WithTimeout(ctx, oracleExecTimeout)
	defer cancel()
	table, qerr := live.conn.Query(execCtx, execSQL, maxOracleMetaRows, args...)
	if qerr != nil {
		return OracleTable{}, errcode.New(errcode.ErrBadRequest, oracleUserError(qerr))
	}
	return table, nil
}

func (c *OracleConsole) lockLive(ctx context.Context, actor OracleActor, sessionID int64) (*oracleLive, func(), error) {
	live, err := c.liveFor(actor.UserID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	live.mu.Lock()
	unlock := func() { live.mu.Unlock() }
	if live.closed || live.conn == nil {
		unlock()
		return nil, nil, errcode.New(errcode.ErrBadRequest, "Oracle SQL 会话已结束")
	}
	if err := c.ensureActive(ctx, live); err != nil {
		unlock()
		return nil, nil, err
	}
	return live, unlock, nil
}

func rejectOracleMeta(in OracleMetaInput) string {
	if msg := rejectOracleOpenTarget(OracleOpenInput{
		Host: in.Host, Port: in.Port, DSN: in.DSN, ServiceName: in.ServiceName,
		Password: in.Password, BastionPassword: in.BastionPassword, DBPassword: in.DBPassword,
		PrivateKey: in.PrivateKey,
	}); msg != "" {
		return msg
	}
	if strings.TrimSpace(in.SQL) != "" {
		return "对象树不接受自行编写的 SQL，只查询固定的数据字典"
	}
	return ""
}

func canonicalOracleIdent(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errcode.New(errcode.ErrBadRequest, "请填写对象名")
	}
	if !oracleIdentRe.MatchString(s) {
		return "", errcode.New(errcode.ErrBadRequest, "对象名只能是 Oracle 标识符")
	}
	return strings.ToUpper(s), nil
}

// oracleMetaLiteral 把已校验的标识符写进审计文本，方便和用户 SQL 对照。
// 真正执行的语句仍用绑定变量。标识符不含引号，这里不会把额外 SQL 拼进去。
func oracleMetaLiteral(template, owner string) string {
	return strings.Replace(template, ":1", "'"+owner+"'", 1)
}

func oracleCell(row []*string, i int) string {
	if i < 0 || i >= len(row) || row[i] == nil {
		return ""
	}
	return strings.TrimSpace(*row[i])
}

func formatOracleDataType(dataType, length, precision, scale string) string {
	dt := strings.ToUpper(strings.TrimSpace(dataType))
	if dt == "" {
		return ""
	}
	switch dt {
	case "VARCHAR2", "NVARCHAR2", "CHAR", "NCHAR", "RAW":
		if length != "" {
			return dt + "(" + length + ")"
		}
	case "NUMBER":
		if precision != "" {
			if scale != "" && scale != "0" {
				return "NUMBER(" + precision + "," + scale + ")"
			}
			return "NUMBER(" + precision + ")"
		}
	}
	return dt
}
