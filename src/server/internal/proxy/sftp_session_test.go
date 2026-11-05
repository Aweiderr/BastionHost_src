package proxy

import (
	"testing"

	"bastion.local/server/internal/managerclient"
)

func TestPickSFTPTarget(t *testing.T) {
	assets := []*managerclient.AssetBrief{
		{AssetID: 1, AssetName: "web", Host: "10.0.0.8", Protocol: "ssh", Accounts: []managerclient.AccountBrief{
			{AccountID: 4, AccountName: "root"},
			{AccountID: 5, AccountName: "app"},
		}},
		{AssetID: 2, AssetName: "win", Protocol: "rdp", Accounts: []managerclient.AccountBrief{{AccountID: 9, AccountName: "administrator"}}},
	}
	if _, _, err := pickSFTPTarget(assets, ""); err == nil {
		t.Fatal("multiple assets need a hint")
	}
	asset, acc, err := pickSFTPTarget(assets, "web#app")
	if err != nil || asset.AssetID != 1 || acc.AccountName != "app" {
		t.Fatalf("picked %#v %#v %v", asset, acc, err)
	}
	if _, _, err := pickSFTPTarget(assets, "win#administrator"); err == nil {
		t.Fatal("rdp is not an sftp target")
	}
	only := []*managerclient.AssetBrief{assets[0]}
	only[0].Accounts = []managerclient.AccountBrief{{AccountID: 4, AccountName: "root"}}
	asset, acc, err = pickSFTPTarget(only, "")
	if err != nil || acc.AccountName != "root" {
		t.Fatalf("single %#v %v", acc, err)
	}
}

func TestSubsystemName(t *testing.T) {
	// SSH string: uint32 length + bytes. ssh.Unmarshal accepts that layout.
	raw := append([]byte{0, 0, 0, 4}, []byte("sftp")...)
	if subsystemName(raw) != "sftp" {
		t.Fatalf("name %q", subsystemName(raw))
	}
	if subsystemName([]byte{0, 0, 0, 4, 'e', 'x', 'e', 'c'}) == "sftp" {
		t.Fatal("exec")
	}
}
