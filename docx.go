package main

// docx.go — M5 (DOCX Writer): LDM → file .docx (ZIP + OOXML tối giản) bằng
// thư viện chuẩn Go (archive/zip), KHÔNG thêm phụ thuộc ngoài.
// Bản đồ ánh xạ theo tài liệu v3.0 mục 4.5, Bảng 3:
//   Block para  → w:p + w:pPr (jc/ind/spacing)
//   heading n   → w:p + pStyle HeadingN (styles.xml khai báo Heading1..3)
//   table       → w:tbl (tblGrid từ ColEdges, tblBorders, gridSpan/vMerge)
//   run         → w:r + w:rPr (rFonts, sz, b, i)
//   ranh giới trang → w:br type="page" (tùy chọn, mặc định giữ)

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	docxPageWTw = 11906 // A4 dọc (twip)
	docxPageHTw = 16838
	docxMarginT = 1440
	docxMarginB = 1440
	docxMarginL = 1701
	docxMarginR = 1417
)

var reXMLChar = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F]`)

// xmlEsc — escape chuỗi cho XML, gạt ký tự điều khiển không hợp lệ.
func xmlEsc(s string) string {
	s = reXMLChar.ReplaceAllString(s, " ")
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// ptToTwip — điểm → twip (1/20 điểm).
func ptToTwip(pt float64) int { return int(pt * 20) }

// defaultFontName — font an toàn cho trang đường OCR (không có font thật).
const defaultFontName = "Times New Roman"

// safeFontName — tên font từ PDF có thể dài/đủ loại; chuẩn hóa về font hệ
// phổ biến nếu lạ.
func safeFontName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultFontName
	}
	known := []string{"Times New Roman", "Arial", "Calibri", "Cambria",
		"Times-Roman", "Helvetica", "Courier New", "Verdana", "Tahoma",
		"Segoe UI", "Tahoma", "Georgia"}
	for _, k := range known {
		if strings.EqualFold(name, k) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(k)) {
			return canonicalFont(k)
		}
	}
	if strings.Contains(strings.ToLower(name), "times") {
		return "Times New Roman"
	}
	if strings.Contains(strings.ToLower(name), "arial") || strings.Contains(strings.ToLower(name), "helvetica") {
		return "Arial"
	}
	if strings.Contains(strings.ToLower(name), "courier") {
		return "Courier New"
	}
	return defaultFontName // lạ → font an toàn
}

func canonicalFont(k string) string {
	switch k {
	case "Times-Roman":
		return "Times New Roman"
	case "Helvetica":
		return "Arial"
	}
	return k
}

// WriteDocx — sinh file .docx từ LDM.
// opts: PageBreaks giữ ranh giới trang; bodySize cỡ chuẩn fallback.
func WriteDocx(d *Document, path string, pageBreaks bool) error {
	var body bytes.Buffer
	for pi, p := range d.Pages {
		if pi > 0 && pageBreaks {
			body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
		}
		for _, b := range p.Blocks {
			body.Write(blockToXML(&b, p.Path == "text"))
		}
	}

	sect := fmt.Sprintf(
		`<w:sectPr><w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`,
		docxPageWTw, docxPageHTw, docxMarginT, docxMarginR, docxMarginB, docxMarginL)

	document := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>%s%s</w:body></w:document>`,
		body.String(), sect)

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
</Types>`

	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	docRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`

	styles := docxStyles()

	return writeZip(path, map[string][]byte{
		"[Content_Types].xml":        []byte(contentTypes),
		"_rels/.rels":                []byte(rels),
		"word/document.xml":          []byte(document),
		"word/_rels/document.xml.rels": []byte(docRels),
		"word/styles.xml":            []byte(styles),
	})
}

// blockToXML — một khối → XML. textPath=true nghĩa là font/cỡ chính xác từ PDF.
func blockToXML(b *Block, textPath bool) []byte {
	switch b.Kind {
	case BlockTable:
		if b.Table != nil {
			return tableToXML(b.Table)
		}
	case BlockPicture:
		return []byte(``) // đường Text chưa có ảnh; vùng ảnh ở GUI ghi chú riêng
	}
	// para / heading / footer / header
	var out bytes.Buffer
	pPr := paraPr(b)
	if b.Kind == BlockHeading && b.Heading >= 1 {
		out.WriteString(`<w:p>` + pPr + `<w:pPr>` + pStyleXML(b.Heading) + paraCorePr(b) + `</w:pPr>`)
	} else {
		out.WriteString(`<w:p>` + pPr)
	}
	for li, ln := range b.Lines {
		for _, r := range ln.Runs {
			out.Write(runToXML(&r, b))
		}
		// xuống dòng giữa các Line trong khối (giữ nguyên 1 paragraph)
		if li < len(b.Lines)-1 {
			out.WriteString(`<w:r><w:br/></w:r>`)
		}
	}
	_ = textPath
	out.WriteString(`</w:p>`)
	return out.Bytes()
}

// paraPr — tiền tố pPr cho paragraph thường (căn giữa, thụt lề).
func paraPr(b *Block) string {
	if b.Center {
		return `<w:pPr><w:jc w:val="center"/>` + paraCorePr(b) + `</w:pPr>`
	}
	if b.FirstInd > 0 {
		return fmt.Sprintf(`<w:pPr><w:ind w:firstLine="%d"/>` + paraCorePr(b) + `</w:pPr>`,
			ptToTwip(b.FirstInd/1.0))
	}
	return `<w:pPr>` + paraCorePr(b) + `</w:pPr>`
}

// paraCorePr — khoảng cách đoạn (after=120tw).
func paraCorePr(b *Block) string {
	_ = b
	return `<w:spacing w:after="120"/>`
}

func pStyleXML(level int) string {
	return `<w:pStyle w:val="Heading` + itoa(level) + `"/>`
}

// runToXML — một run → w:r.
func runToXML(r *Run, b *Block) []byte {
	size := r.SizePt
	if size <= 0 {
		if b != nil && b.Kind == BlockHeading && b.Heading >= 1 {
			size = []float64{0, 16, 14, 13}[b.Heading]
		} else {
			size = 11
		}
	}
	font := safeFontName(r.Font)
	rpr := fmt.Sprintf(`<w:rPr><w:rFonts w:ascii="%s" w:hAnsi="%s" w:cs="%s"/>`,
		font, font, font)
	if r.Bold {
		rpr += `<w:b/>`
	}
	if r.Italic {
		rpr += `<w:i/>`
	}
	rpr += fmt.Sprintf(`<w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr>`,
		ptToTwip(size), ptToTwip(size))
	txt := strings.ReplaceAll(xmlEsc(r.Text), "\n", `</w:t></w:r><w:r>`+rpr+`<w:br/><w:t xml:space="preserve">`)
	if txt == "" {
		return nil
	}
	return []byte(`<w:r>` + rpr + `<w:t xml:space="preserve">` + txt + `</w:t></w:r>`)
}

// tableToXML — TableBlock → w:tbl. Chiều rộng cột từ ColEdges (pt) hoặc đều.
func tableToXML(tb *TableBlock) []byte {
	if tb == nil || len(tb.Grid) == 0 {
		return nil
	}
	ncol := 0
	for _, row := range tb.Grid {
		if len(row) > ncol {
			ncol = len(row)
		}
	}
	if ncol == 0 {
		return nil
	}
	var out bytes.Buffer
	out.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblBorders>` +
		borderXML("top") + borderXML("left") + borderXML("bottom") + borderXML("right") +
		borderXML("insideH") + borderXML("insideV") +
		`</w:tblBorders></w:tblPr><w:tblGrid>`)
	// tblGrid từ ColEdges (khoảng cách giữa biên)
	widths := colWidths(tb, ncol)
	for _, w := range widths {
		out.WriteString(fmt.Sprintf(`<w:gridCol w:w="%d"/>`, w))
	}
	out.WriteString(`</w:tblGrid>`)
	vMergeOpen := false // đang mở vMerge (hàng trước restart)
	for ri, row := range tb.Grid {
		out.WriteString(`<w:tr>`)
		for ci := 0; ci < ncol; ci++ {
			var cell Cell
			if ci < len(row) {
				cell = row[ci]
			} else {
				cell = Cell{Text: ""}
			}
			out.WriteString(`<w:tc>`)
			if cell.ColSpan > 1 {
				out.WriteString(fmt.Sprintf(`<w:tcPr><w:gridSpan w:val="%d"/></w:tcPr>`, cell.ColSpan))
			}
			if cell.RowSpan > 1 {
				out.WriteString(`<w:tcPr><w:vMerge w:val="restart"/></w:tcPr>`)
				vMergeOpen = true
			} else if cell.ContVDown && vMergeOpen {
				out.WriteString(`<w:tcPr><w:vMerge/></w:tcPr>`)
			} else {
				vMergeOpen = false
			}
			out.WriteString(`<w:tcW w:w="0" w:type="auto"/>`)
			out.WriteString(`<w:p><w:r><w:t xml:space="preserve">` + xmlEsc(cell.Text) + `</w:t></w:r></w:p></w:tc>`)
		}
		out.WriteString(`</w:tr>`)
		_ = ri
	}
	out.WriteString(`</w:tbl>`)
	// bảng phải theo sau bằng paragraph trống (quy tắc OOXML sau w:tbl)
	out.WriteString(`<w:p/>`)
	return out.Bytes()
}

func borderXML(edge string) string {
	return fmt.Sprintf(`<w:%s w:val="single" w:sz="4" w:space="0" w:color="000000"/>`, edge)
}

// colWidths — độ rộng từng cột (twip) từ ColEdges; thiếu → chia đều.
func colWidths(tb *TableBlock, ncol int) []int {
	total := docxPageWTw - docxMarginL - docxMarginR
	widths := make([]int, ncol)
	if len(tb.ColEdges) >= ncol+1 {
		for i := 0; i < ncol; i++ {
			w := tb.ColEdges[i+1] - tb.ColEdges[i]
			widths[i] = ptToTwip(w)
			if widths[i] < 200 {
				widths[i] = 200
			}
		}
		return widths
	}
	even := total / ncol
	for i := range widths {
		widths[i] = even
	}
	return widths
}

// docxStyles — styles.xml tối giản: Normal + Heading 1..3 (navy theo nhận diện viocr).
func docxStyles() string {
	headColor := "0b2047" // navy thương hiệu
	mk := func(id, name string, size int, color string) string {
		return fmt.Sprintf(`<w:style w:type="paragraph" w:styleId="%s"><w:name w:val="%s"/><w:qFormat/>`+
			`<w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/></w:pPr>`+
			`<w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:cs="Times New Roman"/>`+
			`<w:b/><w:color w:val="%s"/><w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr></w:style>`,
			id, name, color, size, size)
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr>
<w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:cs="Times New Roman"/>
<w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/>
<w:pPr><w:spacing w:after="120"/></w:pPr></w:style>` +
		mk("Heading1", "heading 1", 32, headColor) +
		mk("Heading2", "heading 2", 28, headColor) +
		mk("Heading3", "heading 3", 24, headColor) +
		`</w:styles>`
}

// writeZip — ghi các file vào zip (docx là zip).
func writeZip(path string, files map[string][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	names := []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml",
		"word/_rels/document.xml.rels", "word/styles.xml"}
	for _, name := range names {
		data, ok := files[name]
		if !ok {
			continue
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return zw.Close()
}
