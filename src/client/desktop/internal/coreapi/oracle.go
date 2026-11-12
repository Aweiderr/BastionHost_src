package coreapi

import (
	"context"
	"net/http"
	"time"
)

// OracleConsoleInfo 是产品内 SQL 会话。没有地址和口令。
type OracleConsoleInfo struct {
	SessionID   int64  `json:"session_id"`
	SessionNo   string `json:"session_no"`
	AssetName   string `json:"asset_name"`
	AccountName string `json:"account_name"`
}

// OracleSQLResult 是一条 SQL 的结果。
type OracleSQLResult struct {
	Kind         string      `json:"kind"`
	Columns      []string    `json:"columns"`
	Rows         [][]*string `json:"rows"`
	RowCount     int         `json:"row_count"`
	RowsAffected int64       `json:"rows_affected"`
	Truncated    bool        `json:"truncated"`
	Message      string      `json:"message"`
	Blocked      bool        `json:"blocked"`
	PolicyAction string      `json:"policy_action"`
	ElapsedMS    int64       `json:"elapsed_ms"`
}

// OracleSchemaList 是对象树的用户列表。
type OracleSchemaList struct {
	Schemas   []string `json:"schemas"`
	Truncated bool     `json:"truncated"`
}

// OracleObject 是一张表或一个视图。
type OracleObject struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// OracleObjectList 是某个用户下的表和视图。
type OracleObjectList struct {
	Schema    string         `json:"schema"`
	Objects   []OracleObject `json:"objects"`
	Truncated bool           `json:"truncated"`
}

// OracleColumn 是一列。
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
	Truncated bool           `json:"truncated"`
}

// OpenOracleConsole 在管理服务上打开 SQL 会话。请求里只有资产和账号。
func (c *Client) OpenOracleConsole(ctx context.Context, assetID, accountID int64) (*OracleConsoleInfo, error) {
	var out OracleConsoleInfo
	err := c.do(ctx, http.MethodPost, "/api/v1/ops/oracle/sessions", map[string]any{
		"asset_id": assetID, "account_id": accountID,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ExecOracleSQL 执行一条 SQL。超时比普通接口长，语句本身可能要等数据库。
func (c *Client) ExecOracleSQL(ctx context.Context, sessionID int64, sql string) (*OracleSQLResult, error) {
	hc := *c.http
	hc.Timeout = 40 * time.Second
	var out OracleSQLResult
	err := c.doHTTP(ctx, &hc, http.MethodPost, "/api/v1/ops/oracle/sessions/"+itoa(sessionID)+"/sql", map[string]any{
		"sql": sql,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseOracleConsole 结束产品内 SQL 会话。
func (c *Client) CloseOracleConsole(ctx context.Context, sessionID int64) error {
	return c.do(ctx, http.MethodPost, "/api/v1/ops/oracle/sessions/"+itoa(sessionID)+"/close", map[string]any{}, nil)
}

// OracleSchemas 读取已打开会话能看见的用户。请求不带 SQL。
func (c *Client) OracleSchemas(ctx context.Context, sessionID int64) (*OracleSchemaList, error) {
	var out OracleSchemaList
	err := c.do(ctx, http.MethodPost, "/api/v1/ops/oracle/sessions/"+itoa(sessionID)+"/schemas", map[string]any{}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// OracleObjects 读取某个用户下的表和视图。
func (c *Client) OracleObjects(ctx context.Context, sessionID int64, schema string) (*OracleObjectList, error) {
	var out OracleObjectList
	err := c.do(ctx, http.MethodPost, "/api/v1/ops/oracle/sessions/"+itoa(sessionID)+"/objects", map[string]any{
		"schema": schema,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// OracleColumns 读取某张表或视图的列。
func (c *Client) OracleColumns(ctx context.Context, sessionID int64, schema, name string) (*OracleColumnList, error) {
	var out OracleColumnList
	err := c.do(ctx, http.MethodPost, "/api/v1/ops/oracle/sessions/"+itoa(sessionID)+"/columns", map[string]any{
		"schema": schema, "name": name,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
