package main

// pipeline.go — glue v3.0: nối M1 → M3 → M4 → LDM cho đường Text, và
// đường OCR → LDM cho ảnh/ trang scan. Thứ tự theo tài liệu thiết kế Pha 1–4.

import (
	"fmt"
	"os"
	"path/filepath"
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
		p.Blocks = buildPageBlocks(runs, rp.W, rp.H)
		doc.Pages = append(doc.Pages, p)
	}
	doc.MsText = float64(time.Since(start).Milliseconds())
	return doc, nil
}

// buildLDFFromImageResults — đường OCR (v2 pipeline) → LDM: mỗi dòng OCR
// thành 1 dòng trong khối đoạn. Cỡ chữ suy luận 11pt, tin cậy trung bình.
func buildLDMFromImageResults(srcName string, pages []*imageResult, doCorrect bool) *Document {
	doc := &Document{Source: srcName, Corrections: []Correction{}}
	for i, r := range pages {
		p := Page{Index: i + 1, Path: "ocr", W: float64(r.Width), H: float64(r.Height)}
		b := Block{Kind: BlockPara, Conf: 1.0, X0: 0, Y0: 0, X1: float64(r.Width), Y1: float64(r.Height)}
		for _, lr := range r.Results {
			txt := lr.Text
			conf := lr.Conf
			if doCorrect {
				var cor []Correction
				txt = CorrectText(txt, conf, i+1, 0, &cor)
				doc.Corrections = append(doc.Corrections, cor...)
			}
			run := Run{Text: txt, Font: "", SizePt: 11, Conf: conf,
				X0: float64(lr.Box[0][0]), Y0: float64(lr.Box[0][1]),
				X1: float64(lr.Box[2][0]), Y1: float64(lr.Box[2][1])}
			b.Lines = append(b.Lines, Line{Runs: []Run{run}})
		}
		if len(b.Lines) > 0 {
			p.Blocks = []Block{b}
		}
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
		for _, lr := range r.Results {
			txt := lr.Text
			if doCorrect {
				var cor []Correction
				txt = CorrectText(txt, lr.Conf, p.Index, 0, &cor)
				doc.Corrections = append(doc.Corrections, cor...)
			}
			b.Lines = append(b.Lines, Line{Runs: []Run{Text_run(txt, lr.Conf)}})
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
