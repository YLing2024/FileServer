"""FileServer 移动端布局 + zip 下载测试"""
import sys
from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:8099"
errors = []


def main():
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True)
        page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: errors.append(str(e)))

        # 1. 手机视口首页
        page.goto(BASE, wait_until="networkidle")
        page.wait_for_selector(".card", timeout=8000)
        cards = page.locator(".card").count()
        # 计算第一行卡片的 Y 坐标，判断网格列数
        boxes = [page.locator(".card").nth(i).bounding_box() for i in range(min(4, cards))]
        row1 = [b for b in boxes if b and abs(b["y"] - boxes[0]["y"]) < 10]
        print(f"[1] 手机首页: {cards} 卡片, 第一行 {len(row1)} 列")
        assert len(row1) >= 2, f"手机网格列数异常: {len(row1)}"
        page.screenshot(path=".tools/shot-mobile.png")

        # 2. 手机进入目录 + 图片灯箱（触屏模拟）
        page.click('.card:has-text("photos")')
        page.wait_for_selector('.card:has-text("sample.jpg")', timeout=8000)
        page.tap('.card:has-text("sample.jpg")')
        page.wait_for_selector("#lightbox:not(.hidden)", timeout=5000)
        # 触屏滑动切换（派发真实 TouchEvent）
        page.evaluate("""() => {
          const lb = document.getElementById('lightbox');
          const t1 = new Touch({identifier: 1, target: lb, clientX: 100, clientY: 400});
          const t2 = new Touch({identifier: 1, target: lb, clientX: 260, clientY: 400});
          lb.dispatchEvent(new TouchEvent('touchstart', {bubbles: true, touches: [t1], changedTouches: [t1], targetTouches: [t1]}));
          lb.dispatchEvent(new TouchEvent('touchend', {bubbles: true, touches: [], changedTouches: [t2], targetTouches: []}));
        }""")
        page.wait_for_timeout(600)
        name = page.locator("#lbName").text_content()
        print(f"[2] 手机灯箱触屏切换: {name}")
        assert "blue" in (name or "")
        page.keyboard.press("Escape")

        # 3. 手机视频预览
        page.goto(BASE, wait_until="networkidle")
        page.tap('.card:has(.card-name:text-is("videos"))')
        page.wait_for_timeout(800)
        page.tap('.card:has-text("sample.mp4")')
        page.wait_for_selector("#player", timeout=8000)
        page.tap("#bigPlay")
        page.wait_for_timeout(1200)
        paused = page.evaluate("document.querySelector('#player video').paused")
        print(f"[3] 手机播放器: paused={paused}")
        assert paused is False

        # 4. zip 下载（目录打包）
        page.goto(BASE, wait_until="networkidle")
        with page.expect_download(timeout=10000) as dl_info:
            page.hover('.card:has-text("docs")')
            page.click('.card:has-text("docs") .card-dl')
        dl = dl_info.value
        print(f"[4] zip 下载: {dl.suggested_filename}")
        assert dl.suggested_filename.endswith(".zip")

        browser.close()

    print("\n=== 结果 ===")
    # "404 (Not Found)"：testdata/big.mp4 是特意构造的"无 moov"损坏文件，缩略图必然 404，
    # 前端已正确降级，属预期行为而非回归。
    real = [e for e in errors if "favicon" not in e and "404 (Not Found)" not in e]
    if real:
        for e in real[:10]:
            print("  ", e)
        sys.exit(1)
    print("移动端 + zip 测试全部通过 ✓")
    sys.exit(0)


if __name__ == "__main__":
    main()
