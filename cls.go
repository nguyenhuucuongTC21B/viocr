package main

// cls.go — phân loại hướng 180° cho crop (PP-OCR cls mobile), tham chiếu
// RapidOCR cls: resize 48x192, normalize (x/255-0.5)/0.5, ngưỡng 0.9.

import (
	ort "github.com/yalue/onnxruntime_go"
)

const (
	clsThresh = 0.9
	clsH      = 48
	clsW      = 192
)

// ClsScore trả về xác suất crop bị lật 180°.
func (e *Engine) ClsScore(crop *RGB) (float64, error) {
	im := crop.ResizeBilinear(clsW, clsH)
	n := clsH * clsW
	x := make([]float32, 3*n)
	for i := 0; i < n; i++ {
		x[i] = (float32(im.Data[i*3])/255 - 0.5) / 0.5
		x[n+i] = (float32(im.Data[i*3+1])/255 - 0.5) / 0.5
		x[2*n+i] = (float32(im.Data[i*3+2])/255 - 0.5) / 0.5
	}
	t, err := ort.NewTensor(ort.NewShape(1, 3, clsH, clsW), x)
	if err != nil {
		return 0, err
	}
	defer t.Destroy()
	outs, err := runAlloc(e.cls, []ort.Value{t}, 1)
	if err != nil {
		return 0, err
	}
	defer destroyValues(outs)
	lg := asTensor(outs[0]).GetData()
	// softmax 2 lớp
	e0, e1 := mathExp2(float64(lg[0])), mathExp2(float64(lg[1]))
	s := e0 + e1
	return e1 / s, nil
}

func mathExp2(x float64) float64 {
	// exp ổn định
	if x > 30 {
		x = 30
	}
	if x < -30 {
		x = -30
	}
	var r, term float64 = 1, 1
	for i := 1; i <= 18; i++ {
		term *= x / float64(i)
		r += term
	}
	return r
}

// Rotate180 xoay crop 180°.
func Rotate180(img *RGB) *RGB {
	out := &RGB{W: img.W, H: img.H, Data: make([]uint8, len(img.Data))}
	n := img.W * img.H
	for i := 0; i < n; i++ {
		j := n - 1 - i
		out.Data[j*3] = img.Data[i*3]
		out.Data[j*3+1] = img.Data[i*3+1]
		out.Data[j*3+2] = img.Data[i*3+2]
	}
	return out
}

// Rotate90 xoay 90° theo chiều kim đồng hồ (k=1) hoặc ngược chiều (k=3).
func Rotate90(img *RGB, k int) *RGB {
	out := &RGB{W: img.H, H: img.W, Data: make([]uint8, len(img.Data))}
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			src := (y*img.W + x) * 3
			var dx, dy int
			if k == 1 { // CW: (x,y) -> (H-1-y, x)
				dx, dy = img.H-1-y, x
			} else { // CCW: (x,y) -> (y, W-1-x)
				dx, dy = y, img.W-1-x
			}
			dst := (dy*out.W + dx) * 3
			out.Data[dst] = img.Data[src]
			out.Data[dst+1] = img.Data[src+1]
			out.Data[dst+2] = img.Data[src+2]
		}
	}
	return out
}
