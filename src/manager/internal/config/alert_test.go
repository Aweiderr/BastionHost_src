package config

import "testing"

func TestLoadAlertWebhookFromEnv(t *testing.T) {
	t.Setenv("BASTION_DEBUG", "true")
	t.Setenv("BASTION_ALERT_WEBHOOK_URL", " https://hooks.example.test/alert ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Alert.WebhookURL != "https://hooks.example.test/alert" {
		t.Fatalf("url %q", cfg.Alert.WebhookURL)
	}
}
