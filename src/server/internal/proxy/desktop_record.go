package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// 图形录像明文以 "BSG1 " 开头，后面是一行一个 JSON。
// 字符会话录像是终端输出原文，不会带这个头。回放必须先看魔数，不能把图形录像当终端播。
//
//	BSG1 1
//	{"type":"meta","protocol":"vnc","w":1280,"h":720}
//	{"type":"frame","t":200,"w":1280,"h":720,"jpeg":"<base64>"}
//
// t 是相对录像开始的毫秒。jpeg 是整屏采样，不是单个脏矩形。
const graphicalRecordMagic = "BSG1 1\n"

// graphicalRecordMinInterval 是两次整屏采样的最小间隔。测试可以改成 0。
var graphicalRecordMinInterval = 200 * time.Millisecond

type graphicalLog struct {
	mu       sync.Mutex
	w        io.Writer
	start    time.Time
	protocol string
	header   bool
}

func newGraphicalLog(w io.Writer, protocol string) *graphicalLog {
	return &graphicalLog{w: w, start: time.Now(), protocol: protocol}
}

func (g *graphicalLog) writeMeta(width, height int) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writeHeaderLocked()
	g.writeJSONLocked(map[string]any{
		"type": "meta", "protocol": g.protocol, "w": width, "h": height,
	})
}

func (g *graphicalLog) writeFrame(width, height int, jpeg []byte) {
	if g == nil || len(jpeg) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writeHeaderLocked()
	g.writeJSONLocked(map[string]any{
		"type": "frame",
		"t":    time.Since(g.start).Milliseconds(),
		"w":    width,
		"h":    height,
		"jpeg": base64.StdEncoding.EncodeToString(jpeg),
	})
}

func (g *graphicalLog) writeHeaderLocked() {
	if g.header {
		return
	}
	_, _ = io.WriteString(g.w, graphicalRecordMagic)
	g.header = true
}

func (g *graphicalLog) writeJSONLocked(v map[string]any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	raw = append(raw, '\n')
	_, _ = g.w.Write(raw)
}

type graphicalFrame struct {
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	T        int64  `json:"t"`
	JPEG     string `json:"jpeg"`
}

func isGraphicalRecord(plain []byte) bool {
	return bytes.HasPrefix(plain, []byte("BSG1 "))
}

func parseGraphicalRecord(plain []byte) (protocol string, width, height int, frames []graphicalFrame, err error) {
	if !isGraphicalRecord(plain) {
		return "", 0, 0, nil, fmt.Errorf("不是图形录像")
	}
	for _, line := range strings.Split(string(plain), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "BSG1 ") {
			continue
		}
		var frame graphicalFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			return "", 0, 0, nil, fmt.Errorf("图形录像无法解析：%w", err)
		}
		switch frame.Type {
		case "meta":
			protocol = frame.Protocol
			if frame.W > 0 && frame.H > 0 {
				width, height = frame.W, frame.H
			}
		case "frame":
			if frame.JPEG == "" {
				return "", 0, 0, nil, fmt.Errorf("图形录像缺少画面")
			}
			if _, err := base64.StdEncoding.DecodeString(frame.JPEG); err != nil {
				return "", 0, 0, nil, fmt.Errorf("图形录像画面无法解码：%w", err)
			}
			frames = append(frames, frame)
			if frame.W > 0 && frame.H > 0 {
				width, height = frame.W, frame.H
			}
		default:
			return "", 0, 0, nil, fmt.Errorf("图形录像事件 %s 不受支持", frame.Type)
		}
	}
	return protocol, width, height, frames, nil
}
