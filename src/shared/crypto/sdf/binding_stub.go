//go:build !(linux && (amd64 || arm64))

package sdf

import "fmt"

func openDevice(path string) (Device, error) {
	return nil, fmt.Errorf("%w: SDF 动态加载仅支持 linux/amd64 与 linux/arm64，当前构建无法打开 %s", errNotImplemented, path)
}
