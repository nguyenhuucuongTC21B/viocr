package main

// main.go — viocr: OCR tiếng Việt chạy offline, nhúng toàn bộ model trong 1
// file binary. Pipeline: PP-OCRv5 det → DBNet postprocess → crop phối cảnh →
// cls 180° → VietOCR (transformer mặc định / seq2seq) → ghép dòng.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

type imageResult struct {
	File    string       `json:"file"`
	Width   int          `json:"width"`
	Height  int          `json:"height"`
	Lines   []string     `json:"lines"`
	Results []LineResult `json:"results"`
	Ms      float64      `json:"ms"`
}

func main() {
	// Bắt panic để hiện thông báo + chờ Enter (console nhấp đúp sẽ không
	// đóng ngay làm mất thông báo lỗi).
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "viocr: lỗi nghiêm trọng:", r)
			pauseIfInteractive()
			os.Exit(1)
		}
	}()

	var (
		flagJSON      = flag.Bool("json", false, "xuất kết quả dạng JSON (mỗi ảnh 1 dòng)")
		flagEngine    = flag.String("rec-engine", "seq2seq", "engine nhận dạng: seq2seq (mặc định) | transformer")
		flagDetSide   = flag.Int("det-side", 960, "cận resize det (pixel)")
		flagDetType   = flag.String("det-type", "max", "kiểu cận det: max | min")
		flagDetThresh = flag.Float64("det-thresh", 0.3, "ngưỡng nhị phân DBNet")
		flagBoxThresh = flag.Float64("box-thresh", 0.5, "ngưỡng điểm box")
		flagUnclip    = flag.Float64("unclip", 1.6, "hệ số mở rộng box (unclip)")
		flagMinSize   = flag.Int("min-size", 3, "kích thước box nhỏ nhất (pixel)")
		flagMaxLen    = flag.Int("max-len", 128, "số bước decode tối đa")
		flagRecMaxW   = flag.Int("rec-max-w", 512, "cận rộng tensor rec (pixel — tăng cho dòng dài)")
		flagNoCls     = flag.Bool("no-cls", false, "tắt phân loại hướng 180°")
		flagThreads   = flag.Int("threads", 0, "số thread ONNX Runtime (0 = mặc định)")
		flagQuiet     = flag.Bool("quiet", false, "không in log ra stderr")
		flagVersion   = flag.Bool("version", false, "in phiên bản")
		flagGUI       = flag.Bool("gui", false, "mở giao diện đồ họa (kể cả khi có ảnh)")
		flagPort      = flag.Int("port", 0, "cổng server giao diện (0 = chọn cổng tự do)")
		flagNoBrowser = flag.Bool("no-browser", false, "không tự mở trình duyệt")
		// ── v3.0: đường Text + xuất Word + bench ──
		flagPDFText     = flag.String("pdf-text", "", "trích LỚP CHỮ của file PDF (không OCR, không cần model): in văn bản ra stdout")
		flagDocx        = flag.String("docx", "", "xuất file Word .docx từ kết quả (đi cùng --pdf-text hoặc ảnh/PDF đầu vào)")
		flagLDMJSON     = flag.String("json-ldm", "", "ghi Logical Document Model (JSON) ra file")
		flagBench       = flag.String("bench", "", "chạy bench trên thư mục có gold/ + out/ (in CER/WER/TS/LFS)")
		flagNoCorrect   = flag.Bool("no-correct", false, "tắt hiệu chỉnh tiếng Việt (M4)")
		flagNoPageBreak = flag.Bool("no-pagebreak", false, "không chèn ngắt trang trong file Word")
	)
	flag.Parse()
	if *flagVersion {
		fmt.Println("viocr", appVersion)
		return
	}
	doCorrect := !*flagNoCorrect
	pageBreaks := !*flagNoPageBreak
	// ── v3.0: bench không cần model ──
	if *flagBench != "" {
		if err := runBenchDir(*flagBench); err != nil {
			fmt.Fprintln(os.Stderr, "viocr:", err)
			os.Exit(2)
		}
		return
	}
	// ── v3.0: đường Text thuần (--pdf-text) — không cần model ──
	if *flagPDFText != "" {
		os.Exit(runPDFTextCLI(*flagPDFText, *flagDocx, *flagLDMJSON, doCorrect, pageBreaks, *flagQuiet))
	}
	if *flagGUI || len(flag.Args()) == 0 {
		// Chế độ giao diện đồ họa: server cục bộ + trình duyệt, chạy tới Ctrl+C.
		if err := runGUI(*flagPort, !*flagNoBrowser); err != nil {
			fmt.Fprintln(os.Stderr, "viocr: không mở được giao diện:", err)
			pauseIfInteractive()
			os.Exit(1)
		}
		return
	}
	files := flag.Args()
	if *flagEngine != "transformer" && *flagEngine != "seq2seq" {
		fmt.Fprintln(os.Stderr, "--rec-engine phải là transformer hoặc seq2seq")
		pauseIfInteractive()
		os.Exit(2)
	}
	maxSeqLen = *flagMaxLen
	recMaxW = *flagRecMaxW

	assetDir, err := InitRuntime()
	if err != nil {
		fatal(err)
	}
	engine, err := NewEngine(assetDir, *flagThreads, *flagQuiet)
	if err != nil {
		fatal(err)
	}
	defer engine.Destroy()

	detP := &DetParams{
		LimitSide: *flagDetSide,
		LimitType: *flagDetType,
		Thresh:    float32(*flagDetThresh),
		BoxThresh: float32(*flagBoxThresh),
		Unclip:    *flagUnclip,
		MinSize:   *flagMinSize,
	}

	exitCode := 0
	var ldmPages []*imageResult
	var ldmNames []string
	for i, path := range files {
		// ── v3.0: file PDF truyền thẳng vào CLI → đường Text (không OCR) ──
		if strings.EqualFold(filepath.Ext(path), ".pdf") {
			pdfData, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "viocr: lỗi %s: %v\n", path, err)
				exitCode = 1
				continue
			}
			doc, err := buildLDMFromPDFBytes(filepath.Base(path), pdfData, doCorrect)
			if err != nil {
				fmt.Fprintf(os.Stderr, "viocr: lỗi %s: %v\n", path, err)
				exitCode = 1
				continue
			}
			if *flagDocx != "" {
				if err := WriteDocx(doc, *flagDocx, pageBreaks); err != nil {
					fmt.Fprintf(os.Stderr, "viocr: lỗi xuất Word %s: %v\n", *flagDocx, err)
					exitCode = 1
				} else if !*flagQuiet {
					fmt.Fprintf(os.Stderr, "viocr: đã xuất %s (%d trang, %d chỗ hiệu chỉnh)\n",
						*flagDocx, len(doc.Pages), len(doc.Corrections))
				}
			}
			if *flagLDMJSON != "" {
				if err := doc.SaveJSON(*flagLDMJSON); err != nil {
					fmt.Fprintf(os.Stderr, "viocr: lỗi ghi LDM %s: %v\n", *flagLDMJSON, err)
					exitCode = 1
				}
			}
			for _, p := range doc.Pages {
				if p.NeedsOCR && !*flagQuiet {
					fmt.Fprintf(os.Stderr, "viocr: trang %d của %s là scan/chữ giả — dùng GUI để OCR trang này\n", p.Index, filepath.Base(path))
				}
			}
			if !*flagJSON {
				fmt.Print(doc.PlainText())
			}
			continue
		}
		img, err := LoadImage(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "viocr: lỗi %s: %v\n", path, err)
			exitCode = 1
			continue
		}
		res, err := processImage(engine, detP, img, filepath.Base(path), *flagEngine, !*flagNoCls)
		if err != nil {
			fmt.Fprintf(os.Stderr, "viocr: lỗi %s: %v\n", path, err)
			exitCode = 1
			continue
		}
		ldmPages = append(ldmPages, res)
		ldmNames = append(ldmNames, filepath.Base(path))
		if *flagJSON {
			b, _ := json.Marshal(res)
			fmt.Println(string(b))
		} else {
			for _, ln := range res.Lines {
				fmt.Println(ln)
			}
			if i < len(files)-1 {
				fmt.Println()
			}
		}
	}
	// ── v3.0: xuất Word từ kết quả OCR ảnh ──
	if *flagDocx != "" && len(ldmPages) > 0 {
		doc := buildLDMFromImageResults(strings.Join(ldmNames, ", "), ldmPages, doCorrect)
		if err := WriteDocx(doc, *flagDocx, pageBreaks); err != nil {
			fmt.Fprintf(os.Stderr, "viocr: lỗi xuất Word %s: %v\n", *flagDocx, err)
			exitCode = 1
		} else if !*flagQuiet {
			fmt.Fprintf(os.Stderr, "viocr: đã xuất %s (%d trang, %d chỗ hiệu chỉnh)\n",
				*flagDocx, len(doc.Pages), len(doc.Corrections))
		}
		if *flagLDMJSON != "" {
			if err := doc.SaveJSON(*flagLDMJSON); err != nil {
				fmt.Fprintf(os.Stderr, "viocr: lỗi ghi LDM %s: %v\n", *flagLDMJSON, err)
				exitCode = 1
			}
		}
	}
	os.Exit(exitCode)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "viocr:", err)
	pauseIfInteractive()
	os.Exit(1)
}

// pauseIfInteractive chờ Enter khi đang chạy trong console tương tác (nhấp đúp
// exe trên Windows) để người dùng kịp đọc thông báo trước khi cửa sổ đóng.
// Khi stdin bị pipe/chuyển hướng (script) thì thoát ngay, không pause.
func pauseIfInteractive() {
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "Nhấn Enter để thoát...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// processImage chạy trọn pipeline cho 1 ảnh (img đã nạp sẵn; name chỉ để hiển thị).
func processImage(e *Engine, p *DetParams, img *RGB, name, engineName string, useCls bool) (*imageResult, error) {
	start := time.Now()
	res := &imageResult{File: name, Width: img.W, Height: img.H, Lines: []string{}, Results: []LineResult{}}

	// 1) detection
	x, nh, nw := DetPreprocess(img, p)
	inTensor, err := newF32Tensor([]int64{1, 3, int64(nh), int64(nw)}, x)
	if err != nil {
		return nil, err
	}
	defer inTensor.Destroy()
	detOuts, err := runAlloc(e.det, []ort.Value{inTensor}, 1)
	if err != nil {
		return nil, err
	}
	defer destroyValues(detOuts)
	raw := asTensor(detOuts[0]).GetData()
	prob := make([]float32, len(raw))
	copy(prob, raw)
	if probMax(prob) > 1.0 || probMin(prob) < 0.0 {
		// model xuất logits → sigmoid (một số bản export đã sigmoid sẵn)
		for i, v := range prob {
			prob[i] = float32(1 / (1 + expNeg(v)))
		}
	}
	boxes := FindBoxes(prob, nh, nw, img.H, img.W, p)
	if os.Getenv("VIOCR_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[dbg] det in=%dx%d outLen=%d min=%.4f max=%.4f mean=%.4f boxes=%d\n",
			nw, nh, len(prob), probMin(prob), probMax(prob), meanF(prob), len(boxes))
	}

	// 2) nhận dạng từng box + ghép dòng
	order := GroupLines(boxes)
	for _, lineIdx := range order {
		lineRes := make([]LineResult, 0, len(lineIdx))
		for _, bi := range lineIdx {
			sb := boxes[bi]
			crop := CropQuad(img, sb.Box)
			if crop == nil {
				continue
			}
			text, conf, err2 := e.recBest(crop, engineName, useCls)
			if err2 != nil {
				return nil, err2
			}
			lineRes = append(lineRes, LineResult{Box: sb.Box, DetScore: sb.Score, Text: text, Conf: conf})
		}
		if len(lineRes) == 0 {
			continue
		}
		res.Results = append(res.Results, lineRes...)
		res.Lines = append(res.Lines, JoinLine(lineRes))
	}
	res.Ms = float64(time.Since(start).Milliseconds())
	return res, nil
}

// recBest nhận dạng 1 crop: crop dọc (h/w >= 1.5) thử 3 hướng như DeepDoc;
// crop ngang áp cls 180° (nếu bật).
func (e *Engine) recBest(crop *RGB, engineName string, useCls bool) (string, float64, error) {
	recOne := func(c *RGB) (string, float64, error) {
		if engineName == "seq2seq" {
			return e.RecSeq2Seq(c)
		}
		return e.RecTransformer(c)
	}
	if float64(crop.H)/float64(maxI(crop.W, 1)) >= 1.5 {
		bestTxt, bestConf := "", 0.0
		for _, c := range []*RGB{crop, Rotate90(crop, 3), Rotate90(crop, 1)} {
			t, cf, err := recOne(c)
			if err != nil {
				return "", 0, err
			}
			if cf > bestConf {
				bestTxt, bestConf = t, cf
			}
		}
		return bestTxt, bestConf, nil
	}
	if useCls {
		p180, err := e.ClsScore(crop)
		if err != nil {
			return "", 0, err
		}
		if p180 > clsThresh {
			crop = Rotate180(crop)
		}
	}
	txt, cf, err := recOne(crop)
	if err != nil || cf >= 0.6 {
		return txt, cf, err
	}
	// conf thấp → thử lại với crop tăng tương phản (scan nhạt, JPEG nén)
	txt2, cf2, err2 := recOne(normalizeContrast(crop))
	if err2 == nil && cf2 > cf {
		return txt2, cf2, nil
	}
	return txt, cf, err
}
