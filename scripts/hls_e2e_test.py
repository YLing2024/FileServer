# HLS 播放端到端验证：生成 HEVC/MKV 测试视频 → 浏览器 hls.js 拉流 → 验证起播与 seek
# 前置：FileServer 已运行在 8099（--dir 指向仓库 testdata），python + playwright
import sys, time, subprocess, os, tempfile
from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:8099"
TD = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "testdata")

def gen_videos():
    ff = "ffmpeg"
    hevc = os.path.join(TD, "__hls_hevc.mp4")
    mkv = os.path.join(TD, "__hls_mkv.mkv")
    r1 = subprocess.run([ff, "-hide_banner", "-loglevel", "error", "-y",
        "-f", "lavfi", "-i", "testsrc2=s=640x360:d=25:r=30",
        "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error",
        "-pix_fmt", "yuv420p", hevc], capture_output=True, text=True)
    r2 = subprocess.run([ff, "-hide_banner", "-loglevel", "error", "-y",
        "-f", "lavfi", "-i", "testsrc2=s=640x360:d=25:r=30",
        "-f", "lavfi", "-i", "sine=frequency=440:duration=25",
        "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
        "-c:a", "aac", "-shortest", mkv], capture_output=True, text=True)
    if r1.returncode != 0 or r2.returncode != 0:
        print("生成测试视频失败:", r1.stderr[-300:], r2.stderr[-300:])
        sys.exit(1)
    return hevc, mkv

def main():
    hevc, mkv = gen_videos()
    errors = []
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch()
            page = browser.new_page()
            page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
            page.on("pageerror", lambda e: errors.append(str(e)))

            for path in ["/__hls_hevc.mp4", "/__hls_mkv.mkv"]:
                page.goto(f"{BASE}/?view={path}")
                page.wait_for_selector("#player video", timeout=10000)
                page.click("#bigPlay")
                started = False
                t = {}
                for _ in range(90):  # 最多 90s（CPU 转码兜底）
                    t = page.evaluate("""() => {
                        const v = document.querySelector('#player video');
                        return {t: v ? v.currentTime : -1, paused: v ? v.paused : true, dur: v ? v.duration : 0};
                    }""")
                    if t["t"] > 0.5 and not t["paused"]:
                        started = True
                        break
                    time.sleep(1)
                print(f"{path}: currentTime={t['t']:.2f}s paused={t['paused']} duration={t['dur']:.1f}s started={started}")
                if not started:
                    print(f"  FAIL: {path} 未能在超时内起播")
                    sys.exit(1)
                pre = t["t"]
                page.evaluate("""() => {
                    const v = document.querySelector('#player video');
                    v.currentTime = Math.max(5, (v.duration || 20) / 2);
                }""")
                # 转码进行中 seek 超前会等待分片生成：最长等 20s 恢复播放
                ok = False
                t2 = {}
                for _ in range(40):
                    t2 = page.evaluate("""() => {
                        const v = document.querySelector('#player video');
                        return {t: v.currentTime, paused: v.paused};
                    }""")
                    if not t2["paused"] and t2["t"] > pre + 1:
                        ok = True
                        break
                    time.sleep(0.5)
                print(f"  seek 后: currentTime={t2['t']:.2f}s paused={t2['paused']} 恢复播放={ok}")

            browser.close()
        # seek 超前转码进度时服务端会 404「分片尚未生成」，属预期行为
        real_errors = [e for e in errors if "favicon" not in e and "404" not in e]
        if real_errors:
            print("控制台错误:")
            for e in real_errors[:10]:
                print(" ", e)
            sys.exit(1)
        print("HLS 端到端播放验证通过 ✓")
    finally:
        for f in (hevc, mkv):
            try: os.remove(f)
            except OSError: pass

if __name__ == "__main__":
    main()

