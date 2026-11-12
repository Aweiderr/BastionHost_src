package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bastion.local/desktop/internal/coreapi"
	"bastion.local/desktop/internal/launch"
)

// LocalLaunchResult 是一次本机工具启动的结果。命令里没有口令。
type LocalLaunchResult struct {
	Protocol string `json:"protocol"`
	Tool     string `json:"tool"`
	Command  string `json:"command"`
	Target   string `json:"target"`
	Login    string `json:"login"`
	Message  string `json:"message"`
	Started  bool   `json:"started"`
}

// LaunchLocalTool 打开本机已安装的 SFTP、RDP 或 VNC 工具，目标是分配到的堡垒机入口。
func (a *App) LaunchLocalTool(protocol, assetName, accountName string, assetID, accountID int64, assetHost string, assetPort int) (*LocalLaunchResult, error) {
	_ = assetHost
	_ = assetPort
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	a.mu.Lock()
	user := a.user
	a.mu.Unlock()
	if user == nil {
		return nil, fmt.Errorf("尚未登录")
	}
	asset, err := a.lookupAsset(assetID)
	if err != nil {
		return nil, err
	}
	account, err := accountOnAsset(asset, accountID, accountName)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(asset.AssetName)
	if name == "" {
		name = strings.TrimSpace(assetName)
	}
	switch protocol {
	case "sftp":
		kind := strings.ToLower(strings.TrimSpace(asset.Protocol))
		if kind != "" && kind != "ssh" && kind != "sftp" {
			return nil, fmt.Errorf("本机 SFTP 只用于 SSH 或 SFTP 资产")
		}
		if !asset.CanFile {
			return nil, fmt.Errorf("当前授权没有文件传输（can_file），已拒绝打开本机 SFTP")
		}
	case "rdp", "vnc":
		if strings.ToLower(strings.TrimSpace(asset.Protocol)) != protocol {
			return nil, fmt.Errorf("这台资产不是 %s", strings.ToUpper(protocol))
		}
	default:
		return nil, fmt.Errorf("本机工具只用于 SFTP、RDP 和 VNC")
	}
	bastion, sshPort, err := a.bastionEntry(protocol, assetID, accountID)
	if err != nil {
		return nil, err
	}
	env := launch.DefaultEnv()
	if a.launchEnv != nil {
		env = *a.launchEnv
	}
	plan, err := launch.Build(launch.Request{
		Protocol:  protocol,
		OpsUser:   user.Username,
		AssetName: name,
		AssetID:   asset.AssetID,
		Account:   account,
		Bastion:   bastion,
		SSHPort:   sshPort,
		AssetHost: asset.Host,
		AssetPort: asset.Port,
	}, env)
	if err != nil {
		return nil, err
	}
	start := a.launchStart
	if start == nil {
		start = launch.Start
	}
	if err := start(plan); err != nil {
		return nil, fmt.Errorf("无法启动本机工具：%s", err.Error())
	}
	return &LocalLaunchResult{
		Protocol: plan.Protocol,
		Tool:     plan.Tool,
		Command:  plan.Display,
		Target:   plan.Target,
		Login:    plan.Login,
		Message:  plan.Message,
		Started:  true,
	}, nil
}

func (a *App) lookupAsset(assetID int64) (*coreapi.Asset, error) {
	if a.api == nil {
		return nil, fmt.Errorf("无法确认资产授权")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := a.api.AccessibleAssets(ctx)
	if err != nil {
		if a.api.Token() == "" {
			return nil, fmt.Errorf("登录已失效，请重新登录")
		}
		return nil, fmt.Errorf("无法确认资产授权：%s", err.Error())
	}
	for _, item := range items {
		if item != nil && item.AssetID == assetID {
			return item, nil
		}
	}
	return nil, fmt.Errorf("没有这台资产的授权")
}

func accountOnAsset(asset *coreapi.Asset, accountID int64, accountName string) (string, error) {
	if asset == nil {
		return "", fmt.Errorf("没有这台资产的授权")
	}
	if accountID != 0 {
		for _, acc := range asset.Accounts {
			if acc.AccountID == accountID {
				return acc.AccountName, nil
			}
		}
		return "", fmt.Errorf("该账号不在授权列表中")
	}
	name := strings.TrimSpace(accountName)
	if name == "" && len(asset.Accounts) == 1 {
		return asset.Accounts[0].AccountName, nil
	}
	for _, acc := range asset.Accounts {
		if acc.AccountName == name {
			return acc.AccountName, nil
		}
	}
	if name == "" {
		return "", nil
	}
	return "", fmt.Errorf("该账号不在授权列表中")
}

func (a *App) bastionEntry(protocol string, assetID, accountID int64) (string, int, error) {
	switch protocol {
	case "sftp":
		addr, err := a.assignedSSHGateway(assetID, accountID)
		if err != nil {
			return "", 0, err
		}
		if strings.TrimSpace(addr) == "" {
			return "", 0, fmt.Errorf("没有堡垒机入口地址，已拒绝直连资产")
		}
		return addr, 0, nil
	case "rdp", "vnc":
		raw, err := a.assignedDesktopURL(assetID, accountID, protocol)
		if err != nil {
			return "", 0, err
		}
		if strings.TrimSpace(raw) == "" {
			return "", 0, fmt.Errorf("没有堡垒机入口地址，已拒绝直连资产")
		}
		return raw, 0, nil
	default:
		return "", 0, fmt.Errorf("本机工具只用于 SFTP、RDP 和 VNC")
	}
}
