package main

// webui.go — giao diện đồ họa kiểu "web cục bộ": exe nhúng sẵn trang HTML
// (ui/index.html), mở server tại 127.0.0.1 và tự động mở trình duyệt.
// Không cần cài gì thêm, không cần mạng — mọi thứ chạy offline trong 1 file.

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui
var uiFS embed.FS

// pdfMu — serialize đường Text (CPU) để hai request PDF không chạy song song.
var pdfMu sync.Mutex

type guiState struct {
	mu     sync.Mutex
	engine *Engine
	ready  bool
	err    string
	busy   bool
}

var gui guiState

// runGUI khởi động server cục bộ và mở trình duyệt. Chặn cho tới khi Ctrl+C.
func runGUI(port int, openBrowser bool) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)

	mux := http.NewServeMux()
	// pdf.js + worker nhúng trong exe (tính năng đọc PDF render phía trình duyệt)
	vendorFS, _ := fs.Sub(uiFS, "ui/vendor")
	mux.Handle("GET /vendor/", http.StripPrefix("/vendor/", http.FileServer(http.FS(vendorFS))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := uiFS.ReadFile("ui/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		gui.mu.Lock()
		defer gui.mu.Unlock()
		writeJSON(w, map[string]any{
			"ready": gui.ready, "error": gui.err, "version": appVersion,
			"addr": addr, "engine": "PP-OCRv5 + VietOCR",
		})
	})
	mux.HandleFunc("POST /api/ocr", func(w http.ResponseWriter, r *http.Request) {
		gui.mu.Lock()
		ready, busy := gui.ready, gui.busy
		if ready {
			gui.busy = true
		}
		gui.mu.Unlock()
		if !ready {
			http.Error(w, `{"error":"Model chưa nạp xong — thử lại sau vài giây."}`, http.StatusServiceUnavailable)
			return
		}
		if busy {
			http.Error(w, `{"error":"Đang xử lý ảnh khác — chờ xong rồi thử lại."}`, http.StatusConflict)
			return
		}
		defer func() { gui.mu.Lock(); gui.busy = false; gui.mu.Unlock() }()

		r.Body = http.MaxBytesReader(w, r.Body, 30<<20)
		if err := r.ParseMultipartForm(30 << 20); err != nil {
			http.Error(w, `{"error":"Ảnh tải lên quá lớn (tối đa 30 MB) hoặc sai định dạng."}`, http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":"Thiếu file ảnh trong yêu cầu."}`, http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, `{"error":"Không đọc được dữ liệu ảnh."}`, http.StatusBadRequest)
			return
		}
		img, err := LoadImageBytes(data)
		if err != nil {
			http.Error(w, `{"error":"Không giải mã được ảnh. Hỗ trợ: PNG, JPG, BMP, GIF."}`, http.StatusBadRequest)
			return
		}
		engineName := r.FormValue("engine")
		if engineName != "transformer" && engineName != "seq2seq" {
			engineName = "transformer"
		}
		useCls := r.FormValue("cls") != "0"

		detSide := 960
		if v := r.FormValue("det"); v != "" {
			if n, e2 := strconv.Atoi(v); e2 == nil && n >= 320 && n <= 3000 {
				detSide = n
			}
		}
		detP := &DetParams{LimitSide: detSide, LimitType: "max", Thresh: 0.3, BoxThresh: 0.5, Unclip: 1.6, MinSize: 3}
		gui.mu.Lock()
		e := gui.engine
		gui.mu.Unlock()
		res, err := processImage(e, detP, img, filepath.Base(hdr.Filename), engineName, useCls)
		if err != nil {
			msg, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("%v", err)})
			http.Error(w, string(msg), http.StatusInternalServerError)
			return
		}
		res.File = filepath.Base(hdr.Filename)
		writeJSON(w, res)
	})

	// ── v3.0: trích lớp chữ PDF (đường Text) — không cần model, không OCR ──
	mux.HandleFunc("POST /api/pdf-text", func(w http.ResponseWriter, r *http.Request) {
		pdfMu.Lock()
		defer pdfMu.Unlock()
		r.Body = http.MaxBytesReader(w, r.Body, 60<<20)
		if err := r.ParseMultipartForm(60 << 20); err != nil {
			http.Error(w, `{"error":"PDF tải lên quá lớn (tối đa 60 MB) hoặc sai định dạng."}`, http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":"Thiếu file PDF."}`, http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, `{"error":"Không đọc được file PDF."}`, http.StatusBadRequest)
			return
		}
		doCorrect := r.FormValue("no_correct") != "1"
		doc, err := buildLDMFromPDFBytes(filepath.Base(hdr.Filename), data, doCorrect)
		if err != nil {
			msg, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("%v", err)})
			http.Error(w, string(msg), http.StatusBadRequest)
			return
		}
		type pageOut struct {
			I        int    `json:"i"`
			Path     string `json:"path"`
			NeedsOCR bool   `json:"needs_ocr"`
			Text     string `json:"text"`
		}
		var pages []pageOut
		for _, p := range doc.Pages {
			var b strings.Builder
			for _, blk := range p.Blocks {
				b.WriteString(blockText(blk))
				b.WriteString("\n")
			}
			pages = append(pages, pageOut{I: p.Index, Path: p.Path, NeedsOCR: p.NeedsOCR, Text: strings.TrimSpace(b.String())})
		}
		writeJSON(w, map[string]any{
			"ms": doc.MsText, "pages": pages,
			"corrections": doc.Corrections, "ldm": doc,
			"source": doc.Source,
		})
	})

	// ── v3.0: xuất Word (.docx) từ LDM (và/hoặc text cuối người dùng sửa) ──
	mux.HandleFunc("POST /api/export-docx", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 30<<20)
		var req struct {
			Source    string            `json:"source"`
			LDM       *Document         `json:"ldm"`
			PageTexts map[string]string `json:"page_texts"`
			OCRPages  map[int][]string  `json:"ocr_lines"`
			PageBreaks *bool            `json:"page_breaks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"JSON không hợp lệ."}`, http.StatusBadRequest)
			return
		}
		pageBreaks := true
		if req.PageBreaks != nil {
			pageBreaks = *req.PageBreaks
		}
		doc := req.LDM
		if doc == nil {
			doc = &Document{Source: req.Source}
		}
		// áp text cuối cùng (đã qua tay người dùng) cho trang OCR;
		// trang đường Text giữ khối giàu định dạng nếu không có sửa
		if len(req.PageTexts) > 0 {
			for i := range doc.Pages {
				p := &doc.Pages[i]
				if t, ok := req.PageTexts[itoa(p.Index)]; ok && t != "" {
					if p.Path != "text" || pNeedsRebuild(p, t) {
						p.Blocks = paraBlocksFromText(t)
					}
					p.NeedsOCR = false
				}
			}
		}
		// trang OCR gửi dạng danh sách dòng (ocr_lines) không qua /api/pdf-text
		if len(req.OCRPages) > 0 {
			idx := map[int]bool{}
			for i := range doc.Pages {
				idx[doc.Pages[i].Index] = true
			}
			keys := make([]int, 0, len(req.OCRPages))
			for k := range req.OCRPages {
				keys = append(keys, k)
			}
			sortInts(keys)
			for _, k := range keys {
				if !idx[k] {
					doc.Pages = append(doc.Pages, Page{Index: k, Path: "ocr",
						Blocks: paraBlocksFromText(strings.Join(req.OCRPages[k], "\n"))})
				}
			}
			sortPages(doc)
		}
		if len(doc.Pages) == 0 {
			http.Error(w, `{"error":"Không có nội dung nào để xuất Word."}`, http.StatusBadRequest)
			return
		}
		name := "viocr-ket-qua.docx"
		if doc.Source != "" {
			name = strings.TrimSuffix(doc.Source, ".pdf") + ".docx"
		}
		tmp, err := os.CreateTemp("", "viocr-*.docx")
		if err != nil {
			http.Error(w, `{"error":"Không tạo được file tạm."}`, http.StatusInternalServerError)
			return
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := WriteDocx(doc, tmp.Name(), pageBreaks); err != nil {
			msg, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("%v", err)})
			http.Error(w, string(msg), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		http.ServeFile(w, r, tmp.Name())
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	// Nạp model ở nền để trang mở được ngay; /api/status báo tiến độ.
	go func() {
		assetDir, err := InitRuntime()
		if err == nil {
			var e *Engine
			e, err = NewEngine(assetDir, 0, true)
			gui.mu.Lock()
			if err == nil {
				gui.engine, gui.ready = e, true
			} else {
				gui.err = err.Error()
			}
			gui.mu.Unlock()
		} else {
			gui.mu.Lock()
			gui.err = err.Error()
			gui.mu.Unlock()
		}
	}()

	if openBrowser {
		openInBrowser(addr)
	}
	fmt.Println("viocr", appVersion, "— giao diện đang chạy tại", addr)
	fmt.Println("Giữ cửa sổ này mở; nhấn Ctrl+C để thoát.")
	return srv.Serve(ln)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// openInBrowser mở URL bằng trình duyệt mặc định (rundll32 trên Windows để
// không nháy cửa sổ console phụ).
func openInBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// ═══════════════ tiện ích export-docx ═══════════════

// pNeedsRebuild — trang đường Text có cần dựng lại từ text người dùng không?
// So plain-text của trang với text mới: khác nhiều (≥ 20% ký tự) → dựng lại.
func pNeedsRebuild(p *Page, t string) bool {
	old := ""
	for _, b := range p.Blocks {
		old += blockText(b) + "\n"
	}
	old = strings.TrimSpace(old)
	t = strings.TrimSpace(t)
	if old == t {
		return false
	}
	if len(old) == 0 {
		return len(t) > 0
	}
	// nhanh: so độ dài + vài ký hiệu
	if absI(len([]rune(old))-len([]rune(t))) > len([]rune(old))/3 {
		return true
	}
	return CER(old, t) > 0.2
}

// paraBlocksFromText — dựng khối đoạn đơn giản từ chuỗi nhiều dòng
// (cho trang OCR hoặc trang người dùng đã sửa tay).
func paraBlocksFromText(t string) []Block {
	lines := strings.Split(strings.ReplaceAll(t, "\r\n", "\n"), "\n")
	b := Block{Kind: BlockPara, Conf: 0.9}
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		b.Lines = append(b.Lines, Line{Runs: []Run{{
			Text: strings.TrimSpace(ln), SizePt: 11, Conf: 0.9,
			X0: 0, X1: 100, Y0: 0, Y1: 14,
		}}})
	}
	if len(b.Lines) == 0 {
		return nil
	}
	return []Block{b}
}

func sortInts(v []int) { sort.Ints(v) }

// sortPages — sắp trang theo Index.
func sortPages(d *Document) {
	sort.Slice(d.Pages, func(i, j int) bool { return d.Pages[i].Index < d.Pages[j].Index })
}
