package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// 客户端类型。登录互斥只区分这两类：同一类可以并存多个会话。
const (
	ClientWeb     = "web"
	ClientDesktop = "desktop"
)

// 会话作废原因。校验时据此决定给用户看的文案。
const (
	// RevokeReasonOpposite 另一端登录成功，本端会话被踢下线。
	RevokeReasonOpposite = "opposite"
	// RevokeReasonSame 同一端的新登录踢掉了本端其它会话（需配置开启）。
	RevokeReasonSame = "same"
	// RevokeReasonLogout 持有者主动登出。
	RevokeReasonLogout = "logout"
)

var (
	// ErrKickedByOtherClient 同一用户在另一类客户端登录，本会话已失效。
	ErrKickedByOtherClient = errors.New("已在其他端登录")
	// ErrKickedBySameClient 同一客户端类型只允许一个会话时，旧会话被新登录替换。
	ErrKickedBySameClient = errors.New("账号已在其他会话登录")
	// ErrSessionRevoked 令牌已被主动作废（例如登出），且不是被另一端踢下线。
	ErrSessionRevoked = errors.New("登录已失效，请重新登录")
)

// IssuedSession 是一次签发的令牌在会话表中的记录。
//
// 访问令牌和刷新令牌各有自己的 jti，用 FamilyID 绑成同一次登录。
// 登出时按族作废，避免只废掉访问令牌、刷新令牌还能换出新会话。
type IssuedSession struct {
	JTI        string
	UserID     int64
	ClientType string
	Scope      string
	FamilyID   string
	ExpiresAt  time.Time
}

// SessionRegistry 记录已签发的令牌，并按用户、客户端类型作废它们。
//
// 未登记的 jti 视为“功能上线前签发的旧令牌”，校验时放行，直到它自己过期。
// 新签发的令牌必须先登记再交给客户端，否则踢下线会漏掉它。
type SessionRegistry interface {
	Save(ctx context.Context, s IssuedSession) error
	// RevokeClient 作废该用户某一客户端类型下仍有效的会话。
	// keep 中的 jti 保留（新登录自己的令牌）。返回实际作废的条数。
	RevokeClient(ctx context.Context, userID int64, clientType, reason string, keep []string) (int, error)
	// RevokeFamily 作废 jti 所属的整次登录（访问令牌 + 刷新令牌）。
	// jti 不存在时视为已经作废，返回 nil。
	RevokeFamily(ctx context.Context, jti, reason string) error
	// Rejection 在 jti 已被作废时返回对应错误；未登记或仍有效时返回 nil。
	Rejection(ctx context.Context, jti string) error
	// ClientType 返回该 jti 登记时的客户端类型。未登记时 ok 为 false。
	ClientType(ctx context.Context, jti string) (clientType string, ok bool, err error)
}

// NormalizeClientType 把登录请求里的客户端类型归一成 web 或 desktop。
//
// 空值按 web 处理：管理接口、脚本和升级前的调用方都不带这个字段，
// 它们与浏览器控制台属于同一类，互相不踢；桌面客户端必须显式传 desktop。
func NormalizeClientType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ClientWeb:
		return ClientWeb, nil
	case ClientDesktop:
		return ClientDesktop, nil
	default:
		return "", fmt.Errorf("client_type 仅支持 web 或 desktop")
	}
}

// OppositeClient 返回另一类客户端。未知值视为 web，对端就是 desktop。
func OppositeClient(clientType string) string {
	if clientType == ClientDesktop {
		return ClientWeb
	}
	return ClientDesktop
}

type memRec struct {
	IssuedSession
	revoked bool
	reason  string
}

// MemorySessionStore 是进程内的会话表。
//
// 单进程测试和数据库尚未接上时使用。管理服务在数据库可用时会换成
// 落库实现，这样重启和多实例都能看到同一次踢下线。
type MemorySessionStore struct {
	mu    sync.Mutex
	items map[string]*memRec
}

// NewMemorySessionStore 创建空的内存会话表。
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{items: make(map[string]*memRec)}
}

// 编译期确认内存实现满足会话登记接口。
var _ SessionRegistry = (*MemorySessionStore)(nil)

// Save 登记一条令牌。已作废的 jti 不会被重新激活。
func (s *MemorySessionStore) Save(_ context.Context, in IssuedSession) error {
	if strings.TrimSpace(in.JTI) == "" || in.UserID == 0 || strings.TrimSpace(in.FamilyID) == "" {
		return fmt.Errorf("登录会话缺少标识")
	}
	ct, err := NormalizeClientType(in.ClientType)
	if err != nil {
		return err
	}
	in.ClientType = ct
	if in.Scope != ScopeAccess && in.Scope != ScopeRefresh {
		return fmt.Errorf("登录会话用途非法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(time.Now())
	if old, ok := s.items[in.JTI]; ok && old.revoked {
		return nil
	}
	s.items[in.JTI] = &memRec{IssuedSession: in}
	return nil
}

// RevokeClient 作废某一客户端类型的有效会话，keep 中的 jti 除外。
func (s *MemorySessionStore) RevokeClient(_ context.Context, userID int64, clientType, reason string, keep []string) (int, error) {
	ct, err := NormalizeClientType(clientType)
	if err != nil {
		return 0, err
	}
	hold := map[string]struct{}{}
	for _, id := range keep {
		if id != "" {
			hold[id] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	n := 0
	for _, rec := range s.items {
		if rec.UserID != userID || rec.ClientType != ct || rec.revoked {
			continue
		}
		if !rec.ExpiresAt.After(now) {
			continue
		}
		if _, skip := hold[rec.JTI]; skip {
			continue
		}
		rec.revoked = true
		rec.reason = reason
		n++
	}
	return n, nil
}

// RevokeFamily 作废 jti 所在的整次登录。
func (s *MemorySessionStore) RevokeFamily(_ context.Context, jti, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[jti]
	if !ok {
		return nil
	}
	family := rec.FamilyID
	for _, item := range s.items {
		if item.FamilyID == family {
			item.revoked = true
			if item.reason == "" {
				item.reason = reason
			}
		}
	}
	return nil
}

// Rejection 报告 jti 是否已被作废。
func (s *MemorySessionStore) Rejection(_ context.Context, jti string) error {
	if jti == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[jti]
	if !ok || !rec.revoked {
		return nil
	}
	return rejectionError(rec.reason)
}

// Owner 返回 jti 属于哪个用户、哪一端。未登记时 ok 为 false。已作废的行仍然返回，登出时还要知道该拆哪一端的 SQL 会话。
func (s *MemorySessionStore) Owner(_ context.Context, jti string) (int64, string, bool, error) {
	if jti == "" {
		return 0, "", false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[jti]
	if !ok {
		return 0, "", false, nil
	}
	return rec.UserID, rec.ClientType, true, nil
}

// ClientType 查询 jti 登记的客户端类型。
func (s *MemorySessionStore) ClientType(_ context.Context, jti string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[jti]
	if !ok {
		return "", false, nil
	}
	return rec.ClientType, true, nil
}

// purgeLocked 丢掉已经过期一会儿的记录。
//
// 多留一分钟，避免令牌里的 exp 和这里的 ExpiresAt 差几毫秒时，
// 记录先被删掉、校验又把“查无此 jti”当成旧令牌放行。
func (s *MemorySessionStore) purgeLocked(now time.Time) {
	cutoff := now.Add(-time.Minute)
	for id, rec := range s.items {
		if rec.ExpiresAt.Before(cutoff) {
			delete(s.items, id)
		}
	}
}

func rejectionError(reason string) error {
	switch reason {
	case RevokeReasonOpposite:
		return ErrKickedByOtherClient
	case RevokeReasonSame:
		return ErrKickedBySameClient
	default:
		return ErrSessionRevoked
	}
}
