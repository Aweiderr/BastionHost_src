package launch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Start 按计划启动本机工具。不等待它退出。{{RDP}} 会换成临时 rdp 文件路径。
func Start(plan Plan) error {
	if plan.Exe == "" {
		return fmt.Errorf("没有可执行文件")
	}
	args := append([]string{}, plan.Args...)
	var temps []string
	for _, file := range plan.Files {
		path, err := writeTemp(file)
		if err != nil {
			return err
		}
		temps = append(temps, path)
		for i, arg := range args {
			if arg == "{{RDP}}" {
				args[i] = path
			}
		}
	}
	cmd := exec.Command(plan.Exe, args...)
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		for _, path := range temps {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}

func writeTemp(file File) (string, error) {
	name := filepath.Base(file.Name)
	if name == "" || name == "." || strings.Contains(name, "..") {
		return "", fmt.Errorf("临时文件名无效")
	}
	f, err := os.CreateTemp("", "bastion-*"+name)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.Write(file.Data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
