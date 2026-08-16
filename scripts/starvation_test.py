# 播放饥饿验收测试：目录浏览（浏览器端抽帧）不得饿死用户点开的视频。
# 缩略图 100% 浏览器抽帧（preload=metadata + Range 小读，3 路并发）：
# 点开视频瞬间前端中止所有在途抽帧（__thumbGrabs→0）、暂停新任务，
# 返回列表后自动恢复。
#
# 用法（真实文件目录，headed 浏览器）：
#   python starvation_test.py --base http://127.0.0.1:8080 --dir /大目录 --video 目标视频关键词
# 可选 --headless（默认 headed，符合本机验收要求）
import argparse, time, sys
from playwright.sync_api import sync_playwright

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8080")
    ap.add_argument("--dir", required=True, help="大目录路径（含大量视频）")
    ap.add_argument("--video", required=True, help="目标视频文件名关键词")
    ap.add_argument("--headless", action="store_true")
    args = ap.parse_args()

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=args.headless)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        errors = []
        page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: errors.append(str(e)))

        # 1. 进入大目录并滚动，触发大量前端抽帧（3 路并发，Range 小读）
        t0 = time.perf_counter()
        page.goto(args.base + "/?path=" + args.dir, wait_until="domcontentloaded")
        page.wait_for_selector("#grid .card", timeout=20000)
        for _ in range(8):  # 约 8s 内滚到底再回顶，让观察者触发尽量多卡片
            page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            page.wait_for_timeout(1000)
        page.evaluate("window.scrollTo(0, 0)")
        page.wait_for_timeout(500)
        browse_secs = time.perf_counter() - t0
        grabs = page.evaluate("window.__thumbGrabs || 0")
        visible = page.locator('.card.kind-video img:not(.hidden)').count()
        print(f"[1] 浏览 {round(browse_secs,1)}s: 可见缩略图 {visible} 张, 进行中抽帧 {grabs} 个（3 路并发）")

        # 2. 抽帧正忙时点开目标视频（卡片 → 预览 → 大播放键，与真实操作一致）
        card = page.locator(".card", has_text=args.video).first
        card.scroll_into_view_if_needed()
        page.wait_for_timeout(300)
        card.click()
        page.wait_for_selector("#player video", timeout=15000)
        page.wait_for_selector("#bigPlay", timeout=5000)
        # 点开预览的瞬间：在途抽帧应被中止（播放优先）
        grabs_at_open = page.evaluate("window.__thumbGrabs || 0")
        page.evaluate("""() => {
            window.__stall = 0;
            const v = document.querySelector('#player video');
            v.addEventListener('waiting', () => window.__stall++);
        }""")
        t_play_click = time.perf_counter()
        page.click("#bigPlay")
        started = False
        t_play = None
        for _ in range(60):
            st = page.evaluate("""() => { const v = document.querySelector('#player video');
                return v ? {t: v.currentTime, p: v.paused} : {t: -1, p: true}; }""")
            if st["t"] > 0.5 and not st["p"]:
                started = True
                t_play = time.perf_counter()
                break
            time.sleep(0.5)
        click_to_play = (t_play - t_play_click) if started else None
        print(f"[2] 点开 {args.video}: 打开预览时在途抽帧 {grabs_at_open} 个（应为 0），按播放键后起播 {click_to_play and round(click_to_play,1)}s (started={started})")

        # 3. 播放 15s 无停顿；期间不得有新的抽帧任务
        reqs_grabs = []
        samples = []
        for _ in range(15):
            st = page.evaluate("""() => { const v = document.querySelector('#player video');
                return v ? {t: v.currentTime, p: v.paused} : {t: -1, p: true}; }""")
            samples.append(st["t"])
            reqs_grabs.append(page.evaluate("window.__thumbGrabs || 0"))
            time.sleep(1)
        new_reqs = max(reqs_grabs)
        advance = max(samples) - (samples[0] if samples else 0)
        stalls = page.evaluate("window.__stall || 0")
        print(f"[3] 播放 15s: 推进 {round(advance,1)}s, waiting 事件 {stalls} 次, 播放期间抽帧峰值 {new_reqs} 个（应为 0）")

        # 4. 返回列表：抽帧恢复（重新渲染的卡片自动重新入队）
        page.click("#btnBack")
        page.wait_for_selector("#grid .card", timeout=15000)
        t_back = time.time()
        resumed = 0
        while time.time() - t_back < 15:
            g = page.evaluate("window.__thumbGrabs || 0")
            if g > 0:
                resumed = 1
                break
            time.sleep(0.5)
        time.sleep(3)
        visible2 = page.locator('.card.kind-video img:not(.hidden)').count()
        print(f"[4] 返回列表: 抽帧恢复={bool(resumed)}, 可见缩略图 {visible2} 张")

        browser.close()

    real_errors = [e for e in errors if "favicon" not in e and "404 (Not Found)" not in e and "503" not in e]
    print("控制台错误:", real_errors[:5] if real_errors else "无 ✓")

    fails = []
    if not started or click_to_play is None or click_to_play > 15:
        fails.append(f"起播过慢/失败: {click_to_play and round(click_to_play,1)}s")
    if grabs_at_open > 0:
        fails.append(f"点开视频时仍有 {grabs_at_open} 个在途抽帧未中止")
    if advance < 10:
        fails.append(f"播放推进不足: {round(advance,1)}s/15s")
    if new_reqs > 0:
        fails.append(f"播放期间仍启动 {new_reqs} 个抽帧任务")
    if not resumed:
        fails.append("返回列表后抽帧未恢复")
    if real_errors:
        fails.append("控制台有错误")
    if fails:
        print("=== 播放饥饿验收失败 ===")
        for f in fails:
            print("  -", f)
        sys.exit(1)
    print("=== 播放饥饿验收通过：浏览抽帧不再饿死播放 ✓ ===")

if __name__ == "__main__":
    main()
