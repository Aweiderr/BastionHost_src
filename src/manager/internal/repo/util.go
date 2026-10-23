package repo

import (
	"encoding/json"
	"time"
)

// jsonMarshal 是 encoding/json 的薄封装，便于集中替换实现。
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// jsonUnmarshal 是 encoding/json 的薄封装。
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// nowDate 返回 YYYYMMDD 格式的当前日期，用于生成单号。
func nowDate() string { return time.Now().Format("20060102") }
