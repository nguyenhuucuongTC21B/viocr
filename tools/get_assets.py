#!/usr/bin/env python3
"""get_assets.py — tải model ONNX + thư viện onnxruntime vào assets/.

Chạy trước khi `go build` (go:embed cần các file này). Hỗ trợ Linux/macOS/Windows:
- Model det/cls/vocab: ModelScope (RapidOCR), HuggingFace (VietOCR ONNX, vocab)
- libonnxruntime.so / onnxruntime.dll: gói NuGet Microsoft.ML.OnnxRuntime
  (nupkg là file zip, giải nén file native tương ứng nền tảng)

Cách dùng:
    python3 tools/get_assets.py            # tải cả 2 runtime lib (đủ để build 2 OS)
    python3 tools/get_assets.py --win-only # chỉ dll (build exe Windows)
"""
import argparse
import os
import shutil
import sys
import urllib.request
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
ASSETS = os.path.join(HERE, "..", "assets")

ORT_VERSION = "1.32.0"
NUGET_ID = "microsoft.ml.onnxruntime"
NUGET_URL = (f"https://api.nuget.org/v3-flatcontainer/{NUGET_ID}/"
             f"{ORT_VERSION}/{NUGET_ID}.{ORT_VERSION}.nupkg")

FILES = [
    # (url nguồn, tên file đích trong assets/)
    ("https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.10.0/onnx/PP-OCRv5/det/ch_PP-OCRv5_det_mobile.onnx", "det.onnx"),
    ("https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.10.0/onnx/PP-OCRv4/cls/ch_ppocr_mobile_v2.0_cls_mobile.onnx", "cls.onnx"),
    ("https://huggingface.co/vemines/vietocr-onnx/resolve/main/vgg_encoder.onnx", "rec_t_enc.onnx"),
    ("https://huggingface.co/vemines/vietocr-onnx/resolve/main/vgg_encoder.onnx.data", "rec_t_enc.onnx.data"),
    ("https://huggingface.co/vemines/vietocr-onnx/resolve/main/vgg_decoder.onnx", "rec_t_dec.onnx"),
    ("https://media.githubusercontent.com/media/hoaivannguyen/deepdoc_vietocr/main/onnx/cnn.onnx", "rec_s_cnn.onnx"),
    ("https://media.githubusercontent.com/media/hoaivannguyen/deepdoc_vietocr/main/onnx/encoder.onnx", "rec_s_enc.onnx"),
    ("https://media.githubusercontent.com/media/hoaivannguyen/deepdoc_vietocr/main/onnx/decoder.onnx", "rec_s_dec.onnx"),
    ("https://huggingface.co/dzngo/vietocr-models/resolve/main/vietocr/vocab.txt", "vocab.txt"),
]

# rec_t_enc.onnx lưu external data .onnx.data riêng — graph tham chiếu tên
# "vgg_encoder.onnx.data" cùng thư mục; khi build ta inline data vào 1 file.
INLINED_ENC = "rec_t_enc.onnx"


def download(url, dst):
    if os.path.exists(dst) and os.path.getsize(dst) > 0:
        print(f"  [có sẵn] {os.path.basename(dst)} ({os.path.getsize(dst)} B)")
        return
    print(f"  [tải] {os.path.basename(dst)} ...")
    tmp = dst + ".part"
    req = urllib.request.Request(url, headers={"User-Agent": "viocr-build/1.0"})
    with urllib.request.urlopen(req, timeout=300) as r, open(tmp, "wb") as f:
        shutil.copyfileobj(r, f)
    os.replace(tmp, dst)
    print(f"  [xong] {os.path.basename(dst)} ({os.path.getsize(dst)} B)")


def inline_encoder_data(assets):
    """Gộp external data vào rec_t_enc.onnx thành 1 file duy nhất (cần package onnx).
    Nếu không có package onnx, giữ 2 file (encoder.onnx + .data) — go:embed vẫn
    nhúng cả 2 và giải nén cạnh nhau khi chạy, ONNX Runtime tự đọc được."""
    enc = os.path.join(assets, INLINED_ENC)
    data = enc + ".data"
    if not os.path.exists(data):
        return  # đã inline hoặc chưa tải
    try:
        import onnx  # noqa
    except ImportError:
        print("LỖI: cần package 'onnx' để gộp external data của encoder.")
        print("Chạy: pip install onnx   rồi chạy lại tools/get_assets.py")
        sys.exit(1)
    m = onnx.load(enc)  # load external data vào bộ nhớ
    from onnx.external_data_helper import convert_model_from_external_data
    convert_model_from_external_data(m)
    onnx.save(m, enc)
    os.remove(data)
    print(f"  [inline] {INLINED_ENC} → 1 file ({os.path.getsize(enc)} B)")


def fetch_nuget_runtime(assets, want):
    """Tải nupkg onnxruntime và giải nên lib native. want: 'dll'|'so'|'both'."""
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
    for url, name in FILES:
        download(url, os.path.join(ASSETS, name))
    inline_encoder_data(ASSETS)
    want = "dll" if args.win_only else "both"
    print(f"Lấy onnxruntime runtime lib ({want}) từ NuGet {ORT_VERSION} ...")
    fetch_nuget_runtime(ASSETS, want)
    print("HOÀN TẤT — assets sẵn sàng cho go build.")


if __name__ == "__main__":
    sys.exit(main())
