package main

// line.go — sắp xếp box thành dòng văn bản (tham chiếu sort theo DeepDoc:
// nhóm theo y, trong dòng sắp theo x; khoảng cách dọc < 10px gộp cùng dòng).

import "sort"

// LineResult kết quả từng box.
type LineResult struct {
	Box      Box     `json:"box"`
	DetScore float64 `json:"det_score"`
	Text     string  `json:"text"`
	Conf     float64 `json:"conf"`
}

// GroupLines sắp box theo dòng: trả về các dòng (mỗi dòng là danh sách box đã
// sắp trái→phải), dòng trên trước dòng dưới.
func GroupLines(boxes []ScoredBox) [][]int {
	type bi struct {
		idx   int
		yc    float64
		x0    int
		ymaxI int
	}
	items := make([]bi, len(boxes))
	for i, sb := range boxes {
		b := sb.Box
		h := boxHeight(b)
		items[i] = bi{idx: i, yc: float64(b[0][1]+b[3][1]) / 2, x0: b[0][0], ymaxI: h}
	}
	sort.Slice(items, func(a, b int) bool {
		if items[a].yc != items[b].yc {
			return items[a].yc < items[b].yc
		}
		return items[a].x0 < items[b].x0
	})
	var lines [][]int
	var cur []int
	curY := 0.0
	curH := 0
	for _, it := range items {
		if len(cur) > 0 {
			// cùng dòng nếu tâm y lệch không quá max(10, 0.5*height)
			thresh := float64(maxI(10, curH/2))
			if absF(it.yc-curY) > thresh {
				lines = append(lines, cur)
				cur = nil
			}
		}
		cur = append(cur, it.idx)
		curY = it.yc
		if it.ymaxI > curH {
			curH = it.ymaxI
		}
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	// sắp trái→phải trong từng dòng
	for _, ln := range lines {
		sort.Slice(ln, func(a, b int) bool {
			return boxes[ln[a]].Box[0][0] < boxes[ln[b]].Box[0][0]
		})
	}
	return lines
}

// JoinLine ghép text các box trong dòng; chèn khoảng trắng nếu gap đủ lớn.
func JoinLine(rs []LineResult) string {
	out := ""
	prevEnd := -1
	prevH := 0
	for _, r := range rs {
		if out != "" {
			h := boxHeight(r.Box)
			gap := r.Box[0][0] - prevEnd
			ref := maxI(prevH, h)
			if gap > ref/2 {
				out += " "
			}
		}
		out += r.Text
		prevEnd = maxI(r.Box[1][0], r.Box[2][0])
		prevH = boxHeight(r.Box)
	}
	return out
}
