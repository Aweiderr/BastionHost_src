package service

import (
	"testing"
	"time"

	"bastion.local/manager/internal/model"
)

func liveSet(ids ...int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func activeSession(id int64, node string, start time.Time) *model.Session {
	return &model.Session{
		SessionID: id,
		ProxyNode: node,
		Status:    model.SessionStatusActive,
		StartTime: start,
	}
}

func TestLiveSessionRegistryNilIsNotEmpty(t *testing.T) {
	reg := NewLiveSessionRegistry()
	reg.Observe("node-1", nil, false)
	snap := reg.snapshot()
	if snap["node-1"].reported {
		t.Fatal("omitted live_session_ids must not arm reconciliation")
	}
	reg.Observe("node-1", []int64{}, true)
	snap = reg.snapshot()
	if !snap["node-1"].reported {
		t.Fatal("empty list means the node is online with no live sessions")
	}
	if snap["node-1"].ids == nil {
		t.Fatal("reported empty set should be a non-nil map")
	}
	reg.Observe("node-1", []int64{40, 0, -3}, true)
	snap = reg.snapshot()
	if _, ok := snap["node-1"].ids[40]; !ok {
		t.Fatal("session 40 was dropped")
	}
	if len(snap["node-1"].ids) != 1 {
		t.Fatalf("non-positive ids must be ignored, got %d", len(snap["node-1"].ids))
	}
}

func TestSelectOrphansClosesOnlyAfterGrace(t *testing.T) {
	r := NewSessionReconciler(nil, nil)
	r.minAge = 30 * time.Second
	r.orphanGrace = 45 * time.Second
	now := time.Date(2026, 10, 26, 10, 30, 0, 0, time.UTC)
	old := now.Add(-2 * time.Minute)
	// 40 is the production shape: DB active, node online, lives does not contain it.
	// 41 is a real WebSSH/SSH session still registered on the node.
	sessions := []*model.Session{
		activeSession(40, "node-1", old),
		activeSession(41, "node-1", old),
	}
	reports := map[string]liveNodeReport{
		"node-1": {reported: true, at: now, ids: liveSet(41)},
	}

	if got := r.selectOrphans(now, sessions, reports, 60*time.Second); len(got) != 0 {
		t.Fatalf("first miss must not close, got %v", got)
	}
	if got := r.selectOrphans(now.Add(44*time.Second), sessions, reports, 60*time.Second); len(got) != 0 {
		t.Fatalf("inside grace must not close, got %v", got)
	}
	got := r.selectOrphans(now.Add(45*time.Second), sessions, reports, 60*time.Second)
	if len(got) != 1 || got[0] != 40 {
		t.Fatalf("orphan 40 should close and live 41 should stay, got %v", got)
	}
}

func TestSelectOrphansKeepsYoungAndUnreportedSessions(t *testing.T) {
	r := NewSessionReconciler(nil, nil)
	r.minAge = 30 * time.Second
	r.orphanGrace = 45 * time.Second
	now := time.Date(2026, 10, 26, 10, 30, 0, 0, time.UTC)
	sessions := []*model.Session{
		// Just created: StartSession has returned, registerLive may not have run.
		activeSession(42, "node-1", now.Add(-5*time.Second)),
		// Old proxy that does not send live_session_ids.
		activeSession(43, "node-2", now.Add(-time.Hour)),
		// Fresh report belongs to another node; must not be applied here.
		activeSession(44, "node-1", now.Add(-time.Hour)),
		// Node heartbeat itself is stale; CloseStale owns that case.
		activeSession(45, "node-3", now.Add(-time.Hour)),
	}
	reports := map[string]liveNodeReport{
		"node-1": {reported: true, at: now, ids: liveSet()},
		"node-2": {reported: false, at: now},
		"node-3": {reported: true, at: now.Add(-2 * time.Minute), ids: liveSet()},
	}
	if got := r.selectOrphans(now, sessions, reports, 60*time.Second); len(got) != 0 {
		t.Fatalf("none of these are confirmed orphans, got %v", got)
	}
	// A later pass still must not close them: 44 is in an empty live set and
	// is old, so it arms on the first call and closes only after grace.
	// The others stay ineligible, so this second call is the grace boundary
	// for 44 only.
	got := r.selectOrphans(now.Add(45*time.Second), sessions, reports, 60*time.Second)
	if len(got) != 1 || got[0] != 44 {
		t.Fatalf("only the aged session on a fresh empty live set closes, got %v", got)
	}
}

func TestSelectOrphansResetsWhenSessionReturns(t *testing.T) {
	r := NewSessionReconciler(nil, nil)
	r.minAge = 30 * time.Second
	r.orphanGrace = 45 * time.Second
	now := time.Date(2026, 10, 26, 10, 30, 0, 0, time.UTC)
	sess := []*model.Session{activeSession(40, "node-1", now.Add(-time.Hour))}
	absent := map[string]liveNodeReport{
		"node-1": {reported: true, at: now, ids: liveSet()},
	}
	if got := r.selectOrphans(now, sess, absent, 60*time.Second); len(got) != 0 {
		t.Fatalf("arming the timer should not close, got %v", got)
	}
	back := map[string]liveNodeReport{
		"node-1": {reported: true, at: now.Add(10 * time.Second), ids: liveSet(40)},
	}
	if got := r.selectOrphans(now.Add(10*time.Second), sess, back, 60*time.Second); len(got) != 0 {
		t.Fatalf("live session must not close, got %v", got)
	}
	// Absence starts over; the earlier timer must not still be running.
	goneAgain := map[string]liveNodeReport{
		"node-1": {reported: true, at: now.Add(time.Minute), ids: liveSet()},
	}
	if got := r.selectOrphans(now.Add(time.Minute), sess, goneAgain, 60*time.Second); len(got) != 0 {
		t.Fatalf("return to the live set must clear the grace timer, got %v", got)
	}
	got := r.selectOrphans(now.Add(time.Minute+45*time.Second), sess, goneAgain, 60*time.Second)
	if len(got) != 1 || got[0] != 40 {
		t.Fatalf("closes only after a fresh grace window, got %v", got)
	}
}
