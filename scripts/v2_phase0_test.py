#!/usr/bin/env python3
"""Phase 0 验收：/v2/ 列目录、进入目录、切换主题，且旧 / 不受影响。

先启动服务（示例）：
    go run ./cmd/fileserver --dir testdata --host 127.0.0.1 --port 18080
再运行：
    /root/.venv/cdp/bin/python scripts/v2_phase0_test.py
可用 FILESERVER_BASE 覆盖地址；脚本只读页面，不改任何文件。
"""

import os
import re
import sys

from playwright.sync_api import expect, sync_playwright

BASE = os.environ.get("FILESERVER_BASE", "http://127.0.0.1:18080")
SHOT = os.environ.get("V2_SCREENSHOT", "/tmp/fileserver-v2-phase0.png")


def main() -> int:
    errors: list[str] = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        page.on("pageerror", lambda error: errors.append(str(error)))

        # 1) /v2/ 能列出 testdata 根目录
        page.goto(f"{BASE}/v2/", wait_until="networkidle")
        expect(page.get_by_role("heading", name="/", exact=True)).to_be_visible()
        for name in ("deep", "docs", "videos"):
            expect(page.get_by_role("button", name=re.compile(rf"^{name}$"))).to_be_visible()
        print("PASS /v2/ 列出 testdata 根目录")

        # 2) 点击目录能进入，?path= 同步，刷新后仍在该目录
        page.get_by_role("button", name=re.compile(r"^videos$")).click()
        expect(page).to_have_url(re.compile(r"\?path=videos$"))
        expect(page.get_by_text("sample.mp4", exact=True)).to_be_visible()
        page.reload(wait_until="networkidle")
        expect(page.get_by_role("heading", name="/videos", exact=True)).to_be_visible()
        expect(page.get_by_text("sample.mp4", exact=True)).to_be_visible()
        print("PASS 点击目录进入并同步 ?path=videos（刷新保持）")

        # 3) 主题开关沿用 fs.theme，刷新后保持
        html = page.locator("html")
        before = html.get_attribute("data-theme")
        page.get_by_role("button", name="切换主题").click()
        expected = "light" if before == "dark" else "dark"
        expect(html).to_have_attribute("data-theme", expected)
        assert page.evaluate("localStorage.getItem('fs.theme')") == expected
        page.reload(wait_until="networkidle")
        expect(page.locator("html")).to_have_attribute("data-theme", expected)
        page.screenshot(path=SHOT, full_page=True)
        print(f"PASS 主题切换 {before} -> {expected}，刷新后保持")

        # 4) 旧前端 / 仍正常
        page.goto(f"{BASE}/", wait_until="networkidle")
        expect(page.locator("#browse")).to_be_visible()
        page.wait_for_selector(".card", timeout=10000)
        expect(page.locator(".card").first).to_be_visible()
        expect(page.locator("body")).to_contain_text("videos")
        print("PASS 旧前端 / 仍正常列出目录")

        browser.close()

    if errors:
        print("页面运行期错误:", errors, file=sys.stderr)
        return 1
    print(f"截图: {SHOT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
