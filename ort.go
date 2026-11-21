package main

// ort.go — khởi tạo ONNX Runtime (giải nén lib từ binary), quản lý session
// và các hàm chạy det/cls/rec. Tham chiếu pipeline RapidOCR / DeepDoc.

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	ort "github.com/yalue/onnxruntime_go"
)

// DetParams tham số postprocess DBNet (mặc định theo RapidOCR/DeepDoc).
type DetParams struct {
	LimitSide int
	LimitType string // "max" | "min"
	Thresh    float32
	BoxThresh float32
	Unclip    float64
	MinSize   int
}

// Engine giữ các session + vocab.
type Engine struct {
	det   *ort.DynamicAdvancedSession
	cls   *ort.DynamicAdvancedSession
	tEnc  *ort.DynamicAdvancedSession // VietOCR transformer: CNN + encoder
	tDec  *ort.DynamicAdvancedSession // VietOCR transformer: decoder full-prefix
	sCnn  *ort.DynamicAdvancedSession // VietOCR seq2seq: CNN
	sEnc  *ort.DynamicAdvancedSession // seq2seq encoder (bi-LSTM)
	sDec  *ort.DynamicAdvancedSession // seq2seq decoder step (token + hidden)
	vocab []string                    // 233 dòng: 4 token đặc biệt + 229 ký tự
}

// InitRuntime giải nén asset và khởi tạo ONNX Runtime.
func InitRuntime() (assetDir string, err error) {
	assetDir, err = extractAssets()
	if err != nil {
		return "", err
	}
	lib := assetDir + "/" + runtimeLibName()
	if _, err := os.Stat(lib); err != nil {
		return "", fmt.Errorf("không tìm thấy libonnxruntime: %w", err)
	}
	ort.SetSharedLibraryPath(lib)
	if err := ort.InitializeEnvironment(); err != nil {
		err = fmt.Errorf("khởi tạo ONNX Runtime: %w", err)
		if runtime.GOOS == "windows" {
			// onnxruntime.dll phụ thuộc VC++ Redistributable (MSVCP140/VCRUNTIME140)
			// — máy thiếu sẽ LoadLibrary thất bại ngay khi khởi động.
			return "", fmt.Errorf("%w\n  Trên Windows hãy cài Microsoft Visual C++ Redistributable (bản x64):\n  https://aka.ms/vs/17/release/vc_redist.x64.exe  rồi chạy lại.", err)
		}
		return "", err
	}
	return assetDir, nil
}

// NewEngine tạo các session từ model trong assetDir.
func NewEngine(assetDir string, threads int, quiet bool) (*Engine, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	if threads > 0 {
		if err := opts.SetIntraOpNumThreads(threads); err != nil {
			return nil, err
		}
		if err := opts.SetInterOpNumThreads(threads); err != nil {
			return nil, err
		}
	}
	mk := func(model string, ins, outs []string) (*ort.DynamicAdvancedSession, error) {
		return ort.NewDynamicAdvancedSession(assetDir+"/"+model, ins, outs, opts)
	}
	e := &Engine{}
	if e.det, err = mk("det.onnx", []string{"x"}, []string{"fetch_name_0"}); err != nil {
		return nil, err
	}
	if e.cls, err = mk("cls.onnx", []string{"x"}, []string{"save_infer_model/scale_0.tmp_1"}); err != nil {
		return nil, err
	}
	if e.tEnc, err = mk("rec_t_enc.onnx", []string{"img"}, []string{"memory"}); err != nil {
		return nil, err
	}
	if e.tDec, err = mk("rec_t_dec.onnx", []string{"tgt", "memory"}, []string{"output"}); err != nil {
		return nil, err
	}
	if e.sCnn, err = mk("rec_s_cnn.onnx", []string{"img"}, []string{"output"}); err != nil {
		return nil, err
	}
	if e.sEnc, err = mk("rec_s_enc.onnx", []string{"src"}, []string{"encoder_outputs", "hidden"}); err != nil {
		return nil, err
	}
	if e.sDec, err = mk("rec_s_dec.onnx", []string{"tgt", "hidden", "encoder_outputs"},
		[]string{"output", "hidden_out", "last"}); err != nil {
		return nil, err
	}
	vb, err := os.ReadFile(assetDir + "/vocab.txt")
	if err != nil {
		return nil, err
	}
	e.vocab = strings.Split(strings.TrimRight(string(vb), "\n"), "\n")
	if len(e.vocab) != 233 {
		return nil, fmt.Errorf("vocab không đúng 233 ký tự: %d", len(e.vocab))
	}
	if !quiet {
		ver := ort.GetVersion()
		fmt.Fprintf(os.Stderr, "viocr %s — ONNX Runtime %s, model đã nhúng, chế độ offline\n", appVersion, ver)
	}
	return e, nil
}

// Destroy giải phóng session.
func (e *Engine) Destroy() {
	for _, s := range []*ort.DynamicAdvancedSession{e.det, e.cls, e.tEnc, e.tDec, e.sCnn, e.sEnc, e.sDec} {
		if s != nil {
			s.Destroy()
		}
	}
}

// i2c chuyển token id thành ký tự (bỏ 4 token đặc biệt <pad><sos><eos><mask>).
func (e *Engine) i2c(id int) string {
	if id <= 3 || id >= len(e.vocab) {
		return ""
	}
	return e.vocab[id]
}
