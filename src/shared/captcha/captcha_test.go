package captcha

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestGenerateProducesValidPNG(t *testing.T) {
	s := NewStore(0)
	id, data, err := s.Generate(4, 120, 40)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if id == "" {
		t.Fatal("标识不能为空")
	}
	if len(data) == 0 {
		t.Fatal("图片内容为空")
	}
	// 用真实解码验证，而不是只比较魔数：能解码才说明前端一定显示得出来
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("生成的图片无法解码: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 120 || b.Dy() != 40 {
		t.Fatalf("尺寸不符：%dx%d", b.Dx(), b.Dy())
	}
}

func TestVerifyCorrectCodeOnce(t *testing.T) {
	s := NewStore(time.Minute)
	id, _, err := s.Generate(4, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	// 同包测试直接读取内部状态，避免为了测试而在生产代码里开导出方法
	s.mu.Lock()
	code := s.items[id].code
	s.mu.Unlock()

	if !s.Verify(id, code) {
		t.Fatal("正确验证码应当通过")
	}
	// 一次性：同一张图不能复用
	if s.Verify(id, code) {
		t.Fatal("验证码必须一次性，第二次校验应当失败")
	}
}

func TestVerifyIsCaseInsensitive(t *testing.T) {
	s := NewStore(time.Minute)
	id, _, _ := s.Generate(6, 120, 40)
	s.mu.Lock()
	code := s.items[id].code
	s.mu.Unlock()

	if !s.Verify(id, strings.ToLower(code)) {
		t.Fatal("大小写不应影响校验结果")
	}
}

func TestVerifyWrongCodeConsumesEntry(t *testing.T) {
	s := NewStore(time.Minute)
	id, _, _ := s.Generate(4, 120, 40)

	if s.Verify(id, "0000") {
		t.Fatal("错误验证码不应通过")
	}
	// 输错也应消费掉：否则同一张图可被反复尝试，验证码就失去了意义
	s.mu.Lock()
	_, still := s.items[id]
	s.mu.Unlock()
	if still {
		t.Fatal("校验失败后该验证码应被消费，避免被反复尝试")
	}
}

func TestVerifyExpired(t *testing.T) {
	s := NewStore(time.Minute)
	id, _, _ := s.Generate(4, 120, 40)
	s.mu.Lock()
	it := s.items[id]
	it.expire = time.Now().Add(-time.Second)
	s.items[id] = it
	code := it.code
	s.mu.Unlock()

	if s.Verify(id, code) {
		t.Fatal("过期验证码不应通过")
	}
}

func TestVerifyEmptyInputs(t *testing.T) {
	s := NewStore(time.Minute)
	id, _, _ := s.Generate(4, 120, 40)
	if s.Verify("", "1234") {
		t.Fatal("空标识不应通过")
	}
	if s.Verify(id, "") {
		t.Fatal("空输入不应通过")
	}
}

// TestCharsetAvoidsConfusable 验证字符集不含易混淆字符。
//
// 这不是洁癖：把 0/O、1/l 放进验证码，用户输错概率明显上升，
// 而输错会计入失败次数，最终表现为"系统难用"而非"更安全"。
func TestCharsetAvoidsConfusable(t *testing.T) {
	for _, bad := range []string{"0", "O", "1", "I", "L", "l"} {
		if strings.Contains(charset, bad) {
			t.Errorf("字符集不应包含易混淆字符 %q", bad)
		}
	}
}

func TestCleanupRemovesExpired(t *testing.T) {
	s := NewStore(time.Minute)
	id, _, _ := s.Generate(4, 120, 40)
	s.mu.Lock()
	it := s.items[id]
	it.expire = time.Now().Add(-time.Minute)
	s.items[id] = it
	s.mu.Unlock()

	s.cleanup()

	s.mu.Lock()
	_, still := s.items[id]
	s.mu.Unlock()
	if still {
		t.Fatal("过期项应被清理，否则内存会随流量无限增长")
	}
}
