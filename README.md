# viocr — OCR tiếng Việt chạy offline, 1 file duy nhất

Phần mềm OCR tiếng Việt viết bằng **Go**: phát hiện dòng chữ (PP-OCRv5 det) →
cắt vùng → phân loại hướng 180° → nhận dạng **VietOCR** (Transformer). Toàn bộ
model + thư viện ONNX Runtime được **nhúng trong 1 file binary** (~290 MB):
không cần cài Python, không cần tải model, **không cần mạng** — chép 1 file là chạy.

- Linux: `viocr-linux-x64` — Windows: `viocr-windows-x64.exe`
- Pipeline và tham số được nghiên cứu/không sai khác từ: RapidOCR, RAGFlow
  DeepDoc, deepdoc_vietocr, pbcquoc/vietocr (chi tiết: [`docs/RESEARCH.md`](docs/RESEARCH.md))

## Cách dùng (Windows & Linux giống hệt nhau)

```
viocr-windows-x64.exe [flags] anh1.jpg [anh2.png ...]
```

```powershell
.\viocr-windows-x64.exe anh.jpg                # in text ra stdout
.\viocr-windows-x64.exe --json scan.png        # JSON: box, text, độ tin cậy
.\viocr-windows-x64.exe --rec-engine seq2seq a.jpg   # engine nhanh hơn
```

| Flag | Mặc định | Ý nghĩa |
|---|---|---|
| `--json` | tắt | Xuất JSON: `box` (4 điểm), `text`, `conf` (rec), `det_score` (det), `ms` |
| `--rec-engine` | `transformer` | `transformer` (chính xác nhất) hoặc `seq2seq` (nhanh hơn) |
| `--det-side` | `960` | Cận resize detection (pixel) |
| `--det-type` | `max` | `max` co ảnh theo cạnh dài / `min` phóng theo cạnh ngắn |
| `--det-thresh` | `0.3` | Ngưỡng nhị phân DBNet |
| `--box-thresh` | `0.5` | Ngưỡng điểm box |
| `--unclip` | `1.6` | Hệ số mở rộng box |
| `--max-len` | `128` | Số bước decode tối đa |
| `--no-cls` | tắt | Tắt phân loại hướng 180° |
| `--threads` | `0` | Số thread ONNX Runtime |
| `--quiet` | tắt | Không in banner stderr |

Lần chạy đầu tiên, binary giải nén model vào thư mục cache rồi mọi lần sau dùng lại:
- Windows: `%LocalAppData%\viocr\1.0.0\`
- Linux: `~/.cache/viocr/1.0.0/`

## Cách 1 — có file exe ngay (không cần GitHub)

Nhóm phát triển đã build sẵn `viocr-windows-x64.exe` (kiểm tra phần Releases
của repo này sau khi bạn push; hoặc dùng file đính kèm dự án). Chép vào máy
Windows x64 bất kỳ và chạy trực tiếp.

## Cách 2 — đưa lên GitHub để CI tự build exe

Repo đã có sẵn workflow `.github/workflows/build.yml`: mỗi lần push, GitHub
Actions sẽ **tự tải model, tự build cả Linux binary và file `.exe` Windows**,
đính kèm vào **Artifacts** (tab Actions) và vào **Releases** khi bạn push tag.

### Bước 1 — tạo repository trống trên GitHub

1. Đăng nhập [github.com](https://github.com) → nút **New repository**.
2. Đặt tên, ví dụ `viocr`. Để trống (KHÔNG tick "Add a README" — sẽ xung đột
   với README có sẵn).
3. Nhấn **Create repository**.

### Bước 2 — push mã nguồn (chạy trong thư mục repo này)

```bash
git init
git add .
git commit -m "viocr: OCR tiếng Việt offline — 1 file, nhúng PP-OCRv5 + VietOCR"
git branch -M main
git remote add origin https://github.com/<TEN_DANG_NHAP>/viocr.git
git push -u origin main
```

### Bước 3 — CI tự build

- Vào tab **Actions** trên GitHub → workflow `build` chạy tự động (~5–8 phút,
  thời gian chủ yếu để tải model lần đầu).
- Xong việc: vào đúng lần chạy đó → mục **Artifacts** → tải `viocr-binaries`
  (chứa `viocr-windows-x64.exe` + `viocr-linux-x64`).

### Bước 4 — phát hành chính thức (khuyến nghị)

Push một tag để CI tạo **Release** kèm exe (file tải từ Release không bị hết hạn):

```bash
git tag v1.0.0
git push origin v1.0.0
```

→ Release `v1.0.0` xuất hiện ở tab **Releases** với 2 file binary đính kèm.

> Lưu ý: model/runtime KHÔNG được commit lên git (GitHub giới hạn file 100 MB,
> repo sẽ nhẹ ~2 MB). CI tự tải bằng `tools/get_assets.py` mỗi lần build —
> script dùng nguồn công khai (ModelScope, HuggingFace, NuGet).

## Build thủ công (không dùng CI)

Yêu cầu: Go ≥ 1.26, gcc; Windows cần mingw-w64 (`gcc` của MSYS2) hoặc build
cross từ Linux (`gcc-mingw-w64-x86-64`).

```bash
pip install onnx
python3 tools/get_assets.py        # tải model + runtime lib vào assets/

# Windows exe (cross-compile từ Linux):
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -trimpath -ldflags "-s -w" -o viocr.exe .

# Linux:
go build -trimpath -ldflags "-s -w" -o viocr .
```

## Cấu trúc mã nguồn

| File | Nội dung |
|---|---|
| `main.go` | CLI, pipeline từng ảnh, crop dọc thử 3 hướng (DeepDoc) |
| `models.go` | `go:embed` model + giải nén vào cache khi chạy |
| `runtime_linux.go` / `runtime_win.go` | Nhúng `libonnxruntime.so` / `onnxruntime.dll` theo OS |
| `ort.go` | Khởi tạo ONNX Runtime, session, vocab 233 ký tự |
| `det.go` | Preprocess det + DBNet postprocess (hull, rotating calipers, unclip) |
| `crop.go` | Homography 4 điểm + bilinear sampling |
| `rec.go` | Preprocess VietOCR + greedy decode (transformer / seq2seq) |
| `cls.go` | Phân loại 180°, xoay ảnh |
| `line.go` | Nhóm box thành dòng, ghép text |
| `imageio.go` | Đọc PNG/JPEG/GIF/BMP; resize bilinear (cv2) + Lanczos (PIL) |
| `tools/get_assets.py` | Tải model + runtime lib trước khi build |

## Hiệu năng (2 vCPU, CPU-only)

| Ảnh | Engine | Thời gian |
|---|---|---|
| 1 dòng (900×137) | transformer | ~1.9 s |
| Văn bản 6 dòng | transformer | ~6.5 s |
| Văn bản 6 dòng | seq2seq | ~2.5 s |

## License

Mã Go: MIT ([LICENSE](LICENSE)). Model PP-OCR / VietOCR / RapidOCR: Apache-2.0
(theo repo gốc); ONNX Runtime: MIT. Chi tiết nguồn model và nghiên cứu:
[`docs/RESEARCH.md`](docs/RESEARCH.md).
