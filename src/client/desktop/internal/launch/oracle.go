package launch

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	oracleGatewayPort = 1521
	// OracleBastionPasswordPlaceholder 与 Web / 桌面说明里的占位符相同。
	OracleBastionPasswordPlaceholder = "BASTION_PASSWORD"
	// OracleDBPasswordPlaceholder 不放进启动参数。DBeaver 的口令框和 sqlplus 粘贴命令各自填写。
	OracleDBPasswordPlaceholder = "数据库口令"
)

// OracleRequest 是拉起本机 sqlplus 或 DBeaver 的输入。没有口令字段。
type OracleRequest struct {
	Tool        string
	OpsUser     string
	AssetID     int64
	Account     string
	GatewayHost string
	GatewayPort int
	AssetHost   string
	AssetPort   int
}

// BuildOracle 生成启动计划。参数只指向堡垒机网关，不带口令，也不带资产地址。
func BuildOracle(req OracleRequest, env Env) (Plan, error) {
	if env.GOOS == "" {
		env.GOOS = "linux"
	}
	if env.Look == nil {
		env.Look = func(string) (string, bool) { return "", false }
	}
	if env.Exists == nil {
		env.Exists = func(string) bool { return false }
	}
	host, port, err := oracleGateway(req)
	if err != nil {
		return Plan{}, err
	}
	user, err := token(req.OpsUser, "运维用户名")
	if err != nil {
		return Plan{}, err
	}
	account, err := token(req.Account, "账号名")
	if err != nil {
		return Plan{}, err
	}
	if req.AssetID <= 0 {
		return Plan{}, fmt.Errorf("资产 ID 无效")
	}
	service := fmt.Sprintf("%s@%d#%s", user, req.AssetID, account)
	target := net.JoinHostPort(host, strconv.Itoa(port))
	tool := strings.ToLower(strings.TrimSpace(req.Tool))
	var plan Plan
	switch tool {
	case "sqlplus", "cmd":
		plan, err = buildSQLPlus(env, target, service)
	case "dbeaver":
		plan, err = buildDBeaver(env, host, port, account, service)
	default:
		return Plan{}, fmt.Errorf("本机 Oracle 工具只支持 sqlplus 和 DBeaver")
	}
	if err != nil {
		return Plan{}, err
	}
	blob := plan.Exe + "\n" + strings.Join(plan.Args, "\n") + "\n" + plan.Display + "\n" + plan.Target
	if err := rejectOracleAsset(host, port, blob, req.AssetHost, req.AssetPort); err != nil {
		return Plan{}, err
	}
	if strings.Contains(blob, OracleDBPasswordPlaceholder) || containsPasswordArg(blob) {
		return Plan{}, fmt.Errorf("启动参数含有口令，已拒绝")
	}
	plan.Login = service
	plan.Target = target
	return plan, nil
}

func oracleGateway(req OracleRequest) (string, int, error) {
	host := strings.TrimSpace(req.GatewayHost)
	if host == "" || strings.ContainsAny(host, " \t\r\n'\"\\") {
		return "", 0, fmt.Errorf("没有堡垒机 Oracle 网关地址，已拒绝直连资产")
	}
	port := req.GatewayPort
	if port <= 0 {
		port = oracleGatewayPort
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("堡垒机 Oracle 网关端口无效")
	}
	return host, port, nil
}

func buildSQLPlus(env Env, target, service string) (Plan, error) {
	names := []string{"sqlplus", "sqlplus.exe"}
	if env.GOOS == "windows" {
		names = []string{"sqlplus.exe", "sqlplus"}
	}
	sqlplus, ok := find(env, names...)
	if !ok {
		return Plan{}, fmt.Errorf("未找到 sqlplus。请安装 Oracle Instant Client 并把 sqlplus 加入 PATH。连接说明里的命令仍可复制，口令请自行替换占位符，不要直连资产")
	}
	note := "已打开 sqlplus /nolog，网关是 " + target + "（服务名 " + service + "）。请把连接说明里的 sqlplus 命令粘贴进去，自行把占位符换成堡垒机口令和数据库口令。命令行没有口令，也没有资产地址。"
	if env.GOOS == "windows" {
		cmd, ok := find(env, `C:\Windows\System32\cmd.exe`, "cmd.exe", "cmd")
		if !ok {
			return Plan{}, fmt.Errorf("未找到 cmd.exe。%s", note)
		}
		return Plan{
			Protocol: "sqlplus",
			Tool:     "cmd",
			Exe:      cmd,
			Args:     []string{"/k", "sqlplus /nolog"},
			Display:  "cmd /k sqlplus /nolog",
			Target:   target,
			Message:  note + "窗口会单独打开。",
		}, nil
	}
	plan := Plan{
		Protocol: "sqlplus",
		Tool:     "sqlplus",
		Exe:      sqlplus,
		Args:     []string{"/nolog"},
		Display:  "sqlplus /nolog",
		Target:   target,
		Message:  note,
	}
	wrapped, tool, args, extra, ok := wrapTerminal(env, plan)
	if !ok {
		return Plan{}, fmt.Errorf("未找到终端程序（mate-terminal、ukui-terminal、gnome-terminal 或 xterm）。可以在终端执行 sqlplus /nolog，再粘贴连接说明里的命令")
	}
	plan.Tool = tool
	plan.Exe = wrapped
	plan.Args = args
	plan.Message += extra
	return plan, nil
}

func buildDBeaver(env Env, host string, port int, account, service string) (Plan, error) {
	names := []string{"dbeaver", "dbeaver-ce"}
	if env.GOOS == "windows" {
		names = []string{
			`C:\Program Files\DBeaver\dbeaver.exe`,
			`C:\Program Files\DBeaverCE\dbeaver.exe`,
			"dbeaver.exe", "dbeaver",
		}
	}
	exe, ok := find(env, names...)
	if !ok {
		return Plan{}, fmt.Errorf("未找到 DBeaver。请先安装，连接说明里的 JDBC 地址仍可复制。口令请在 DBeaver 的口令框填写，不要直连资产")
	}
	if strings.ContainsAny(account, "|") {
		return Plan{}, fmt.Errorf("账号名不能放进 DBeaver 启动参数")
	}
	url := oracleJDBC(host, port, service)
	spec := "name=Bastion Oracle|driver=oracle|user=" + account + "|url=" + url + "|savePassword=false|connect=false"
	if containsPasswordArg(spec) {
		return Plan{}, fmt.Errorf("DBeaver 启动参数含有口令，已拒绝")
	}
	return Plan{
		Protocol: "dbeaver",
		Tool:     "dbeaver",
		Exe:      exe,
		Args:     []string{"-con", spec},
		Display:  "dbeaver -con " + spec,
		Target:   net.JoinHostPort(host, strconv.Itoa(port)),
		Message: "已打开 DBeaver，连接指向堡垒机网关 " + net.JoinHostPort(host, strconv.Itoa(port)) +
			"。URL 里的 " + OracleBastionPasswordPlaceholder + " 仍是占位符，请在 DBeaver 中替换，并在口令框填写数据库口令。启动参数没有数据库口令，也没有资产地址。",
	}, nil
}

func oracleJDBC(host string, port int, service string) string {
	desc := fmt.Sprintf("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=%s)(PORT=%d))(CONNECT_DATA=(SERVICE_NAME=%s)(BASTION_PASSWORD=%s)))",
		host, port, service, OracleBastionPasswordPlaceholder)
	return "jdbc:oracle:thin:@" + desc
}

func containsPasswordArg(blob string) bool {
	lower := strings.ToLower(blob)
	// savePassword=false 是开关，不是口令。只拒绝真正的 password 参数。
	return strings.Contains(lower, "|password=") || strings.Contains(lower, " password=")
}

func rejectOracleAsset(gatewayHost string, gatewayPort int, blob, assetHost string, assetPort int) error {
	assetHost = strings.TrimSpace(assetHost)
	if assetHost != "" && !strings.EqualFold(assetHost, gatewayHost) && strings.Contains(blob, assetHost) {
		return fmt.Errorf("启动参数含有资产地址 %s，已拒绝", assetHost)
	}
	if assetPort > 0 && assetPort != gatewayPort {
		port := ":" + strconv.Itoa(assetPort)
		if strings.Contains(blob, port) {
			return fmt.Errorf("启动参数含有资产端口 %s，已拒绝", port)
		}
	}
	return nil
}
