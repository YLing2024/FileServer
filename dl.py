#!/usr/bin/env python3
r"""下载并解压 Go 工具链与可选 ffmpeg 到 .tools 目录（OpenSSL 栈，规避 schannel 限制）"""
import json
import os
import shutil
import sys
import urllib.request
import zipfile

ROOT = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.join(ROOT, ".tools")
os.makedirs(TOOLS, exist_ok=True)


def download(url, dest, timeout=120, retries=5):
    """带断点续传的下载：中断后从已有字节继续，最多重试 retries 次"""
    for attempt in range(1, retries + 1):
        try:
            print(f"[dl] {url} (attempt {attempt}, resume={os.path.exists(dest)})")
            req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
            mode = "wb"
            if os.path.exists(dest):
                have = os.path.getsize(dest)
                if have > 0:
                    req.add_header("Range", f"bytes={have}-")
                    mode = "ab"
            with urllib.request.urlopen(req, timeout=timeout) as r, open(dest, mode) as f:
                total = int(r.headers.get("Content-Length") or 0)
                done = os.path.getsize(dest)
                while True:
                    chunk = r.read(1 << 20)
                    if not chunk:
                        break
                    f.write(chunk)
                    done += len(chunk)
                    print(f"\r[dl] {done//(1<<20)}MB / {total and (done//(1<<20)) or '?'}MB", end="", flush=True)
            print()
            return
        except Exception as e:
            print(f"\n[dl] attempt {attempt} failed: {e}")
            if attempt == retries:
                raise
    raise RuntimeError("download failed")


def dl_go():
    go_exe = os.path.join(TOOLS, "go", "bin", "go.exe")
    if os.path.exists(go_exe):
        print("[go] already present, skip")
        return
    meta = json.load(urllib.request.urlopen("https://go.dev/dl/?mode=json", timeout=30))
    rel = next(r for r in meta if not r.get("prerelease"))
    f = next(x for x in rel["files"] if x["os"] == "windows" and x["arch"] == "amd64" and x["kind"] == "archive")
    url = "https://dl.google.com/go/" + f["filename"]
    zip_path = os.path.join(TOOLS, f["filename"])
    ok = False
    try:
        download(url, zip_path)
        print(f"[go] extracting {f['filename']} ...")
        with zipfile.ZipFile(zip_path) as z:
            z.extractall(TOOLS)
        print(f"[go] OK: {go_exe} ({rel['version']})")
        ok = True
    finally:
        if ok and os.path.exists(zip_path):
            os.remove(zip_path)


def dl_ffmpeg():
    ff_dir = os.path.join(TOOLS, "ffmpeg")
    ff_exe = os.path.join(ff_dir, "ffmpeg.exe")
    if os.path.exists(ff_exe):
        print("[ffmpeg] already present, skip")
        return
    urls = [
        "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip",
        "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip",
    ]
    zip_path = os.path.join(TOOLS, "ffmpeg.zip")
    tmp = os.path.join(TOOLS, "ffmpeg_tmp")
    for url in urls:
        try:
            download(url, zip_path, timeout=600)
            break
        except Exception as e:
            print(f"[ffmpeg] download failed ({e}), trying next source")
            zip_path = None
    if not zip_path:
        print("[ffmpeg] all sources failed")
        return
    try:
        if os.path.exists(tmp):
            shutil.rmtree(tmp)
        print("[ffmpeg] extracting ...")
        with zipfile.ZipFile(zip_path) as z:
            z.extractall(tmp)
        os.makedirs(ff_dir, exist_ok=True)
        for name in ("ffmpeg.exe", "ffprobe.exe"):
            for root, _, files in os.walk(tmp):
                if name in files:
                    shutil.copy2(os.path.join(root, name), os.path.join(ff_dir, name))
                    print(f"[ffmpeg] copied {name}")
                    break
        print(f"[ffmpeg] OK: {ff_dir}")
        if os.path.exists(zip_path):
            os.remove(zip_path)
    finally:
        if os.path.exists(tmp):
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    with_ffmpeg = "--with-ffmpeg" in sys.argv
    dl_go()
    if with_ffmpeg:
        dl_ffmpeg()
    print("done")
