package auditmask

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestWatermarkRenderAndBypass(t *testing.T) {
	wm := DefaultWatermark()
	wm.Template = "${operator} ${session} ${asset} ${purpose} ${time}"
	text := wm.Render(Vars("张三", "S1", "db-1", "巡检", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)))
	for _, part := range []string{"张三", "S1", "db-1", "巡检"} {
		if !strings.Contains(text, part) {
			t.Fatalf("水印缺少 %s: %s", part, text)
		}
	}
	if _, err := (Watermark{Enabled: false, Opacity: 0.2, Template: "x"}).Normalize(); !IsBypass(err) {
		t.Fatal("关闭水印必须视为绕过")
	}
	if _, err := (Watermark{Enabled: true, Opacity: 0.01, Template: "x"}).Normalize(); !IsBypass(err) {
		t.Fatal("过低透明度必须视为绕过")
	}
	got, err := (Watermark{Enabled: true, Style: "nope", Opacity: 0.2, FontSize: 8, Angle: 90, Template: "${operator}"}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Style != StyleDiagonal || got.FontSize != 10 || got.Angle != 60 {
		t.Fatalf("样式没有被校正: %+v", got)
	}
}

func TestPackageWatermarkAndTextRecording(t *testing.T) {
	raw, err := BuildZip(Evidence{
		Session:   map[string]any{"session_no": "S1"},
		Commands:  []map[string]string{{"command_text": "password=******"}},
		Files:     []any{},
		Logs:      []any{},
		Recording: []byte("password=******\r\nls\r\n"),
		Integrity: Integrity{SessionNo: "S1", RecordDigest: "abc", RecordChunks: 2},
		Watermark: "张三 S1 db-1 2026-01-02 03:04:05",
		Note:      "事故排查",
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReadZip(raw)
	if err != nil {
		t.Fatal(err)
	}
	wm := string(files["WATERMARK.txt"])
	if !strings.Contains(wm, "张三 S1 db-1") {
		t.Fatalf("压缩包水印不正确: %s", wm)
	}
	rec := string(files["recording.txt"])
	if !strings.Contains(rec, "张三 S1 db-1") || !strings.Contains(rec, "ls") {
		t.Fatalf("字符录像首尾水印缺失: %s", rec)
	}
	if _, ok := files["COVER.txt"]; ok {
		t.Fatal("字符录像不应再附二进制封面")
	}
	integrity := string(files["integrity.json"])
	if !strings.Contains(integrity, `"record_digest": "abc"`) || !strings.Contains(integrity, `"package_digest"`) {
		t.Fatalf("完整性证明不完整: %s", integrity)
	}
	pdf := files["COVER.pdf"]
	if !bytesHasPDFHeader(pdf) || !pdfContains(pdf, "张三") {
		t.Fatal("封面 PDF 缺少水印页眉")
	}
	if !strings.Contains(string(files["EXPORT-NOTE.txt"]), "事故排查") {
		t.Fatal("导出说明缺失")
	}
}

func TestBinaryCoverDoesNotChangeBytes(t *testing.T) {
	body := []byte{0xff, 0xfe, 0x00, 0x80, 'B', 'S', 'G'}
	raw, err := BuildZip(Evidence{
		Session:   map[string]string{"session_no": "S2"},
		Commands:  []any{},
		Files:     []any{},
		Logs:      []any{},
		Recording: body,
		Integrity: Integrity{SessionNo: "S2", RecordDigest: "dig"},
		Watermark: "审计水印",
		Note:      "导出",
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReadZip(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["recording.bin"]) != string(body) {
		t.Fatal("二进制录像字节被改写")
	}
	cover := string(files["COVER.txt"])
	if !strings.Contains(cover, "审计水印") || !strings.Contains(cover, "不改写画面字节") {
		t.Fatalf("二进制封面水印不正确: %s", cover)
	}
	if _, ok := files["recording.txt"]; ok {
		t.Fatal("二进制录像不应写成文本录像")
	}
}

func TestGraphicalPackageKeepsFrames(t *testing.T) {
	frame := []byte("BSG1 1\n{\"type\":\"frame\",\"jpeg\":\"abc13900001111\"}\n")
	raw, err := BuildZip(Evidence{
		Session:   map[string]string{"session_no": "S3"},
		Commands:  []any{},
		Files:     []any{},
		Logs:      []any{},
		Recording: frame,
		Integrity: Integrity{SessionNo: "S3"},
		Watermark: "图形水印",
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReadZip(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["recording.bsg1"]) != string(frame) {
		t.Fatal("图形录像字节被改写")
	}
	if !strings.Contains(string(files["COVER.txt"]), "图形水印") {
		t.Fatal("图形录像缺少封面水印")
	}
}

func TestOfficeHeaderFooter(t *testing.T) {
	doc, err := BuildDocx("页眉水印", "页脚水印", []string{"登录成功 3", "高危命令 1"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReadZip(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["word/header1.xml"]), "页眉水印") {
		t.Fatal("文档页眉没有水印")
	}
	if !strings.Contains(string(files["word/footer1.xml"]), "页脚水印") {
		t.Fatal("文档页脚没有水印")
	}
	if !strings.Contains(string(files["word/document.xml"]), "高危命令 1") {
		t.Fatal("文档正文缺失")
	}
	text := ReportText("页眉水印", "页脚水印", []string{"正文"})
	if !strings.HasPrefix(text, "页眉水印\n") || !strings.HasSuffix(text, "页脚水印\n") {
		t.Fatalf("文本报表首尾水印不对: %q", text)
	}
}

func TestZipRejectsEmptyWatermark(t *testing.T) {
	if _, err := BuildZip(Evidence{Watermark: "  "}); err == nil {
		t.Fatal("没有水印的取证包必须拒绝")
	}
}

func bytesHasPDFHeader(b []byte) bool {
	return len(b) > 8 && string(b[:8]) == "%PDF-1.4"
}

func pdfContains(pdf []byte, text string) bool {
	encoded := utf16.Encode([]rune(text))
	var b strings.Builder
	for _, u := range encoded {
		fmt.Fprintf(&b, "%04X", u)
	}
	return strings.Contains(string(pdf), b.String())
}
