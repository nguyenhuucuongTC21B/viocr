package main

// models.go — nhúng các model ONNX + vocab vào binary (go:embed) và giải nén
// vào thư mục cache khi chạy lần đầu. Thư viện runtime (libonnxruntime.so /
// onnxruntime.dll) được nhúng riêng theo từng hệ điều hành trong
// runtime_linux.go / runtime_win.go. Sau khi giải nén xong, chương trình hoàn
// toàn offline: không tải thêm bất kỳ thứ gì từ mạng.

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const appVersion = "1.0.1"

//go:embed assets/det.onnx assets/cls.onnx assets/rec_t_enc.onnx assets/rec_t_dec.onnx
//go:embed assets/rec_s_cnn.onnx assets/rec_s_enc.onnx assets/rec_s_dec.onnx
//go:embed assets/vocab.txt
var modelsFS embed.FS

var modelFiles = []string{
	"det.onnx", "cls.onnx",
	"rec_t_enc.onnx", "rec_t_dec.onnx",
	"rec_s_cnn.onnx", "rec_s_enc.onnx", "rec_s_dec.onnx",
	"vocab.txt",
}

// extractAssets giải nén model + runtime lib vào cacheDir (Linux:
// ~/.cache/viocr/<version>, Windows: %LocalAppData%/viocr/<version>).
// Nếu file đã tồn tại với dung lượng khớp thì bỏ qua. Trả về đường dẫn thư mục.
func extractAssets() (string, error) {
	var base string
	if d, err := os.UserCacheDir(); err == nil {
		base = filepath.Join(d, "viocr", appVersion)
	} else {
		base = filepath.Join(os.TempDir(), "viocr", appVersion)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("tạo thư mục cache %s: %w", base, err)
	}
	files := append(append([]string{}, modelFiles...), runtimeLibName())
	for _, name := range files {
		target := filepath.Join(base, name)
		embedSize, err := embeddedSize(name)
		if err != nil {
			return "", err
		}
		if st, err := os.Stat(target); err == nil && st.Size() == embedSize {
			continue // đã giải nén từ lần chạy trước
		}
		if err := writeAsset(name, target); err != nil {
			return "", err
		}
	}
	return base, nil
}

// openAsset mở asset từ modelsFS hoặc runtimeFS (theo tên file).
func openAsset(name string) (fs.File, error) {
	path := "assets/" + name
	if f, err := modelsFS.Open(path); err == nil {
		return f, nil
	}
	return runtimeFS.Open(path)
}

func embeddedSize(name string) (int64, error) {
	f, err := openAsset(name)
	if err != nil {
		return 0, fmt.Errorf("asset %s: %w (hãy chạy tools/get_assets.py trước khi build)", name, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func writeAsset(name, target string) error {
	src, err := openAsset(name)
	if err != nil {
		return fmt.Errorf("mở asset %s: %w", name, err)
	}
	defer src.Close()
	tmp := target + ".tmp"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("tạo %s: %w", tmp, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("ghi %s: %w", tmp, err)
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
