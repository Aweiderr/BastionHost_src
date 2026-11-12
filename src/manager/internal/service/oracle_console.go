package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/oracleprov"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/errcode"
	sharedmodel "bastion.local/shared/model"
	"bastion.local/shared/policy"
)

const (
	oracleConsoleClient = "oracle_console"
	// oracleConsoleNode 不是代理节点。巡检只收「某台在线代理的清单里没有」的会话，
	// 这个名字不会出现在心跳里，产品内会话就不会被当成僵尸关掉。
	oracleConsoleNode = "console"
	oracleConsoleIdle = 15 * time.Minute
	oracleExecTimeout = 20 * time.Second
	maxOracleConsoles = 4
)

// OracleDirectory 是打开会话要用的授权和托管凭据。*OpsService 满足它。
type OracleDirectory interface {
	CheckGrant(ctx context.Context, userID, assetID, accountID int64) (*OpsGrantCheck, error)
	FetchCredential(ctx context.Context, op OperatorContext, userID, assetID, accountID int64, sessionNo string) (*OpsCredential, error)
}

// OracleAudit 是会话和命令留痕。*SessionService 满足它。
type OracleAudit interface {
	Start(ctx context.Context, req StartSessionRequest) (*model.Session, error)
	AddCommand(ctx context.Context, sessionID int64, c *model.SessionCommand) (*model.SessionCommand, error)
	Finish(ctx context.Context, sessionID int64, in repo.SessionCloseInput) error
	Get(ctx context.Context, sessionID int64) (*model.Session, error)
}

// OraclePolicy 是命令策略。*PolicyService 满足它。
type OraclePolicy interface {
	Engine(ctx context.Context) (*policy.Engine, error)
}

// OracleConn 是管理服务持有的一条 Oracle 连接。浏览器拿不到它。
type OracleConn interface {
	Query(ctx context.Context, statement string, maxRows int, args ...any) (OracleTable, error)
	Exec(ctx context.Context, statement string) (int64, error)
	Close() error
}

// OracleTable 是查询结果。单元格为 nil 表示 NULL。
type OracleTable struct {
	Columns   []string
	Rows      [][]*string
	Truncated bool
}

// OracleOpener 拨到资产库。口令只在这次调用里使用，实现不得把 DSN 写进日志。
type OracleOpener func(ctx context.Context, target oracleprov.Target, username, password string) (OracleConn, error)

// OracleActor 是已经登录的运维人员。口令不在这里。
type OracleActor struct {
	UserID      int64
	Username    string
	DisplayName string
	ClientIP    string
}

// OracleOpenInput 只接受资产和账号。填写地址或口令会被拒绝，避免把这条接口当成直连入口。
type OracleOpenInput struct {
	AssetID         int64  `json:"asset_id"`
	AccountID       int64  `json:"account_id"`
	Host            string `json:"host,omitempty"`
	Port            int    `json:"port,omitempty"`
	DSN             string `json:"dsn,omitempty"`
	ServiceName     string `json:"service_name,omitempty"`
	Password        string `json:"password,omitempty"`
	BastionPassword string `json:"bastion_password,omitempty"`
	DBPassword      string `json:"db_password,omitempty"`
	PrivateKey      string `json:"private_key,omitempty"`
}

// OracleSessionInfo 返回给浏览器和桌面端。没有地址、没有口令、没有连接串。
type OracleSessionInfo struct {
	SessionID   int64  `json:"session_id"`
	SessionNo   string `json:"session_no"`
	AssetName   string `json:"asset_name"`
	AccountName string `json:"account_name"`
}

// OracleSQLInput 是一条待执行的 SQL。附带口令字段会被拒绝。
type OracleSQLInput struct {
	SQL             string `json:"sql"`
	Password        string `json:"password,omitempty"`
	BastionPassword string `json:"bastion_password,omitempty"`
	DBPassword      string `json:"db_password,omitempty"`
}

// OracleSQLResult 是执行结果。阻断时不会连接数据库去跑这条语句。
type OracleSQLResult struct {
	Kind         string      `json:"kind"`
	Columns      []string    `json:"columns,omitempty"`
	Rows         [][]*string `json:"rows,omitempty"`
	RowCount     int         `json:"row_count"`
	RowsAffected int64       `json:"rows_affected,omitempty"`
	Truncated    bool        `json:"truncated,omitempty"`
	Message      string      `json:"message,omitempty"`
	Blocked      bool        `json:"blocked,omitempty"`
	PolicyAction string      `json:"policy_action,omitempty"`
	// ElapsedMS 是这次执行耗时（含策略判定）。旧客户端忽略未知字段。
	ElapsedMS int64 `json:"elapsed_ms"`
}

// OracleConsole 是产品内的 Oracle SQL 会话。
//
// 运维人员只调用管理服务。管理服务在授权通过后，用托管账号自己拨到资产库，
// 和建库用户时的 go-ora 连接同一侧，不把浏览器指到资产地址，也不经过 TNS 监听。
// 每条语句先过命令策略再执行，并写入同一张会话命令表。
type OracleConsole struct {
	ops      OracleDirectory
	sessions OracleAudit
	policies OraclePolicy
	open     OracleOpener
	now      func() time.Time
	idle     time.Duration

	mu   sync.Mutex
	live map[int64]*oracleLive
}

type oracleLive struct {
	mu       sync.Mutex
	userID   int64
	assetID  int64
	level    string
	credMode string
	conn     OracleConn
	seq      int
	blocked  int
	last     time.Time
	closed   bool
	info     OracleSessionInfo
}

// NewOracleConsole 创建产品内 SQL 会话。open 为空时使用 go-ora。
func NewOracleConsole(ops OracleDirectory, sessions OracleAudit, policies OraclePolicy, open OracleOpener) *OracleConsole {
	if open == nil {
		open = openOracleConn
	}
	return &OracleConsole{
		ops: ops, sessions: sessions, policies: policies, open: open,
		now: time.Now, idle: oracleConsoleIdle,
		live: map[int64]*oracleLive{},
	}
}

// StartJanitor 周期回收空闲会话，进程退出时关掉还开着的连接。
func (c *OracleConsole) StartJanitor(ctx context.Context) {
	if c == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				c.closeAll()
				return
			case <-ticker.C:
				c.Reap()
			}
		}
	}()
}

// Open 校验授权、取出托管口令并拨号。口令用完即丢，响应里不出现。
func (c *OracleConsole) Open(ctx context.Context, actor OracleActor, in OracleOpenInput) (*OracleSessionInfo, error) {
	if c == nil || c.ops == nil || c.sessions == nil || c.policies == nil || c.open == nil {
		return nil, errcode.New(errcode.ErrDBUnavailable, "Oracle SQL 会话不可用")
	}
	if actor.UserID == 0 {
		return nil, errcode.New(errcode.ErrUnauthorized, "尚未登录")
	}
	if msg := rejectOracleOpenTarget(in); msg != "" {
		return nil, errcode.New(errcode.ErrBadRequest, msg)
	}
	if in.AssetID == 0 || in.AccountID == 0 {
		return nil, errcode.New(errcode.ErrBadRequest, "请选择 Oracle 资产和账号")
	}
	if _, err := c.policies.Engine(ctx); err != nil {
		return nil, errcode.New(errcode.ErrNotReady, "命令策略未就绪，已拒绝打开 SQL 会话")
	}
	check, err := c.ops.CheckGrant(ctx, actor.UserID, in.AssetID, in.AccountID)
	if err != nil {
		return nil, err
	}
	if check == nil || !check.Allowed {
		reason := "没有可用的授权"
		if check != nil && check.Reason != "" {
			reason = check.Reason
		}
		return nil, errcode.New(errcode.ErrForbidden, reason)
	}
	if c.countUser(actor.UserID) >= maxOracleConsoles {
		return nil, errcode.New(errcode.ErrTooManyReq, "打开的 Oracle SQL 会话过多，请先结束已有会话")
	}

	display := strings.TrimSpace(actor.DisplayName)
	if display == "" {
		display = actor.Username
	}
	sess, err := c.sessions.Start(ctx, StartSessionRequest{
		UserID: actor.UserID, Username: actor.Username, DisplayName: display,
		AssetID: in.AssetID, AccountID: in.AccountID,
		Protocol: "oracle", ClientType: oracleConsoleClient, ClientIP: actor.ClientIP,
		ProxyNode: oracleConsoleNode,
		GrantID:   check.GrantID, GrantSource: check.GrantSource, TicketID: check.TicketID,
		SkipPlacement: true,
	})
	if err != nil {
		return nil, err
	}
	credMode := model.CredentialManaged
	fail := func(reason string, ret error) (*OracleSessionInfo, error) {
		c.finishRecord(sess.SessionID, model.SessionStatusClosed, reason, credMode, 0, 0)
		return nil, ret
	}

	cred, err := c.ops.FetchCredential(ctx, OperatorContext{
		UserID: actor.UserID, Username: actor.Username, ClientIP: actor.ClientIP,
	}, actor.UserID, in.AssetID, in.AccountID, sess.SessionNo)
	if err != nil {
		return fail("connect_failed", err)
	}
	password := cred.Password
	cred.Password = ""
	if cred.CredentialMode != "" {
		credMode = cred.CredentialMode
	}
	if cred.RequiresUserInput || password == "" {
		return fail("connect_failed", errcode.New(errcode.ErrBadRequest,
			"该账号没有托管口令，产品内 SQL 会话无法代连。请改用「本机 CMD / DBeaver」，在工具里自行输入数据库口令"))
	}
	if cred.Protocol != "" && !strings.EqualFold(cred.Protocol, "oracle") {
		return fail("connect_failed", errcode.New(errcode.ErrBadRequest, "资产协议不是 Oracle"))
	}
	if strings.TrimSpace(cred.OracleServiceName) == "" && strings.TrimSpace(cred.OracleSID) == "" {
		return fail("connect_failed", errcode.New(errcode.ErrBadRequest, "资产未配置 Oracle 服务名或 SID"))
	}
	if cred.AccountID != 0 && cred.AccountID != in.AccountID {
		return fail("connect_failed", errcode.New(errcode.ErrForbidden, "账号不在授权范围内"))
	}

	conn, err := c.open(ctx, oracleprov.Target{
		Host: cred.Host, Port: cred.Port,
		ServiceName: cred.OracleServiceName, SID: cred.OracleSID,
	}, cred.AccountName, password)
	password = ""
	if err != nil {
		slog.Warn("Oracle SQL 会话拨号失败", "session_id", sess.SessionID, "err", oracleprov.Redact(err.Error()))
		return fail("connect_failed", errcode.New(errcode.ErrBadRequest, "连接目标 Oracle 失败"))
	}

	info := OracleSessionInfo{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo,
		AssetName: cred.AssetName, AccountName: cred.AccountName,
	}
	if info.AssetName == "" {
		info.AssetName = sess.AssetName
	}
	if info.AccountName == "" {
		info.AccountName = sess.AccountName
	}
	live := &oracleLive{
		userID: actor.UserID, assetID: cred.AssetID, level: cred.Level,
		credMode: credMode, conn: conn, last: c.now(), info: info,
	}
	if live.assetID == 0 {
		live.assetID = in.AssetID
	}
	c.mu.Lock()
	if c.live == nil {
		c.live = map[int64]*oracleLive{}
	}
	c.live[info.SessionID] = live
	c.mu.Unlock()
	return &info, nil
}

// Exec 执行一条 SQL。策略阻断和确认都不会送到数据库。
func (c *OracleConsole) Exec(ctx context.Context, actor OracleActor, sessionID int64, in OracleSQLInput) (*OracleSQLResult, error) {
	if strings.TrimSpace(in.Password) != "" || strings.TrimSpace(in.BastionPassword) != "" || strings.TrimSpace(in.DBPassword) != "" {
		return nil, errcode.New(errcode.ErrBadRequest, "SQL 接口不接受口令")
	}
	live, err := c.liveFor(actor.UserID, sessionID)
	if err != nil {
		return nil, err
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.closed || live.conn == nil {
		return nil, errcode.New(errcode.ErrBadRequest, "Oracle SQL 会话已结束")
	}
	if err := c.ensureActive(ctx, live); err != nil {
		return nil, err
	}
	statement, err := prepareOracleSQL(in.SQL)
	if err != nil {
		return nil, errcode.New(errcode.ErrBadRequest, err.Error())
	}
	started := time.Now()
	finish := func(res *OracleSQLResult, err error) (*OracleSQLResult, error) {
		if res != nil {
			res.ElapsedMS = time.Since(started).Milliseconds()
		}
		return res, err
	}
	decision, err := c.judge(ctx, live, in.SQL)
	if err != nil {
		return nil, err
	}
	blocked := decision != nil && (decision.Action == policy.ActionDeny || decision.Action == policy.ActionConfirm)
	resultText := "submitted"
	if blocked {
		resultText = "blocked"
		live.blocked++
	}
	live.seq++
	cmd := &model.SessionCommand{
		SeqNo: live.seq, CommandText: strings.TrimSpace(in.SQL), CommandType: "sql",
		CommandOrigin: sharedmodel.CommandOriginUser,
		ExecResult:    resultText, PolicyAction: oracleAction(decision), RiskLevel: oracleRisk(decision),
		MatchedRule: truncateRunes(oracleRule(decision), 254),
		CommandTime: time.Now(),
	}
	if decision != nil && decision.MatchedRule != nil && decision.MatchedRule.PolicyID != 0 {
		id := decision.MatchedRule.PolicyID
		cmd.PolicyID = &id
	}
	if _, err := c.sessions.AddCommand(ctx, live.info.SessionID, cmd); err != nil {
		slog.Warn("Oracle SQL 审计写入失败，已拒绝执行", "session_id", live.info.SessionID, "err", err)
		return finish(nil, errcode.New(errcode.ErrInternal, "命令审计写入失败，已拒绝执行"))
	}
	live.last = c.now()
	if blocked {
		msg := "语句已被命令策略阻断"
		if decision != nil && decision.Action == policy.ActionConfirm {
			msg = "语句需要二次确认。产品内会话没有确认通道，已按阻断处理"
		}
		if rule := oracleRule(decision); rule != "" {
			msg += "：" + rule
		}
		return finish(&OracleSQLResult{
			Kind: "blocked", Blocked: true, Message: msg, PolicyAction: oracleAction(decision),
		}, nil)
	}

	execCtx, cancel := context.WithTimeout(ctx, oracleExecTimeout)
	defer cancel()
	if queryOracleSQL(statement) {
		table, qerr := live.conn.Query(execCtx, statement, maxOracleRows)
		if qerr != nil {
			return finish(nil, errcode.New(errcode.ErrBadRequest, oracleUserError(qerr)))
		}
		msg := ""
		if table.Truncated {
			msg = "结果已截断，只返回前若干行"
		}
		return finish(&OracleSQLResult{
			Kind: "query", Columns: table.Columns, Rows: table.Rows,
			RowCount: len(table.Rows), Truncated: table.Truncated, Message: msg,
			PolicyAction: oracleAction(decision),
		}, nil)
	}
	n, eerr := live.conn.Exec(execCtx, statement)
	if eerr != nil {
		return finish(nil, errcode.New(errcode.ErrBadRequest, oracleUserError(eerr)))
	}
	return finish(&OracleSQLResult{
		Kind: "exec", RowsAffected: n, RowCount: int(n), Message: "语句已执行",
		PolicyAction: oracleAction(decision),
	}, nil)
}

// Close 结束会话并断开数据库连接。
func (c *OracleConsole) Close(actor OracleActor, sessionID int64) error {
	live, err := c.take(actor.UserID, sessionID)
	if err != nil {
		return err
	}
	c.shutdown(live, model.SessionStatusClosed, model.EndReasonClientDisconnect)
	return nil
}

// Reap 回收空闲会话。测试可以直接调用。
func (c *OracleConsole) Reap() int {
	if c == nil {
		return 0
	}
	now := c.now()
	c.mu.Lock()
	var stale []*oracleLive
	for id, live := range c.live {
		live.mu.Lock()
		old := !live.closed && now.Sub(live.last) >= c.idle
		live.mu.Unlock()
		if old {
			stale = append(stale, live)
			delete(c.live, id)
		}
	}
	c.mu.Unlock()
	for _, live := range stale {
		c.shutdown(live, model.SessionStatusTimeout, model.EndReasonTimeout)
	}
	return len(stale)
}

func (c *OracleConsole) closeAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	all := c.live
	c.live = map[int64]*oracleLive{}
	c.mu.Unlock()
	for _, live := range all {
		c.shutdown(live, model.SessionStatusClosed, model.EndReasonGatewayShutdown)
	}
}

func (c *OracleConsole) shutdown(live *oracleLive, status, reason string) {
	if live == nil {
		return
	}
	live.mu.Lock()
	if live.closed {
		live.mu.Unlock()
		return
	}
	live.closed = true
	conn := live.conn
	live.conn = nil
	seq, blocked := live.seq, live.blocked
	mode := live.credMode
	id := live.info.SessionID
	live.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	c.finishRecord(id, status, reason, mode, seq, blocked)
}

func (c *OracleConsole) finishRecord(sessionID int64, status, reason, credMode string, commands, blocked int) {
	if c == nil || c.sessions == nil || sessionID == 0 {
		return
	}
	if credMode == "" {
		credMode = model.CredentialManaged
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.sessions.Finish(ctx, sessionID, repo.SessionCloseInput{
		Status: status, EndReason: reason,
		CommandCount: commands, BlockCount: blocked,
		RecordStatus: model.RecordStatusNone, CredentialMode: credMode,
	}); err != nil {
		slog.Warn("Oracle SQL 会话归档失败", "session_id", sessionID, "err", err)
	}
}

func (c *OracleConsole) liveFor(userID, sessionID int64) (*oracleLive, error) {
	if userID == 0 {
		return nil, errcode.New(errcode.ErrUnauthorized, "尚未登录")
	}
	if sessionID == 0 {
		return nil, errcode.New(errcode.ErrBadRequest, "缺少会话")
	}
	c.mu.Lock()
	live := c.live[sessionID]
	c.mu.Unlock()
	if live == nil || live.userID != userID {
		return nil, errcode.New(errcode.ErrNotFound, "Oracle SQL 会话不存在")
	}
	return live, nil
}

func (c *OracleConsole) take(userID, sessionID int64) (*oracleLive, error) {
	live, err := c.liveFor(userID, sessionID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	delete(c.live, sessionID)
	c.mu.Unlock()
	return live, nil
}

func (c *OracleConsole) countUser(userID int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, live := range c.live {
		if live.userID == userID {
			n++
		}
	}
	return n
}

func (c *OracleConsole) ensureActive(ctx context.Context, live *oracleLive) error {
	sess, err := c.sessions.Get(ctx, live.info.SessionID)
	if err != nil {
		return errcode.New(errcode.ErrInternal, "无法确认会话状态")
	}
	if sess == nil || sess.Status != model.SessionStatusActive {
		return errcode.New(errcode.ErrBadRequest, "Oracle SQL 会话已结束")
	}
	if sess.UserID != live.userID {
		return errcode.New(errcode.ErrForbidden, "不能使用他人的 Oracle SQL 会话")
	}
	return nil
}

func (c *OracleConsole) judge(ctx context.Context, live *oracleLive, sql string) (*policy.Decision, error) {
	engine, err := c.policies.Engine(ctx)
	if err != nil || engine == nil {
		return nil, errcode.New(errcode.ErrNotReady, "命令策略未就绪，已拒绝执行")
	}
	return engine.Evaluate(policy.EvalContext{
		AssetID: live.assetID, AssetLevel: live.level, UserID: live.userID, Protocol: "oracle",
	}, sql), nil
}

func rejectOracleOpenTarget(in OracleOpenInput) string {
	if strings.TrimSpace(in.Host) != "" || in.Port != 0 || strings.TrimSpace(in.DSN) != "" || strings.TrimSpace(in.ServiceName) != "" {
		return "不能指定数据库地址或连接串，会话只连接已授权的资产"
	}
	if strings.TrimSpace(in.Password) != "" || strings.TrimSpace(in.BastionPassword) != "" ||
		strings.TrimSpace(in.DBPassword) != "" || strings.TrimSpace(in.PrivateKey) != "" {
		return "SQL 会话不接受口令。托管账号由堡垒机代连，未托管账号请使用本机 CMD / DBeaver"
	}
	return ""
}

func oracleUserError(err error) string {
	if err == nil {
		return "Oracle 执行失败"
	}
	msg := oracleprov.Redact(err.Error())
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "Oracle 执行失败"
	}
	if len(msg) > 400 {
		msg = msg[:400]
	}
	return msg
}

func oracleAction(d *policy.Decision) string {
	if d == nil || d.Action == "" {
		return policy.ActionAllow
	}
	return d.Action
}

func oracleRisk(d *policy.Decision) string {
	if d == nil || d.RiskLevel == "" {
		return policy.RiskNormal
	}
	return d.RiskLevel
}

func oracleRule(d *policy.Decision) string {
	if d == nil || d.MatchedRule == nil {
		return ""
	}
	return d.MatchedRule.Name
}
