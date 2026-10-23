// Package captcha 提供图形验证码的生成与校验。
//
// 定位说明（避免用错地方）：
//   - 它**不防 DDoS** —— DDoS 是流量/连接层问题，攻击者不会打开登录页；
//   - 它防的是**自动化口令爆破**：脚本反复尝试口令时，必须先过图形识别；
//   - 因此它按"连续失败次数"触发，而不是每次登录都要填 ——
//     正常用户几乎感知不到，脚本却会被卡住。
//
// 存储选择：验证码量大、时效短（默认 2 分钟），放数据库会产生大量写放大，
// 因此用进程内存。**代价是不支持多实例共享** —— 若将来管理服务横向扩展，
// 需要换成 Redis，届时只需替换本包的 Store 实现。
package captcha

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
	mrand "math/rand"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// 字符集刻意去掉 0/O、1/l/I/L 等易混淆字符。
//
// 验证码是"给人看、让人敲"的：把难以分辨的字符放进去，
// 用户输错的概率会明显上升，而输错又会计入失败次数 ——
// 最终变成"系统难用"，而不是"系统更安全"。
// L 一并去掉：即便已排除 1 与 I，用户看到 L 仍会本能读成 1。
const charset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// DefaultTTL 验证码默认有效期。
const DefaultTTL = 2 * time.Minute

type item struct {
	code   string
	expire time.Time
}

// Store 是内存中的验证码存储。
type Store struct {
	mu    sync.Mutex
	items map[string]item
	ttl   time.Duration
	// maxItems 上限，防止被恶意刷爆内存
	maxItems int
}

// NewStore 创建存储。
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	s := &Store{items: make(map[string]item), ttl: ttl, maxItems: 20000}
	go s.loop()
	return s
}

// loop 定期清理过期项，避免内存随流量无限增长。
func (s *Store) loop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		s.cleanup()
	}
}

func (s *Store) cleanup() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.items {
		if now.After(v.expire) {
			delete(s.items, k)
		}
	}
}

// randomString 用 crypto/rand 生成字符集内的随机串。
//
// 不用 math/rand：验证码是认证凭据，可预测等于没有。
func randomString(n int) (string, error) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b.WriteByte(charset[idx.Int64()])
	}
	return b.String(), nil
}

// randomID 生成不可猜测的验证码标识。
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// Generate 生成一张验证码图片，返回标识与 PNG 字节。
//
// 绘制要点：
//   - 每个字符随机纵向偏移与颜色，增加简单 OCR 的难度；
//   - 叠加若干干扰线与噪点；
//   - 使用 basicfont 位图字体（Go 官方扩展库自带），因此**无需外部字体文件**，
//     在最小化容器里也能正常工作。
func (s *Store) Generate(length, width, height int) (string, []byte, error) {
	if length <= 0 {
		length = 4
	}
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 40
	}
	code, err := randomString(length)
	if err != nil {
		return "", nil, err
	}
	id, err := randomID()
	if err != nil {
		return "", nil, err
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// 浅色底：深色字符在浅底上更清晰，用户更容易辨认
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{245, 247, 250, 255}}, image.Point{}, draw.Src)

	face := basicfont.Face7x13
	charW := 14
	startX := (width - charW*length) / 2
	if startX < 4 {
		startX = 4
	}

	for i := 0; i < length; i++ {
		// 随机的纵向抖动与颜色
		dy := mrand.Intn(9) - 4
		col := color.RGBA{
			uint8(40 + mrand.Intn(80)),
			uint8(40 + mrand.Intn(80)),
			uint8(40 + mrand.Intn(80)),
			255,
		}
		d := &font.Drawer{
			Dst:  img,
			Src:  &image.Uniform{col},
			Face: face,
			Dot: fixed.Point26_6{
				X: fixed.I(startX + i*charW),
				Y: fixed.I(height/2 + 5 + dy),
			},
		}
		d.DrawString(string(code[i]))
	}

	// 干扰线：穿过字符区域，但保持较细以免影响可读性
	for i := 0; i < 4; i++ {
		x1, y1 := mrand.Intn(width), mrand.Intn(height)
		x2, y2 := mrand.Intn(width), mrand.Intn(height)
		drawLine(img, x1, y1, x2, y2, color.RGBA{
			uint8(150 + mrand.Intn(80)), uint8(150 + mrand.Intn(80)), uint8(150 + mrand.Intn(80)), 255,
		})
	}
	// 噪点
	for i := 0; i < width*height/60; i++ {
		x, y := mrand.Intn(width), mrand.Intn(height)
		img.Set(x, y, color.RGBA{
			uint8(120 + mrand.Intn(120)), uint8(120 + mrand.Intn(120)), uint8(120 + mrand.Intn(120)), 255,
		})
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", nil, fmt.Errorf("编码验证码图片失败: %w", err)
	}

	s.mu.Lock()
	// 超过上限时先清理一次，仍超限则拒绝（宁可让用户重试，也不让内存失控）
	if len(s.items) >= s.maxItems {
		s.mu.Unlock()
		s.cleanup()
		s.mu.Lock()
		if len(s.items) >= s.maxItems {
			s.mu.Unlock()
			return "", nil, fmt.Errorf("验证码服务繁忙，请稍后再试")
		}
	}
	s.items[id] = item{code: code, expire: time.Now().Add(s.ttl)}
	s.mu.Unlock()

	return id, buf.Bytes(), nil
}

// Verify 校验并**立即删除**，确保一次性。
//
// 大小写不敏感：字符集里已排除易混淆字符，再苛求大小写只会增加
// 用户输错的概率，而安全增益有限。
func (s *Store) Verify(id, input string) bool {
	if id == "" || input == "" {
		return false
	}
	s.mu.Lock()
	it, ok := s.items[id]
	if ok {
		delete(s.items, id) // 无论对错都消费掉，防止同一张图被反复尝试
	}
	s.mu.Unlock()

	if !ok || time.Now().After(it.expire) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(input), it.code)
}

// drawLine 画一条直线（Bresenham 的简化版，够用即可）。
func drawLine(img *image.RGBA, x1, y1, x2, y2 int, c color.Color) {
	dx, dy := x2-x1, y2-y1
	steps := dx
	if dy > dx {
		steps = dy
	}
	if steps < 0 {
		steps = -steps
	}
	if steps == 0 {
		img.Set(x1, y1, c)
		return
	}
	xInc := float64(dx) / float64(steps)
	yInc := float64(dy) / float64(steps)
	x, y := float64(x1), float64(y1)
	for i := 0; i <= steps; i++ {
		img.Set(int(x), int(y), c)
		x += xInc
		y += yInc
	}
}
