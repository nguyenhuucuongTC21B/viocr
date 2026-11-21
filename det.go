package main

// det.go — tiền xử lý + hậu xử lý DBNet cho model detection PP-OCR
// (tham chiếu DetResizeForTest / DBPostProcess của PaddleOCR-DeepDoc).

import (
	"math"
	"sort"
)

// DetPreprocess resize + normalize theo chuẩn PaddleOCR det.
// Trả về tensor CHW float32 và kích thước đã resize.
func DetPreprocess(img *RGB, p *DetParams) ([]float32, int, int) {
	var ratio float64 = 1.0
	h, w := img.H, img.W
	switch p.LimitType {
	case "min":
		if minI(h, w) < p.LimitSide {
			ratio = float64(p.LimitSide) / float64(minI(h, w))
		}
	default: // "max"
		if maxI(h, w) > p.LimitSide {
			ratio = float64(p.LimitSide) / float64(maxI(h, w))
		}
	}
	nh := maxI(int(round(float64(h)*ratio/32))*32, 32)
	nw := maxI(int(round(float64(w)*ratio/32))*32, 32)
	resized := img.ResizeBilinear(nw, nh)
	// normalize ImageNet stats (chuẩn PaddleOCR det), CHW
	out := make([]float32, 3*nh*nw)
	mean := [3]float32{0.485, 0.456, 0.406}
	std := [3]float32{0.229, 0.224, 0.225}
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			i := (y*nw + x) * 3
			out[y*nw+x] = (float32(resized.Data[i])/255 - mean[0]) / std[0]
			out[nh*nw+y*nw+x] = (float32(resized.Data[i+1])/255 - mean[1]) / std[1]
			out[2*nh*nw+y*nw+x] = (float32(resized.Data[i+2])/255 - mean[2]) / std[2]
		}
	}
	return out, nh, nw
}

// Box là quadrilateral 4 điểm theo thứ tự: trên-trái, trên-phải, dưới-phải, dưới-trái.
type Box [4][2]int

// ScoredBox là box kèm điểm tin cậy detection.
type ScoredBox struct {
	Box   Box
	Score float64
}

// FindBoxes: prob map (đã sigmoid nếu cần) → danh sách box tọa độ ảnh gốc.
func FindBoxes(prob []float32, ph, pw, srcH, srcW int, p *DetParams) []ScoredBox {
	type comp struct {
		pix []int // các vị trí (y*pw+x)
	}
	bitmap := make([]bool, len(prob))
	for i, v := range prob {
		bitmap[i] = v > p.Thresh
	}
	// connected components 8-kề (BFS bằng stack)
	label := make([]int32, len(prob))
	var comps []comp
	stack := make([]int, 0, 1024)
	for start := 0; start < len(prob); start++ {
		if !bitmap[start] || label[start] != 0 {
			continue
		}
		id := int32(len(comps) + 1)
		var c comp
		stack = append(stack[:0], start)
		label[start] = id
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			c.pix = append(c.pix, cur)
			cy, cx := cur/pw, cur%pw
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					ny, nx := cy+dy, cx+dx
					if ny < 0 || ny >= ph || nx < 0 || nx >= pw {
						continue
					}
					ni := ny*pw + nx
					if bitmap[ni] && label[ni] == 0 {
						label[ni] = id
						stack = append(stack, ni)
					}
				}
			}
		}
		comps = append(comps, c)
	}

	var boxes []ScoredBox
	for _, c := range comps {
		if len(c.pix) < p.MinSize {
			continue
		}
		// points (x, y) cho hull
		pts := make([][2]float64, len(c.pix))
		for i, pi := range c.pix {
			pts[i] = [2]float64{float64(pi % pw), float64(pi / pw)}
		}
		rect := minAreaRect(pts)
		if rect == nil {
			continue
		}
		cx, cy, rw, rh, ang := rect[0], rect[1], rect[2], rect[3], rect[4]
		if minF(rw, rh) < float64(p.MinSize) {
			continue
		}
		// score = mean prob trên pixel của component
		var sum float64
		for _, pi := range c.pix {
			sum += float64(prob[pi])
		}
		score := sum / float64(len(c.pix))
		if score < float64(p.BoxThresh) {
			continue
		}
		// unclip: dist = area * unclip_ratio / perimeter (chuẩn DBNet)
		q := rectCorners(cx, cy, rw, rh, ang)
		area, per := quadAreaPerim(q)
		dist := area * p.Unclip / math.Max(per, 1e-6)
		q2 := rectCorners(cx, cy, rw+2*dist, rh+2*dist, ang)
		if minF(rect[2]+2*dist, rect[3]+2*dist) < float64(p.MinSize+2) {
			continue
		}
		// scale về ảnh gốc + clip
		box := Box{}
		for i, pt := range q2 {
			x := clampI(int(round(pt[0]/float64(pw)*float64(srcW))), 0, srcW)
			y := clampI(int(round(pt[1]/float64(ph)*float64(srcH))), 0, srcH)
			box[i] = [2]int{x, y}
		}
		box = orderPointsClockwise(box)
		if boxWidth(box) <= 3 || boxHeight(box) <= 3 {
			continue
		}
		boxes = append(boxes, ScoredBox{Box: box, Score: score})
	}
	return boxes
}

func boxWidth(b Box) int {
	return maxI(absI(b[1][0]-b[0][0]), absI(b[2][0]-b[3][0]))
}

func boxHeight(b Box) int {
	return maxI(absI(b[3][1]-b[0][1]), absI(b[2][1]-b[1][1]))
}

func orderPointsClockwise(b Box) Box {
	sum := func(i int) int { return b[i][0] + b[i][1] }
	diff := func(i int) int { return b[i][1] - b[i][0] } // y - x (như np.diff)
	tl, br := 0, 0
	for i := 1; i < 4; i++ {
		if sum(i) < sum(tl) {
			tl = i
		}
		if sum(i) > sum(br) {
			br = i
		}
	}
	tr, bl := -1, -1
	for i := 0; i < 4; i++ {
		if i != tl && i != br {
			if tr == -1 || diff(i) < diff(tr) {
				tr = i
			}
			if bl == -1 || diff(i) > diff(bl) {
				bl = i
			}
		}
	}
	return Box{b[tl], b[tr], b[br], b[bl]}
}

// minAreaRect: convex hull + rotating calipers → [cx, cy, w, h, angle].
func minAreaRect(pts [][2]float64) []float64 {
	hull := convexHull(pts)
	n := len(hull)
	if n == 0 {
		return nil
	}
	if n < 3 {
		x0, y0 := hull[0][0], hull[0][1]
		x1, y1 := hull[n-1][0], hull[n-1][1]
		return []float64{(x0 + x1) / 2, (y0 + y1) / 2, absF(x1-x0) + 1, absF(y1-y0) + 1, 0}
	}
	best := -1.0
	var out []float64
	for i := 0; i < n; i++ {
		a, b := hull[i], hull[(i+1)%n]
		dx, dy := b[0]-a[0], b[1]-a[1]
		L := math.Hypot(dx, dy) + 1e-12
		ux, uy := dx/L, dy/L
		var pmin, pmax, qmin, qmax float64
		for _, p := range hull {
			px := (p[0]-a[0])*ux + (p[1]-a[1])*uy
			py := (p[0]-a[0])*(-uy) + (p[1]-a[1])*ux
			pmin = math.Min(pmin, px)
			pmax = math.Max(pmax, px)
			qmin = math.Min(qmin, py)
			qmax = math.Max(qmax, py)
		}
		w, h := pmax-pmin, qmax-qmin
		if best < 0 || w*h < best {
			best = w * h
			cx := a[0] + ux*(pmin+pmax)/2 - uy*(qmin+qmax)/2
			cy := a[1] + uy*(pmin+pmax)/2 + ux*(qmin+qmax)/2
			ang := math.Atan2(dy, dx)
			if w < h {
				w, h = h, w
				ang += math.Pi / 2
			}
			out = []float64{cx, cy, w, h, ang}
		}
	}
	return out
}

func convexHull(pts [][2]float64) [][2]float64 {
	// unique + sort theo (x, y)
	seen := make(map[[2]float64]bool)
	var u [][2]float64
	for _, p := range pts {
		q := [2]float64{math.Round(p[0]), math.Round(p[1])}
		if !seen[q] {
			seen[q] = true
			u = append(u, q)
		}
	}
	if len(u) < 2 {
		return u
	}
	sortPts(u)
	cross := func(o, a, b [2]float64) float64 {
		return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0])
	}
	var lower, upper [][2]float64
	for _, p := range u {
		for len(lower) >= 2 && cross(lower[len(lower)-2], lower[len(lower)-1], p) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, p)
	}
	for i := len(u) - 1; i >= 0; i-- {
		p := u[i]
		for len(upper) >= 2 && cross(upper[len(upper)-2], upper[len(upper)-1], p) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, p)
	}
	lower = lower[:len(lower)-1]
	upper = upper[:len(upper)-1]
	return append(lower, upper...)
}

func sortPts(p [][2]float64) {
	sort.Slice(p, func(i, j int) bool { return lessPt(p[i], p[j]) })
}

func lessPt(a, b [2]float64) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	return a[1] < b[1]
}

func rectCorners(cx, cy, w, h, ang float64) [][2]float64 {
	ca, sa := math.Cos(ang), math.Sin(ang)
	hw, hh := w/2, h/2
	deltas := [][2]float64{{hw, hh}, {-hw, hh}, {-hw, -hh}, {hw, -hh}}
	out := make([][2]float64, 4)
	for i, d := range deltas {
		out[i] = [2]float64{cx + d[0]*ca - d[1]*sa, cy + d[0]*sa + d[1]*ca}
	}
	return out
}

func quadAreaPerim(q [][2]float64) (float64, float64) {
	var area, per float64
	for i := 0; i < 4; i++ {
		x1, y1 := q[i][0], q[i][1]
		x2, y2 := q[(i+1)%4][0], q[(i+1)%4][1]
		area += x1*y2 - x2*y1
		per += math.Hypot(x2-x1, y2-y1)
	}
	return math.Abs(area) / 2, per
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func absF(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}

func absI(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func round(v float64) float64 {
	return math.Floor(v + 0.5)
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
