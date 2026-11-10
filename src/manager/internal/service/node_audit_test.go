package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

type fakeNodes struct {
	items map[string]*model.Node
}

func (f *fakeNodes) Upsert(context.Context, repo.HeartbeatInput) (*model.Node, error) {
	return nil, nil
}
func (f *fakeNodes) List(context.Context, string) ([]*model.Node, error) {
	out := make([]*model.Node, 0, len(f.items))
	for _, n := range f.items {
		out = append(out, n)
	}
	return out, nil
}
func (f *fakeNodes) Get(_ context.Context, id string) (*model.Node, error) {
	n := f.items[id]
	if n == nil {
		return nil, nil
	}
	cp := *n
	return &cp, nil
}
func (f *fakeNodes) SetAdminState(_ context.Context, id, state string) (*model.Node, error) {
	n := f.items[id]
	if n == nil {
		return nil, nil
	}
	n.AdminState = state
	cp := *n
	return &cp, nil
}
func (f *fakeNodes) Update(context.Context, string, repo.NodeUpdate) (*model.Node, error) {
	return nil, nil
}
func (f *fakeNodes) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

func TestNodeDisableAndRemoveAreAudited(t *testing.T) {
	logs := &memLog{}
	nodes := &fakeNodes{items: map[string]*model.Node{
		"proxy-a": {
			NodeID: "proxy-a", NodeName: "代理甲", NodeRole: model.NodeRoleProxy,
			AdminState: model.NodeStateActive, Weight: 100, Address: "10.0.0.8:8081",
		},
	}}
	svc := &NodeService{nodes: nodes, oplog: logs}
	op := OperatorContext{UserID: 7, Username: "sec", ClientIP: "10.0.0.1"}
	ctx := context.Background()

	if _, err := svc.SetState(ctx, "proxy-a", model.NodeStateDisabled, op); err != nil {
		t.Fatal(err)
	}
	if len(logs.items) != 1 || logs.items[0].Action != "node_disable" || logs.items[0].Module != "cluster" {
		t.Fatalf("disable log: %+v", logs.items)
	}
	if logs.items[0].TargetID != "proxy-a" || logs.items[0].OperatorNameSnap != "sec" {
		t.Fatalf("audit identity: %+v", logs.items[0])
	}
	if !strings.Contains(logs.items[0].AfterValue, `"admin_state":"disabled"`) {
		t.Fatalf("after: %s", logs.items[0].AfterValue)
	}

	if err := svc.Remove(ctx, "proxy-a", op); err != nil {
		t.Fatal(err)
	}
	if len(logs.items) != 2 || logs.items[1].Action != "node_remove" {
		t.Fatalf("remove log: %+v", logs.items)
	}
	if _, ok := nodes.items["proxy-a"]; ok {
		t.Fatal("node still present")
	}
}

func TestNodeRemoveRefusesOnlineWithoutAudit(t *testing.T) {
	logs := &memLog{}
	hb := time.Now()
	nodes := &fakeNodes{items: map[string]*model.Node{
		"proxy-a": {
			NodeID: "proxy-a", AdminState: model.NodeStateActive, Weight: 100,
			LastHeartbeatAt: &hb,
		},
	}}
	svc := &NodeService{nodes: nodes, oplog: logs}
	err := svc.Remove(context.Background(), "proxy-a", OperatorContext{Username: "sec"})
	if err == nil {
		t.Fatal("online node was removed")
	}
	if len(logs.items) != 0 {
		t.Fatalf("failed remove should not be audited: %+v", logs.items)
	}
}

func TestNodeDrainIsAudited(t *testing.T) {
	logs := &memLog{}
	nodes := &fakeNodes{items: map[string]*model.Node{
		"proxy-a": {NodeID: "proxy-a", NodeName: "甲", AdminState: model.NodeStateActive, Weight: 100},
	}}
	svc := &NodeService{nodes: nodes, oplog: logs}
	if _, err := svc.SetState(context.Background(), "proxy-a", model.NodeStateDraining, OperatorContext{Username: "ops"}); err != nil {
		t.Fatal(err)
	}
	if logs.items[0].Action != "node_drain" {
		t.Fatalf("action=%s", logs.items[0].Action)
	}
}
