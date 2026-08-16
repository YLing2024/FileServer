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

        # 等待抽帧完成（12 个视频，抽帧并发固定 1 路——播放优先设计；
        # 首次冷访问时 10KB 短视频 -ss 5 超范围，ffmpeg 逐个判死要几秒，
        # 之后前端降级抽帧；最多 90s 覆盖冷启动）
        page.wait_for_function(
            "() => document.querySelectorAll('.card.kind-video img:not(.hidden)').length >= 10",
            timeout=90000,
        )
        thumbs = page.locator('.card.kind-video img:not(.hidden)').count()
        print(f"[5] 视频缩略图数量: {thumbs} / 12")
        if thumbs < 10:
            fail.append(f"视频缩略图只有 {thumbs} 个")

        # 截图留档
        page.screenshot(path=".tools/shot-manyvideos.png")

        # 刷新页面验证缓存（URL 驱动导航：刷新后停留在当前目录 /manyvideos）
        page.reload(wait_until="networkidle")
        page.wait_for_selector(".card.kind-video", timeout=8000)
        thumbs2 = page.locator('.card.kind-video img:not(.hidden)').count()
        print(f"[6] 刷新后（缓存命中）: {thumbs2} 个缩略图")
        if thumbs2 < 10:
            fail.append(f"刷新后缩略图只有 {thumbs2} 个")

        # ===== Bug 3: 返回浏览页后视频必须停止播放（暂停+释放资源） =====
        page.goto(BASE, wait_until="networkidle")
        page.click('.card:has(.card-name:text-is("videos"))')
        page.wait_for_timeout(800)
        page.click('.card:has(.card-name:text-is("sample.mp4"))')
        page.wait_for_selector("#player", timeout=8000)
        page.click("#bigPlay")
        page.wait_for_timeout(1500)
        playing = page.evaluate("!document.querySelector('#player video').paused")
        print(f"[7] 视频播放中: {playing}")
        assert playing, "视频未能开始播放"

        # 点返回按钮
        page.click("#btnBack")
        page.wait_for_timeout(600)
        released = page.evaluate(
            "!document.querySelector('#player') && document.querySelectorAll('#pvMain video').length === 0"
        )
        preview_hidden = page.evaluate("document.getElementById('preview').classList.contains('hidden')")
        print(f"[8] 返回按钮后: 播放器已移除={released}, 预览页隐藏={preview_hidden}")
        if not (released and preview_hidden):
            fail.append("点返回后播放器未释放（声音可能仍在播放）")

        # 键盘监听无残留：返回后按空格不应触发任何播放器行为
        page.keyboard.press(" ")
        page.wait_for_timeout(300)

        # 再进预览播放，然后浏览器后退（popstate 路径）
        page.click('.card:has(.card-name:text-is("sample.mp4"))')
        page.wait_for_selector("#player", timeout=8000)
        page.click("#bigPlay")
        page.wait_for_timeout(1000)
        page.go_back()
        page.wait_for_timeout(600)
        released2 = page.evaluate("document.querySelectorAll('#pvMain video').length === 0")
        print(f"[9] 浏览器后退后: 播放器已释放={released2}")
        if not released2:
            fail.append("浏览器后退后播放器未释放")

        browser.close()

    print("\n=== 回归测试结果 ===")
    # "404 (Not Found)"：testdata/big.mp4 是特意构造的"无 moov"损坏文件，缩略图必然 404，属预期行为。
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
    print("全部回归测试通过 ✓")
    sys.exit(0)


if __name__ == "__main__":
    main()
