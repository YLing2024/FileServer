# 缩略图速度验收：大目录浏览时浏览器抽帧出图速度 + 播放不受影响
# 缩略图 100% 浏览器抽帧（3 路并发，Range 小读），验证首屏铺满速度。
import argparse, time, sys
from playwright.sync_api import sync_playwright

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8080")
    ap.add_argument("--dir", required=True)
    ap.add_argument("--video", required=True)
    ap.add_argument("--headless", action="store_true")
    args = ap.parse_args()

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=args.headless)
        page = browser.new_page(viewport={"width": 1440, "height": 900})

        t0 = time.perf_counter()
        page.goto(args.base + "/?path=" + args.dir, wait_until="domcontentloaded")
        page.wait_for_selector("#grid .card", timeout=20000)
        # 出图时间线：每 0.5s 采样一次可见缩略图数量与进行中抽帧数
        first_ok_t = None
        timeline = []
        for i in range(40):  # 20s
            n = page.locator('.card.kind-video img:not(.hidden)').count()
            if n > 0 and first_ok_t is None:
                first_ok_t = time.perf_counter() - t0
            g = page.evaluate("window.__thumbGrabs || 0")
            timeline.append((round(time.perf_counter() - t0, 1), n, g))
            if n >= 12:
                break
            time.sleep(0.5)
        browse_secs = time.perf_counter() - t0
        n_now = page.locator('.card.kind-video img:not(.hidden)').count()
        print(f"[1] 浏览 {round(browse_secs,1)}s: 首张缩略图 {first_ok_t and round(first_ok_t,1)}s, 当前可见 {n_now} 张")
        print(f"    时间线(秒:张:抽帧中): {[(t, n, g) for t, n, g in timeline[::4]]}")

        # 抽帧进行中点开视频：起播时间
        card = page.locator(".card", has_text=args.video).first
        card.scroll_into_view_if_needed()
        page.wait_for_timeout(300)
        card.click()
        page.wait_for_selector("#player video", timeout=15000)
        page.wait_for_selector("#bigPlay", timeout=5000)
        t_play_click = time.perf_counter()
        page.click("#bigPlay")
        started = False
        for _ in range(40):
            st = page.evaluate("""() => { const v = document.querySelector('#player video');
                return v ? {t: v.currentTime, p: v.paused} : {t: -1, p: true}; }""")
            if st["t"] > 0.5 and not st["p"]:
                started = True
                break
            time.sleep(0.5)
        print(f"[2] 抽帧进行中点开 {args.video}: 按播放键后起播 {started and round(time.perf_counter() - t_play_click, 1)}s")

        # 播放 12s 平滑度
        s0 = time.perf_counter()
        samples = []
        while time.perf_counter() - s0 < 12:
            t = page.evaluate("() => document.querySelector('#player video').currentTime")
            samples.append(t)
            time.sleep(1)
        advance = max(samples) - samples[0] if samples else 0
        print(f"[3] 播放 12s: 推进 {round(advance,1)}s")
        browser.close()

    fails = []
    if first_ok_t is None or first_ok_t > 10:
        fails.append(f"首张缩略图过慢: {first_ok_t and round(first_ok_t,1)}s")
    if n_now < 6:
        fails.append(f"可见缩略图不足: {n_now}")
    if not started:
        fails.append("视频未起播")
    if advance < 9:
        fails.append(f"播放推进不足: {round(advance,1)}s/12s")
    if fails:
        print("=== 缩略图速度验收失败 ===")
        for f in fails: print("  -", f)
        sys.exit(1)
    print("=== 缩略图速度验收通过：出图快、播放不受影响 ✓ ===")

if __name__ == "__main__":
    main()
