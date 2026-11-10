package service

import (
	"context"
	"testing"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

// clusterStub 只实现停用 / 摘除审计测试会碰到的方法，其余返回零值。
type clusterStub struct {
	member *model.ClusterMember
	gone   bool
}

func (c *clusterStub) Create(context.Context, *model.Cluster, []repo.MemberInput) error { return nil }
func (c *clusterStub) List(context.Context) ([]*model.Cluster, error)                   { return nil, nil }
func (c *clusterStub) Get(context.Context, int64) (*model.Cluster, error)               { return nil, nil }
func (c *clusterStub) Update(context.Context, int64, string, string, string) error      { return nil }
func (c *clusterStub) Delete(context.Context, int64) error                              { return nil }
func (c *clusterStub) Members(context.Context, int64) ([]*model.ClusterMember, error) {
	return nil, nil
}
func (c *clusterStub) AddMembers(context.Context, int64, []repo.MemberInput) error { return nil }
func (c *clusterStub) RemoveMember(context.Context, int64, int64) error {
	c.gone = true
	return nil
}
func (c *clusterStub) SetMemberState(_ context.Context, _, _ int64, state string) error {
	if c.member != nil {
		c.member.AdminState = state
	}
	return nil
}
func (c *clusterStub) UpdateMember(context.Context, int64, int64, *string, *int, *int) error {
	return nil
}
func (c *clusterStub) MemberByIP(context.Context, string) (*model.ClusterMember, error) {
	return nil, nil
}
func (c *clusterStub) MemberByID(context.Context, int64, int64) (*model.ClusterMember, error) {
	if c.gone || c.member == nil {
		return nil, nil
	}
	cp := *c.member
	return &cp, nil
}
func (c *clusterStub) SaveProbe(context.Context, int64, repo.ProbeResult) error { return nil }
func (c *clusterStub) RecordFailover(context.Context, *model.ClusterFailover) error {
	return nil
}
func (c *clusterStub) ListFailovers(context.Context, int64, int) ([]*model.ClusterFailover, error) {
	return nil, nil
}
func (c *clusterStub) LastFailovers(context.Context) (map[int64]*model.ClusterFailover, error) {
	return nil, nil
}

func TestMemberDisableAndRemoveAreAudited(t *testing.T) {
	logs := &memLog{}
	stub := &clusterStub{member: &model.ClusterMember{
		MemberID: 4, ClusterID: 1, DeviceIP: "10.0.0.9", DeviceName: "备机",
		AdminState: model.MemberStateActive,
	}}
	svc := &ClusterService{clusters: stub, oplog: logs}
	op := OperatorContext{UserID: 3, Username: "sys", ClientIP: "10.9.9.9"}
	ctx := context.Background()
	if err := svc.SetMemberState(ctx, 1, 4, model.MemberStateDisabled, op); err != nil {
		t.Fatal(err)
	}
	if len(logs.items) != 1 || logs.items[0].Action != "member_disable" || logs.items[0].TargetType != "cluster_member" {
		t.Fatalf("disable: %+v", logs.items)
	}
	if logs.items[0].TargetID != "4" || logs.items[0].OperatorNameSnap != "sys" {
		t.Fatalf("identity: %+v", logs.items[0])
	}
	if err := svc.RemoveMember(ctx, 1, 4, op); err != nil {
		t.Fatal(err)
	}
	if len(logs.items) != 2 || logs.items[1].Action != "member_remove" {
		t.Fatalf("remove: %+v", logs.items)
	}
}
