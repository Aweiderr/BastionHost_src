package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/model"
	"bastion.local/shared/auth"
)

func newSessionAuth(t *testing.T) *AuthService {
	t.Helper()
	tm, err := auth.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Auth.AccessTokenTTL = time.Hour
	cfg.Auth.RefreshTokenTTL = 2 * time.Hour
	return NewAuthService(nil, nil, nil, tm, cfg, nil)
}

func TestDesktopLoginKicksWebButNotAnotherDesktop(t *testing.T) {
	svc := newSessionAuth(t)
	ctx := context.Background()
	user := &model.User{UserID: 9, Username: "ops", DisplayName: "运维"}

	web1, err := svc.issueSessionTokens(ctx, user, auth.ClientWeb, true)
	if err != nil {
		t.Fatal(err)
	}
	web2, err := svc.issueSessionTokens(ctx, user, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyToken(ctx, web1.AccessToken); err != nil {
		t.Fatalf("同一端的第二次登录不应踢掉第一次: %v", err)
	}

	desk, err := svc.issueSessionTokens(ctx, user, "Desktop", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyToken(ctx, web1.AccessToken); !errors.Is(err, auth.ErrKickedByOtherClient) {
		t.Fatalf("web1 = %v", err)
	}
	if _, err := svc.VerifyToken(ctx, web2.AccessToken); !errors.Is(err, auth.ErrKickedByOtherClient) {
		t.Fatalf("web2 = %v", err)
	}
	webRefresh, err := svc.tm.Verify(web1.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.rejectIfRevoked(ctx, webRefresh.TokenID); !errors.Is(err, auth.ErrKickedByOtherClient) {
		t.Fatalf("刷新令牌必须一起失效，否则还能换回 Web 会话: %v", err)
	}
	if _, err := svc.VerifyToken(ctx, desk.AccessToken); err != nil {
		t.Fatalf("刚签发的桌面令牌应有效: %v", err)
	}

	desk2, err := svc.issueSessionTokens(ctx, user, auth.ClientDesktop, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyToken(ctx, desk.AccessToken); err != nil {
		t.Fatalf("默认同一端可以保留多个会话: %v", err)
	}
	if _, err := svc.VerifyToken(ctx, desk2.AccessToken); err != nil {
		t.Fatal(err)
	}

	claims, err := svc.VerifyToken(ctx, desk2.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(ctx, claims.TokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyToken(ctx, desk2.AccessToken); !errors.Is(err, auth.ErrSessionRevoked) {
		t.Fatalf("登出后应是会话作废而不是“已在其他端登录”: %v", err)
	}
	if _, err := svc.VerifyToken(ctx, desk.AccessToken); err != nil {
		t.Fatalf("登出只作废当前这次登录: %v", err)
	}
}

func TestKickFlags(t *testing.T) {
	ctx := context.Background()
	user := &model.User{UserID: 3, Username: "ops"}

	t.Run("opposite off", func(t *testing.T) {
		svc := newSessionAuth(t)
		svc.kickOverride = &kickFlags{opposite: false, same: false}
		web, err := svc.issueSessionTokens(ctx, user, auth.ClientWeb, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.issueSessionTokens(ctx, user, auth.ClientDesktop, true); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.VerifyToken(ctx, web.AccessToken); err != nil {
			t.Fatalf("关闭互斥后 Web 会话应仍然有效: %v", err)
		}
	})

	t.Run("same client on", func(t *testing.T) {
		svc := newSessionAuth(t)
		svc.kickOverride = &kickFlags{opposite: true, same: true}
		first, err := svc.issueSessionTokens(ctx, user, auth.ClientDesktop, true)
		if err != nil {
			t.Fatal(err)
		}
		second, err := svc.issueSessionTokens(ctx, user, auth.ClientDesktop, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.VerifyToken(ctx, first.AccessToken); !errors.Is(err, auth.ErrKickedBySameClient) {
			t.Fatalf("first = %v", err)
		}
		if _, err := svc.VerifyToken(ctx, second.AccessToken); err != nil {
			t.Fatalf("新会话不应被自己踢掉: %v", err)
		}
	})
}

func TestRejectsUnknownClientType(t *testing.T) {
	svc := newSessionAuth(t)
	_, err := svc.issueSessionTokens(context.Background(), &model.User{UserID: 1, Username: "u"}, "windows", true)
	if err == nil {
		t.Fatal("client_type=windows 应被拒绝")
	}
}
