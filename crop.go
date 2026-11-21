package main

// crop.go — cắt vùng text từ quadrilateral 4 điểm bằng perspective transform
// (tương đương cv2.getPerspectiveTransform + warpPerspective của DeepDoc),
// lấy mẫu bilinear với biên clamp (BORDER_REPLICATE).

import "math"

// CropQuad cắt và hiệu chỉnh phối cảnh 4 điểm box thành ảnh w×h.
func CropQuad(img *RGB, box Box) *RGB {
	pts := make([][2]float64, 4)
	for i := 0; i < 4; i++ {
		pts[i] = [2]float64{float64(box[i][0]), float64(box[i][1])}
	}
	w := maxI(dist2(pts[0], pts[1]), dist2(pts[2], pts[3]))
	h := maxI(dist2(pts[0], pts[3]), dist2(pts[1], pts[2]))
	if w < 2 || h < 2 {
		return nil
	}
	dst := [][2]float64{{0, 0}, {float64(w), 0}, {float64(w), float64(h)}, {0, float64(h)}}
	// giải hệ 8 ẩn cho homography: dst -> src
	hm := solveHomography(dst, pts)
	if hm == nil {
		return nil
	}
	out := &RGB{W: w, H: h, Data: make([]uint8, w*h*3)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx, sy, sw := applyH(hm, float64(x)+0.5, float64(y)+0.5)
			if sw <= 0 {
				continue
			}
			sxi := int(math.Floor(sx))
			syi := int(math.Floor(sy))
			fx := sx - math.Floor(sx)
			fy := sy - math.Floor(sy)
			for c := 0; c < 3; c++ {
				p00 := img.atClamped(sxi, syi, c)
				p01 := img.atClamped(sxi+1, syi, c)
				p10 := img.atClamped(sxi, syi+1, c)
				p11 := img.atClamped(sxi+1, syi+1, c)
				v := float64(p00)*(1-fx)*(1-fy) + float64(p01)*fx*(1-fy) +
					float64(p10)*(1-fx)*fy + float64(p11)*fx*fy
				out.Data[(y*w+x)*3+c] = uint8(clampU8(v + 0.5))
			}
		}
	}
	return out
}

func dist2(a, b [2]float64) int {
	return int(round(math.Hypot(b[0]-a[0], b[1]-a[1])))
}

func (r *RGB) atClamped(x, y, c int) uint8 {
	x = clampI(x, 0, r.W-1)
	y = clampI(y, 0, r.H-1)
	return r.Data[(y*r.W+x)*3+c]
}

// applyH áp homography h (8 phần tử) trả về (x', y', w').
func applyH(h []float64, x, y float64) (float64, float64, float64) {
	den := h[6]*x + h[7]*y + 1
	hx := (h[0]*x + h[1]*y + h[2]) / den
	hy := (h[3]*x + h[4]*y + h[5]) / den
	return hx, hy, den
}

// solveHomography tìm H sao cho H·dst = src (8 ẩn, bỏ h22=1).
func solveHomography(dst, src [][2]float64) []float64 {
	// H: x' = (a x + b y + c)/(g x + h y + 1); y' = (d x + e y + f)/(g x + h y + 1)
	var A [8][9]float64
	for i := 0; i < 4; i++ {
		x, y := dst[i][0], dst[i][1]
		X, Y := src[i][0], src[i][1]
		A[2*i] = [9]float64{x, y, 1, 0, 0, 0, -X * x, -X * y, X}
		A[2*i+1] = [9]float64{0, 0, 0, x, y, 1, -Y * x, -Y * y, Y}
	}
	// khử Gauss
	for col := 0; col < 8; col++ {
		piv := -1
		for r := col; r < 8; r++ {
			if math.Abs(A[r][col]) > 1e-8 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return nil
		}
		A[col], A[piv] = A[piv], A[col]
		pv := A[col][col]
		for j := col; j < 9; j++ {
			A[col][j] /= pv
		}
		for r := 0; r < 8; r++ {
			if r == col {
				continue
			}
			f := A[r][col]
			if f == 0 {
				continue
			}
			for j := col; j < 9; j++ {
				A[r][j] -= f * A[col][j]
			}
		}
	}
	return []float64{A[0][8], A[1][8], A[2][8], A[3][8], A[4][8], A[5][8], A[6][8], A[7][8]}
}
