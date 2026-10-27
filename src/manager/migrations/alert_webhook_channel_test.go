package migrations

import (
	"strings"
	"testing"
)

func TestAlertWebhookChannelMigration(t *testing.T) {
	raw, err := FS.ReadFile("0035_alert_webhook_channel.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	sql := stripSQLLineComments(string(raw))
	for _, want := range []string{
		"cmd_high_risk",
		`notify_channels = '["db"]'::jsonb`,
		`'["db", "webhook"]'::jsonb`,
		"BASTION_ALERT_WEBHOOK_URL",
	} {
		if !strings.Contains(string(raw), want) && !strings.Contains(sql, want) {
			t.Fatalf("迁移缺少 %q", want)
		}
	}
	lower := strings.ToLower(sql)
	if strings.Contains(lower, "https://") || strings.Contains(lower, "http://") {
		t.Fatal("迁移不应写入具体 Webhook 地址")
	}
}
