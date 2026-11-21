#!/usr/bin/env python3
"""get_assets.py — tải model ONNX + thư viện onnxruntime vào assets/.

Chạy trước khi `go build` (go:embed cần các file này). Hỗ trợ Linux/macOS/Windows:
- Model det/cls/vocab: ModelScope (RapidOCR), HuggingFace (VietOCR ONNX, vocab)
- libonnxruntime.so / onnxruntime.dll: gói NuGet Microsoft.ML.OnnxRuntime
  (nupkg là file zip, giải nén file native tương ứng nền tảng)

Lưu ý quan trọng: vgg_encoder.onnx của vemines/vietocr-onnx dùng external data
tham chiếu ĐÚNG TÊN "vgg_encoder.onnx.data" nằm cạnh model. Vì vậy phải:
  1) tải về giữ nguyên tên gốc vgg_encoder.onnx + vgg_encoder.onnx.data
  2) onnx.load + convert_model_from_external_data (gộp data vào file)
  3) lưu thành rec_t_enc.onnx (tên mà go:embed dùng) rồi xóa 2 file gốc.
Không được đổi tên trước khi inline — onnx.load sẽ không tìm thấy data.

Cách dùng:
    python3 tools/get_assets.py            # tải cả 2 runtime lib (đủ để build 2 OS)
    python3 tools/get_assets.py --win-only # chỉ dll (build exe Windows)
"""
import argparse
import os
import shutil
import sys
import time
import urllib.error
import urllib.request
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
ASSETS = os.path.abspath(os.path.join(HERE, "..", "assets"))

ORT_VERSION = "1.32.0"
NUGET_ID = "microsoft.ml.onnxruntime"
NUGET_URL = (f"https://api.nuget.org/v3-flatcontainer/{NUGET_ID}/"
             f"{ORT_VERSION}/{NUGET_ID}.{ORT_VERSION}.nupkg")

RAW_ENC = "vgg_encoder.onnx"       # tên gốc trên HuggingFace
INLINED_ENC = "rec_t_enc.onnx"     # tên go:embed dùng sau khi inline

FILES = [
    # (url nguồn, tên file đích trong assets/)
    ("https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.10.0/onnx/PP-OCRv5/det/ch_PP-OCRv5_det_mobile.onnx", "det.onnx"),
    ("https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.10.0/onnx/PP-OCRv4/cls/ch_ppocr_mobile_v2.0_cls_mobile.onnx", "cls.onnx"),
    (f"https://huggingface.co/vemines/vietocr-onnx/resolve/main/{RAW_ENC}", RAW_ENC),
    (f"https://huggingface.co/vemines/vietocr-onnx/resolve/main/{RAW_ENC}.data", RAW_ENC + ".data"),
    ("https://huggingface.co/vemines/vietocr-onnx/resolve/main/vgg_decoder.onnx", "rec_t_dec.onnx"),
    ("https://media.githubusercontent.com/media/hoaivannguyen/deepdoc_vietocr/main/onnx/cnn.onnx", "rec_s_cnn.onnx"),
    ("https://media.githubusercontent.com/media/hoaivannguyen/deepdoc_vietocr/main/onnx/encoder.onnx", "rec_s_enc.onnx"),
    ("https://media.githubusercontent.com/media/hoaivannguyen/deepdoc_vietocr/main/onnx/decoder.onnx", "rec_s_dec.onnx"),
    ("https://huggingface.co/dzngo/vietocr-models/resolve/main/vietocr/vocab.txt", "vocab.txt"),
]


RETRYABLE_HTTP = {429, 500, 502, 503, 504}  # lỗi tạm thời — thử lại được
BACKOFF = (3, 8, 20)  # giây chờ giữa các lần thử (3 lần mỗi nguồn)


def _try_download(url, dst):
    tmp = dst + ".part"
    req = urllib.request.Request(url, headers={"User-Agent": "viocr-build/1.0"})
    with urllib.request.urlopen(req, timeout=300) as r, open(tmp, "wb") as f:
        shutil.copyfileobj(r, f)
    os.replace(tmp, dst)


def download(url, dst):
    if os.path.exists(dst) and os.path.getsize(dst) > 0:
        print(f"  [có sẵn] {os.path.basename(dst)} ({os.path.getsize(dst)} B)")
        return
    print(f"  [tải] {os.path.basename(dst)} ...")
    # Link HuggingFace bị 429 (rate limit) khi chạy nhiều → thêm mirror dự phòng
    urls = [url]
    if "huggingface.co/" in url:
        urls.append(url.replace("https://huggingface.co/", "https://hf-mirror.com/"))
    last_err = None
    for u in urls:
        for i in range(len(BACKOFF)):
            try:
                _try_download(u, dst)
                print(f"  [xong] {os.path.basename(dst)} ({os.path.getsize(dst)} B)  <- {u.split('/')[2]}")
                return
            except urllib.error.HTTPError as e:
                last_err = e
                if e.code in RETRYABLE_HTTP and i < len(BACKOFF) - 1:
                    print(f"  [retry] {u.split('/')[2]}: HTTP {e.code} — chờ {BACKOFF[i]}s ...")
                    time.sleep(BACKOFF[i])
                else:
                    break  # lỗi không retry được (404...) → sang nguồn kế
            except (urllib.error.URLError, TimeoutError, OSError) as e:
                last_err = e
                if i < len(BACKOFF) - 1:
                    print(f"  [retry] {u.split('/')[2]}: {e} — chờ {BACKOFF[i]}s ...")
                    time.sleep(BACKOFF[i])
    raise RuntimeError(f"Không tải được {os.path.basename(dst)}: {last_err}")


def inline_encoder_data(assets):
    """Gộp external data thành 1 file: vgg_encoder.onnx(+.data) → rec_t_enc.onnx.
    Idempotent: nếu rec_t_enc.onnx đã tồn tại thì dọn file gốc còn sót và thoát."""
    final = os.path.join(assets, INLINED_ENC)
    raw = os.path.join(assets, RAW_ENC)
    data = raw + ".data"
    stale = final + ".data"  # .data sót từ lần chạy lỗi cũ
    if os.path.exists(final):
        if os.path.exists(stale):
            os.remove(stale)
            print(f"  [dọn] xóa {os.path.basename(stale)} cũ (model đã inline)")
        if os.path.exists(raw):
            os.remove(raw)
        return
    if not (os.path.exists(raw) and os.path.exists(data)):
        return
    try:
        import onnx  # noqa
    except ImportError:
        print("LỖI: cần package 'onnx' để gộp external data của encoder.")
        print("Chạy: pip install onnx   rồi chạy lại tools/get_assets.py")
        sys.exit(1)
    from onnx.external_data_helper import convert_model_from_external_data
    print("  [inline] gộp external data vào encoder ...")
    m = onnx.load(raw)  # đọc external data cùng thư mục (đúng tên gốc)
    convert_model_from_external_data(m)
    onnx.save(m, final)
    os.remove(raw)
    os.remove(data)
    print(f"  [xong] {INLINED_ENC} 1 file ({os.path.getsize(final)} B)")


def fetch_nuget_runtime(assets, want):
    """Tải nupkg onnxruntime và giải nén lib native. want: 'dll'|'so'|'both'."""
    nupkg = os.path.join(assets, "ort.nupkg")
    download(NUGET_URL, nupkg)
    picks = {
        "dll": ("runtimes/win-x64/native/onnxruntime.dll", "onnxruntime.dll"),
        "so": ("runtimes/linux-x64/native/libonnxruntime.so", "libonnxruntime.so"),
    }
    with zipfile.ZipFile(nupkg) as z:
        names = set(z.namelist())
        for key in (["dll", "so"] if want == "both" else [want]):
            member, out = picks[key]
            if member not in names:
                print(f"  [cảnh báo] {member} không có trong nupkg")
                continue
            dst = os.path.join(assets, out)
            with z.open(member) as src, open(dst, "wb") as f:
                shutil.copyfileobj(src, f)
            print(f"  [giải nén] {out} ({os.path.getsize(dst)} B)")
    os.remove(nupkg)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--win-only", action="store_true", help="chỉ cần onnxruntime.dll")
    args = ap.parse_args()
    os.makedirs(ASSETS, exist_ok=True)
    print(f"Tải model vào {ASSETS} ...")
    have_final = os.path.exists(os.path.join(ASSETS, INLINED_ENC))
    for url, name in FILES:
        if name.startswith(RAW_ENC) and have_final:
            print(f"  [bỏ qua] {name} (đã có {INLINED_ENC})")
            continue
        download(url, os.path.join(ASSETS, name))
    inline_encoder_data(ASSETS)
    want = "dll" if args.win_only else "both"
    print(f"Lấy onnxruntime runtime lib ({want}) từ NuGet {ORT_VERSION} ...")
    fetch_nuget_runtime(ASSETS, want)
    print("HOÀN TẤT — assets sẵn sàng cho go build.")


if __name__ == "__main__":
    sys.exit(main())
