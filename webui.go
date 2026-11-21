package main

// webui.go — giao diện đồ họa kiểu "web cục bộ": exe nhúng sẵn trang HTML
// (ui/index.html), mở server tại 127.0.0.1 và tự động mở trình duyệt.
// Không cần cài gì thêm, không cần mạng — mọi thứ chạy offline trong 1 file.

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

//go:embed ui/index.html
var uiFS embed.FS

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

		detP := &DetParams{LimitSide: 960, LimitType: "max", Thresh: 0.3, BoxThresh: 0.5, Unclip: 1.6, MinSize: 3}
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
