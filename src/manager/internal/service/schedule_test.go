package service

import (
	"testing"
	"time"

	"bastion.local/manager/internal/model"
)

func proxy(id string, active, weight, max int, state string, online bool) SchedNode {
	return SchedNode{
		NodeID: id, NodeName: id, Address: id + ":8081",
		AdminState: state, Weight: weight, ActiveSessions: active,
		MaxSessions: max, Online: online,
	}
}

func TestEligibleSkipsUnhealthyAndMaintenance(t *testing.T) {
	cases := []struct {
		name string
		n    SchedNode
		ok   bool
	}{
		{"healthy", proxy("a", 1, 100, 10, model.NodeStateActive, true), true},
		{"offline", proxy("a", 0, 100, 10, model.NodeStateActive, false), false},
		{"draining", proxy("a", 0, 100, 10, model.NodeStateDraining, true), false},
		{"disabled", proxy("a", 0, 100, 10, model.NodeStateDisabled, true), false},
		{"weight zero", proxy("a", 0, 0, 10, model.NodeStateActive, true), false},
		{"full", proxy("a", 10, 100, 10, model.NodeStateActive, true), false},
		{"member blocked", func() SchedNode {
			n := proxy("a", 0, 100, 10, model.NodeStateActive, true)
			n.MemberBlocked = true
			return n
		}(), false},
	}
	for _, tc := range cases {
		if got := tc.n.Eligible(); got != tc.ok {
			t.Errorf("%s: eligible=%v, want %v", tc.name, got, tc.ok)
		}
	}
}

func TestSelectLeastLoadSkipsBadNodesAndIsStable(t *testing.T) {
	nodes := []SchedNode{
		proxy("node-c", 1, 100, 50, model.NodeStateActive, true),
		proxy("node-a", 9, 100, 50, model.NodeStateDraining, true),
		proxy("node-b", 0, 100, 50, model.NodeStateDisabled, true),
		proxy("node-d", 1, 100, 50, model.NodeStateActive, false),
		proxy("node-e", 4, 100, 50, model.NodeStateActive, true),
	}
	full := proxy("node-f", 50, 100, 50, model.NodeStateActive, true)
	nodes = append(nodes, full)

	first, direct, err := SelectNode(nodes, ScheduleLeastLoad, "7|3|ssh")
	if err != nil || direct || first == nil {
		t.Fatalf("select: node=%v direct=%v err=%v", first, direct, err)
	}
	if first.NodeID != "node-c" {
		t.Fatalf("least load = %s, want node-c (1 session; node-e has 4; others ineligible)", first.NodeID)
	}
	second, _, err := SelectNode(nodes, ScheduleLeastLoad, "7|3|ssh")
	if err != nil || second.NodeID != first.NodeID {
		t.Fatalf("repeat select = %v err=%v", second, err)
	}

	// 负载相同取 node_id 较小者。
	tied := []SchedNode{
		proxy("node-z", 2, 100, 10, model.NodeStateActive, true),
		proxy("node-m", 2, 100, 10, model.NodeStateActive, true),
	}
	got, _, err := SelectNode(tied, ScheduleLeastLoad, "")
	if err != nil || got.NodeID != "node-m" {
		t.Fatalf("tie break = %v err=%v", got, err)
	}
}

func TestSelectWeightPrefersHigherWeight(t *testing.T) {
	nodes := []SchedNode{
		proxy("light", 2, 100, 100, model.NodeStateActive, true),
		proxy("heavy", 3, 300, 100, model.NodeStateActive, true),
	}
	// light 得分 20，heavy 得分 10，选 heavy。
	got, _, err := SelectNode(nodes, ScheduleLeastLoad, "")
	if err != nil || got.NodeID != "heavy" {
		t.Fatalf("weighted = %v err=%v", got, err)
	}
}

func TestSelectConsistentHashStableUntilNodeDrops(t *testing.T) {
	nodes := []SchedNode{
		proxy("n1", 1, 100, 10, model.NodeStateActive, true),
		proxy("n2", 1, 100, 10, model.NodeStateActive, true),
		proxy("n3", 1, 100, 10, model.NodeStateActive, true),
	}
	sticky := "42|9|ssh"
	a, _, err := SelectNode(nodes, ScheduleConsistentHash, sticky)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := SelectNode(nodes, ScheduleConsistentHash, sticky)
	if err != nil || a.NodeID != b.NodeID {
		t.Fatalf("hash not stable: %s vs %v err=%v", a.NodeID, b, err)
	}
	var rest []SchedNode
	for _, n := range nodes {
		if n.NodeID == a.NodeID {
			n.Online = false
		}
		rest = append(rest, n)
	}
	c, _, err := SelectNode(rest, ScheduleConsistentHash, sticky)
	if err != nil {
		t.Fatal(err)
	}
	if c.NodeID == a.NodeID {
		t.Fatal("offline node still selected")
	}
	if !c.Eligible() {
		t.Fatal("replacement is not eligible")
	}
}

func TestSelectEmptyIsDirectAndNoneHealthyErrors(t *testing.T) {
	if _, direct, err := SelectNode(nil, ScheduleLeastLoad, ""); err != nil || !direct {
		t.Fatalf("empty: direct=%v err=%v", direct, err)
	}
	nodes := []SchedNode{proxy("a", 0, 100, 10, model.NodeStateDraining, true)}
	if _, _, err := SelectNode(nodes, ScheduleLeastLoad, ""); err != ErrNoScheduleNode {
		t.Fatalf("err=%v", err)
	}
}

func TestCanAcquireLease(t *testing.T) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	if !CanAcquireLease("", time.Time{}, now, "a") {
		t.Fatal("empty owner should be acquirable")
	}
	if CanAcquireLease("a", now.Add(time.Second), now, "b") {
		t.Fatal("live lease must not be stolen")
	}
	if !CanAcquireLease("a", now.Add(time.Second), now, "a") {
		t.Fatal("holder should renew")
	}
	if !CanAcquireLease("a", now.Add(-time.Second), now, "b") {
		t.Fatal("expired lease should be taken over")
	}
}

func TestBuildAssignmentUsesReachableIP(t *testing.T) {
	n := proxy("node-a", 0, 100, 10, model.NodeStateActive, true)
	n.Address = "bastion-a:8081"
	n.LocalIPs = []string{"10.1.2.3"}
	a := buildAssignment(&n, ScheduleLeastLoad, 2222, "wss")
	if a.Host != "10.1.2.3" || a.HTTPPort != 8081 {
		t.Fatalf("host=%s port=%d", a.Host, a.HTTPPort)
	}
	if a.WebSSHURL != "wss://10.1.2.3:8081/api/v1/webssh" {
		t.Fatalf("webssh=%s", a.WebSSHURL)
	}
	if a.SSHAddr != "10.1.2.3:2222" {
		t.Fatalf("ssh=%s", a.SSHAddr)
	}
}
