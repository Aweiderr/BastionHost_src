package service

import (
	"context"
	"testing"
	"time"

	"bastion.local/manager/internal/repo"
)

// memLease 用与数据库 WHERE 相同的 CanAcquireLease 判断能否接管。
type memLease struct {
	row *repo.LeaderLease
	now func() time.Time
}

func newMemLease(now func() time.Time) *memLease {
	if now == nil {
		now = time.Now
	}
	return &memLease{now: now}
}

func (m *memLease) TryAcquire(_ context.Context, scope, leaderID, addr string, ttl time.Duration) (repo.LeaderLease, bool, error) {
	now := m.now()
	owner, until := "", time.Time{}
	if m.row != nil {
		owner, until = m.row.LeaderID, m.row.Until
	}
	if !CanAcquireLease(owner, until, now, leaderID) {
		return *m.row, false, nil
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	m.row = &repo.LeaderLease{Scope: scope, LeaderID: leaderID, Address: addr, Until: now.Add(ttl)}
	return *m.row, true, nil
}

func (m *memLease) Get(_ context.Context, scope string) (repo.LeaderLease, bool, error) {
	if m.row == nil || (scope != "" && m.row.Scope != scope) {
		return repo.LeaderLease{}, false, nil
	}
	return *m.row, true, nil
}

func TestLeaderRenewAndTakeover(t *testing.T) {
	now := time.Now()
	store := newMemLease(func() time.Time { return now })
	ctx := context.Background()

	got, ok, err := store.TryAcquire(ctx, leaderScopeSchedule, "manager-a", "a:8080", 15*time.Second)
	if err != nil || !ok || got.LeaderID != "manager-a" {
		t.Fatalf("acquire a: %+v ok=%v err=%v", got, ok, err)
	}
	_, ok, err = store.TryAcquire(ctx, leaderScopeSchedule, "manager-b", "b:8080", 15*time.Second)
	if err != nil || ok {
		t.Fatalf("b stole a live lease: ok=%v err=%v", ok, err)
	}
	renewed, ok, err := store.TryAcquire(ctx, leaderScopeSchedule, "manager-a", "a:8080", 15*time.Second)
	if err != nil || !ok || !renewed.Until.Equal(now.Add(15*time.Second)) {
		t.Fatalf("renew: %+v ok=%v err=%v", renewed, ok, err)
	}

	now = now.Add(16 * time.Second)
	taken, ok, err := store.TryAcquire(ctx, leaderScopeSchedule, "manager-b", "b:8080", 15*time.Second)
	if err != nil || !ok || taken.LeaderID != "manager-b" {
		t.Fatalf("takeover: %+v ok=%v err=%v", taken, ok, err)
	}

	leader := NewLeader(store, nil, "manager-b", "b:8080")
	view := leader.Current(ctx)
	if !view.Held || !view.IsSelf || view.ID != "manager-b" {
		t.Fatalf("view=%+v", view)
	}
}
