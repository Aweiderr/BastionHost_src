// Package configx 提供环境变量读取的小工具，供各服务复用。
package configx

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Str 读取字符串，带默认值。
func Str(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Int 读取整数，带默认值。
func Int(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Bool 读取布尔值，带默认值。
func Bool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// Duration 读取时长（如 30s、5m），带默认值。
func Duration(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// CSV 读取逗号分隔列表；空环境变量时返回 def 的副本（可为 nil）。
func CSV(key string, def []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		if def == nil {
			return nil
		}
		out := make([]string, len(def))
		copy(out, def)
		return out
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
