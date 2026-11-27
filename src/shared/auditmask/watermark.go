package auditmask

import (
	"strconv"
	"strings"
	"time"
)

// 水印样式。
const (
	StyleDiagonal = "diagonal"
	StyleTile     = "tile"
	StyleCorner   = "corner"
)

// Watermark 是回放叠层和导出件共用的水印配置。
type Watermark struct {
	Enabled  bool    `json:"enabled"`
	Style    string  `json:"style"`
	Opacity  float64 `json:"opacity"`
	FontSize int     `json:"font_size"`
	Angle    int     `json:"angle"`
	Template string  `json:"template"`
}

// DefaultWatermark 是未单独配置时的水印。默认开启，不能靠“没配”绕过。
func DefaultWatermark() Watermark {
	return Watermark{
		Enabled:  true,
		Style:    StyleDiagonal,
		Opacity:  0.18,
		FontSize: 14,
		Angle:    -24,
		Template: "${operator} ${session} ${asset} ${time}",
	}
}

// Normalize 校正样式、字号和角度。
//
// 关闭水印，或把透明度降到看不清，视为绕过。调用方必须记审计并拒绝保存。
func (w Watermark) Normalize() (Watermark, error) {
	if !w.Enabled {
		return Watermark{}, errBypass("水印不能关闭")
	}
	if w.Opacity < 0.08 {
		return Watermark{}, errBypass("水印透明度不能低于 0.08")
	}
	if w.Opacity > 0.6 {
		w.Opacity = 0.6
	}
	switch w.Style {
	case StyleDiagonal, StyleTile, StyleCorner:
	default:
		w.Style = StyleDiagonal
	}
	if w.FontSize < 10 {
		w.FontSize = 10
	}
	if w.FontSize > 32 {
		w.FontSize = 32
	}
	if w.Angle < -60 {
		w.Angle = -60
	}
	if w.Angle > 60 {
		w.Angle = 60
	}
	w.Template = strings.TrimSpace(w.Template)
	if w.Template == "" {
		w.Template = DefaultWatermark().Template
	}
	if len(w.Template) > 255 {
		return Watermark{}, errInvalid("水印模板过长")
	}
	w.Enabled = true
	return w, nil
}

// Render 替换 ${operator} ${time} ${session} ${asset} ${purpose}。
//
// 未知占位符保留，方便发现模板写错。结果为空时退回固定字样，避免导出件没有水印。
func (w Watermark) Render(vars map[string]string) string {
	tpl := w.Template
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultWatermark().Template
	}
	replacer := strings.NewReplacer(
		"${operator}", vars["operator"],
		"${time}", vars["time"],
		"${session}", vars["session"],
		"${asset}", vars["asset"],
		"${purpose}", vars["purpose"],
	)
	text := strings.TrimSpace(replacer.Replace(tpl))
	if text == "" {
		text = "堡垒机审计"
	}
	return text
}

// FormatTime 是水印里的时间格式。
func FormatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("2006-01-02 15:04:05")
}

// Vars 组装水印变量。空字段留空，不写入“未知”之类的占位，避免和水印正文混在一起。
func Vars(operator, session, asset, purpose string, at time.Time) map[string]string {
	return map[string]string{
		"operator": operator,
		"session":  session,
		"asset":    asset,
		"purpose":  purpose,
		"time":     FormatTime(at),
	}
}

type bypassError string

func (e bypassError) Error() string { return string(e) }

func errBypass(msg string) error { return bypassError(msg) }

// IsBypass 判断这次保存或导出是不是在试图去掉水印。
func IsBypass(err error) bool {
	_, ok := err.(bypassError)
	return ok
}

// OpacityText 给导出说明用，避免浮点格式飘。
func OpacityText(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// Policy 是回放和展示接口使用的脱敏规则加水印。
type Policy struct {
	Rules     []Rule    `json:"rules"`
	Watermark Watermark `json:"watermark"`
}
