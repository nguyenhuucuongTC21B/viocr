package main

// rec.go — nhận dạng: VietOCR transformer (mặc định, vemines/deepdoc_vietocr
// scheme) và VietOCR seq2seq (deepdoc_vietocr). Preprocess đúng chuẩn
// pbcquoc/vietocr: RGB, cao 32px, width làm tròn lên bội 10, clamp [32,512],
// LANCZOS, /255 (không mean/std).

import (
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	recImgH  = 32
	recMinW  = 32
	recMaxW  = 512
	recRound = 10
	sosToken = 1
	eosToken = 2
)

// maxSeqLen số bước decode tối đa (đặt qua --max-len).
var maxSeqLen = 128

// RecPreprocess chuẩn bị tensor [1,3,32,W] từ crop.
func RecPreprocess(crop *RGB) ([]float32, int, int) {
	w := int(float64(recImgH) * float64(crop.W) / float64(crop.H))
	w = (w + recRound - 1) / recRound * recRound // làm tròn LÊN bội 10
	w = maxI(recMinW, minI(w, recMaxW))
	im := crop.ResizeLanczos(w, recImgH)
	n := recImgH * w
	out := make([]float32, 3*n)
	for i := 0; i < n; i++ {
		out[i] = float32(im.Data[i*3]) / 255
		out[n+i] = float32(im.Data[i*3+1]) / 255
		out[2*n+i] = float32(im.Data[i*3+2]) / 255
	}
	return out, w, w // (tensor, widthLog, 0)
}

// runAlloc chạy session và tự cấp phát output; trả về các tensor (caller Destroy).
func runAlloc(sess *ort.DynamicAdvancedSession, inputs []ort.Value, nOut int) ([]ort.Value, error) {
	outs := make([]ort.Value, nOut)
	if err := sess.Run(inputs, outs); err != nil {
		return nil, err
	}
	return outs, nil
}

func destroyValues(vs []ort.Value) {
	for _, v := range vs {
		if v != nil {
			v.Destroy()
		}
	}
}

func asTensor(v ort.Value) *ort.Tensor[float32] {
	return v.(*ort.Tensor[float32])
}

// RecTransformer nhận dạng 1 crop bằng engine transformer.
func (e *Engine) RecTransformer(crop *RGB) (string, float64, error) {
	x, w, _ := RecPreprocess(crop)
	memLen := int64(2 * (w / 4))
	inTensor, err := ort.NewTensor(ort.NewShape(1, 3, recImgH, int64(w)), x)
	if err != nil {
		return "", 0, err
	}
	defer inTensor.Destroy()
	encOuts, err := runAlloc(e.tEnc, []ort.Value{inTensor}, 1)
	if err != nil {
		return "", 0, err
	}
	defer destroyValues(encOuts)
	mem := asTensor(encOuts[0])
	if !mem.GetShape().Equals(ort.NewShape(memLen, 1, 256)) {
		// một số bản export dùng T khác — vẫn chấp nhận shape thật
		memLen = mem.GetShape()[0]
	}

	tgt := []int64{sosToken}
	var ids []int
	var probs []float64
	for step := 0; step < maxSeqLen; step++ {
		tgtTensor, err := ort.NewTensor(ort.NewShape(int64(len(tgt)), 1), tgt)
		if err != nil {
			return "", 0, err
		}
		decOuts, err := runAlloc(e.tDec, []ort.Value{tgtTensor, mem}, 1)
		tgtTensor.Destroy()
		if err != nil {
			return "", 0, err
		}
		lg := asTensor(decOuts[0])
		// output [1, T, 233] — lấy vị trí cuối
		sh := lg.GetShape()
		t := sh[1]
		v := 233
		lastPos := lg.GetData()[int(t-1)*v : int(t)*v]
		idx, p := argmaxSoftmax(lastPos)
		destroyValues(decOuts)
		tgt = append(tgt, int64(idx))
		ids = append(ids, idx)
		probs = append(probs, p)
		if idx == eosToken {
			break
		}
	}
	txt, conf := e.decode(ids, probs)
	return txt, conf, nil
}

// RecSeq2Seq nhận dạng 1 crop bằng engine seq2seq (deepdoc_vietocr).
func (e *Engine) RecSeq2Seq(crop *RGB) (string, float64, error) {
	x, w, _ := RecPreprocess(crop)
	t := int64(2 * (w / 4))
	inTensor, err := ort.NewTensor(ort.NewShape(1, 3, recImgH, int64(w)), x)
	if err != nil {
		return "", 0, err
	}
	defer inTensor.Destroy()
	cnnOuts, err := runAlloc(e.sCnn, []ort.Value{inTensor}, 1)
	if err != nil {
		return "", 0, err
	}
	cnn := asTensor(cnnOuts[0])
	if s := cnn.GetShape(); !s.Equals(ort.NewShape(t, 1, 256)) {
		t = s[0]
	}
	encOuts, err := runAlloc(e.sEnc, []ort.Value{cnn}, 2)
	destroyValues(cnnOuts)
	if err != nil {
		return "", 0, err
	}
	defer destroyValues(encOuts)
	mem := asTensor(encOuts[0])
	// copy hidden sang tensor riêng để tự quản lý vòng đời
	hSrc := asTensor(encOuts[1]).GetData()
	hidden, err := ort.NewTensor(ort.NewShape(1, 256), append([]float32(nil), hSrc...))
	if err != nil {
		return "", 0, err
	}
	defer hidden.Destroy()

	var ids []int
	var probs []float64
	last := int64(sosToken)
	for step := 0; step < maxSeqLen; step++ {
		tgtTensor, err := ort.NewTensor(ort.NewShape(1), []int64{last})
		if err != nil {
			return "", 0, err
		}
		decOuts, err := runAlloc(e.sDec, []ort.Value{tgtTensor, hidden, mem}, 3)
		tgtTensor.Destroy()
		if err != nil {
			return "", 0, err
		}
		lg := asTensor(decOuts[0])
		idx, p := argmaxSoftmax(lg.GetData())
		newHidden := asTensor(decOuts[1])
		hiddenNew, err := ort.NewTensor(ort.NewShape(1, 256), append([]float32(nil), newHidden.GetData()...))
		destroyValues(decOuts)
		if err != nil {
			return "", 0, err
		}
		hidden.Destroy()
		hidden = hiddenNew
		last = int64(idx)
		ids = append(ids, idx)
		probs = append(probs, p)
		if idx == eosToken {
			break
		}
	}
	txt, conf := e.decode(ids, probs)
	return txt, conf, nil
}

// decode chuyển token ids thành chuỗi + độ tin cậy trung bình (bỏ id <= 3).
func (e *Engine) decode(ids []int, probs []float64) (string, float64) {
	out := ""
	sum, n := 0.0, 0
	for i, id := range ids {
		if id == eosToken {
			break
		}
		if id > 3 {
			out += e.i2c(id)
			sum += probs[i]
			n++
		}
	}
	var conf float64
	if n > 0 {
		conf = sum / float64(n)
	}
	return out, conf
}

func argmaxSoftmax(v []float32) (int, float64) {
	best := 0
	bv := v[0]
	for i, x := range v {
		if x > bv {
			bv = x
			best = i
		}
	}
	var sum float64
	for _, x := range v {
		sum += math.Exp(float64(x - bv))
	}
	return best, 1.0 / sum // exp(max)/sum
}

// ---- helpers dung chung cho main ----

func newF32Tensor(shape []int64, data []float32) (*ort.Tensor[float32], error) {
	return ort.NewTensor(ort.Shape(shape), data)
}

func probMax(v []float32) float32 {
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func probMin(v []float32) float32 {
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func expNeg(v float32) float64 {
	x := -float64(v)
	if x > 30 {
		x = 30
	}
	if x < -30 {
		x = -30
	}
	r, term := 1.0, 1.0
	for i := 1; i <= 18; i++ {
		term *= x / float64(i)
		r += term
	}
	return r
}

func meanF(v []float32) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += float64(x)
	}
	return s / float64(len(v))
}
