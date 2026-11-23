package main

// pdfmini_test.go — kiểm tra M1: trích lớp chữ từ PDF tiếng Việt thật
// (testdata/minimal.pdf sinh bằng reportlab + DejaVu, có ToUnicode).

import (
	"os"
	"strings"
	"testing"
)

func TestExtractPDFPagesMinimal(t *testing.T) {
	data, err := os.ReadFile("testdata/minimal.pdf")
	if err != nil {
		t.Skip("thiếu testdata/minimal.pdf")
	}
	pages, err := ExtractPDFPages(data)
	if err != nil {
		t.Fatalf("ExtractPDFPages lỗi: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("kỳ vọng 1 trang, nhận %d", len(pages))
	}
	p := pages[0]
	if p.W <= 0 || p.H <= 0 {
		t.Fatalf("kích thước trang sai: %v×%v", p.W, p.H)
	}
	if len(p.Runs) < 3 {
		t.Fatalf("kỳ vọng ≥3 run (tiêu đề + 3 dòng), nhận %d", len(p.Runs))
	}
	text := ""
	for _, r := range p.Runs {
		text += r.Text + "\n"
	}
	for _, want := range []string{"THÔNG BÁO", "Giám đốc", "Đà Nẵng", "12/2026", "Ủy ban nhân dân"} {
		if !strings.Contains(text, want) {
			t.Errorf("thiếu %q trong lớp chữ trích được:\n%s", want, text)
		}
	}
	// ToUnicode phải cho chữ có dấu đúng — không được có U+FFFD
	if strings.ContainsRune(text, '\uFFFD') {
		t.Error("có ký tự mất mã (U+FFFD) — ToUnicode đọc sai")
	}
}

func TestPageClassification(t *testing.T) {
	// trang chữ tốt
	if r := ValidSyllableRatio("Căn cứ Nghị định về công tác cơ yếu của Bộ"); r < 0.85 {
		t.Errorf("ratio văn bản tốt = %.2f, kỳ vọng ≥0.85", r)
	}
	// chuỗi rác (chữ giả ToUnicode hỏng)
	if r := ValidSyllableRatio("qqq www zzz kkk jjj xfv lwr"); r > 0.3 {
		t.Errorf("ratio chuỗi rác = %.2f, kỳ vọng ≤0.3", r)
	}
}

func TestLayoutPageBlocks(t *testing.T) {
	data, err := os.ReadFile("testdata/minimal.pdf")
	if err != nil {
		t.Skip("thiếu testdata/minimal.pdf")
	}
	pages, err := ExtractPDFPages(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("không trích được trang")
	}
	doc, err := buildLDMFromPDFBytes("minimal.pdf", data, false)
	if err != nil {
		t.Fatal(err)
	}
	pg := doc.Pages[0]
	if pg.NeedsOCR {
		t.Error("trang chữ tốt không được đánh dấu NeedsOCR")
	}
	// tiêu đề 16pt tách riêng; 3 dòng thân bài (cùng cỡ, cùng lề trái, khe nhỏ)
	// PHẢI gom thành 1 khối đoạn — tổng 2 khối là đúng
	if len(pg.Blocks) < 2 {
		t.Fatalf("kỳ vọng ≥2 khối (đề mục + đoạn), nhận %d", len(pg.Blocks))
	}
	hasHeading := false
	paraLines := 0
	for _, b := range pg.Blocks {
		if b.Kind == BlockHeading && b.Heading >= 1 {
			hasHeading = true
		}
		if b.Kind == BlockPara {
			paraLines += len(b.Lines)
		}
	}
	if !hasHeading {
		t.Error("không nhận diện được đề mục 'THÔNG BÁO' (cỡ 16pt > thân 12pt)")
	}
	if paraLines < 3 {
		t.Errorf("khối đoạn phải gồm 3 dòng thân bài, nhận %d dòng", paraLines)
	}
}

func TestTextPathCERZero(t *testing.T) {
	// đường Text phải cho CER ≈ 0 so với chuỗi gốc
	data, err := os.ReadFile("testdata/minimal.pdf")
	if err != nil {
		t.Skip("thiếu testdata/minimal.pdf")
	}
	doc, err := buildLDMFromPDFBytes("minimal.pdf", data, false)
	if err != nil {
		t.Fatal(err)
	}
	gold := "THÔNG BÁO\nGiám đốc Công an tỉnh Đà Nẵng phản hồi về kế hoạch bảo mật.\nVăn bản số 12/2026 trình ký ngày 23/11/2026.\nỦy ban nhân dân thành phố kết luận nội dung kiểm tra cơ yếu."
	got := doc.PlainText()
	// bỏ phần "===== Trang i/N =====" đầu mỗi trang
	got = strings.ReplaceAll(got, "===== Trang 1/1 =====\n", "")
	got = strings.TrimSpace(got)
	if c := CER(gold, got); c > 0.02 {
		t.Errorf("CER đường Text = %.4f (>0.02):\nGOLD: %q\nGOT : %q", c, gold, got)
	}
}
