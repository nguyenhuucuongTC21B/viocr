package main

// layout.go — M3 (Layout Analyzer) cho đường Text: dựng khối logic từ các Run
// trích được (pdfmini.go). Giải thuật theo tài liệu v3.0 mục 4.3:
//   (1) gom dòng: cluster theo tâm-y, sắp x, chèn khoảng trắng theo khe;
//   (2) gom khối đoạn: khe dọc ≤ 1.5×cỡ chữ + thụt lề tương hợp;
//   (3) đề mục: cỡ chữ lớn hơn thân bài → Heading 1..3;
//   (4) đầu/cuối trang: vùng biên trên/dưới + lặp lại qua nhiều trang;
//   (5) bảng heuristic: ≥3 hàng × ≥3 cột thẳng hàng biên x (đường kẻ thuộc
//       đường OCR — xử lý ở mốc sau, xem mục 7 tài liệu thiết kế).

import (
	"sort"
	"strings"
)

const (
	headFooterBand = 0.09 // 9% chiều cao trang tính là vùng đầu/cuối trang
)

// buildPageBlocks — dựng khối cho 1 trang từ các run thô (đường Text).
// Trả về các Block theo thứ tự đọc. Đầu/cuối trang chỉ được coi là
// Header/Footer khi CHỮ LẶP LẠI ở cùng vị trí trên ≥2 trang (xem repeatedHeadFoot):
// tránh ăn nhầm tiêu đề lớn nằm trong dải biên.
func buildPageBlocks(runs []Run, w, h float64) []Block {
	return buildPageBlocksWithHF(runs, w, h, nil, nil)
}

// buildPageBlocksWithHF — như trên, kèm tập chữ đầu/cuối trang đã biết lặp
// (từ phân tích cả tài liệu). key = chữ thường hóa của dòng.
func buildPageBlocksWithHF(runs []Run, w, h float64, repHead, repFoot map[string]bool) []Block {
	if len(runs) == 0 {
		return nil
	}
	// 1) heuristic bảng từ run: ĐANG TẮT trong alpha — trên tài liệu nhiều cột
	// (ví dụ bảng so sánh 2 cột của quy chế) nó nhận nhầm vùng chữ thành lưới
	// và làm mất/sai thứ tự chữ — với hồ sơ pháp lý, đọc đúng thứ tự quan trọng
	// hơn có bảng. Bảng thật (có đường kẻ) sẽ nhận diện bằng morphology ở beta
	// (mục 4.3 tài liệu thiết kế). Hàm giữ lại kèm test để mở khi hoàn thiện.
	rest, tableBlocks := runs, []Block(nil)
	// rest, tableBlocks := detectTableRuns(runs)

	lines := groupLines(rest)
	if len(lines) == 0 {
		renumber(tableBlocks)
		return tableBlocks
	}
	// cỡ chữ phổ biến = thân bài
	bodySize := medianLineSize(lines)

	// tách đầu/cuối trang (vùng biên) khỏi phần thân — chỉ khi dòng lặp lại
	var body, heads, foots []lLine
	for _, ln := range lines {
		key := hfKey(ln)
		yMid := (ln.y0 + ln.y1) / 2
		inHead := yMid < h*headFooterBand
		inFoot := yMid > h*(1-headFooterBand)
		switch {
		case inHead && (repHead == nil || repHead[key]):
			heads = append(heads, ln)
		case inFoot && (repFoot == nil || repFoot[key]):
			foots = append(foots, ln)
		default:
			body = append(body, ln)
		}
	}

	// thứ tự đọc: các dòng đưa vào groupBlocks theo y (đã sort bởi groupLines);
	// cột trái/phải sẽ thành các khối riêng và được sortBlocksReadingOrder cuối
	// hàm xếp đúng trái→phải trong từng băng y. KHÔNG reorder dòng trước đó:
	// splitColumns kiểu "trái hết rồi phải" làm mất các dòng vắt qua cột
	// (tiêu đề, đoạn đầy trang) — lỗi thực tế trên Quy chế 274.
	blocks := groupBlocks(body, bodySize, w)
	// đầu trang → BlockHeader, cuối trang → BlockFooter (tọa độ đầy đủ)
	for _, ln := range heads {
		b := Block{Kind: BlockHeader, Lines: []Line{ln.toLine()}, Conf: 1.0}
		b.X0, b.Y0, b.X1, b.Y1 = ln.x0, ln.y0, ln.x1, ln.y1
		blocks = append(blocks, b)
	}
	for _, ln := range foots {
		b := Block{Kind: BlockFooter, Lines: []Line{ln.toLine()}, Conf: 1.0}
		b.X0, b.Y0, b.X1, b.Y1 = ln.x0, ln.y0, ln.x1, ln.y1
		blocks = append(blocks, b)
	}
	blocks = append(blocks, tableBlocks...)
	sortBlocksReadingOrder(blocks)
	renumber(blocks)
	return blocks
}

// hfKey — khóa so sánh dòng đầu/cuối trang (chữ thường, gọn khoảng trắng).
func hfKey(ln lLine) string {
	return strings.Join(strings.Fields(strings.ToLower(ln.toLine().Text())), " ")
}

// repeatedHeadFoot — quét mọi trang: dòng trong dải biên xuất hiện ở ≥2 trang
// → trả (tập header lặp, tập footer lặp). Nhận diện "chân trang lặp" theo
// mục 4.3 tài liệu thiết kế.
func repeatedHeadFoot(pages []rawPage) (map[string]bool, map[string]bool) {
	headCount := map[string]int{}
	footCount := map[string]int{}
	for _, rp := range pages {
		seenH := map[string]bool{}
		seenF := map[string]bool{}
		for _, ln := range groupLines(rp.Runs) {
			yMid := (ln.y0 + ln.y1) / 2
			key := hfKey(ln)
			if key == "" {
				continue
			}
			if yMid < rp.H*headFooterBand && !seenH[key] {
				headCount[key]++
				seenH[key] = true
			} else if yMid > rp.H*(1-headFooterBand) && !seenF[key] {
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

// sortBlocksReadingOrder — thứ tự đọc: gom khối theo BĂNG Y (khối chồng y
// ≥ 30% chiều cao khối thấp hơn vào cùng băng — cùng "hàng"), sort băng theo
// y, trong băng sort theo x0. Khắc phục trộn cột: khối trái đọc trước khối phải.
func sortBlocksReadingOrder(blocks []Block) {
	type band struct {
		y0, y1 float64
		items  []int
	}
	idx := make([]int, len(blocks))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return blocks[idx[a]].Y0 < blocks[idx[b]].Y0
	})
	var bands []band
	for _, bi := range idx {
		b := &blocks[bi]
		placed := false
		for k := len(bands) - 1; k >= 0 && !placed; k-- {
			bd := &bands[k]
			// chồng y: phần giao ≥ 30% chiều cao nhỏ hơn
			ov := minF(b.Y1, bd.y1) - maxF(b.Y0, bd.y0)
			hmin := minF(b.Y1-b.Y0, bd.y1-bd.y0)
			if hmin <= 0 {
				hmin = 1
			}
			if ov > 0.3*hmin {
				bd.items = append(bd.items, bi)
				if b.Y0 < bd.y0 {
					bd.y0 = b.Y0
				}
				if b.Y1 > bd.y1 {
					bd.y1 = b.Y1
				}
				placed = true
			}
			if b.Y0 > bd.y1 {
				break // băng này đã nằm hoàn toàn phía trên
			}
		}
		if !placed {
			bands = append(bands, band{y0: b.Y0, y1: b.Y1, items: []int{bi}})
		}
	}
	sort.SliceStable(bands, func(a, b int) bool { return bands[a].y0 < bands[b].y0 })
	ordered := make([]Block, 0, len(blocks))
	for _, bd := range bands {
		items := append([]int(nil), bd.items...)
		sort.SliceStable(items, func(a, b int) bool {
			return blocks[items[a]].X0 < blocks[items[b]].X0
		})
		for _, bi := range items {
			ordered = append(ordered, blocks[bi])
		}
	}
	copy(blocks, ordered)
}

// ═══════════════ dòng ═══════════════

type lLine struct {
	runs    []Run
	x0, x1  float64
	y0, y1  float64
	size    float64
}

func (ln lLine) toLine() Line { return Line{Runs: ln.runs} }

// groupLines — cluster run theo tâm y (dung sai 0.45×cỡ chữ), sắp theo x.
func groupLines(runs []Run) []lLine {
	type cluster struct {
		ySum, yN  float64
		lines     []lLine
	}
	// sắp theo y trước
	sorted := append([]Run(nil), runs...)
	sort.Slice(sorted, func(i, j int) bool {
		yi := (sorted[i].Y0 + sorted[i].Y1) / 2
		yj := (sorted[j].Y0 + sorted[j].Y1) / 2
		if yi != yj {
			return yi < yj
		}
		return sorted[i].X0 < sorted[j].X0
	})
	var clusters []cluster
	for _, r := range sorted {
		yMid := (r.Y0 + r.Y1) / 2
		size := maxF(r.SizePt, 6)
		placed := false
		for ci := len(clusters) - 1; ci >= 0; ci-- {
			c := &clusters[ci]
			cy := c.ySum / c.yN
			if yMid-cy <= 0.45*size {
				// chỉ nhận nếu chưa lệch quá xa (tránh cộng dồn trang dài)
				if yMid-cy > 4*size {
					break
				}
				c.ySum += yMid
				c.yN++
				placed = true
				break
			}
			if yMid-cy > 4*size {
				break
			}
		}
		if !placed {
			clusters = append(clusters, cluster{ySum: yMid, yN: 1})
			ci := len(clusters) - 1
			clusters[ci].lines = append(clusters[ci].lines, lLine{runs: []Run{r},
				x0: r.X0, x1: r.X1, y0: r.Y0, y1: r.Y1, size: r.SizePt})
			continue
		}
		// thêm run vào cluster vừa nhận (cluster cuối gần nhất đã chạm)
		c := &clusters[len(clusters)-1]
		// tìm line trong cluster mà run giao theo y — alpha: thêm vào line
		// gần x nhất
		best := -1
		bestGap := 1e18
		for li := range c.lines {
			gap := gapX(c.lines[li], r)
			if gap < bestGap {
				bestGap = gap
				best = li
			}
		}
		if best >= 0 && bestGap <= 2.5*maxF(r.SizePt, 6) {
			ln := &c.lines[best]
			ln.runs = append(ln.runs, r)
			ln.x0 = minF(ln.x0, r.X0)
			ln.x1 = maxF(ln.x1, r.X1)
			ln.y0 = minF(ln.y0, r.Y0)
			ln.y1 = maxF(ln.y1, r.Y1)
		} else {
			c.lines = append(c.lines, lLine{runs: []Run{r},
				x0: r.X0, x1: r.X1, y0: r.Y0, y1: r.Y1, size: r.SizePt})
		}
	}
	var out []lLine
	for _, c := range clusters {
		for _, ln := range c.lines {
			sortRunsByX(ln.runs)
			out = append(out, ln)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].y0 < out[j].y0 })
	return out
}

func gapX(ln lLine, r Run) float64 {
	if r.X0 >= ln.x1 {
		return r.X0 - ln.x1
	}
	if r.X1 <= ln.x0 {
		return ln.x0 - r.X1
	}
	return 0 // giao nhau
}

func sortRunsByX(rs []Run) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].X0 < rs[j].X0 })
}

func medianLineSize(lines []lLine) float64 {
	sizes := make([]float64, 0, len(lines))
	for _, ln := range lines {
		for _, r := range ln.runs {
			sizes = append(sizes, r.SizePt)
		}
	}
	if len(sizes) == 0 {
		return 11
	}
	sort.Float64s(sizes)
	return sizes[len(sizes)/2]
}

// ═══════════════ cột ═══════════════

// splitColumns — nếu tìm được dải trắng dọc gần giữa trang đủ cao thì tách
// 2 cột và trả thứ tự đọc: cột trái hết → cột phải. Ngược lại nil.
func splitColumns(body []lLine, w float64) []lLine {
	if len(body) < 6 {
		return nil
	}
	mid := w / 2
	var left, right []lLine
	spanTop, spanBot := 0.0, 0.0
	nSpan := 0
	for _, ln := range body {
		if ln.x0 < mid-0.02*w && ln.x1 > mid+0.02*w {
			nSpan++ // dòng vắt qua giữa → chống chỉ định 2 cột rõ ràng
			if spanTop == 0 || ln.y0 < spanTop {
				spanTop = ln.y0
			}
			if ln.y1 > spanBot {
				spanBot = ln.y1
			}
			continue
		}
		if ln.x1 <= mid+0.01*w {
			left = append(left, ln)
		} else if ln.x0 >= mid-0.01*w {
			right = append(right, ln)
		} else {
			nSpan++
		}
	}
	if len(left) < 4 || len(right) < 4 || nSpan > len(body)/4 {
		return nil
	}
	sort.Slice(left, func(i, j int) bool { return left[i].y0 < left[j].y0 })
	sort.Slice(right, func(i, j int) bool { return right[i].y0 < right[j].y0 })
	return append(left, right...)
}

// ═══════════════ khối ═══════════════

// groupBlocks — gom dòng thành khối: khe dọc ≤ 1.5×size + biên trái tương hợp
// (bằng nhau, hoặc dòng sau thụt đầu dòng 0.5–2.5×size = đoạn mới nhưng vẫn
// cùng khối); dòng riêng lẻ lệch lớn → khối mới. Đề mục: cỡ chữ > 1.12×body.
func groupBlocks(lines []lLine, bodySize, pageW float64) []Block {
	var blocks []Block
	var cur []lLine
	curX0 := 0.0
	curKind := BlockPara
	curSize := bodySize
	flush := func() {
		if len(cur) == 0 {
			return
		}
		b := Block{Kind: curKind, Lines: make([]Line, 0, len(cur)), Conf: 1.0}
		if curKind == BlockHeading {
			ratio := curSize / bodySize
			switch {
			case ratio >= 1.5:
				b.Heading = Heading1
			case ratio >= 1.25:
				b.Heading = Heading2
			default:
				b.Heading = Heading3
			}
		}
		// thụt lề dòng đầu so với phần thân khối
		if len(cur) >= 2 {
			ind := cur[0].x0
			bodyX0 := cur[1].x0
			if ind-bodyX0 > 0.4*cur[0].size && ind-bodyX0 < 3*cur[0].size {
				b.FirstInd = ind - bodyX0
			}
		}
		// căn giữa: biên trái/trái lệch gần đối xứng và dòng ngắn
		f := cur[0]
		if len(cur) == 1 && f.x0 > pageW*0.14 && f.x1 < pageW*0.86 {
			marginL, marginR := f.x0, pageW-f.x1
			if marginL > 8 && absF(marginL-marginR) < 0.12*pageW {
				b.Center = true
			}
		}
		b.X0, b.Y0, b.X1, b.Y1 = 1e18, 1e18, 0, 0
		for _, ln := range cur {
			b.Lines = append(b.Lines, ln.toLine())
			b.X0 = minF(b.X0, ln.x0)
			b.Y0 = minF(b.Y0, ln.y0)
			b.X1 = maxF(b.X1, ln.x1)
			b.Y1 = maxF(b.Y1, ln.y1)
		}
		blocks = append(blocks, b)
		cur = nil
	}
	for _, ln := range lines {
		kind := BlockPara
		if maxLineSize(ln) > 1.12*bodySize {
			kind = BlockHeading
		}
		newBlock := false
		if len(cur) == 0 {
			newBlock = true
		} else {
			prev := cur[len(cur)-1]
			gap := ln.y0 - prev.y1
			size := maxF(maxLineSize(ln), prev.size)
			kindChanged := kind != curKind
			gapOK := gap <= 1.5*size && gap >= -0.5*size
			alignOK := absF(ln.x0-curX0) <= 0.7*size ||
				(ln.x0-curX0 >= 0.4*size && ln.x0-curX0 <= 3*size) ||
				(curX0-ln.x0 >= 0.4*size && curX0-ln.x0 <= 3*size)
			if kindChanged || !gapOK || !alignOK {
				newBlock = true
			}
		}
		if newBlock {
			flush()
			curKind = kind
			curSize = maxLineSize(ln)
			curX0 = ln.x0
		}
		cur = append(cur, ln)
	}
	flush()
	return blocks
}

func maxLineSize(ln lLine) float64 {
	s := 0.0
	for _, r := range ln.runs {
		if r.SizePt > s {
			s = r.SizePt
		}
	}
	if s == 0 {
		return 11
	}
	return s
}

func renumber(blocks []Block) {
	for i := range blocks {
		blocks[i].Order = i
	}
}

// detectTableRuns — heuristic bảng từ run: tìm vùng ≥3 "hàng" mà mỗi hàng có
// ≥3 run thẳng biên x nhau (dung sai 25% cỡ chữ) → dựng TableBlock.
// Trả về (các run còn lại sau khi lấy ra làm bảng, các Block bảng).
func detectTableRuns(runs []Run) ([]Run, []Block) {
	if len(runs) < 9 {
		return runs, nil
	}
	// cluster biên x (bắt đầu + kết thúc)
	edges := clusterEdges(runs)
	if len(edges) < 4 {
		return runs, nil
	}
	// nhóm theo y
	byY := groupLines(runs)
	// đếm mỗi "hàng" giao bao nhiêu cột
	var rows [][]Run
	for _, ln := range byY {
		row := rowCells(ln, edges)
		if len(row) >= 3 {
			rows = append(rows, row)
		}
	}
	if len(rows) < 3 {
		return runs, nil
	}
	// dựng bảng từ các hàng đủ cột; các run còn lại trả lại
	used := map[string]bool{}
	var grid [][]Cell
	var colEdges []float64
	for _, ln := range byY {
		row := rowCells(ln, edges)
		if len(row) < 3 {
			continue
		}
		for _, r := range ln.runs {
			used[rKey(r)] = true
		}
		cells := make([]Cell, 0, len(row))
		for _, r := range row {
			cells = append(cells, Cell{Text: strings.TrimSpace(r.Text)})
		}
		grid = append(grid, cells)
	}
	if len(grid) < 3 {
		return runs, nil
	}
	// chuẩn hóa: mọi hàng đủ số cột (pad ô rỗng)
	ncol := 0
	for _, row := range grid {
		if len(row) > ncol {
			ncol = len(row)
		}
	}
	for i := range grid {
		for len(grid[i]) < ncol {
			grid[i] = append(grid[i], Cell{Text: ""})
		}
	}
	for i, e := range edges[:ncol+1] {
		_ = i
		colEdges = append(colEdges, e)
	}
	tb := &TableBlock{Grid: grid, ColEdges: colEdges}
	blk := Block{Kind: BlockTable, Table: tb, Conf: 1.0}
	// biên bảng
	blk.X0, blk.Y0, blk.X1, blk.Y1 = 1e18, 1e18, 0, 0
	for _, row := range grid {
		for _, c := range row {
			_ = c
		}
	}
	for _, r := range runs {
		if used[rKey(r)] {
			blk.X0 = minF(blk.X0, r.X0)
			blk.Y0 = minF(blk.Y0, r.Y0)
			blk.X1 = maxF(blk.X1, r.X1)
			blk.Y1 = maxF(blk.Y1, r.Y1)
		}
	}
	var rest []Run
	for _, r := range runs {
		if !used[rKey(r)] {
			rest = append(rest, r)
		}
	}
	return rest, []Block{blk}
}

func rKey(r Run) string {
	return itoa(int(r.X0)) + "," + itoa(int(r.Y0)) + "," + r.Text
}

// clusterEdges — biên cột từ vị trí bắt đầu/kết thúc run (dồn cụm 25% size).
func clusterEdges(runs []Run) []float64 {
	var xs []float64
	for _, r := range runs {
		xs = append(xs, r.X0, r.X1)
	}
	sort.Float64s(xs)
	var out []float64
	clusterSum := xs[0]
	clusterN := 1
	size := 10.0
	if len(runs) > 0 {
		size = maxF(runs[0].SizePt, 6)
	}
	tol := 0.25 * size * 2 // dung cụm nới (không có metrics độ rộng chuẩn)
	for i := 1; i < len(xs); i++ {
		if xs[i]-xs[i-1] <= tol {
			clusterSum += xs[i]
			clusterN++
		} else {
			out = append(out, clusterSum/float64(clusterN))
			clusterSum = xs[i]
			clusterN = 1
		}
	}
	out = append(out, clusterSum/float64(clusterN))
	return out
}

// rowCells — các run của dòng chia theo biên cột edges.
func rowCells(ln lLine, edges []float64) []Run {
	var cells []Run
	cur := -1
	for _, r := range ln.runs {
		ci := colIndexOf(r, edges)
		if ci != cur {
			cells = append(cells, r)
			cur = ci
		} else if len(cells) > 0 {
			c := &cells[len(cells)-1]
			c.Text += r.Text
			c.X1 = maxF(c.X1, r.X1)
		}
	}
	return cells
}

func colIndexOf(r Run, edges []float64) int {
	mid := (r.X0 + r.X1) / 2
	for i := 0; i < len(edges)-1; i++ {
		if mid >= edges[i] && mid < edges[i+1] {
			return i
		}
	}
	return len(edges) - 1
}
