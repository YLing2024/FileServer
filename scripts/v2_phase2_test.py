#!/usr/bin/env python3
"""Phase 2 验收：根路径预览路由、图片灯箱、视频播放器与移动端手势。

先启动服务（示例，HLS 用例需要 --ffmpeg）：
    go run ./cmd/fileserver --dir testdata --host 127.0.0.1 --port 18080 --no-qr --ffmpeg
再运行：
    /root/.venv/cdp/bin/python scripts/v2_phase2_test.py

脚本只读页面；HLS 用例会在 testdata 下临时生成 __hls_phase2.mkv（已被 .gitignore
忽略），结束后删除自己创建的文件。没有 ffmpeg 或服务端未开启转码时跳过 HLS
并打印 SKIP 原因。
"""

import os
import shutil
import subprocess
import sys
import urllib.request

from playwright.sync_api import expect, sync_playwright

BASE = os.environ.get("FILESERVER_BASE", "http://127.0.0.1:18080")
LIGHTBOX_SHOT = os.environ.get(
    "V2_LIGHTBOX_SHOT", "/tmp/fileserver-v2-phase2-lightbox.png"
)
PLAYER_SHOT = os.environ.get("V2_PLAYER_SHOT", "/tmp/fileserver-v2-phase2-player.png")
MOBILE_SHOT = os.environ.get("V2_MOBILE_PLAYER_SHOT", "/tmp/fileserver-v2-phase2-375.png")
HLS_FIXTURE = os.path.join("testdata", "__hls_phase2.mkv")

TOUCH_JS = """
({type, x, y, id}) => {
  const target = document.querySelector('[data-testid="video-element"]');
  const touch = new Touch({identifier: id || 1, target, clientX: x, clientY: y});
  const active = (type === 'touchend' || type === 'touchcancel') ? [] : [touch];
  target.dispatchEvent(new TouchEvent(type, {
    bubbles: true, cancelable: true, composed: true,
    touches: active, targetTouches: active, changedTouches: [touch],
  }));
}
"""


def touch(page, kind: str, x: float, y: float, identifier: int = 1) -> None:
    page.evaluate(TOUCH_JS, {"type": kind, "x": x, "y": y, "id": identifier})


def server_capabilities() -> dict:
    with urllib.request.urlopen(f"{BASE}/api/info", timeout=5) as response:
        import json

        return json.loads(response.read().decode("utf-8"))


def ensure_hls_fixture(created: list[str]) -> str | None:
    if os.path.exists(HLS_FIXTURE):
        return HLS_FIXTURE
    ffmpeg = shutil.which("ffmpeg")
    if not ffmpeg:
        return None
    os.makedirs("testdata", exist_ok=True)
    subprocess.run(
        [
            ffmpeg,
            "-y",
            "-loglevel",
            "error",
            "-i",
            "testdata/videos/sample.mp4",
            "-c",
            "copy",
            HLS_FIXTURE,
        ],
        check=True,
    )
    created.append(HLS_FIXTURE)
    return HLS_FIXTURE


def main() -> int:
    errors: list[str] = []
    warnings: list[str] = []
    skipped: list[str] = []
    created: list[str] = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.on(
            "console",
            lambda message: errors.append(message.text)
            if message.type == "error"
            else None,
        )

        # 1) 图片灯箱：原图、缩放、切图、URL 同步、刷新恢复
        page.goto(f"{BASE}/?path=photos", wait_until="networkidle")
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        page.locator(
            '[data-testid="entry-card"][data-entry-name="sample.jpg"]'
        ).click()
        page.wait_for_selector('[data-testid="lightbox"]', timeout=10000)
        assert "view=photos%2Fsample.jpg" in page.url, page.url
        src = page.get_attribute('[data-testid="lightbox-image"]', "src")
        assert src and "/api/file?path=photos%2Fsample.jpg" in src, src
        page.wait_for_function(
            """() => {
              const img = document.querySelector('[data-testid="lightbox-image"]');
              return !!img && getComputedStyle(img).visibility === 'visible';
            }""",
            timeout=10000,
        )
        page.mouse.move(640, 450)
        page.mouse.wheel(0, -100)
        page.wait_for_timeout(150)
        zoom = page.locator('[data-testid="lightbox"] span[aria-label^="缩放"]').inner_text()
        assert zoom != "100%", zoom
        page.screenshot(path=LIGHTBOX_SHOT)
        print(f"PASS 图片灯箱：原图加载，滚轮缩放后 {zoom}")

        page.keyboard.press("ArrowRight")
        page.wait_for_timeout(300)
        expect(page.locator('[data-testid="lightbox"]')).to_contain_text("blue.png")
        assert "view=photos%2Fblue.png" in page.url, page.url
        print("PASS 灯箱 ←/→ 切图：sample.jpg -> blue.png，URL 同步")

        page.reload(wait_until="networkidle")
        page.wait_for_selector('[data-testid="lightbox"]', timeout=10000)
        expect(page.locator('[data-testid="lightbox"]')).to_contain_text("blue.png")
        print("PASS 刷新后按 ?view= 恢复灯箱当前图片")

        page.keyboard.press("Escape")
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        assert "view=" not in page.url, page.url
        print("PASS Esc 退出灯箱并回到浏览视图")

        # 2) 文本预览
        page.goto(f"{BASE}/?view=README.txt", wait_until="networkidle")
        pre = page.wait_for_selector('[data-testid="text-preview"]', timeout=10000)
        assert "FileServer" in pre.inner_text()
        assert "monospace" in pre.evaluate("el => getComputedStyle(el).fontFamily")
        print("PASS 文本预览：等宽只读显示 README.txt")
        page.keyboard.press("Escape")
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)

        # 3) 视频播放：进度前进、空格暂停、画中画、离开释放
        page.goto(f"{BASE}/?view=videos%2Fsample.mp4", wait_until="networkidle")
        page.wait_for_selector('[data-testid="video-player"]', timeout=10000)
        page.locator('button[aria-label="播放"]').first.click()
        page.wait_for_function(
            "() => document.querySelector('video').currentTime > 0.2", timeout=10000
        )
        page.wait_for_timeout(700)
        current = page.evaluate("document.querySelector('video').currentTime")
        assert current > 0.2, current
        page.keyboard.press("Space")
        page.wait_for_timeout(300)
        assert page.evaluate("document.querySelector('video').paused") is True
        print(f"PASS 视频播放：进度前进到 {current:.2f}s，空格暂停")

        pip_button = page.locator('button[aria-label*="画中画"]')
        if pip_button.count() > 0:
            pip_button.first.click()
            page.wait_for_function(
                "() => Boolean(document.pictureInPictureElement)", timeout=5000
            )
            print("PASS 画中画：进入 PiP")
            page.evaluate(
                "() => document.exitPictureInPicture().catch(() => {})"
            )
            page.wait_for_timeout(300)
        else:
            skipped.append("画中画：当前浏览器未提供 PiP 能力")
            print("SKIP 画中画：当前浏览器未提供 PiP 能力")

        page.screenshot(path=PLAYER_SHOT)
        page.keyboard.press("Escape")
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        print("PASS 离开预览：浏览器返回浏览视图")

        # 4) 375×780 移动端手势
        mobile = browser.new_page(
            viewport={"width": 375, "height": 780}, has_touch=True, is_mobile=True
        )
        mobile.on("pageerror", lambda error: errors.append(str(error)))
        mobile.goto(f"{BASE}/?view=videos%2Fsample.mp4", wait_until="networkidle")
        mobile.wait_for_selector('[data-testid="video-player"]', timeout=10000)
        mobile.wait_for_timeout(500)
        mobile.locator('button[aria-label="播放"]').first.click()
        mobile.wait_for_timeout(500)
        box = mobile.locator('[data-testid="video-element"]').bounding_box()
        assert box, "拿不到视频尺寸"
        left, top, width, height = box["x"], box["y"], box["width"], box["height"]

        before = mobile.evaluate("document.querySelector('video').currentTime")
        x = left + width * 0.85
        y = top + height * 0.4
        for _ in range(2):
            touch(mobile, "touchstart", x, y, 1)
            touch(mobile, "touchend", x, y, 1)
            mobile.wait_for_timeout(100)
        mobile.wait_for_timeout(250)
        after = mobile.evaluate("document.querySelector('video').currentTime")
        assert after > before, (before, after)
        print(f"PASS 双击右侧 +10s：{before:.2f}s -> {after:.2f}s")

        rate_before = mobile.evaluate("document.querySelector('video').playbackRate")
        touch(mobile, "touchstart", left + width / 2, top + height / 2, 2)
        mobile.wait_for_timeout(500)
        during = mobile.evaluate("document.querySelector('video').playbackRate")
        assert during == 2, during
        touch(mobile, "touchend", left + width / 2, top + height / 2, 2)
        mobile.wait_for_timeout(150)
        restored = mobile.evaluate("document.querySelector('video').playbackRate")
        assert restored == rate_before, (rate_before, restored)
        print(f"PASS 长按 2x：{rate_before}x -> {during}x -> {restored}x")

        mobile.evaluate("document.querySelector('video').currentTime = 4")
        mobile.wait_for_timeout(200)
        sx = left + width * 0.82
        sy = top + height * 0.5
        touch(mobile, "touchstart", sx, sy, 3)
        for delta in (-20, -50, -90, -120):
            touch(mobile, "touchmove", sx + delta, sy, 3)
            mobile.wait_for_timeout(30)
        touch(mobile, "touchend", sx - 120, sy, 3)
        mobile.wait_for_timeout(250)
        seeked = mobile.evaluate("document.querySelector('video').currentTime")
        assert seeked < 3.5, seeked
        print(f"PASS 横滑 seek：4.00s -> {seeked:.2f}s")

        volume_before = mobile.evaluate("document.querySelector('video').volume")
        vx = left + width * 0.9
        vy = top + height * 0.3
        touch(mobile, "touchstart", vx, vy, 4)
        for delta in (20, 50, 90):
            touch(mobile, "touchmove", vx, vy + delta, 4)
            mobile.wait_for_timeout(30)
        touch(mobile, "touchend", vx, vy + 90, 4)
        mobile.wait_for_timeout(250)
        volume_after = mobile.evaluate("document.querySelector('video').volume")
        assert volume_after < volume_before, (volume_before, volume_after)
        print(f"PASS 右侧竖滑音量：{volume_before:.2f} -> {volume_after:.2f}")

        viewport = mobile.evaluate(
            "() => ({scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth})"
        )
        assert viewport["scrollWidth"] <= viewport["clientWidth"] + 1, viewport
        mobile.screenshot(path=MOBILE_SHOT)
        print(
            "PASS 375×780 播放器无横向溢出 "
            f"scrollWidth={viewport['scrollWidth']} clientWidth={viewport['clientWidth']}"
        )
        mobile.close()

        # 5) HLS：离开预览必须请求 abandon=1（无 ffmpeg/未开启转码时跳过）
        capabilities = server_capabilities()
        fixture = None
        if capabilities.get("ffmpeg") and capabilities.get("hls"):
            fixture = ensure_hls_fixture(created)
        if not capabilities.get("ffmpeg") or not capabilities.get("hls"):
            skipped.append("HLS：服务端未开启 ffmpeg/转码（/api/info hls=false）")
            print("SKIP HLS：服务端未开启 ffmpeg/转码（/api/info hls=false）")
        elif fixture is None:
            skipped.append("HLS：本机没有 ffmpeg，无法生成 MKV 样本")
            print("SKIP HLS：本机没有 ffmpeg，无法生成 MKV 样本")
        else:
            hls_page = browser.new_page(viewport={"width": 1280, "height": 900})
            hls_errors: list[str] = []
            hls_page.on("pageerror", lambda error: hls_errors.append(str(error)))
            hls_page.goto(f"{BASE}/?path=", wait_until="networkidle")
            hls_page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
            hls_page.goto(
                f"{BASE}/?view={os.path.basename(fixture)}", wait_until="networkidle"
            )
            hls_page.wait_for_selector('[data-testid="video-player"]', timeout=10000)
            hls_page.wait_for_function(
                "() => document.querySelector('[data-testid=video-player]').dataset.mode === 'hls'",
                timeout=15000,
            )
            with hls_page.expect_request(
                lambda request: "abandon=1" in request.url, timeout=8000
            ):
                hls_page.keyboard.press("Escape")
            hls_page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
            assert not hls_errors, hls_errors
            print("PASS HLS：离开预览触发 /api/hls?…&abandon=1")
            hls_page.close()

        # 6) 滚动位置：进入预览再返回保持不变
        scroll_page = browser.new_page(viewport={"width": 375, "height": 780})
        scroll_page.goto(f"{BASE}/?path=manyvideos", wait_until="networkidle")
        scroll_page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        scroll_page.evaluate("window.scrollTo(0, 600)")
        scroll_page.wait_for_timeout(200)
        before_scroll = scroll_page.evaluate("window.scrollY")
        scroll_page.locator(
            '[data-testid="entry-card"][data-entry-name="v12.mp4"]'
        ).click()
        scroll_page.wait_for_selector('[data-testid="video-player"]', timeout=10000)
        scroll_page.keyboard.press("Escape")
        scroll_page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        scroll_page.wait_for_timeout(400)
        after_scroll = scroll_page.evaluate("window.scrollY")
        assert abs(after_scroll - before_scroll) <= 2, (before_scroll, after_scroll)
        print(f"PASS 返回浏览保留滚动位置：{before_scroll} -> {after_scroll}")
        scroll_page.close()

        browser.close()

    for path in created:
        try:
            os.remove(path)
        except OSError:
            pass

    if warnings:
        print("WARN", warnings, file=sys.stderr)
    if errors:
        print("页面运行期错误:", errors, file=sys.stderr)
        return 1
    print(f"截图: {LIGHTBOX_SHOT}")
    print(f"截图: {PLAYER_SHOT}")
    print(f"截图: {MOBILE_SHOT}")
    if skipped:
        print("跳过项: " + "；".join(skipped))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
