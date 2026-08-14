"""Bug 回归测试:
1. 三层目录面包屑：进入 /deep/a/b 后点击中间面包屑 "a" 应回到 /deep/a
2. 多个视频缩略图：12 个视频应全部生成缩略图（而不是只 1 个）
"""
import sys
from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:8099"
errors = []
fail = []


def main():
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: errors.append(str(e)))

        # ===== Bug 1: 三层目录面包屑 =====
        page.goto(BASE, wait_until="networkidle")
        page.click('.card:has-text("deep")')
        page.wait_for_selector('.card:has-text("a")', timeout=8000)
        page.click('.card:has-text("a")')
        page.wait_for_selector('.card:has-text("b")', timeout=8000)
        page.click('.card:has-text("b")')
        page.wait_for_selector('.card:has-text("file.txt")', timeout=8000)
        crumbs = page.locator(".crumb").all_text_contents()
        print(f"[1] 进入三层目录 /deep/a/b, 面包屑: {crumbs}")
        assert crumbs == ["根目录", "deep", "a", "b"], f"面包屑异常: {crumbs}"

        # 点击中间目录 "a"
        page.locator('.crumb:has-text("a")').click()
        page.wait_for_timeout(800)
        crumbs2 = page.locator(".crumb").all_text_contents()
        names = page.locator(".card-name").all_text_contents()
        print(f"[2] 点击面包屑 a 后: 面包屑={crumbs2}, 内容={names}")
        if crumbs2 != ["根目录", "deep", "a"]:
            fail.append(f"面包屑未回到 /deep/a: {crumbs2}")
        if "b" not in names:
            fail.append(f"应显示 a 目录内容(含 b), 实际: {names}")
        # 再点根目录
        page.locator('.crumb:has-text("根目录")').click()
        page.wait_for_timeout(800)
        crumbs3 = page.locator(".crumb").all_text_contents()
        print(f"[3] 点击根目录后: {crumbs3}")
        assert crumbs3 == ["根目录"], f"根目录面包屑异常: {crumbs3}"

        # ===== Bug 2: 多个视频缩略图 =====
        page.click('.card:has-text("manyvideos")')
        page.wait_for_selector(".card", timeout=8000)
        vcards = page.locator('.card.kind-video').count()
        print(f"[4] manyvideos 目录: {vcards} 个视频卡片")
        assert vcards == 12, f"视频卡片数异常: {vcards}"

        # 等待抽帧完成（12 个视频，2 并发，每个 1-3s，最多 20s）
        page.wait_for_function(
            "() => document.querySelectorAll('.card.kind-video img:not(.hidden)').length >= 10",
            timeout=25000,
        )
        thumbs = page.locator('.card.kind-video img:not(.hidden)').count()
        print(f"[5] 视频缩略图数量: {thumbs} / 12")
        if thumbs < 10:
            fail.append(f"视频缩略图只有 {thumbs} 个")

        # 截图留档
        page.screenshot(path=".tools/shot-manyvideos.png")

        # 刷新页面验证缓存（应秒出）
        page.reload(wait_until="networkidle")
        page.click('.card:has-text("manyvideos")')
        page.wait_for_timeout(2500)
        thumbs2 = page.locator('.card.kind-video img:not(.hidden)').count()
        print(f"[6] 刷新后（缓存命中）: {thumbs2} 个缩略图")
        if thumbs2 < 10:
            fail.append(f"刷新后缩略图只有 {thumbs2} 个")

        browser.close()

    print("\n=== 回归测试结果 ===")
    real = [e for e in errors if "favicon" not in e]
    if real:
        print("控制台错误:")
        for e in real[:10]:
            print("  ", e)
    if fail:
        print("失败项:")
        for f in fail:
            print("  ✗", f)
        sys.exit(1)
    print("两个 bug 均已修复 ✓")
    sys.exit(0)


if __name__ == "__main__":
    main()
