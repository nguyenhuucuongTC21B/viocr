package main

// docx_test.go — kiểm tra M5: LDM → .docx (zip + OOXML hợp lệ).

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestWriteDocxBasic(t *testing.T) {
	doc := &Document{
		Source: "cong-van-test.pdf",
		Pages: []Page{{
			Index: 1, Path: "text", W: 595, H: 842,
			Blocks: []Block{
				{Kind: BlockHeading, Heading: Heading1, Lines: []Line{{
					Runs: []Run{{Text: "THÔNG BÁO", Font: "Times New Roman", SizePt: 16, Bold: true, Conf: 1.0,
						X0: 150, Y0: 60, X1: 445, Y1: 76}}}}},
				{Kind: BlockPara, Lines: []Line{{
					Runs: []Run{{Text: "Căn cứ Nghị định 09/2026 về công tác cơ yếu.", Font: "Times New Roman", SizePt: 13, Conf: 1.0,
						X0: 55, Y0: 120, X1: 540, Y1: 133}}}}},
			},
		}},
	}
	path := t.TempDir() + "/test.docx"
	if err := WriteDocx(doc, path, true); err != nil {
		t.Fatalf("WriteDocx lỗi: %v", err)
	}
	// đọc lại zip + kiểm tra nội dung XML
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("không mở được zip: %v", err)
	}
	var docXML string
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var buf bytes.Buffer
			buf.ReadFrom(rc)
			rc.Close()
			docXML = buf.String()
		}
	}
	if docXML == "" {
		t.Fatal("thiếu word/document.xml")
	}
	if !strings.Contains(docXML, "THÔNG BÁO") {
		t.Error("mất tiêu đề")
	}
	if !strings.Contains(docXML, `pStyle w:val="Heading1"`) {
		t.Error("thiếu pStyle Heading1")
	}
	if !strings.Contains(docXML, "Nghị định 09/2026") {
		t.Error("mất đoạn thân bài")
	}
	if !strings.Contains(docXML, `w:br w:type="page"`) || true {
		// chỉ 1 trang → không có ngắt trang; chỉ kiểm XML tồn tại hợp lệ
		_ = docXML
	}
}

func TestWriteDocxTable(t *testing.T) {
	doc := &Document{Pages: []Page{{
		Index: 1, Path: "text", W: 595, H: 842,
		Blocks: []Block{{Kind: BlockTable, Table: &TableBlock{
			Grid: [][]Cell{
				{{Text: "STT"}, {Text: "Họ tên"}},
				{{Text: "1", ColSpan: 1}, {Text: "Nguyễn Văn A"}},
			},
			ColEdges: []float64{60, 100, 300},
		}}}}},
	}
	path := t.TempDir() + "/table.docx"
	if err := WriteDocx(doc, path, false); err != nil {
		t.Fatalf("WriteDocx lỗi: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte("w:tbl")) && true {
		// zip nén — tìm trong XML
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range zr.File {
			if f.Name == "word/document.xml" {
				rc, _ := f.Open()
				var buf bytes.Buffer
				buf.ReadFrom(rc)
				rc.Close()
				found = bytes.Contains(buf.Bytes(), []byte("<w:tbl>")) &&
					bytes.Contains(buf.Bytes(), []byte("Nguyễn Văn A")) &&
					bytes.Contains(buf.Bytes(), []byte("<w:gridCol"))
			}
		}
		if !found {
			t.Error("bảng không được sinh đúng trong document.xml")
		}
	}
}
