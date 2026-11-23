# viocr — OCR tiếng Việt chạy offline, 1 file duy nhất

Phần mềm OCR tiếng Việt viết bằng **Go**: phát hiện dòng chữ (PP-OCRv5 det) →
cắt vùng → phân loại hướng 180° → nhận dạng **VietOCR** (Seq2Seq mặc định /
Transformer). Toàn bộ model + thư viện ONNX Runtime được **nhúng trong 1 file
binary** (~290 MB): không cần cài Python, không cần tải model, **không cần mạng**
— chép 1 file là chạy.

- Linux: `viocr-linux-x64` — Windows: `viocr-windows-x64.exe`
- Pipeline và tham số được nghiên cứu/không sai khác từ: RapidOCR, RAGFlow
  DeepDoc, deepdoc_vietocr, pbcquoc/vietocr (chi tiết: [`docs/RESEARCH.md`](docs/RESEARCH.md))

## Mới ở bản 3.0.0-alpha — đường Text + xuất Word giữ định dạng

Bản alpha triển khai đúng lộ trình trong tài liệu thiết kế v3.0: hai mốc đầu
(M6 bench + M1 đường Text) và bộ sinh Word (M5) với Logical Document Model
(LDM) làm trục dữ liệu trung tâm.

- **Đường Text cho PDF (M1)** — trang PDF sinh từ Word có sẵn lớp chữ: viocr
  đọc trực tiếp chuỗi ký tự + font + cỡ + tọa độ bằng bộ parser PDF thuần Go
  (`pdfmini.go`, hỗ trợ xref/xref-stream/ObjStm, FlateDecode, ASCII85Decode,
  ToUnicode CMap) — **CER ≈ 0% và ~10 ms/trang** thay vì 4–5 giây OCR. Trang
  scan/chữ giả (ToUnicode hỏng) tự động rớt sang đường OCR: phân loại trang
  chấm điểm bằng độ phủ chữ + tỉ lệ âm tiết hợp lệ (ngưỡng 85%).
- **Logical Document Model (LDM)** — mô hình 4 cấp Document → Page → Block
  (para/heading/table/footer/header) → Line → Run kèm font, cỡ, vị trí, độ tin
  cậy. Mọi module ghi vào LDM, bộ sinh Word chỉ đọc từ LDM.
- **Layout Analyzer (M3-lite)** — gom dòng/khối theo khe dọc + thụt lề, nhận
  diện đề mục theo cỡ chữ, phát hiện 2 cột, tách đầu/cuối trang, bảng heuristic
  (≥3 hàng × ≥3 cột thẳng biên).
- **Hiệu chỉnh tiếng Việt CÓ KIỂM SOÁT (M4)** — từ điển **18.778 âm tiết**
  (hunspell vi + sinh đủ biến thể thanh điệu, nhúng sẵn trong exe) + khoảng
  cách sửa có trọng số dấu. Alpha **chỉ sửa âm tiết vô nghĩa và chỉ khi ứng
  viên duy nhất**; âm tiết hợp lệ không bị đụng tới; **mọi chỗ sửa đều ghi
  log** xem lại trong giao diện — không sửa im lặng. Sửa theo ngữ cảnh/thống
  kê dấu là nội dung v3.0-beta.
- **Xuất Word .docx (M5)** — LDM → OOXML tối giản tự dựng bằng Go chuẩn
  (w:p/w:tbl/gridSpan/vMerge, Heading 1–3, ngắt trang tuỳ chọn), mở được trên
  Word 2016/365, WPS, Google Docs, LibreOffice. Font/cỡ lấy thẳng từ metadata
  PDF cho trang chữ.
- **Bench M6** — `viocr --bench <dir>` chấm CER/WER (cặp file `gold/` vs
  `out/`), TS/LFS khi có thêm cặp `*.ldm.json`; kết quả lưu
  `bench_result.json` theo phiên bản.

### Cách dùng mới (v3)

```
viocr-linux-x64 --pdf-text van-ban.pdf                 # trích lớp chữ (không cần model!)
viocr-linux-x64 --pdf-text van-ban.pdf --docx ra.docx  # + xuất Word giữ định dạng
viocr-linux-x64 --docx ra.docx anh1.jpg anh2.png       # OCR ảnh → Word
viocr-linux-x64 --bench benchdir/                      # chấm CER/WER/TS/LFS
```

| Flag mới (v3) | Mặc định | Ý nghĩa |
|---|---|---|
| `--pdf-text file.pdf` | — | Trích lớp chữ PDF ra stdout, **không OCR, không cần model** |
| `--docx out.docx` | — | Xuất file Word từ LDM (đi cùng `--pdf-text` hoặc ảnh/PDF đầu vào) |
| `--json-ldm out.json` | — | Ghi Logical Document Model (JSON) ra file |
| `--bench dir` | — | Chạy bench trên thư mục `gold/` + `out/` |
| `--no-correct` | tắt | Tắt hiệu chỉnh tiếng Việt |
| `--no-pagebreak` | tắt | Không chèn ngắt trang trong file Word |

Giao diện đồ họa (v3): PDF được xử lý theo trang — trang chữ dùng lớp chữ
(không OCR), trang scan vẫn OCR bằng pdf.js render như v2; nút **📝 Xuất Word**
tải file .docx; nút **🔁 Chỗ đã sửa** liệt kê toàn bộ log hiệu chỉnh. Logo,
màu thương hiệu (navy `#0b2047` + vàng `#f0b41c`) và footer giữ nguyên từ v2.

Giới hạn của alpha (đúng phạm vi mục 7 tài liệu thiết kế): bảng không đường
kẻ chưa xử lý; ô gộp bảng chỉ nhận ở mức heuristic; sửa theo ngữ cảnh chưa có;
dewarp/chữ viết tay/đa ngôn ngữ ngoài tiếng Việt + tiếng Anh nằm ngoài phạm vi.

## Mới ở bản 2.0.0

- **Đọc file PDF**: kéo thả PDF vào giao diện — từng trang được render ngay trên
  trình duyệt bằng **pdf.js (nhúng sẵn trong exe, offline)**, gửi OCR theo trang
  có tiến độ, kết quả ghép cả file với tiêu đề `===== Trang i/N =====`, xuất .txt
  một thể. PDF tối đa 60 MB; ảnh trong PDF text-based lẫn scan đều nhận được.
- **Chất lượng nhận dạng tốt hơn, đo bằng benchmark** (40 ảnh stress tự dựng:
  JPEG nén 55–75%, nhiễu, blur, nền tối, dòng dài, ký tự `@ $ ^ # % & = < >`):
  CER (tỉ lệ ký tự sai) giảm **10.32% → 8.39%** nhờ: engine mặc định chuyển sang
  **Seq2Seq** (giữ chữ IN HOA tốt hơn, nhanh hơn), cắt đuôi "ảo giác" khi decode
  (độ dài decode bị chặn theo độ dài ảnh + trim lặp cuối dòng), thử lại với
  tăng tương phản khi độ tin cậy thấp, det-side 1440 cho trang PDF.
- **Giới hạn đã biết** (do charset/training của model VietOCR, không phải lỗi
  cài đặt): ký hiệu `@ $ ^` vẫn nhầm ở nhiều vị trí; `— ° ₫ € £` không có trong
  vocab nên sẽ thành ký tự khác; dòng IN HOA toàn bộ có thể bị đổi thành chữ
  thường ở một số ảnh. Cải tiến sâu hơn cần thay/về huấn luyện lại model —
  model latin của PaddleOCR đã được thử và loại vì dict thiếu ký tự tiếng Việt
  (ơ ư ạ ế ộ...).

## Giao diện đồ họa

**Nhấp đúp exe (hoặc chạy không kèm ảnh) là mở giao diện ngay**: exe nhúng sẵn
một trang web cục bộ, tự mở trình duyệt tại `http://127.0.0.1:<cổng>` — kéo thả
ảnh/PDF vào, bấm **Nhận dạng**, xem kết quả kèm độ tin cậy, vẽ khung chữ trên ảnh,
Copy hoặc tải file `.txt`. Toàn bộ chạy offline trên máy, dữ liệu không rời khỏi
máy. Giao diện làm theo phong cách dashboard quản trị: sidebar xanh navy + điểm
nhấn vàng, 2 panel "Ảnh đầu vào" / "Kết quả OCR", responsive cả điện thoại.

- Server chỉ lắng nghe `127.0.0.1` (không mở ra mạng LAN).
- Model nạp nền ngay khi mở trang; thanh trạng thái chuyển **Sẵn sàng** (xanh)
  khi dùng được (lần đầu mất vài giây để giải nén model).

| Flag giao diện | Ý nghĩa |
|---|---|
| `--gui` | Mở giao diện kể cả khi có truyền ảnh |
| `--port N` | Chọn cổng server (mặc định: cổng tự do) |
| `--no-browser` | Chạy server nhưng không tự mở trình duyệt |

## Cách dùng dòng lệnh (Windows & Linux giống hệt nhau)

```
viocr-windows-x64.exe [flags] anh1.jpg [anh2.png ...]
```

```powershell
.\viocr-windows-x64.exe anh.jpg                # in text ra stdout
.\viocr-windows-x64.exe --json scan.png        # JSON: box, text, độ tin cậy
.\viocr-windows-x64.exe --rec-engine transformer a.jpg   # đổi engine (mặc định seq2seq)
```

| Flag | Mặc định | Ý nghĩa |
|---|---|---|
| `--json` | tắt | Xuất JSON: `box` (4 điểm), `text`, `conf` (rec), `det_score` (det), `ms` |
| `--rec-engine` | `seq2seq` | `seq2seq` (mặc định, nhanh, giữ IN HOA tốt hơn) hoặc `transformer` |
| `--rec-max-w` | `512` | Cận rộng tensor rec — dòng quá dài tăng lên 640-768 (có thể tệ hơn) |
| `--det-side` | `960` | Cận resize detection (pixel) — trang PDF nên 1440 |
| `--det-type` | `max` | `max` co ảnh theo cạnh dài / `min` phóng theo cạnh ngắn |
| `--det-thresh` | `0.3` | Ngưỡng nhị phân DBNet |
| `--box-thresh` | `0.5` | Ngưỡng điểm box |
| `--unclip` | `1.6` | Hệ số mở rộng box |
| `--max-len` | `128` | Số bước decode tối đa |
| `--no-cls` | tắt | Tắt phân loại hướng 180° |
| `--threads` | `0` | Số thread ONNX Runtime |
| `--quiet` | tắt | Không in banner stderr |

Lần chạy đầu tiên, binary giải nén model vào thư mục cache rồi mọi lần sau dùng lại:
- Windows: `%LocalAppData%\viocr\2.0.0\`
- Linux: `~/.cache/viocr/2.0.0/`

## Chạy trên Windows: yêu cầu & lỗi thường gặp

- **Cần Visual C++ Redistributable (bản x64)** —
  `onnxruntime.dll` phụ thuộc `MSVCP140.dll` / `VCRUNTIME140.dll` vốn KHÔNG có
  sẵn trong Windows. Nếu chạy lên là console nháy rồi tắt, hãy tải và cài bản
  x64 tại: https://aka.ms/vs/17/release/vc_redist.x64.exe (máy đã cài qua game
  hoặc phần mềm khác thường có sẵn, có thể bỏ qua). Chỉ cần cài một lần.
- **Nhấp đúp exe → mở giao diện đồ họa trong trình duyệt** (từ bản 1.1.0).
  Không cần vào cmd; muốn dùng dòng lệnh (xử lý nhiều ảnh, xuất JSON) thì mở
  `cmd`/`PowerShell` rồi chạy kèm ảnh:
  ```powershell
  .\viocr-windows-x64.exe anh.jpg
  .\viocr-windows-x64.exe --json scan.png
  ```
- Khi chạy tương tác (nhấp đúp), nếu gặp lỗi chương trình **chờ Enter trước khi
  thoát** — luôn đọc được thông báo.
- Dung lượng ~259 MB hiển thị trong Windows Explorer = 272.001.536 bytes
  (Explorer tính theo đơn vị MiB) — file đúng nguyên vẹn.

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
| `webui.go` | Giao diện đồ họa: server cục bộ `127.0.0.1`, API `/api/ocr`, `/api/pdf-text`, `/api/export-docx` (v3), tự mở trình duyệt |
| `ui/index.html` | Trang giao diện nhúng trong exe (HTML/CSS/JS tự chứa, offline 100%) |
| `ldm.go` | **v3** Logical Document Model: Document → Page → Block → Line → Run |
| `pdfmini.go` | **v3** M1: parser PDF thuần Go — trích lớp chữ (đường Text) |
| `layout.go` | **v3** M3-lite: gom dòng/khối, đề mục, cột, bảng heuristic |
| `corrector.go` | **v3** M4: hiệu chỉnh tiếng Việt có kiểm soát + từ điển nhúng |
| `docx.go` | **v3** M5: LDM → OOXML (.docx) bằng archive/zip chuẩn Go |
| `bench.go` | **v3** M6: CER / WER / TS / LFS |
| `pipeline.go` | **v3** glue: phân loại trang, ghép đường Text + đường OCR |
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
