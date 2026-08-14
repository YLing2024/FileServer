"""FileServer 前端冒烟测试 — Playwright 真实浏览器验证"""
import sys
from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:8099"
errors = []


def main():
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("console", lambda m: errors.append(f"[console.{m.type}] {m.text}") if m.type in ("error", "warning") else None)
        page.on("pageerror", lambda e: errors.append(f"[pageerror] {e}"))

        # 1. 首页渲染
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector(".card", timeout=8000)
        cards = page.locator(".card").count()
        crumbs = page.locator(".crumb").count()
        print(f"[1] 首页: {cards} 个卡片, {crumbs} 个面包屑")
        assert cards >= 4, f"卡片数异常: {cards}"
        page.screenshot(path=".tools/shot-home.png")

        # 2. 主题切换
        page.click("#btnTheme")
        theme = page.evaluate("document.documentElement.dataset.theme")
        print(f"[2] 主题切换: {theme}")
        page.click("#btnTheme")

        # 3. 网格/列表切换
        page.click("#btnList")
        rows = page.locator("#listBody tr").count()
        print(f"[3] 列表视图: {rows} 行")
        assert rows >= 4, f"列表行数异常: {rows}"
        page.click("#btnGrid")

        # 4. 进入目录
        page.click('.card:has-text("photos")')
        page.wait_for_selector('.card:has-text("sample.jpg")', timeout=8000)
        img_ok = page.locator('.card:has-text("sample.jpg") img').count()
        print(f"[4] 进入 photos 目录: 图片缩略图 img 元素 {img_ok} 个")
        page.screenshot(path=".tools/shot-photos.png")

        # 5. 图片灯箱
        page.click('.card:has-text("sample.jpg")')
        page.wait_for_selector("#lightbox:not(.hidden)", timeout=5000)
        lb_visible = page.locator("#lightbox").is_visible()
        lb_name = page.locator("#lbName").text_content()
        print(f"[5] 灯箱: visible={lb_visible}, name={lb_name}")
        assert lb_visible and "sample" in (lb_name or "")
        # 灯箱键盘切换
        page.keyboard.press("ArrowRight")
        page.wait_for_timeout(600)
        lb_name2 = page.locator("#lbName").text_content()
        print(f"[5b] 灯箱切换后: {lb_name2}")
        page.keyboard.press("Escape")
        page.wait_for_timeout(400)
        assert page.locator("#lightbox").is_hidden(), "灯箱未关闭"
        print("[5c] 灯箱 ESC 关闭 OK")

        # 6. 视频预览页（自绘播放器）
        page.goto(BASE, wait_until="networkidle")
        page.click('.card:has(.card-name:text-is("videos"))')
        page.wait_for_timeout(800)
        page.click('.card:has-text("sample.mp4")')
        page.wait_for_selector("#player", timeout=8000)
        has_video = page.locator("#player video").count() == 1
        has_bar = page.locator("#playerBar").count() == 1
        print(f"[6] 视频播放器: video={has_video}, 控制条={has_bar}")
        assert has_video and has_bar
        # 播放
        page.click("#bigPlay")
        page.wait_for_timeout(1500)
        paused = page.evaluate("document.querySelector('#player video').paused")
        print(f"[6b] 播放后 paused={paused}")
        page.screenshot(path=".tools/shot-player.png")

        # 7. 返回浏览视图
        page.click("#btnBack")
        page.wait_for_timeout(600)
        print("[7] 返回浏览视图 OK")

        # 8. 搜索（先回根目录，从全量范围搜索）
        page.goto(BASE, wait_until="networkidle")
        page.fill("#searchInput", "sample")
        page.wait_for_timeout(1200)
        scards = page.locator(".card").count()
        print(f"[8] 搜索 sample: {scards} 个结果")
        assert scards >= 2
        page.click("#searchClear")
        page.wait_for_timeout(800)

        # 9. 视频缩略图（前端抽帧，网络空闲后检查是否有 img 替换）
        page.goto(BASE, wait_until="networkidle")
        page.click('.card:has(.card-name:text-is("videos"))')
        page.wait_for_timeout(4000)  # 等待 IntersectionObserver 触发抽帧
        vthumb_img = page.locator('.card:has-text("sample.mp4") img:not(.hidden)').count()
        print(f"[9] 视频前端抽帧: 可见 img {vthumb_img} 个")
        page.screenshot(path=".tools/shot-video-thumb.png")

        # 10. PDF/文本预览
        page.goto(BASE, wait_until="networkidle")
        page.click('.card:has-text("docs")')
        page.wait_for_timeout(800)
        page.click('.card:has-text("说明文档")')
        page.wait_for_selector(".pv-text", timeout=8000)
        txt = page.locator(".pv-text").text_content()
        print(f"[10] 文本预览: {len(txt)} 字符, 内容含'测试文档'={('测试文档' in txt)}")

        browser.close()

    print("\n=== 结果 ===")
    real_errors = [e for e in errors if "favicon" not in e and "net::ERR" not in e]
    if real_errors:
        print(f"发现 {len(real_errors)} 个控制台错误/警告:")
        for e in real_errors[:20]:
            print("  ", e)
        sys.exit(1)
    print("无控制台错误，全部通过 ✓")
    sys.exit(0)


if __name__ == "__main__":
    main()
