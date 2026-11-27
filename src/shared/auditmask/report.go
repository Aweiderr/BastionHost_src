package auditmask

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"unicode/utf16"
)

// BuildPDF 生成带页眉页脚的 PDF。
//
// 正文使用 Type0 / STSong-Light / UniGB-UCS2-H，中文按 UTF-16BE 十六进制写入，
// 不嵌入字体文件。页眉和页脚都是同一条水印。
func BuildPDF(header, footer string, lines []string) []byte {
	const perPage = 36
	pages := splitLines(lines, perPage)
	if len(pages) == 0 {
		pages = [][]string{{}}
	}

	// 对象编号：1 目录，2 页面树，之后每页两个对象（页面、内容），最后一个是字体。
	fontID := 3 + len(pages)*2
	var body bytes.Buffer
	offsets := make([]int, fontID+1)
	writeObj := func(id int, payload string) {
		offsets[id] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", id, payload)
	}

	kids := make([]string, 0, len(pages))
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i*2))
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	for i, pageLines := range pages {
		pageID := 3 + i*2
		contentID := pageID + 1
		stream := pdfPageStream(header, footer, pageLines)
		writeObj(pageID, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>", contentID, fontID))
		writeObj(contentID, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}
	writeObj(fontID, "<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H >>")

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	start := out.Len()
	out.Write(body.Bytes())
	xrefAt := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", fontID+1)
	out.WriteString("0000000000 65535 f \n")
	for id := 1; id <= fontID; id++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", start+offsets[id])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", fontID+1, xrefAt)
	return out.Bytes()
}

func splitLines(lines []string, n int) [][]string {
	if n <= 0 {
		n = 36
	}
	var pages [][]string
	for i := 0; i < len(lines); i += n {
		j := i + n
		if j > len(lines) {
			j = len(lines)
		}
		pages = append(pages, lines[i:j])
	}
	return pages
}

func pdfPageStream(header, footer string, lines []string) string {
	var b strings.Builder
	b.WriteString("BT\n/F1 11 Tf\n")
	b.WriteString("1 0 0 1 48 810 Tm\n")
	b.WriteString(pdfHex(header) + " Tj\n")
	b.WriteString("1 0 0 1 48 36 Tm\n")
	b.WriteString(pdfHex(footer) + " Tj\n")
	y := 780
	for _, line := range lines {
		fmt.Fprintf(&b, "1 0 0 1 48 %d Tm\n", y)
		b.WriteString(pdfHex(line) + " Tj\n")
		y -= 18
	}
	b.WriteString("ET\n")
	return b.String()
}

func pdfHex(s string) string {
	encoded := utf16.Encode([]rune(s))
	var b strings.Builder
	b.WriteByte('<')
	for _, u := range encoded {
		fmt.Fprintf(&b, "%04X", u)
	}
	b.WriteByte('>')
	return b.String()
}

// BuildDocx 生成带页眉页脚的 Office 文档。页眉和页脚都写水印。
func BuildDocx(header, footer string, lines []string) ([]byte, error) {
	var paras strings.Builder
	for _, line := range lines {
		fmt.Fprintf(&paras, `<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, xmlEscape(line))
	}
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<w:body>` + paras.String() +
		`<w:sectPr>` +
		`<w:headerReference w:type="default" r:id="rIdHeader"/>` +
		`<w:footerReference w:type="default" r:id="rIdFooter"/>` +
		`</w:sectPr></w:body></w:document>`
	headerXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:p><w:r><w:t>` + xmlEscape(header) + `</w:t></w:r></w:p></w:hdr>`
	footerXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:p><w:r><w:t>` + xmlEscape(footer) + `</w:t></w:r></w:p></w:ftr>`
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>` +
		`<Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>` +
		`</Types>`
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`</Relationships>`
	docRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rIdHeader" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>` +
		`<Relationship Id="rIdFooter" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>` +
		`</Relationships>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rels},
		{"word/document.xml", document},
		{"word/_rels/document.xml.rels", docRels},
		{"word/header1.xml", headerXML},
		{"word/footer1.xml", footerXML},
	}
	for _, p := range parts {
		w, err := zw.Create(p.name)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := w.Write([]byte(p.body)); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(s)
}

// ReportText 是纯文本报表，首尾各一行水印，方便没有 Office 的环境直接看。
func ReportText(header, footer string, lines []string) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(footer)
	b.WriteByte('\n')
	return b.String()
}
