# 移动端 UI 全面检查：溢出 / 面包屑 / 播放器控制条 / 搜索 / 列表视图 / 灯箱 / 触屏交互
import os, sys, time
from playwright.sync_api import sync_playwright

BASE = os.environ.get("BASE", "http://127.0.0.1:8099")
fail = []
errors = []

VIEWPORTS = [
    {"width": 390, "height": 844, "dpr": 2, "touch": True, "name": "iPhone14-ish"},
    {"width": 375, "height": 667, "dpr": 2, "touch": True, "name": "iPhoneSE-ish"},
    {"width": 768, "height": 1024, "dpr": 2, "touch": True, "name": "tablet"},
]


def check_overflow(page, label):
    ov = page.evaluate("() => ({sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth, st: document.documentElement.scrollTop})")
    if ov["sw"] > ov["cw"] + 1:
        fail.append(f"{label}: 页面横向溢出 scrollWidth={ov['sw']} clientWidth={ov['cw']}")


with sync_playwright() as p:
    for vp in VIEWPORTS:
        name = vp["name"]
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": vp["width"], "height": vp["height"]},
                                device_scale_factor=vp["dpr"], has_touch=vp["touch"], is_mobile=vp["touch"])
        page.on("console", lambda m: errors.append(f"{name}:{m.text}") if m.type == "error" else None)
        page.on("pageerror", lambda e: errors.append(f"{name}:{str(e)}"))

        # ---- 1. 首页：溢出 + 工具栏 + 网格列数 ----
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector("#grid .card", timeout=10000)
        check_overflow(page, f"{name} 首页")
        boxes = [page.locator(".card").nth(i).bounding_box() for i in range(min(6, page.locator(".card").count()))]
        boxes = [b for b in boxes if b]
        row1 = [b for b in boxes if abs(b["y"] - boxes[0]["y"]) < 10]
        cols = len(row1)
        print(f"[{name}] 首页卡片列数={cols}（应>=2）")
        if cols < 2:
            fail.append(f"{name}: 网格只有 {cols} 列")
        # 工具栏按钮都在视口内
        for sel in ["#btnHome", "#btnRefresh", "#btnGrid", "#btnList", "#searchInput", "#btnTheme"]:
            el = page.locator(sel)
            if el.count() == 0:
                fail.append(f"{name}: 缺少工具栏元素 {sel}")
                continue
            bb = el.first.bounding_box()
            if bb and (bb["x"] < 0 or bb["x"] + bb["width"] > vp["width"]):
                fail.append(f"{name}: 工具栏元素 {sel} 超出视口 x={bb['x']} w={bb['width']}")

        # ---- 2. 面包屑（三层目录） ----
        page.click('.card:has-text("deep")')
        page.wait_for_selector('.card:has-text("a")', timeout=8000)
        page.click('.card:has-text("a")')
        page.wait_for_selector('.card:has-text("b")', timeout=8000)
        page.click('.card:has-text("b")')
        page.wait_for_selector('.card:has-text("file.txt")', timeout=8000)
        check_overflow(page, f"{name} 三层目录")
        crumbs = page.locator(".crumb").all_text_contents()
        print(f"[{name}] 面包屑: {crumbs}")
        # 面包屑可点中间层
        page.locator('.crumb:has-text("a")').click()
        page.wait_for_timeout(500)
        names = page.locator(".card-name").all_text_contents()
        if "b" not in names:
            fail.append(f"{name}: 面包屑点击 a 未回到目录")

        # ---- 3. manyvideos 播放器控制条 ----
        page.goto(BASE + "/?path=/manyvideos", wait_until="networkidle")
        page.wait_for_selector('#grid .card:has-text("v01.mp4")', timeout=10000)
        page.tap('.card:has-text("v01.mp4")')
        page.wait_for_selector("#player video", timeout=10000)
        page.tap("#bigPlay")
        page.wait_for_timeout(1500)
        playing = page.evaluate("!document.querySelector('#player video').paused")
        print(f"[{name}] 视频播放中: {playing}")
        if not playing:
            fail.append(f"{name}: 视频未能播放")
        # 控制条元素位置
        bar = page.evaluate("""() => {
            const r = {};
            for (const id of ['pbPlay','pbProgress','pbTime','pbVol','pbFull']) {
                const el = document.getElementById(id);
                if (!el) { r[id] = null; continue; }
                const b = el.getBoundingClientRect();
                const cs = getComputedStyle(el);
                r[id] = {x: Math.round(b.x), w: Math.round(b.width), y: Math.round(b.y), h: Math.round(b.height), display: cs.display, vis: cs.visibility};
            }
            return r;
        }""")
        print(f"[{name}] 控制条: {bar}")
        pw = page.evaluate("document.getElementById('player').clientWidth")
        for k, v in bar.items():
            if v is None:
                continue
            if v["display"] == "none" or v["vis"] == "hidden":
                continue
            if v["x"] < 0 or v["x"] + v["w"] > pw + 1:
                fail.append(f"{name}: 控制条 {k} 超出播放器 x={v['x']} w={v['w']} pw={pw}")
        # 音量条在触屏下应可见（hover:none 规则）
        volVis = page.evaluate("getComputedStyle(document.getElementById('pbVol')).opacity")
        print(f"[{name}] 音量条 opacity={volVis}（触屏应常显）")
        # 进度条宽度：窄屏下不能被其他控件挤成一条线（修复点）
        progW = bar.get("pbProgress", {}).get("w", 0)
        print(f"[{name}] 进度条宽度={progW}px")
        if progW and progW < 100:
            fail.append(f"{name}: 进度条过窄 {progW}px（<100）")
        # 返回
        page.tap("#btnBack")
        page.wait_for_timeout(600)
        if page.locator("#player").count() != 0:
            fail.append(f"{name}: 返回后播放器未移除")

        # ---- 4. 搜索框：聚焦/输入/清空，布局不破 ----
        page.goto(BASE, wait_until="networkidle")  # 先回到干净的浏览页
        page.wait_for_selector("#grid .card", timeout=10000)
        sb = page.locator("#searchInput")
        sb.tap()
        page.wait_for_timeout(300)
        check_overflow(page, f"{name} 搜索聚焦")
        sb.fill("file")
        page.wait_for_timeout(800)
        results = page.locator("#grid .card").count()
        print(f"[{name}] 搜索'file'结果: {results} 条")
        if results < 1:
            fail.append(f"{name}: 搜索无结果")
        page.locator("#searchClear").tap()
        page.wait_for_timeout(600)
        check_overflow(page, f"{name} 搜索清空")

        # ---- 5. 列表视图（行是 table <tr>） ----
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector("#grid .card", timeout=10000)
        page.tap("#btnList")
        page.wait_for_timeout(500)
        check_overflow(page, f"{name} 列表视图")
        rows = page.locator("#listView #listBody tr").count()
        print(f"[{name}] 列表行数={rows}")
        if rows < 1:
            fail.append(f"{name}: 列表视图无行")
        page.tap("#btnGrid")  # 切回网格，避免影响后续步骤
        page.wait_for_timeout(400)

        # ---- 6. 灯箱（触屏滑动已在 mobile_test 覆盖，这里验证打开/关闭） ----
        page.goto(BASE + "/?path=/photos", wait_until="networkidle")
        page.wait_for_selector('.card:has-text("sample.jpg")', timeout=10000)
        page.tap('.card:has-text("sample.jpg")')
        page.wait_for_selector("#lightbox:not(.hidden)", timeout=5000)
        check_overflow(page, f"{name} 灯箱")
        page.keyboard.press("Escape")
        page.wait_for_timeout(400)
        if not page.locator("#lightbox.hidden").count():
            fail.append(f"{name}: 灯箱未关闭")

        # ---- 6b. 预览页返回按钮（btnBack → history.back → popstate） ----
        page.goto(BASE + "/?path=/videos", wait_until="networkidle")
        page.wait_for_selector('.card:has-text("sample.mp4")', timeout=10000)
        page.tap('.card:has-text("sample.mp4")')
        page.wait_for_selector("#player", timeout=10000)
        page.tap("#btnBack")
        page.wait_for_timeout(800)
        back_ok = page.evaluate("() => document.getElementById('player') === null && document.getElementById('browse') && !document.getElementById('browse').classList.contains('hidden')")
        print(f"[{name}] btnBack 返回列表: {back_ok}")
        if not back_ok:
            fail.append(f"{name}: btnBack 未返回列表")

        # ---- 7. 长文件名省略号（computed style） ----
        ell = page.evaluate("""() => {
            const n = document.querySelector('.card-name');
            const cs = n ? getComputedStyle(n) : null;
            return cs ? {overflow: cs.overflow, ellipsis: cs.textOverflow, nowrap: cs.whiteSpace} : null;
        }""")
        print(f"[{name}] card-name CSS: {ell}")
        if not (ell and ell["overflow"] == "hidden" and ell["ellipsis"] == "ellipsis"):
            fail.append(f"{name}: 卡片名缺少省略号样式")

        browser.close()

print("\n=== 移动端 UI 检查结果 ===")
real = [e for e in errors if "favicon" not in e and "404 (Not Found)" not in e]
if real:
    print("控制台错误:")
    for e in real[:10]:
        print("  ", e)
if fail:
    print("失败项:")
    for f in fail:
        print("  ✗", f)
    sys.exit(1)
print("全部通过 ✓")
