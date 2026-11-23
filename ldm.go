package main

// ldm.go — Logical Document Model (LDM): trục dữ liệu trung tâm của viocr v3.0.
// Mọi module ghi kết quả vào LDM; bộ sinh DOCX (docx.go) chỉ đọc từ LDM.
// Thiết kế theo tài liệu "Thiết kế kỹ thuật viocr v3.0" mục 3.2, Bảng 2:
//   Document → Page → Block (Para | Heading | Table | Picture | Footer | Header)
//            → Line → Run (chữ + font + cỡ + vị trí + độ tin cậy).

import (
	"encoding/json"
	"os"
)

// Cấp đề mục (Block.Kind = BlockHeading).
const (
	HeadingNone = 0 // không phải đề mục
	Heading1    = 1
	Heading2    = 2
	Heading3    = 3
)

// BlockKind — loại khối logic.
type BlockKind int

const (
	BlockPara    BlockKind = iota // đoạn văn thường
	BlockHeading                  // đề mục theo cấp
	BlockTable                    // bảng (chi tiết trong TableBlock)
	BlockPicture                  // hình ảnh / vùng đồ họa
	BlockFooter                   // chân trang lặp lại
	BlockHeader                   // đầu trang lặp lại
)

func (k BlockKind) String() string {
	switch k {
	case BlockPara:
		return "para"
	case BlockHeading:
		return "heading"
	case BlockTable:
		return "table"
	case BlockPicture:
		return "picture"
	case BlockFooter:
		return "footer"
	case BlockHeader:
		return "header"
	}
	return "para"
}

// Rect — hình chữ nhật, tọa độ pixel từ GÓC TRÊN-TRÁI trang (y tăng xuống).
type Rect struct {
	X0, Y0, X1, Y1 float64
}

func (r Rect) W() float64 { return r.X1 - r.X0 }
func (r Rect) H() float64 { return r.Y1 - r.Y0 }

// Run — đơn vị nhỏ nhất: chuỗi chữ + định dạng + vị trí + độ tin cậy.
// Với trang đường Text: Font/SizePt lấy thẳng từ metadata PDF (chính xác 100%).
type Run struct {
	Text   string  `json:"text"`
	Font   string  `json:"font,omitempty"`
	SizePt float64 `json:"size_pt,omitempty"` // cỡ chữ điểm
	Bold   bool    `json:"bold,omitempty"`
	Italic bool    `json:"italic,omitempty"`
	X0     float64 `json:"x0"`
	Y0     float64 `json:"y0"`
	X1     float64 `json:"x1"`
	Y1     float64 `json:"y1"`
	Conf   float64 `json:"conf,omitempty"` // 0..1; 1.0 với đường Text
}

// Line — một dòng chữ trong khối.
type Line struct {
	Runs []Run `json:"runs"`
}

// Text trả về chuỗi ghép của các run trong dòng (chèn khoảng trắng theo khe).
func (ln Line) Text() string {
	out := ""
	prevX1 := 0.0
	for i, r := range ln.Runs {
		if i > 0 && r.X0-prevX1 > 0.22*maxF(r.SizePt, 6) {
			out += " "
		}
		out += r.Text
		prevX1 = r.X1
	}
	return out
}

// Cell — một ô trong bảng.
type Cell struct {
	Text      string `json:"text"`
	ColSpan   int    `json:"col_span,omitempty"` // gộp ngang (mặc định 1)
	RowSpan   int    `json:"row_span,omitempty"` // gộp dọc (mặc định 1)
	ContVDown bool   `json:"cont_v,omitempty"`   // ô tiếp tục của gộp dọc (vMerge continue)
}

// TableBlock — lưới ô bảng (Block.Kind = BlockTable).
type TableBlock struct {
	Grid      [][]Cell  `json:"grid"`      // hàng × cột
	ColEdges  []float64 `json:"col_edges"` // biên cột (x), dùng cho tblGrid
	HeaderRow bool      `json:"header_row,omitempty"`
}

// Block — khối logic có nhãn loại và thứ tự đọc.
type Block struct {
	Kind     BlockKind   `json:"kind"`
	Heading  int         `json:"heading,omitempty"` // 1..3 khi Kind=heading
	Lines    []Line      `json:"lines,omitempty"`
	Table    *TableBlock `json:"table,omitempty"`
	Order    int         `json:"order"`               // thứ tự đọc trong trang
	Conf     float64     `json:"conf"`                // tin cậy trung bình của khối
	FirstInd float64     `json:"first_ind,omitempty"` // thụt lề dòng đầu (pixel)
	X0       float64     `json:"x0"`
	Y0       float64     `json:"y0"`
	X1       float64     `json:"x1"`
	Y1       float64     `json:"y1"`
	Center   bool        `json:"center,omitempty"` // căn giữa (theo vị trí)
}

// Block bounding box helper.
func (b *Block) Bounds() Rect {
	r := Rect{b.X0, b.Y0, b.X1, b.Y1}
	return r
}

// Page — một trang tài liệu + đường xử lý đã chọn ở Pha 1.
type Page struct {
	Index    int     `json:"index"` // 1-based
	Path     string  `json:"path"`  // "text" | "ocr"
	W        float64 `json:"w"`     // kích thước trang (pixel 72dpi)
	H        float64 `json:"h"`
	Blocks   []Block `json:"blocks"`
	NeedsOCR bool    `json:"needs_ocr,omitempty"` // trang chữ giả/không đủ chữ → phải OCR
}

// Correction — một chỗ hiệu chỉnh tiếng Việt (M4) để log + GUI xem lại.
type Correction struct {
	Page  int     `json:"page"`
	Orig  string  `json:"orig"`
	Fixed string  `json:"fixed"`
	Conf  float64 `json:"conf"`  // tin cậy của run trước khi sửa
	Block int     `json:"block"` // chỉ số khối trong trang
}

// Document — gốc của toàn tài liệu, một bản cho cả file.
type Document struct {
	Source      string       `json:"source"` // tên file nguồn
	Pages       []Page       `json:"pages"`
	Corrections []Correction `json:"corrections,omitempty"`
	MsText      float64      `json:"ms_text_path"` // thời gian đường Text
	MsOCR       float64      `json:"ms_ocr"`       // thời gian đường OCR
}

// SaveJSON ghi LDM ra file (phục vụ --json-ldm và bench).
func (d *Document) SaveJSON(path string) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadJSON đọc LDM từ file (phục vụ bench TS/LFS và GUI export).
func LoadJSON(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// PlainText — văn bản thô của cả tài liệu (định dạng giống v2: "===== Trang i/N =====").
func (d *Document) PlainText() string {
	out := ""
	for i, p := range d.Pages {
		out += "===== Trang " + itoa(i+1) + "/" + itoa(len(d.Pages)) + " =====\n"
		for _, b := range p.Blocks {
			out += blockText(b) + "\n"
		}
		out += "\n"
	}
	return out
}

// blockText — chuỗi của một khối (bảng ghép ô bằng " | ").
func blockText(b Block) string {
	if b.Kind == BlockTable && b.Table != nil {
		s := ""
		for _, row := range b.Table.Grid {
			cells := make([]string, 0, len(row))
			for _, c := range row {
				cells = append(cells, c.Text)
			}
			s += joinStrings(cells, " | ") + "\n"
		}
		return s
	}
	lines := make([]string, 0, len(b.Lines))
	for _, ln := range b.Lines {
		lines = append(lines, ln.Text())
	}
	return joinStrings(lines, "\n")
}

// WordCount — số "từ" (tách khoảng trắng) toàn tài liệu.
func (d *Document) WordCount() int {
	n := 0
	for _, p := range d.Pages {
		for _, b := range p.Blocks {
			n += countWords(blockText(b))
		}
	}
	return n
}

func countWords(s string) int {
	in := false
	n := 0
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' {
			in = false
		} else if !in {
			in = true
			n++
		}
	}
	return n
}
