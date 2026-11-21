package main

// imageio.go — đọc ảnh (png/jpeg/gif/bmp), buffer RGB, resize bilinear
// (chuẩn cv2 INTER_LINEAR cho det) và Lanczos3 separable (chuẩn PIL cho rec).

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"

	"golang.org/x/image/bmp"
)

// RGB là buffer ảnh 3 kênh (mỗi pixel 3 byte, theo hàng).
type RGB struct {
	W, H int
	Data []uint8 // len = W*H*3
}

// LoadImage đọc file ảnh thành RGB.
func LoadImage(path string) (*RGB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadImageBytes(data)
}

// LoadImageBytes giải mã ảnh từ bộ nhớ (png/jpeg/bmp/gif), dùng cho CLI và GUI.
func LoadImageBytes(data []byte) (*RGB, error) {
	var img image.Image
	var err error
	try := func(fn func(io.Reader) (image.Image, error)) bool {
		img, err = fn(bytes.NewReader(data))
		return err == nil
	}
	if try(png.Decode) || try(jpeg.Decode) || try(bmp.Decode) || try(gif.Decode) {
		return imageToRGB(img), nil
	}
	return nil, err
}

func imageToRGB(img image.Image) *RGB {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := &RGB{W: w, H: h, Data: make([]uint8, w*h*3)}
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cr, cg, cb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out.Data[i] = uint8(cr >> 8)
			out.Data[i+1] = uint8(cg >> 8)
			out.Data[i+2] = uint8(cb >> 8)
			i += 3
		}
	}
	return out
}

func clampU8(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// ResizeBilinear resize theo chuẩn cv2.INTER_LINEAR (không align corners).
func (r *RGB) ResizeBilinear(nw, nh int) *RGB {
	out := &RGB{W: nw, H: nh, Data: make([]uint8, nw*nh*3)}
	sx := float64(r.W) / float64(nw)
	sy := float64(r.H) / float64(nh)
	for y := 0; y < nh; y++ {
		fy := (float64(y)+0.5)*sy - 0.5
		if fy < 0 {
			fy = 0
		}
		y0 := int(fy)
		dy := fy - float64(y0)
		y1 := y0 + 1
		if y1 > r.H-1 {
			y1 = r.H - 1
		}
		if y0 > r.H-1 {
			y0 = r.H - 1
		}
		for x := 0; x < nw; x++ {
			fx := (float64(x)+0.5)*sx - 0.5
			if fx < 0 {
				fx = 0
			}
			x0 := int(fx)
			dx := fx - float64(x0)
			x1 := x0 + 1
			if x1 > r.W-1 {
				x1 = r.W - 1
			}
			if x0 > r.W-1 {
				x0 = r.W - 1
			}
			for c := 0; c < 3; c++ {
				p00 := float64(r.Data[(y0*r.W+x0)*3+c])
				p01 := float64(r.Data[(y0*r.W+x1)*3+c])
				p10 := float64(r.Data[(y1*r.W+x0)*3+c])
				p11 := float64(r.Data[(y1*r.W+x1)*3+c])
				v := p00*(1-dx)*(1-dy) + p01*dx*(1-dy) + p10*(1-dx)*dy + p11*dx*dy
				out.Data[(y*nw+x)*3+c] = uint8(clampU8(v + 0.5))
			}
		}
	}
	return out
}

func lanczos3(x float64) float64 {
	if x == 0 {
		return 1
	}
	if x < 0 {
		x = -x
	}
	if x >= 3 {
		return 0
	}
	px := math.Pi * x
	return 3 * math.Sin(px) * math.Sin(px/3) / (px * px)
}

// ResizeLanczos resize separable 2-pass theo kiểu PIL Image.LANCZOS.
func (r *RGB) ResizeLanczos(nw, nh int) *RGB {
	tmp := resizeLanczosAxis(r, nw, true) // trục ngang
	return resizeLanczosAxis(tmp, nh, false)
}

// resizeLanczosAxis resize 1 trục; support mở rộng khi downscale (chuẩn PIL).
func resizeLanczosAxis(src *RGB, n int, horizontal bool) *RGB {
	var sw, dw int
	if horizontal {
		sw, dw = src.W, n
	} else {
		sw, dw = src.H, n
	}
	scale := float64(sw) / float64(dw) // >1: downscale
	denom := maxF(1.0, scale)
	support := 3.0 * denom

	var out *RGB
	if horizontal {
		out = &RGB{W: dw, H: src.H, Data: make([]uint8, dw*src.H*3)}
	} else {
		out = &RGB{W: src.W, H: dw, Data: make([]uint8, src.W*dw*3)}
	}

	for j := 0; j < dw; j++ { // j là vị trí trên trục đích
		center := (float64(j)+0.5)*scale - 0.5
		lo := int(math.Ceil(center - support))
		hi := int(math.Floor(center + support))
		if lo < 0 {
			lo = 0
		}
		if hi > sw-1 {
			hi = sw - 1
		}
		// precompute weights
		ws := make([]float64, 0, hi-lo+1)
		for k := lo; k <= hi; k++ {
			ws = append(ws, lanczos3((float64(k)-center)/denom))
		}
		wsum := 0.0
		for _, w := range ws {
			wsum += w
		}
		if wsum == 0 {
			wsum = 1
		}
		count := hi - lo + 1
		if horizontal {
			for y := 0; y < src.H; y++ {
				var acc [3]float64
				for idx := 0; idx < count; idx++ {
					si := lo + idx
					px := (y*sw + si) * 3
					w := ws[idx]
					acc[0] += w * float64(src.Data[px])
					acc[1] += w * float64(src.Data[px+1])
					acc[2] += w * float64(src.Data[px+2])
				}
				o := (y*dw + j) * 3
				out.Data[o] = uint8(clampU8(acc[0] / wsum))
				out.Data[o+1] = uint8(clampU8(acc[1] / wsum))
				out.Data[o+2] = uint8(clampU8(acc[2] / wsum))
			}
		} else {
			for x := 0; x < src.W; x++ {
				var acc [3]float64
				for idx := 0; idx < count; idx++ {
					sj := lo + idx
					px := (sj*src.W + x) * 3
					w := ws[idx]
					acc[0] += w * float64(src.Data[px])
					acc[1] += w * float64(src.Data[px+1])
					acc[2] += w * float64(src.Data[px+2])
				}
				o := (j*src.W + x) * 3
				out.Data[o] = uint8(clampU8(acc[0] / wsum))
				out.Data[o+1] = uint8(clampU8(acc[1] / wsum))
				out.Data[o+2] = uint8(clampU8(acc[2] / wsum))
			}
		}
	}
	return out
}
