package main

// pipeline.go — glue v3.0: nối M1 → M3 → M4 → LDM cho đường Text, và
// đường OCR → LDM cho ảnh/ trang scan. Thứ tự theo tài liệu thiết kế Pha 1–4.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ngưỡng phân loại trang (mục 3.1 — tinh chỉnh trên gold set, không đóng cứng)
const (
	textValidRatio = 0.85 // tỉ lệ âm tiết hợp lệ tối thiểu để coi là "trang chữ"
	textMinRuns    = 3    // tối thiểu số run để tin là trang chữ
)

// buildLDFFromPDFBytes — pipeline đường Text cho toàn file PDF:
// Pha 1: trích lớp chữ + chấm điểm trang; Pha 3: layout; Pha 4: corrector.
func buildLDMFromPDFBytes(srcName string, data []byte, doCorrect bool) (*Document, error) {
	start := time.Now()
	rawPages, err := ExtractPDFPages(data)
	if err != nil {
		return nil, err
	}
	doc := &Document{Source: srcName, Corrections: []Correction{}}
	// nhận diện chân/đầu trang LẶP LẠI trên cả tài liệu (trước khi dựng khối)
	repHead, repFoot := repeatedHeadFoot(rawPages)
	for i, rp := range rawPages {
		p := Page{Index: i + 1, Path: "text", W: rp.W, H: rp.H}
		// chấm điểm trang: tỉ lệ âm tiết hợp lệ + số run
		fullText := concatRunText(rp.Runs)
		ratio := ValidSyllableRatio(fullText)
		if ratio < textValidRatio || len(rp.Runs) < textMinRuns {
			// "PDF chữ giả" (ToUnicode hỏng) hoặc trang scan → rớt sang OCR
			p.Path = "ocr"
			p.NeedsOCR = true
			doc.Pages = append(doc.Pages, p)
			continue
		}
		runs := append([]Run(nil), rp.Runs...)
		if doCorrect {
			CorrectRunSlice(runs, i+1, -1, &doc.Corrections)
		}
		p.Blocks = buildPageBlocksWithHF(runs, rp.W, rp.H, repHead, repFoot)
		doc.Pages = append(doc.Pages, p)
	}
	doc.MsText = float64(time.Since(start).Milliseconds())
	return doc, nil
}

// ═══════════════ đường OCR: thứ tự đọc + footer lặp ═══════════════

// ocrBoxRect — box của LineResult (4 điểm góc) → Rect.
func ocrBoxRect(lr LineResult) (x0, y0, x1, y1 float64) {
	x0 = float64(lr.Box[0][0])
	y0 = float64(lr.Box[0][1])
	x1 = float64(lr.Box[2][0])
	y1 = float64(lr.Box[2][1])
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	return x0, y0, x1, y1
}

// sortOCRReadingOrder — thứ tự đọc cho các dòng OCR: băng y (dòng chồng nhau
// thành "hàng") rồi x trong băng — khôi phục cột trái→phải của trang scan.
func sortOCRReadingOrder(rs []LineResult) []LineResult {
	out := append([]LineResult(nil), rs...)
	sort.SliceStable(out, func(a, b int) bool {
		_, ya0, _, _ := ocrBoxRect(out[a])
		_, yb0, _, _ := ocrBoxRect(out[b])
		return ya0 < yb0
	})
	// gom băng
	type band struct {
		y0, y1 float64
		items  []int
	}
	var bands []band
	for i := range out {
		_, y0, _, y1 := ocrBoxRect(out[i])
		placed := false
		for k := len(bands) - 1; k >= 0 && !placed; k-- {
			bd := &bands[k]
			ov := minF(y1, bd.y1) - maxF(y0, bd.y0)
			hmin := minF(y1-y0, bd.y1-bd.y0)
			if hmin <= 0 {
				hmin = 1
			}
			if ov > 0.3*hmin {
				bd.items = append(bd.items, i)
				if y0 < bd.y0 {
					bd.y0 = y0
				}
				if y1 > bd.y1 {
					bd.y1 = y1
				}
				placed = true
			}
			if y0 > bd.y1 {
				break
			}
		}
		if !placed {
			bands = append(bands, band{y0: y0, y1: y1, items: []int{i}})
		}
	}
	sort.SliceStable(bands, func(a, b int) bool { return bands[a].y0 < bands[b].y0 })
	final := make([]LineResult, 0, len(out))
	for _, bd := range bands {
		items := append([]int(nil), bd.items...)
		sort.SliceStable(items, func(a, b int) bool {
			xa0, _, _, _ := ocrBoxRect(out[items[a]])
			xb0, _, _, _ := ocrBoxRect(out[items[b]])
			return xa0 < xb0
		})
		for _, i := range items {
			final = append(final, out[i])
		}
	}
	return final
}

// ocrHFKey — khóa dòng cho nhận diện đầu/cuối trang OCR.
func ocrHFKey(lr LineResult) string {
	return strings.Join(strings.Fields(strings.ToLower(lr.Text)), " ")
}

// repeatedOCRHeadFoot — dòng OCR nằm trong dải biên 10% lặp lại trên ≥2 trang
// → (header lặp, footer lặp). pages theo thứ tự index.
func repeatedOCRHeadFoot(pages []*imageResult) (map[string]bool, map[string]bool) {
	headCount := map[string]int{}
	footCount := map[string]int{}
	for _, r := range pages {
		if r == nil {
			continue
		}
		seenH := map[string]bool{}
		seenF := map[string]bool{}
		for _, lr := range r.Results {
			_, y0, _, y1 := ocrBoxRect(lr)
			key := ocrHFKey(lr)
			if key == "" || len(key) < 6 {
				continue
			}
			h := float64(r.Height)
			if y1 < h*0.10 && !seenH[key] {
				headCount[key]++
				seenH[key] = true
			} else if y0 > h*0.90 && !seenF[key] {
				footCount[key]++
				seenF[key] = true
			}
		}
	}
	head := map[string]bool{}
	foot := map[string]bool{}
	for k, c := range headCount {
		if c >= 2 {
			head[k] = true
		}
	}
	for k, c := range footCount {
		if c >= 2 {
			foot[k] = true
		}
	}
	return head, foot
}

// buildLDFFromImageResults — đường OCR (v2 pipeline) → LDM: các dòng OCR được
// sắp theo thứ tự đọc (băng y → x), dòng chân/đầu trang lặp tách thành khối
// riêng. Cỡ chữ suy luận 11pt, tin cậy trung bình.
func buildLDMFromImageResults(srcName string, pages []*imageResult, doCorrect bool) *Document {
	doc := &Document{Source: srcName, Corrections: []Correction{}}
	repHead, repFoot := repeatedOCRHeadFoot(pages)
	for i, r := range pages {
		p := Page{Index: i + 1, Path: "ocr", W: float64(r.Width), H: float64(r.Height)}
		ordered := sortOCRReadingOrder(r.Results)
		b := Block{Kind: BlockPara, Conf: 1.0, X0: 0, Y0: 0, X1: float64(r.Width), Y1: float64(r.Height)}
		var hfBlocks []Block
		for _, lr := range ordered {
			txt := lr.Text
			conf := lr.Conf
			if doCorrect {
				var cor []Correction
				txt = CorrectText(txt, conf, i+1, 0, &cor)
				doc.Corrections = append(doc.Corrections, cor...)
			}
			x0, y0, x1, y1 := ocrBoxRect(lr)
			run := Run{Text: txt, Font: "", SizePt: 11, Conf: conf, X0: x0, Y0: y0, X1: x1, Y1: y1}
			ln := Line{Runs: []Run{run}}
			key := ocrHFKey(lr)
			h := float64(r.Height)
			switch {
			case y1 < h*0.10 && repHead[key]:
				hb := Block{Kind: BlockHeader, Lines: []Line{ln}, Conf: conf}
				hb.X0, hb.Y0, hb.X1, hb.Y1 = x0, y0, x1, y1
				hfBlocks = append(hfBlocks, hb)
			case y0 > h*0.90 && repFoot[key]:
				fb := Block{Kind: BlockFooter, Lines: []Line{ln}, Conf: conf}
				fb.X0, fb.Y0, fb.X1, fb.Y1 = x0, y0, x1, y1
				hfBlocks = append(hfBlocks, fb)
			default:
				b.Lines = append(b.Lines, ln)
			}
		}
		if len(b.Lines) > 0 {
			p.Blocks = []Block{b}
		}
		p.Blocks = append(p.Blocks, hfBlocks...)
		doc.Pages = append(doc.Pages, p)
	}
	return doc
}

// mergeLDMPages — ghép kết quả OCR của các trang NeedsOCR vào LDM đường Text
// (GUI: pdf-text trước, OCR sau theo trang).
func mergeLDMPages(doc *Document, ocrPages map[int]*imageResult, doCorrect bool) {
	for _, p := range doc.Pages {
		if !p.NeedsOCR {
			continue
		}
		r, ok := ocrPages[p.Index]
		if !ok {
			continue
		}
		b := Block{Kind: BlockPara, Conf: 1.0, X0: 0, Y0: 0, X1: p.W, Y1: p.H}
		for _, lr := range sortOCRReadingOrder(r.Results) {
			txt := lr.Text
			if doCorrect {
				var cor []Correction
				txt = CorrectText(txt, lr.Conf, p.Index, 0, &cor)
				doc.Corrections = append(doc.Corrections, cor...)
			}
			x0, y0, x1, y1 := ocrBoxRect(lr)
			b.Lines = append(b.Lines, Line{Runs: []Run{{Text: txt, SizePt: 11, Conf: lr.Conf,
				X0: x0, Y0: y0, X1: x1, Y1: y1}}})
		}
		p.Blocks = []Block{b}
		p.NeedsOCR = false
	}
}

// Text_run — run tiện dụng cho đường OCR.
func Text_run(txt string, conf float64) Run {
	return Run{Text: txt, SizePt: 11, Conf: conf, X0: 0, X1: 100, Y0: 0, Y1: 14}
}

// concatRunText — chuỗi toàn bộ run (để chấm điểm trang).
func concatRunText(runs []Run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
		b.WriteString(" ")
	}
	return b.String()
}

// ═══════════════ CLI đường Text ═══════════════

// runPDFTextCLI — `--pdf-text file.pdf`: trích lớp chữ → in stdout (+ tuỳ chọn
// --docx, --json-ldm). KHÔNG cần model ONNX — chạy được trên máy không model.
func runPDFTextCLI(pdfPath, docxPath, ldmPath string, doCorrect, pageBreaks bool, quiet bool) int {
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "viocr: lỗi đọc PDF:", err)
		return 1
	}
	doc, err := buildLDMFromPDFBytes(filepath.Base(pdfPath), data, doCorrect)
	if err != nil {
		fmt.Fprintln(os.Stderr, "viocr: lỗi trích lớp chữ:", err)
		return 1
	}
	needOCR := 0
	for _, p := range doc.Pages {
		if p.NeedsOCR {
			needOCR++
		}
	}
	if needOCR > 0 && !quiet {
		fmt.Fprintf(os.Stderr, "viocr: %d/%d trang không có lớp chữ đủ tin (scan/chữ giả) — trang này cần OCR qua GUI\n",
			needOCR, len(doc.Pages))
	}
	fmt.Print(doc.PlainText())
	if docxPath != "" {
		if err := WriteDocx(doc, docxPath, pageBreaks); err != nil {
			fmt.Fprintln(os.Stderr, "viocr: lỗi xuất Word:", err)
			return 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "viocr: đã xuất %s (%d trang, %d chỗ hiệu chỉnh)\n",
				docxPath, len(doc.Pages), len(doc.Corrections))
		}
	}
	if ldmPath != "" {
		if err := doc.SaveJSON(ldmPath); err != nil {
			fmt.Fprintln(os.Stderr, "viocr: lỗi ghi LDM JSON:", err)
			return 1
		}
	}
	return 0
}
