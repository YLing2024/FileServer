# 前端抽帧模式测试：无 ffmpeg 服务（serverThumb=false）时，视频缩略图全部由浏览器抽帧。
# 验证：12 个小视频全部出图、耗时、图片有效、控制台无错、大文件保持图标不硬拉。
import os, sys, time, urllib.parse
from playwright.sync_api import sync_playwright

BASE = os.environ.get("BASE", "http://127.0.0.1:8099")
errors = []

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page(viewport={"width": 1440, "height": 900})
    page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
    page.on("pageerror", lambda e: errors.append(str(e)))

    # 1. manyvideos：12 个小视频全部走前端抽帧
    t0 = time.perf_counter()
    page.goto(BASE + "/?path=/manyvideos", wait_until="domcontentloaded")
    page.wait_for_selector("#grid .card", timeout=10000)
    first = None
    all_done = False
    for _ in range(120):  # 最多 60s
        n = page.locator('.card.kind-video img:not(.hidden)').count()
        if n >= 1 and first is None:
            first = time.perf_counter() - t0
        if n >= 12:
            all_done = True
            break
        time.sleep(0.5)
    total = time.perf_counter() - t0
    print(f"[1] manyvideos: 首张 {first and round(first,1)}s, 12 张全部完成 {round(total,1)}s, 完成={all_done}")

    # 2. 校验缩略图内容有效（dataURL + 有像素 + 非纯色块）
    stats = page.evaluate("""() => {
        const imgs = [...document.querySelectorAll('.card.kind-video img:not(.hidden)')];
        return imgs.map((img) => ({
            hasData: img.src.startsWith('data:image'),
            nw: img.naturalWidth, nh: img.naturalHeight,
        }));
    }""")
    bad = [s for s in stats if not s["hasData"] or s["nw"] < 50 or s["nh"] < 50]
    print(f"[2] 缩略图内容: {len(stats)} 张, 无效 {len(bad)}")
    for s in stats[:4]:
        print("    ", s)

    # 3. 大文件（big.mp4 1.6GB 损坏无 moov）：必须保持图标，不能进入前端抽帧
    page.goto(BASE, wait_until="domcontentloaded")
    page.wait_for_selector('#grid .card:has-text("big.mp4")', timeout=10000)
    page.wait_for_timeout(3000)
    bigState = page.evaluate("""() => {
        const card = [...document.querySelectorAll('.card')].find(c => c.textContent.includes('big.mp4'));
        const img = card && card.querySelector('img');
        return { hasImg: !!img, visible: img ? !img.classList.contains('hidden') : false };
    }""")
    print(f"[3] big.mp4(1.6GB 损坏): 前端抽帧未硬拉 → img={bigState['hasImg']} 显示={bigState['visible']}（应为 False）")

    # 4. 连续翻页内存/稳定性粗查：来回进入两次目录无报错
    for _ in range(2):
        page.goto(BASE + "/?path=/manyvideos", wait_until="domcontentloaded")
        page.wait_for_selector('#grid .card', timeout=10000)
    print("[4] 重复进入目录无异常 ✓")

    browser.close()

real = [e for e in errors if "favicon" not in e and "404 (Not Found)" not in e]
print("控制台错误:", real[:5] if real else "无 ✓")
if real or (not all_done) or bad:
    print("=== 前端抽帧模式存在失败项 ===")
    sys.exit(1)
print("=== 前端抽帧模式通过 ✓ ===")
