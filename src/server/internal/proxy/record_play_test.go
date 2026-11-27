package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
	"bastion.local/shared/auditmask"
)

func TestApplyPlaybackRedactsSecrets(t *testing.T) {
	plain := []byte("password=s3cret-value\r\nmobile 13900001111\r\n")
	out, redacted := applyPlayback(plain, auditmask.BuiltinRules(), false)
	if !redacted || strings.Contains(string(out), "s3cret-value") || strings.Contains(string(out), "13900001111") {
		t.Fatalf("回放未脱敏: %s", out)
	}
	revealed, redacted := applyPlayback(plain, auditmask.BuiltinRules(), true)
	if redacted || string(revealed) != string(plain) {
		t.Fatal("已授权的原文查看不应改写录像")
	}
}

func TestPlaybackRejectsWatermarkOffButRecordsIt(t *testing.T) {
	fake := &playFake{}
	s := &Server{cfg: &config.Config{}, client: fake}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/records/{sessionNo}", s.RecordPlayHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/records/S1?token=t&watermark=off", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !fake.bypass {
		t.Fatalf("watermark=off 必须记审计，HTTP %d", rec.Code)
	}
	if rec.Code == http.StatusOK && rec.Header().Get("X-Watermark-Required") != "1" {
		t.Fatal("成功回放也必须要求水印")
	}
}

func TestWatermarkDisabledQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x?watermark=OFF", nil)
	if !watermarkDisabled(req) {
		t.Fatal("OFF 应视为关闭水印")
	}
	req = httptest.NewRequest(http.MethodGet, "/x?watermark=1", nil)
	if watermarkDisabled(req) {
		t.Fatal("未关闭时不应记绕过")
	}
}

// playFake 只实现回放鉴权和展示策略。其余网关方法用不到。
type playFake struct {
	bypass bool
	reveal bool
}

func (f *playFake) VerifyOps(context.Context, string, string, string, string) (*managerclient.OpsIdentity, error) {
	return nil, nil
}
func (f *playFake) VerifyToken(context.Context, string) (*managerclient.OpsIdentity, error) {
	return &managerclient.OpsIdentity{
		UserID: 1, TenantID: 1, Username: "audit",
		Permissions: []string{"audit:record:play"},
	}, nil
}
func (f *playFake) AccessibleAssets(context.Context, int64) ([]*managerclient.AssetBrief, error) {
	return nil, nil
}
func (f *playFake) AccessibleFileAssets(context.Context, int64) ([]*managerclient.AssetBrief, error) {
	return nil, nil
}
func (f *playFake) CheckGrant(context.Context, int64, int64, int64) (*managerclient.GrantCheck, error) {
	return nil, nil
}
func (f *playFake) FetchCredential(context.Context, int64, string, int64, int64, string, string) (*managerclient.Credential, error) {
	return nil, nil
}
func (f *playFake) LearnHostKey(context.Context, int64, string, string) error { return nil }
func (f *playFake) StartSession(context.Context, map[string]any) (*managerclient.SessionInfo, error) {
	return nil, nil
}
func (f *playFake) AddCommand(context.Context, int64, *managerclient.SessionCommandPayload) error {
	return nil
}
func (f *playFake) AddFile(context.Context, int64, *managerclient.SessionFilePayload) error {
	return nil
}
func (f *playFake) FinishSession(context.Context, int64, map[string]any) error { return nil }
func (f *playFake) Schedule(context.Context, managerclient.ScheduleRequest) (*managerclient.Placement, error) {
	return nil, nil
}
func (f *playFake) DisplayPolicy(context.Context, int64) (auditmask.Policy, error) {
	return auditmask.Policy{Rules: auditmask.BuiltinRules(), Watermark: auditmask.DefaultWatermark()}, nil
}
func (f *playFake) NoteWatermarkBypass(context.Context, int64, string, int64, string, string, string) error {
	f.bypass = true
	return nil
}
func (f *playFake) AskReveal(context.Context, int64, string, int64, string, string) (bool, error) {
	return f.reveal, nil
}
