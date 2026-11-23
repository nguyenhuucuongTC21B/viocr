package main

// bench_test.go — kiểm tra M6: CER/WER/TS/LFS.

import (
	"encoding/json"
	"testing"
)

func TestCERKnown(t *testing.T) {
	if got := CER("hà nội", "hà nội"); got != 0 {
		t.Errorf("CER giống hệt = 0, nhận %.4f", got)
	}
	if got := CER("hà nội", "ha noi"); got <= 0 || got >= 1 {
		t.Errorf("CER mất dấu phải nằm trong (0,1), nhận %.4f", got)
	}
	// 1 ký tự sai trong 8 → 0.125
	if got := CER("abcdefgh", "abcdxfgh"); got != 0.125 {
		t.Errorf("CER = 0.125, nhận %.4f", got)
	}
}

func TestWERKnown(t *testing.T) {
	// "nhà nước pháp quyền" vs "nhà nước pháp quyền" = 0
	if got := WER("nhà nước pháp quyền", "nhà nước pháp quyền"); got != 0 {
		t.Errorf("WER giống hệt = 0, nhận %.4f", got)
	}
	// sai 1 từ trong 4 → 0.25
	if got := WER("nhà nước pháp quyền", "nhà nước pháp luật"); got != 0.25 {
		t.Errorf("WER = 0.25, nhận %.4f", got)
	}
}

func TestTSTables(t *testing.T) {
	d1 := &Document{Pages: []Page{{Index: 1, Blocks: []Block{{
		Kind: BlockTable,
		Table: &TableBlock{Grid: [][]Cell{
			{{Text: "STT"}, {Text: "Nội dung"}},
			{{Text: "1"}, {Text: "Cán bộ"}},
		}},
	}}}}}
	// giống hệt → TS = 1
	d2 := cloneDoc(d1)
	if got := TS(d1, d2); got != 1 {
		t.Errorf("TS bảng giống hệt = 1, nhận %.4f", got)
	}
	// sai cột → 0
	d2.Pages[0].Blocks[0].Table.Grid[0][1].Text = "Khác hẳn rồi nè"
	if got := TS(d1, d2); got != 0 {
		t.Errorf("TS bảng sai ô = 0, nhận %.4f", got)
	}
}

func TestLFS(t *testing.T) {
	gold := &Document{Pages: []Page{{Index: 1, Blocks: []Block{
		{Kind: BlockHeading}, {Kind: BlockPara}, {Kind: BlockTable},
	}}}}
	out := cloneDoc(gold)
	if got := LFS(gold, out); got != 1 {
		t.Errorf("LFS giống hệt = 1, nhận %.4f", got)
	}
	// đổi 1 khối bảng thành đoạn → chênh 1 bước trong chuỗi 3 → LFS = 2/3
	out.Pages[0].Blocks[2].Kind = BlockPara
	out.Pages[0].Blocks[2].Table = nil
	if got := LFS(gold, out); got < 0.65 || got > 0.7 {
		t.Errorf("LFS = 2/3, nhận %.4f", got)
	}
}

// cloneDoc — sao bản LDM qua JSON (đơn giản, đủ dùng cho test).
func cloneDoc(d *Document) *Document {
	b, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	var out Document
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return &out
}
