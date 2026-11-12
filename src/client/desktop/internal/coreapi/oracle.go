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
