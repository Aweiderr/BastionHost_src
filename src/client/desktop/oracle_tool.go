package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bastion.local/desktop/internal/coreapi"
	"bastion.local/desktop/internal/launch"
)

// LaunchOracleClient 打开本机 sqlplus（经 cmd）或 DBeaver，只连堡垒机 Oracle 网关。
func (a *App) LaunchOracleClient(tool string, assetID, accountID int64) (*LocalLaunchResult, error) {
	a.mu.Lock()
	user := a.user
	a.mu.Unlock()
	if user == nil {
		return nil, fmt.Errorf("尚未登录")
	}
	asset, err := a.lookupAsset(assetID)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(asset.Protocol), "oracle") {
		return nil, fmt.Errorf("本机 sqlplus / DBeaver 只用于 Oracle 资产")
	}
	account, err := accountOnAsset(asset, accountID, "")
	if err != nil {
		return nil, err
	}
	if account == "" {
		return nil, fmt.Errorf("请选择账号")
	}
	host, err := a.oracleGatewayHost(assetID, accountID)
	if err != nil {
		return nil, err
	}
	env := launch.DefaultEnv()
	if a.launchEnv != nil {
		env = *a.launchEnv
	}
	plan, err := launch.BuildOracle(launch.OracleRequest{
		Tool:        tool,
		OpsUser:     user.Username,
		AssetID:     asset.AssetID,
		Account:     account,
		GatewayHost: host,
		GatewayPort: 1521,
		AssetHost:   asset.Host,
		AssetPort:   asset.Port,
	}, env)
	if err != nil {
		return nil, err
	}
	start := a.launchStart
	if start == nil {
		start = launch.Start
	}
	if err := start(plan); err != nil {
		return nil, fmt.Errorf("无法启动本机工具：%s", err.Error())
	}
	return &LocalLaunchResult{
		Protocol: plan.Protocol,
		Tool:     plan.Tool,
		Command:  plan.Display,
		Target:   plan.Target,
		Login:    plan.Login,
		Message:  plan.Message,
		Started:  true,
	}, nil
}

func (a *App) oracleGatewayHost(assetID, accountID int64) (string, error) {
	core := ""
	if a.api != nil {
		core = a.api.Base()
	}
	fallback := oracleListenHost(true, "", core)
	if a.api == nil || assetID == 0 {
		return fallback, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	asg, err := a.api.AssignSession(ctx, assetID, accountID, "oracle", "desktop")
	if err != nil {
		if coreapi.AssignmentUnavailable(err) {
			return fallback, nil
		}
		return "", err
	}
	direct := asg == nil || asg.Direct
	addr := ""
	if asg != nil {
		addr = asg.SSHAddr
	}
	return oracleListenHost(direct, addr, core), nil
}

// OpenOracleConsole 在管理服务上打开 SQL 会话。界面不提供口令。
func (a *App) OpenOracleConsole(assetID, accountID int64) (*coreapi.OracleConsoleInfo, error) {
	if err := a.requireOracleUser(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	info, err := a.api.OpenOracleConsole(ctx, assetID, accountID)
	if err != nil {
		return nil, err
	}
	if info != nil {
		a.rememberOracle(info.SessionID)
	}
	return info, nil
}

// ExecOracleSQL 把一条 SQL 交给管理服务执行。
func (a *App) ExecOracleSQL(sessionID int64, sql string) (*coreapi.OracleSQLResult, error) {
	if err := a.requireOracleUser(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return a.api.ExecOracleSQL(ctx, sessionID, sql)
}

// CloseOracleConsole 结束产品内 SQL 会话。
func (a *App) CloseOracleConsole(sessionID int64) error {
	if a.api == nil {
		return fmt.Errorf("无法连接管理服务")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := a.api.CloseOracleConsole(ctx, sessionID)
	if err == nil {
		a.forgetOracle(sessionID)
	}
	return err
}

func (a *App) rememberOracle(id int64) {
	if a == nil || id <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.oracleIDs == nil {
		a.oracleIDs = map[int64]struct{}{}
	}
	a.oracleIDs[id] = struct{}{}
}

func (a *App) forgetOracle(id int64) {
	if a == nil || id <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.oracleIDs, id)
}

func (a *App) forgetOracleAll() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.oracleIDs = map[int64]struct{}{}
	a.mu.Unlock()
}

// closeOracleConsoles 在令牌还有效时结束本机打开的 SQL 会话。
func (a *App) closeOracleConsoles() {
	if a == nil || a.api == nil {
		return
	}
	a.mu.Lock()
	ids := make([]int64, 0, len(a.oracleIDs))
	for id := range a.oracleIDs {
		ids = append(ids, id)
	}
	a.oracleIDs = map[int64]struct{}{}
	a.mu.Unlock()
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		if err := a.api.CloseOracleConsole(ctx, id); err != nil {
			slog.Warn("登出时结束 SQL 会话失败", "session_id", id, "err", err)
		}
		cancel()
	}
}

// ListOracleSchemas 列出已打开会话能看见的数据库用户。
func (a *App) ListOracleSchemas(sessionID int64) (*coreapi.OracleSchemaList, error) {
	if err := a.requireOracleUser(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return a.api.OracleSchemas(ctx, sessionID)
}

// ListOracleObjects 列出某个用户下的表和视图。
func (a *App) ListOracleObjects(sessionID int64, schema string) (*coreapi.OracleObjectList, error) {
	if err := a.requireOracleUser(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return a.api.OracleObjects(ctx, sessionID, schema)
}

// ListOracleColumns 列出某张表或视图的列。
func (a *App) ListOracleColumns(sessionID int64, schema, name string) (*coreapi.OracleColumnList, error) {
	if err := a.requireOracleUser(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return a.api.OracleColumns(ctx, sessionID, schema, name)
}

func (a *App) requireOracleUser() error {
	a.mu.Lock()
	user := a.user
	a.mu.Unlock()
	if user == nil {
		return fmt.Errorf("尚未登录")
	}
	if a.api == nil || a.api.Token() == "" {
		return fmt.Errorf("登录已失效，请重新登录")
	}
	return nil
}
