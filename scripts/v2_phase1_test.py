#!/usr/bin/env python3
"""Phase 1 验收：根路径浏览视图（工具栏/网格/列表/搜索/排序/分页/缩略图/窄屏）。

先启动服务（示例）：
    go run ./cmd/fileserver --dir testdata --host 127.0.0.1 --port 18080
再运行：
    /root/.venv/cdp/bin/python scripts/v2_phase1_test.py
可用 FILESERVER_BASE 覆盖地址；脚本只读页面，不改任何文件。
"""

import os
import re
import sys

from playwright.sync_api import expect, sync_playwright

BASE = os.environ.get("FILESERVER_BASE", "http://127.0.0.1:18080")
GRID_SHOT = os.environ.get("V2_GRID_SHOT", "/tmp/fileserver-v2-phase1-grid.png")
LIST_SHOT = os.environ.get("V2_LIST_SHOT", "/tmp/fileserver-v2-phase1-list.png")
MOBILE_SHOT = os.environ.get("V2_MOBILE_SHOT", "/tmp/fileserver-v2-phase1-375.png")

ROOT_NAMES = {"README.txt", "deep", "docs", "manyvideos", "photos", "special", "videos"}
# 服务端排序：目录永远在最前，组内按名称升序。
ROOT_ORDER = ["deep", "docs", "manyvideos", "photos", "special", "videos", "README.txt"]


def card_names(page) -> list[str]:
    return page.eval_on_selector_all(
        '[data-testid="entry-card"]',
        "els => els.map(el => el.dataset.entryName || '')",
    )


def wait_names(page, expected: list[str], timeout: int = 8000) -> None:
    page.wait_for_function(
        """expected => {
          const names = Array.from(document.querySelectorAll('[data-testid="entry-card"]'))
            .map(el => el.dataset.entryName || '');
          return names.length === expected.length && names.every((name, i) => name === expected[i]);
        }""",
        arg=expected,
        timeout=timeout,
    )


def main() -> int:
    errors: list[str] = []
    console_errors: list[str] = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.on(
            "console",
            lambda message: console_errors.append(message.text)
            if message.type == "error"
            else None,
        )

        # 1) 根目录 7 项
        page.goto(f"{BASE}/", wait_until="networkidle")
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        names = card_names(page)
        assert len(names) == 7, f"根目录应为 7 项，实际 {len(names)}: {names}"
        assert set(names) == ROOT_NAMES, f"根目录条目不符: {names}"
        expect(page.get_by_role("heading", name="/", exact=True)).to_be_visible()
        page.screenshot(path=GRID_SHOT, full_page=True)
        print(f"PASS 根目录 {len(names)} 项: {', '.join(names)}")

        # 2) 进入 photos 得 2 项
        page.locator('[data-testid="entry-card"][data-entry-name="photos"]').click()
        expect(page).to_have_url(re.compile(r"\?path=photos$"))
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        expect(page.get_by_role("heading", name="/photos", exact=True)).to_be_visible()
        wait_names(page, ["blue.png", "sample.jpg"])
        print(f"PASS 进入 photos: {', '.join(card_names(page))}")

        # 3) 图片缩略图加载出 /api/thumb 的 src
        page.wait_for_function(
            """() => Array.from(document.querySelectorAll('img[data-testid="thumb-image"]'))
              .some(img => img.src.includes('/api/thumb'))""",
            timeout=10000,
        )
        thumb_srcs = page.eval_on_selector_all(
            'img[data-testid="thumb-image"]', "els => els.map(el => el.src)"
        )
        assert len(thumb_srcs) == 2, f"photos 应有 2 张图片缩略图，实际 {len(thumb_srcs)}"
        print(f"PASS 图片缩略图 {len(thumb_srcs)} 张: {thumb_srcs[0][:80]}…")

        # 4) 排序生效（photos 两个文件按大小升/降序互换）
        page.select_option('select[aria-label="排序方式"]', "size")
        wait_names(page, ["blue.png", "sample.jpg"])
        page.locator('button[aria-label*="升序"]').click()
        wait_names(page, ["sample.jpg", "blue.png"])
        print("PASS 排序：按大小升序 -> 降序，顺序已反转")

        # 5) 列表视图 + 桌面列表截图（回到根目录，恢复名称升序）
        page.get_by_role("button", name="回到根目录").click()
        page.select_option('select[aria-label="排序方式"]', "name")
        if page.locator('button[aria-label*="降序"]').count() > 0:
            page.locator('button[aria-label*="降序"]').click()
        wait_names(page, ROOT_ORDER)
        page.get_by_role("button", name="列表视图").click()
        page.wait_for_selector('[data-testid="list"]', timeout=10000)
        expect(page.locator('[data-testid="entry-row"]')).to_have_count(7)
        expect(page.locator("thead")).to_contain_text("修改时间")
        page.screenshot(path=LIST_SHOT, full_page=True)
        print("PASS 列表视图 7 行，含名称/大小/修改时间三列")
        page.get_by_role("button", name="网格视图").click()
        page.wait_for_selector('[data-testid="grid"]', timeout=10000)

        # 6) 搜索 mp4：250ms 防抖、/api/search、高亮
        with page.expect_request(lambda request: "/api/search" in request.url) as request_info:
            page.fill('input[aria-label="搜索文件"]', "mp4")
        search_url = request_info.value.url
        assert "/api/search?q=mp4" in search_url, f"搜索 URL 不符: {search_url}"
        page.wait_for_function(
            """() => document.querySelectorAll('[data-testid="entry-card"]').length > 0
              && document.querySelectorAll('mark').length > 0""",
            timeout=10000,
        )
        search_names = card_names(page)
        assert any("mp4" in name.lower() for name in search_names), search_names
        print(f"PASS 搜索 mp4：{len(search_names)} 条结果，命中已高亮")

        # Esc 清除搜索回到根目录
        page.press('input[aria-label="搜索文件"]', "Escape")
        wait_names(page, ROOT_ORDER)
        assert page.input_value('input[aria-label="搜索文件"]') == ""
        print("PASS Esc 清除搜索并恢复根目录")

        # 7) 视频缩略图：浏览器抽帧得到 dataURL img
        page.goto(f"{BASE}/?path=videos", wait_until="networkidle")
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        page.wait_for_function(
            """() => {
              const img = document.querySelector('img[data-testid="thumb-video"]');
              return !!img && img.src.startsWith('data:image/');
            }""",
            timeout=25000,
        )
        video_src = page.get_attribute('img[data-testid="thumb-video"]', "src")
        assert video_src and video_src.startswith("data:image/"), video_src
        print(f"PASS 视频缩略图（浏览器抽帧）: {video_src[:48]}…")

        # 8) 375×780 不横向溢出
        mobile = browser.new_page(viewport={"width": 375, "height": 780})
        mobile.on("pageerror", lambda error: errors.append(str(error)))
        mobile.goto(f"{BASE}/", wait_until="networkidle")
        mobile.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        overflow = mobile.evaluate(
            """() => ({
              scrollWidth: document.documentElement.scrollWidth,
              clientWidth: document.documentElement.clientWidth,
            })"""
        )
        assert overflow["scrollWidth"] <= overflow["clientWidth"] + 1, overflow
        mobile.screenshot(path=MOBILE_SHOT, full_page=True)
        print(
            "PASS 375×780 无横向溢出 "
            f"scrollWidth={overflow['scrollWidth']} clientWidth={overflow['clientWidth']}"
        )
        mobile.close()

        # 9) /v2/ 兼容入口仍返回同一套新前端
        page.goto(f"{BASE}/v2/", wait_until="networkidle")
        page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
        expect(page.locator('[data-testid="entry-card"]').first).to_be_visible()
        expect(page.locator("body")).to_contain_text("videos")
        print("PASS /v2/ 兼容入口正常列出目录")

        browser.close()

    if errors:
        print("页面运行期错误:", errors, file=sys.stderr)
        return 1
    if console_errors:
        print("浏览器控制台错误:", console_errors, file=sys.stderr)
        return 1
    print(f"截图: {GRID_SHOT}")
    print(f"截图: {LIST_SHOT}")
    print(f"截图: {MOBILE_SHOT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
