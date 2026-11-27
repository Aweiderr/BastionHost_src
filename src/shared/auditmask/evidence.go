package auditmask

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Integrity 是封存录像的完整性证明，来自会话归档，不是本包明文的摘要。
type Integrity struct {
	SessionNo         string `json:"session_no"`
	RecordPath        string `json:"record_path,omitempty"`
	RecordSize        int64  `json:"record_size"`
	RecordChunks      int    `json:"record_chunks"`
	RecordDigest      string `json:"record_digest"`
	PackageDigest     string `json:"package_digest,omitempty"`
	RecordingIncluded bool   `json:"recording_included"`
	RecordingRedacted bool   `json:"recording_redacted"`
	RecordingNote     string `json:"recording_note,omitempty"`
	Note              string `json:"note"`
}

// Evidence 是一份取证包的材料。调用方负责传入已经脱敏的命令和录像副本。
type Evidence struct {
	Session     any
	Commands    any
	Files       any
	Logs        any
	Recording   []byte
	MissingNote string
	Integrity   Integrity
	Watermark   string
	Note        string
	Now         time.Time
}

// BuildZip 组装取证包。
//
// 压缩包里始终有 WATERMARK.txt。字符录像在正文首尾写水印；
// 图形或二进制录像不改字节，另附 COVER.txt。COVER.pdf 用页眉页脚再写一遍水印。
func BuildZip(in Evidence) ([]byte, error) {
	wm := strings.TrimSpace(in.Watermark)
	if wm == "" {
		return nil, errors.New("取证包必须带水印")
	}
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "取证导出"
	}

	files := map[string][]byte{}
	files["WATERMARK.txt"] = []byte(watermarkReadme(wm, in.Now))
	files["EXPORT-NOTE.txt"] = []byte(exportNote(note, wm, in.Now))

	sessionJSON, err := json.MarshalIndent(in.Session, "", "  ")
	if err != nil {
		return nil, err
	}
	commandsJSON, err := json.MarshalIndent(in.Commands, "", "  ")
	if err != nil {
		return nil, err
	}
	filesJSON, err := json.MarshalIndent(in.Files, "", "  ")
	if err != nil {
		return nil, err
	}
	logsJSON, err := json.MarshalIndent(in.Logs, "", "  ")
	if err != nil {
		return nil, err
	}
	files["session.json"] = sessionJSON
	files["commands.json"] = commandsJSON
	files["files.json"] = filesJSON
	files["logs.json"] = logsJSON

	in.Integrity.RecordingIncluded = len(in.Recording) > 0
	in.Integrity.Note = "record_digest 是封存时的摘要链，不是本包内明文的摘要。包内明文可能已按展示层规则脱敏。"
	if len(in.Recording) == 0 {
		in.Integrity.RecordingNote = strings.TrimSpace(in.MissingNote)
		if in.Integrity.RecordingNote == "" {
			in.Integrity.RecordingNote = "本次未能取回录像明文，完整性证明仍以会话归档中的摘要为准。"
		}
		files["recording-missing.txt"] = []byte(in.Integrity.RecordingNote + "\n" + wm + "\n")
	} else if TextRecording(in.Recording) {
		files["recording.txt"] = []byte(frameText(wm, string(in.Recording)))
		in.Integrity.RecordingRedacted = true
	} else {
		name := "recording.bin"
		if bytes.HasPrefix(in.Recording, []byte("BSG1 ")) {
			name = "recording.bsg1"
		}
		files[name] = append([]byte(nil), in.Recording...)
		files["COVER.txt"] = []byte(binaryCover(wm))
		in.Integrity.RecordingRedacted = true
	}

	sum := sha256.Sum256(bytes.Join([][]byte{
		files["session.json"], files["commands.json"], files["recording.txt"],
		files["recording.bin"], files["recording.bsg1"],
	}, []byte{0}))
	in.Integrity.PackageDigest = hex.EncodeToString(sum[:])
	integrityJSON, err := json.MarshalIndent(in.Integrity, "", "  ")
	if err != nil {
		return nil, err
	}
	files["integrity.json"] = integrityJSON

	body := []string{
		"会话 " + in.Integrity.SessionNo,
		note,
		"录像摘要 " + in.Integrity.RecordDigest,
		"封存分片 " + itoa(in.Integrity.RecordChunks),
	}
	files["COVER.pdf"] = BuildPDF(wm, wm, body)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// 固定顺序，方便测试和比对。
	order := []string{
		"WATERMARK.txt", "EXPORT-NOTE.txt", "COVER.txt", "COVER.pdf",
		"session.json", "commands.json", "files.json", "logs.json",
		"integrity.json", "recording.txt", "recording.bsg1", "recording.bin",
		"recording-missing.txt",
	}
	for _, name := range order {
		payload, ok := files[name]
		if !ok {
			continue
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetModTime(in.Now)
		w, err := zw.CreateHeader(h)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := w.Write(payload); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func watermarkReadme(wm string, at time.Time) string {
	return "本取证包含水印，删除或遮挡水印的尝试会被审计。\n" +
		"水印：" + wm + "\n" +
		"生成时间：" + FormatTime(at) + "\n"
}

func exportNote(reason, wm string, at time.Time) string {
	return "导出说明\n" +
		"事由：" + reason + "\n" +
		"时间：" + FormatTime(at) + "\n" +
		"水印：" + wm + "\n" +
		"包内包含会话、命令、相关日志、录像副本和封存摘要。\n" +
		"命令与字符录像副本已按展示层规则脱敏；查看未脱敏原文需要单独权限并记审计。\n"
}

func frameText(wm, body string) string {
	line := strings.Repeat("-", 24) + " " + wm + " " + strings.Repeat("-", 24)
	return line + "\n" + body + "\n" + line + "\n"
}

func binaryCover(wm string) string {
	return "封面 / 清单水印\n" +
		"水印：" + wm + "\n" +
		"本文件是图形录像或二进制录像。水印写在本说明和 COVER.pdf 的页眉页脚，不改写画面字节，以免破坏封存摘要所能对照的内容。\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ReadZip 测试和核查时按文件名取出条目。
func ReadZip(raw []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		_, err = buf.ReadFrom(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		out[f.Name] = buf.Bytes()
	}
	return out, nil
}
