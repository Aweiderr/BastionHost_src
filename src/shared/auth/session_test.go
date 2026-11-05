package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeClientType(t *testing.T) {
	web, err := NormalizeClientType("  ")
	if err != nil || web != ClientWeb {
		t.Fatalf("empty = %q %v", web, err)
	}
	desk, err := NormalizeClientType("Desktop")
	if err != nil || desk != ClientDesktop {
		t.Fatalf("desktop = %q %v", desk, err)
	}
	if _, err := NormalizeClientType("windows"); err == nil {
		t.Fatal("未知客户端类型应被拒绝")
	}
}

func TestMemoryStoreKicksOppositeKeepsSameType(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySessionStore()
	now := time.Now()
	webAccess := IssuedSession{JTI: "web-a", UserID: 7, ClientType: ClientWeb, Scope: ScopeAccess, FamilyID: "fam-web", ExpiresAt: now.Add(time.Hour)}
	webRefresh := IssuedSession{JTI: "web-r", UserID: 7, ClientType: ClientWeb, Scope: ScopeRefresh, FamilyID: "fam-web", ExpiresAt: now.Add(2 * time.Hour)}
	web2 := IssuedSession{JTI: "web-a2", UserID: 7, ClientType: ClientWeb, Scope: ScopeAccess, FamilyID: "fam-web-2", ExpiresAt: now.Add(time.Hour)}
	for _, s := range []IssuedSession{webAccess, webRefresh, web2} {
		if err := store.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	// 默认策略：同一端的第二次登录不踢第一次。
	n, err := store.RevokeClient(ctx, 7, ClientDesktop, RevokeReasonOpposite, nil)
	if err != nil || n != 0 {
		t.Fatalf("desktop 侧还没有会话，作废数 = %d err=%v", n, err)
	}
	n, err = store.RevokeClient(ctx, 7, ClientWeb, RevokeReasonOpposite, nil)
	if err != nil || n != 3 {
		t.Fatalf("应作废两条 web 登录（含刷新令牌），实际 %d err=%v", n, err)
	}
	if err := store.Rejection(ctx, "web-a"); !errors.Is(err, ErrKickedByOtherClient) {
		t.Fatalf("web 访问令牌 = %v", err)
	}
	if err := store.Rejection(ctx, "web-r"); !errors.Is(err, ErrKickedByOtherClient) {
		t.Fatalf("web 刷新令牌也必须失效，否则还能换新令牌: %v", err)
	}
	if err := store.Rejection(ctx, "never-registered"); err != nil {
		t.Fatalf("未登记的旧令牌应放行: %v", err)
	}
}

func TestMemoryStoreSameClientKeepsNewSession(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySessionStore()
	now := time.Now()
	oldS := IssuedSession{JTI: "old", UserID: 3, ClientType: ClientDesktop, Scope: ScopeAccess, FamilyID: "old-fam", ExpiresAt: now.Add(time.Hour)}
	newS := IssuedSession{JTI: "new", UserID: 3, ClientType: ClientDesktop, Scope: ScopeAccess, FamilyID: "new-fam", ExpiresAt: now.Add(time.Hour)}
	if err := store.Save(ctx, oldS); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, newS); err != nil {
		t.Fatal(err)
	}
	n, err := store.RevokeClient(ctx, 3, ClientDesktop, RevokeReasonSame, []string{"new"})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if err := store.Rejection(ctx, "old"); !errors.Is(err, ErrKickedBySameClient) {
		t.Fatalf("old = %v", err)
	}
	if err := store.Rejection(ctx, "new"); err != nil {
		t.Fatalf("新会话不应被自己踢掉: %v", err)
	}
}

func TestMemoryStoreLogoutRevokesFamilyOnly(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySessionStore()
	now := time.Now()
	if err := store.Save(ctx, IssuedSession{JTI: "a", UserID: 1, ClientType: ClientWeb, Scope: ScopeAccess, FamilyID: "f1", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, IssuedSession{JTI: "r", UserID: 1, ClientType: ClientWeb, Scope: ScopeRefresh, FamilyID: "f1", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, IssuedSession{JTI: "other", UserID: 1, ClientType: ClientWeb, Scope: ScopeAccess, FamilyID: "f2", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeFamily(ctx, "a", RevokeReasonLogout); err != nil {
		t.Fatal(err)
	}
	if err := store.Rejection(ctx, "a"); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("access = %v", err)
	}
	if err := store.Rejection(ctx, "r"); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("refresh = %v", err)
	}
	if err := store.Rejection(ctx, "other"); err != nil {
		t.Fatalf("另一次登录不应被这次登出带走: %v", err)
	}
	if err := store.RevokeFamily(ctx, "missing", RevokeReasonLogout); err != nil {
		t.Fatalf("未知 jti 登出应成功: %v", err)
	}
	ct, ok, err := store.ClientType(ctx, "other")
	if err != nil || !ok || ct != ClientWeb {
		t.Fatalf("client type = %q ok=%v err=%v", ct, ok, err)
	}
}
