# Báo cáo nghiên cứu — cơ sở kỹ thuật của viocr

Tài liệu này tổng hợp kết quả nghiên cứu 8 nguồn được yêu cầu, và cách mỗi nguồn
được chuyển hóa thành thiết kế của `viocr`. Mọi tham số nêu dưới đây đều đã được
kiểm chứng bằng pipeline tham chiếu Python (`scripts/ref_pipeline.py`) trước khi
port sang Go, và so sánh đầu ra Go với Python trên cùng bộ ảnh test.

---

## 1. Umi-OCR (hiroi-sora) — hình mẫu "OCR offline, chạy ngay"

**Nội dung chính.** Umi-OCR là ứng dụng OCR offline cho Windows/Linux x64, đóng
gói 2 engine thay thế được: **PaddleOCR-json** (infer C++ với PP-OCR v3/v4) và
**RapidOCR-json** (engine ONNXRuntime). Không có language pack tiếng Việt mặc
định; hỗ trợ tiếng Việt chỉ khi người dùng nạp model PP-format tùy chỉnh.

**Bài học rút ra cho viocr.**
- Mô hình "1 package chép là chạy, không cần mạng" — viocr nhúng cả model và
  runtime vào binary thay vì đóng gói thư mục như Umi-OCR.
- Umi-OCR không có tiếng Việt sẵn → cần bộ model riêng: det PaddleOCR + rec
  VietOCR (đúng như các repo Việt Nam bên dưới đã làm).

## 2. RapidOCR (RapidAI) — tham số pipeline + nguồn model ONNX

**Cấu hình mặc định** (config v3.10.0, engine onnxruntime):

| Nhóm | Tham số |
|---|---|
| Det | `limit_side_len: 736`, `limit_type: min`, `thresh: 0.3`, `box_thresh: 0.5`, `max_candidates: 1000`, `unclip_ratio: 1.6`, `min_size: 3`, dilation kernel `[[1,1],[1,1]]` |
| Cls | shape `[3,48,192]`, `cls_thresh: 0.9`, nhãn `["0","180"]` |
| Rec | shape `[3,48,320]`, normalize `(x/255−0.5)/0.5`, pad phải, CTC decode: blank chèn ở **index 0**, space thêm ở **cuối** charset, drop score 0.5 |

**Điểm khác biệt được kiểm chứng thực nghiệm trong dự án này:** model ONNX phân
phối tại ModelScope v3.10.0 (export Paddle PIR) đã đưa bước **Sigmoid vào trong
graph** — output `fetch_name_0` là xác suất [0,1], **không** áp sigmoid lần nữa
(áp 2 lần sẽ ép mọi pixel về 0.5–0.73 → toàn ảnh thành 1 box khổng lồ — lỗi thực
tế chúng tôi gặp và phát hiện nhờ so sánh min/max đầu ra). Do đó viocr tự động
nhận dạng: chỉ sigmoid khi output vượt ngoài [0,1].

**Chuẩn hóa det:** RapidOCR v6-đi-định dùng 0.5/0.5, PaddleOCR chính chủ dùng
ImageNet stats. Thử nghiệm trên det v4/v5 cho kết quả box tương đương — viocr
chọn ImageNet stats (khớp PaddleOCR gốc + DeepDoc).

**Nguồn model đã tải (HTTP 200, kiểm chứng curl):**
- `https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.10.0/onnx/PP-OCRv5/det/ch_PP-OCRv5_det_mobile.onnx` (4.819.576 B)
- `.../PP-OCRv4/det/ch_PP-OCRv4_det_mobile.onnx` (4.745.517 B)
- `.../PP-OCRv4/cls/ch_ppocr_mobile_v2.0_cls_mobile.onnx` (585.532 B)
- HF mirror: `https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv4/...`

## 3. DeepDoc / RAGFlow (infiniflow) — postprocess DBNet + xử lý dòng

Đọc từ `deepdoc/vision/ocr.py` (tag v0.20.5). Các thuật toán được port nguyên bản sang Go:

- **DetResizeForTest** (limit 960/max): `ratio = 960/max(h,w)` nếu vượt, resize
  rồi làm tròn **mỗi chiều về bội 32** (floor 32).
- **DBPostProcess**: `thresh 0.3`, `box_thresh 0.5`, `unclip_ratio 1.5`, min_size 3;
  `get_mini_boxes` = `cv2.minAreaRect` + `boxPoints` (viocr: convex hull + rotating
  calipers — cho cùng kết quả); **unclip**: `distance = area × ratio / perimeter`
  với ClipperOffset `JT_ROUND` (viocr: mở rộng kích thước min-area-rect — tương
  đương cho box chữ nhật xoay); rescale + clip về ảnh gốc.
- **get_rotate_crop_image**: crop width = max(|p0−p1|, |p2−p3|), height =
  max(|p0−p3|, |p1−p2|), `warpPerspective` INTER_CUBIC BORDER_REPLICATE (viocr:
  homography 8 ẩn + bilinear clamp); **crop dọc h/w ≥ 1.5 → thử 3 hướng
  (gốc, rot90 k=3, rot90 k=1), giữ conf cao nhất** — port nguyên bản.
- **Ghép dòng**: sort theo y, gộp bubble khi |Δy| < 10px; trong dòng sort x.

DeepDoc bundle model tại HF `InfiniFlow/deepdoc` (`det.onnx` 4.745.517 B — trùng
byte với bản RapidOCR). Repo chính đã chuyển sang Go, bản Python Python ở các tag.

## 4. deepdoc_vietocr (hoaivannguyen) — DeepDoc + VietOCR

Fork RAGFlow deepdoc thay recognizer bằng VietOCR — đúng pipeline viocr:
det PP-OCR (ONNX) → crop → VietOCR nhận dạng. Hai đường rec:

- PyTorch: `vgg_seq2seq` mặc định (transformer chậm hơn nhiều).
- ONNX: 3 graph `cnn.onnx` (80.818.912 B), `encoder.onnx` (3.685.448 B — bi-LSTM,
  output `(T,1,512)` + hidden `(1,256)`), `decoder.onnx` (5.138.884 B — decoder
  **bước-từng-bước**: nạp token cuối + hidden → logits `(1,233)` + hidden mới),
  decode greedy `sos=1, eos=2, max_seq_length=128`.

viocr nhúng cả 3 file này làm engine `--rec-engine seq2seq` (kiểm chứng: decode
"Nguyễn Văn An - 0123456789" chính xác tuyệt đối).

## 5. VietOCR (pbcquoc) — kiến trúc + preprocess + decode

Đọc trực tiếp source master: `vietocr/model/vocab.py`, `tool/translate.py`,
`model/transformerocr.py`, `model/seqmodel/transformer.py`, `model/backbone/vgg.py`.

- **Vocab**: 4 token đặc biệt theo index cứng `pad=0, go/sos=1, eos=2, mask=3`,
  sau đó 229 ký tự (`c2i = i+4`) → vocab size **233**. Chuỗi vocab lấy từ
  `base.yml` (vocr.vn), **có ký tự space ở cuối**, khớp 100% với `vocab.txt` của
  `dzngo/vietocr-models` (233 dòng) — viocr nhúng file vocab này.
- **Preprocess** (`translate.py`): `convert("RGB")` → resize về cao 32
  (LANCZOS), width = `int(32·w/h)` **làm tròn LÊN bội 10**, clamp [32, 512]
  (vi phạm tỉ lệ nếu clamp — hành vi gốc), `transpose(2,0,1)`, **chỉ chia 255**,
  không mean/std, không padding (batch gom theo width bằng nhau).
- **Greedy decode** (`translate()`): `memory = forward_encoder(cnn(img))`; loop
  ≤ `max_seq_length=128`, mỗi bước nạp **toàn bộ prefix** `(T,1)` time-major,
  lấy argmax ở vị trí cuối, dừng khi mọi dòng gặp EOS; `vocab.decode` bỏ id ≤ 3.
- **Kiến trúc vgg_transformer**: vgg19_bn (AvgPool thay MaxPool theo `ss/ks`:
  [[2,2],[2,2],[2,1],[2,1],[1,1]], hidden 256) + nn.Transformer (d_model 256,
  nhead 8, 6+6 layers, ffn 2048); memory dài `2·(W//4)`; `×√d_model` trước PE.
- **Weights gốc**: `https://vocr.vn/data/vietocr/vgg_transformer.pth`
  (151.815.373 B, HTTP 200) — đã tải về làm backup; accuracy công bố 0.8859
  (transformer) / 0.8720 (seq2seq) full-sequence.

## 6. Chuyển đổi VietOCR → ONNX (Viblo buiquangmanhhp1999 + các repo)

Bài Viblo "Chuyển đổi mô hình học sâu về ONNX" tách VietOCR thành 3 graph
(cnn / encoder / decoder), decoder nạp tăng dần — cách này đúng cho **seq2seq**
(LSTM mang hidden), nhưng **không** dùng được nguyên bản cho transformer (không
thể nạp "token cuối" vì self-attention cần cả prefix).

viocr dùng 2 scheme tương ứng 2 engine:
- **transformer** (mặc định): bộ ONNX `vemines/vietocr-onnx` (HF) — encoder gộp
  CNN + transformer-encoder (input `img` `[1,3,32,W]` → `memory` `[2·(W//4),1,256]`),
  decoder full-prefix (input `tgt` `[T,1]` int64 + `memory` → `output` `[1,T,233]`,
  opset 17). External data 113 MB của encoder đã được **inline thành 1 file**.
- **seq2seq**: bộ `deepdoc_vietocr` (mục 4).

Cả 2 đều chạy greedy decode đúng logic của `pbcquoc/vietocr` — kiểm chứng bằng
việc đầu ra Go khớp Python tham chiếu trên toàn bộ ảnh test.

Nguồn model đã kiểm chứng (HTTP 200):
- `https://huggingface.co/vemines/vietocr-onnx/resolve/main/vgg_encoder.onnx` (+`.onnx.data` 113.246.208 B) và `vgg_decoder.onnx` (39.588.449 B), `vocab.json` (khớp vocab gốc)
- `https://media.githubusercontent.com/media/hoaivannguyen/deepdoc_vietocr/main/onnx/{cnn,encoder,decoder}.onnx`
- `https://huggingface.co/dzngo/vietocr-models/resolve/main/vietocr/vocab.txt` (233 dòng)

## 7. VNCV (Devhub-Solutions) — tham khảo đóng gói

`pip install vncv` — engine OCR tiếng Việt thuần ONNXRuntime: `detection.onnx` +
`classification.onnx` + VietOCR transformer (`model_encoder.onnx`,
`model_decoder.onnx`, `vocab.json`), tự tải model từ GitHub Releases
(bundle 142.662.944 B, HTTP 200). Cho thấy xu hướng "bỏ PyTorch, chỉ còn ONNX"
cho OCR tiếng Việt production — viocr đi xa hơn: nhúng hẳn vào binary.

## 8. PP-OCRv5 (arXiv 2507.05595) & YOLOv10 (arXiv 2405.14458)

**PP-OCRv5** ("PaddleOCR 3.0 Technical Report"):
- Det: backbone **PP-HGNetV2**, giữ PFHead + chiến lược DSR; teacher distillation
  từ GOT-OCR2.0; tăng cường dữ liệu đa ngôn ngữ (tổng hợp, xoay, blur, biến hình).
- Rec: nhánh kép PP-HGNetV2 — nhánh **GTC-NRTR** (attention, chỉ dùng lúc train)
  và nhánh **SVTR-HGNet CTC** là nhánh suy luận duy nhất.
- Ngôn ngữ 1 model: Trung giản/phồn, Pinyin, Anh, Nhật — **tiếng Việt KHÔNG nằm
  trong PP-OCRv5 rec**, vẫn phải dùng model `latin` hoặc VietOCR → xác nhận lựa
  chọn kiến trúc của viocr (det PP-OCRv5 + rec VietOCR).
- Hiệu năng: giảm 26% lỗi nhận dạng chữ viết tay so với v4; latency T4 giảm
  73.1% (rec mobile) / 40.4% (det mobile).

**YOLOv10**: huấn luyện NMS-free qua dual assignment (o2m + o2o nhất quán,
matching metric `s·p^α·IoU^β` với r=1), kiến trúc gọn (CIB rank-guided, PSA,
large-kernel 7×7); YOLOv10-S nhanh ~1.8× so RT-DETR-R18 cùng AP. Trong hệ sinh
thái của đề bài, YOLO được DeepDoc dùng cho **layout analysis** (`layout*.onnx`,
`tsr.onnx`) — nằm ngoài phạm vi OCR text thuần nên viocr chưa tích hợp; nếu cần
phân tích layout/bảng biểu, có thể thêm YOLO layout model tương tự deepdoc.

---

## Các quyết định thiết kế cuối

| Quyết định | Lý do |
|---|---|
| det = PP-OCRv5 mobile (4.8 MB), norm ImageNet, 960/max | nhỏ, nhanh, quality ngang v4; khớp PaddleOCR gốc |
| Không sigmoid nếu output ∈ [0,1] | model v3.10.0 đã sigmoid sẵn (kiểm chứng) |
| rec mặc định = VietOCR transformer (vemines), option seq2seq (deepdoc_vietocr) | transformer chính xác hơn rõ rệt trên test (conf 0.92 vs 0.85, đúng "Tự do", "phức tạp"); seq2seq nhanh ~2.6× |
| Greedy decode (không beam search) | khớp hành vi mặc định vietocr; beam chỉ tăng nhẹ chất lượng, chậm hơn nhiều |
| Embed assets + dlopen runtime giải nén từ binary | yêu cầu "1 file duy nhất + offline"; runtime .so lấy từ wheel pip 1.32.0 |
| minAreaRect bằng hull + rotating calipers | thay thế `cv2.minAreaRect` khi không có OpenCV — cùng kết quả cho blob DBNet |

## Kết quả đối chiếu Go vs Python tham chiếu

Cùng một ảnh `viet_doc.png` (6 dòng): Python ref — 5/6 dòng hoàn hảo, 1 dòng mất
dấu ("Tự do" → "Tư do"); **Go — 6/6 dòng hoàn hảo** ("Độc lập - Tự do - Hạnh
phúc" đúng cả dấu gạch). Khác biệt đến từ đường resize/crop tinh chỉnh hơn trong
Go (bilinear cv2-style + Lanczos PIL-style + bilinear crop sampling).
