package launch

import (
	"strings"
	"testing"
)

func TestOracleSQLPlusDoesNotDialAssetOrEmbedPassword(t *testing.T) {
	plan, err := BuildOracle(OracleRequest{
		Tool:        "sqlplus",
		OpsUser:     "ops",
		AssetID:     42,
		Account:     "app",
		GatewayHost: "10.4.4.4",
		AssetHost:   "10.8.8.8",
		AssetPort:   1522,
	}, linuxEnv("sqlplus", "xterm"))
	if err != nil {
		t.Fatal(err)
	}
	blob := plan.Exe + " " + strings.Join(plan.Args, " ") + " " + plan.Display + " " + plan.Target
	if strings.Contains(blob, "10.8.8.8") || strings.Contains(blob, "1522") || strings.Contains(blob, OracleDBPasswordPlaceholder) {
		t.Fatalf("leaked %s", blob)
	}
	if plan.Target != "10.4.4.4:1521" || !strings.Contains(plan.Args[len(plan.Args)-1], "/nolog") {
		t.Fatalf("plan %+v", plan)
	}
	if strings.Contains(strings.Join(plan.Args, " "), "10.4.4.4") {
		t.Fatal("sqlplus /nolog should not put the descriptor on the command line")
	}
	if !strings.Contains(plan.Message, "10.4.4.4:1521") || !strings.Contains(plan.Message, "占位符") {
		t.Fatal(plan.Message)
	}
}

func TestOracleSQLPlusMissingTool(t *testing.T) {
	_, err := BuildOracle(OracleRequest{
		Tool: "sqlplus", OpsUser: "ops", AssetID: 1, Account: "app", GatewayHost: "10.4.4.4",
		AssetHost: "10.8.8.8", AssetPort: 1522,
	}, linuxEnv("xterm"))
	if err == nil || !strings.Contains(err.Error(), "未找到 sqlplus") {
		t.Fatal(err)
	}
}

func TestOracleDBeaverPointsAtGatewayWithoutPassword(t *testing.T) {
	plan, err := BuildOracle(OracleRequest{
		Tool:        "dbeaver",
		OpsUser:     "ops",
		AssetID:     42,
		Account:     "app",
		GatewayHost: "10.4.4.4",
		AssetHost:   "127.0.0.1",
		AssetPort:   1522,
	}, linuxEnv("dbeaver"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Args, " ")
	if strings.Contains(joined, "127.0.0.1") || strings.Contains(joined, ":1522") || strings.Contains(joined, OracleDBPasswordPlaceholder) {
		t.Fatalf("args %s", joined)
	}
	if strings.Contains(strings.ToLower(joined), "|password=") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "10.4.4.4") || !strings.Contains(joined, "1521") || !strings.Contains(joined, "SERVICE_NAME=ops@42#app") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "BASTION_PASSWORD="+OracleBastionPasswordPlaceholder) || !strings.Contains(joined, "savePassword=false") {
		t.Fatal(joined)
	}
}

func TestOracleDBeaverMissingTool(t *testing.T) {
	_, err := BuildOracle(OracleRequest{
		Tool: "dbeaver", OpsUser: "ops", AssetID: 1, Account: "app", GatewayHost: "10.4.4.4",
	}, linuxEnv())
	if err == nil || !strings.Contains(err.Error(), "未找到 DBeaver") {
		t.Fatal(err)
	}
}

func TestOracleRefusesEmptyGateway(t *testing.T) {
	_, err := BuildOracle(OracleRequest{
		Tool: "sqlplus", OpsUser: "ops", AssetID: 1, Account: "app", AssetHost: "10.8.8.8",
	}, linuxEnv("sqlplus", "xterm"))
	if err == nil || !strings.Contains(err.Error(), "拒绝直连资产") {
		t.Fatal(err)
	}
}
