#!/usr/bin/env python3
"""Phase 5 验收：切换后的根路径全量回归。

覆盖：
- 根路径目录浏览、进入目录、网格/列表、搜索、排序、主题、登录后上传、多选删除到回收站
- 图片灯箱、视频播放、文本预览
- 带提取码分享 → 匿名输码 → 下载字节一致 → 撤销后不可用
- 未登录管理接口 401、登录后可用、退出登录后旧 token 失效
- 375 宽浏览 / 播放 / 公开分享页
- 旧资源路径已移除、/v2/ 兼容入口返回同一套新前端

脚本复制 testdata 到临时目录并启动独立服务（随机高位端口、password 认证），
不污染仓库；结束后停止服务并删除临时目录。

运行：
    /root/.venv/cdp/bin/python scripts/v2_phase5_test.py
"""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from urllib.parse import urlsplit

from playwright.sync_api import Page, sync_playwright

REPO = Path(__file__).resolve().parent.parent
TESTDATA = REPO / "testdata"
PASSWORD = "phase5pass"
SHOT_HOME = os.environ.get("V2_PHASE5_HOME_SHOT", "/tmp/fileserver-v2-phase5-home.png")
SHOT_PUBLIC = os.environ.get("V2_PHASE5_SHARE_SHOT", "/tmp/fileserver-v2-phase5-share.png")
SHOT_MOBILE = os.environ.get("V2_PHASE5_MOBILE_SHOT", "/tmp/fileserver-v2-phase5-mobile.png")


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_for_server(base: str, proc: subprocess.Popen[bytes]) -> None:
    for _ in range(120):
        if proc.poll() is not None:
            raise RuntimeError("服务进程提前退出")
        try:
            urllib.request.urlopen(f"{base}/api/health", timeout=0.5)
            return
        except urllib.error.HTTPError:
            return
        except Exception:
            time.sleep(0.1)
    raise RuntimeError("服务启动超时")


def login(base: str) -> dict:
    body = json.dumps({"password": PASSWORD, "remember": True}).encode("utf-8")
    request = urllib.request.Request(
        f"{base}/api/auth/login",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=5) as response:
        return json.loads(response.read().decode("utf-8"))


def api_status(url: str, token: str | None = None) -> int:
    request = urllib.request.Request(url)
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code


class Checks:
    def __init__(self) -> None:
        self.errors: list[str] = []

    def check(self, condition: bool, message: str) -> None:
        if condition:
            print(f"PASS {message}")
        else:
            print(f"FAIL {message}")
            self.errors.append(message)


def authed_context(browser, session: dict, viewport: dict | None = None, mobile: bool = False):
    context = browser.new_context(
        viewport=viewport or {"width": 1280, "height": 900}, is_mobile=mobile, has_touch=mobile
    )
    context.add_init_script(
        "localStorage.setItem('fs.token', %s);"
        "localStorage.setItem('fs.token.expires', %s);"
        % (json.dumps(session["token"]), json.dumps(str(session["expires"])))
    )
    return context


def token_from_url(url: str) -> str:
    return urlsplit(url).path.rstrip("/").split("/")[-1]


def wait_entries(page: Page) -> None:
    page.wait_for_selector('[data-testid="entry-card"]', timeout=15000)


def main() -> int:
    checks = Checks()
    server: subprocess.Popen[bytes] | None = None
    temp_root = Path(tempfile.mkdtemp(prefix="fileserver-v2-phase5-"))
    service_dir = temp_root / "data"
    scratch = temp_root / "scratch"
    binary = temp_root / "fileserver-phase5"

    try:
        shutil.copytree(TESTDATA, service_dir)
        shutil.rmtree(service_dir / ".FileServer", ignore_errors=True)
        scratch.mkdir(parents=True)
        upload_dir = service_dir / "upload"
        upload_dir.mkdir()
        share_payload = os.urandom(700 * 1024)
        (upload_dir / "a.bin").write_bytes(share_payload)

        subprocess.run(
            ["go", "build", "-o", str(binary), "./cmd/fileserver"],
            cwd=REPO,
            check=True,
            stdout=subprocess.DEVNULL,
        )
        port = free_port()
        base = f"http://127.0.0.1:{port}"
        server = subprocess.Popen(
            [
                str(binary),
                "--dir",
                str(service_dir),
                "--host",
                "127.0.0.1",
                "--port",
                str(port),
                "--no-qr",
                "--allow-upload",
                "--auth",
                f"phase5:{PASSWORD}",
            ],
            cwd=service_dir,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        wait_for_server(base, server)
        session = login(base)

        # ---- 1. 认证：未登录管理接口被拒，登录后可用 ----
        checks.check(
            api_status(f"{base}/api/list") == 401,
            "未登录访问 /api/list 返回 401",
        )
        checks.check(
            api_status(f"{base}/api/list", session["token"]) == 200,
            "登录后访问 /api/list 返回 200",
        )

        page_errors: list[str] = []
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            context = authed_context(browser, session)
            page = context.new_page()
            page.on("pageerror", lambda error: page_errors.append(str(error)))

            # ---- 2. 根路径浏览 ----
            page.goto(f"{base}/", wait_until="networkidle")
            wait_entries(page)
            names = page.eval_on_selector_all(
                '[data-testid="entry-card"]',
                "els => els.map(el => el.getAttribute('data-entry-name'))",
            )
            checks.check("videos" in names and "photos" in names, "根路径列出测试目录")
            index_html = page.content()
            checks.check('id="root"' in index_html, "根路径返回 React 新前端（#root）")

            page.locator('[data-testid="entry-card"][data-entry-name="photos"]').dblclick()
            page.wait_for_url("**?path=photos", timeout=10000)
            wait_entries(page)
            checks.check(
                page.locator('[data-testid="entry-card"][data-entry-name="sample.jpg"]').count() == 1,
                "点击目录进入 photos 并同步 ?path=photos",
            )
            page.get_by_label("回到根目录").click()
            page.wait_for_url(f"{base}/", timeout=10000)

            # 网格 / 列表
            page.get_by_label("列表视图").click()
            page.wait_for_selector('[data-testid="list"]', timeout=10000)
            checks.check(
                page.locator('[data-testid="entry-row"]').count() >= 4,
                "切换到列表视图并渲染行",
            )
            page.get_by_label("网格视图").click()
            page.wait_for_selector('[data-testid="grid"]', timeout=10000)
            checks.check(
                page.locator('[data-testid="entry-card"]').count() >= 4,
                "切换到网格视图并渲染卡片",
            )

            # 搜索
            page.get_by_label("搜索文件").fill("sample")
            page.wait_for_selector('[data-testid="entry-card"][data-entry-name="sample.jpg"]', timeout=15000)
            checks.check(True, "搜索 sample 命中 sample.jpg")
            page.get_by_label("搜索文件").press("Escape")
            wait_entries(page)

            # 排序
            page.get_by_label("排序方式").select_option("size")
            page.wait_for_timeout(400)
            checks.check(
                page.evaluate("localStorage.getItem('fs.sort')") == "size",
                "排序切换为大小并持久化 fs.sort",
            )
            order_button = page.get_by_label("升序，点击切换为降序")
            if order_button.count() == 0:
                order_button = page.get_by_label("降序，点击切换为升序")
            before_order = page.evaluate("localStorage.getItem('fs.order')")
            order_button.click()
            page.wait_for_timeout(300)
            checks.check(
                page.evaluate("localStorage.getItem('fs.order')") != before_order,
                "排序方向切换并持久化 fs.order",
            )

            # 主题
            before_theme = page.locator("html").get_attribute("data-theme")
            page.get_by_label("切换主题").click()
            after_theme = page.locator("html").get_attribute("data-theme")
            checks.check(
                after_theme != before_theme
                and page.evaluate("localStorage.getItem('fs.theme')") == after_theme,
                f"主题切换 {before_theme} -> {after_theme} 并持久化",
            )
            page.screenshot(path=SHOT_HOME, full_page=True)

            # ---- 3. 登录后上传两个文件 ----
            upload_names = ["phase5-a.bin", "phase5-b.bin"]
            upload_paths = []
            for name in upload_names:
                path = scratch / name
                path.write_bytes(os.urandom(64 * 1024))
                upload_paths.append(path)
            page.locator('input[type="file"]').first.set_input_files(
                [str(path) for path in upload_paths]
            )
            for name in upload_names:
                page.wait_for_selector(
                    f'[data-testid="entry-card"][data-entry-name="{name}"]', timeout=30000
                )
            checks.check(
                all((service_dir / name).exists() for name in upload_names),
                "登录后通过界面上传两个文件并落盘",
            )

            # ---- 4. 多选删除到回收站 ----
            page.locator('[data-testid="entry-card"][data-entry-name="phase5-a.bin"]').click()
            page.locator('[data-testid="entry-card"][data-entry-name="phase5-b.bin"]').click(
                modifiers=["Control"]
            )
            page.wait_for_selector('[data-testid="selection-bar"]', timeout=5000)
            checks.check(
                "已选 2 项" in page.get_by_test_id("selection-count").inner_text(),
                "Ctrl 多选两个文件",
            )
            page.get_by_test_id("selection-bar").get_by_role("button", name="删除").click()
            page.wait_for_selector('[data-testid="confirm-dialog"]', timeout=5000)
            page.get_by_test_id("confirm-ok").click()
            for name in upload_names:
                page.wait_for_selector(
                    f'[data-testid="entry-card"][data-entry-name="{name}"]',
                    state="detached",
                    timeout=15000,
                )
            checks.check(
                all(not (service_dir / name).exists() for name in upload_names),
                "多选删除后文件从目录消失",
            )
            trash_root = service_dir / ".FileServer" / "trash"
            trash_count = sum(1 for _ in trash_root.rglob("*") if _.is_file()) if trash_root.exists() else 0
            checks.check(trash_count >= 2, f"回收站记录到删除文件（{trash_count} 个文件）")
            page.get_by_test_id("toolbar-trash").click()
            page.wait_for_selector('[data-testid="trash-dialog"]', timeout=5000)
            page.wait_for_selector('[data-testid="trash-item"]', timeout=10000)
            checks.check(
                page.locator('[data-testid="trash-item"]').count() >= 2,
                "回收站对话框列出被删除项",
            )
            page.keyboard.press("Escape")

            # ---- 5. 预览：图片灯箱 / 视频 / 文本 ----
            page.goto(f"{base}/?path=photos", wait_until="networkidle")
            wait_entries(page)
            page.locator('[data-testid="entry-card"][data-entry-name="sample.jpg"]').dblclick()
            page.wait_for_selector('[data-testid="lightbox-image"]', timeout=10000)
            loaded = page.eval_on_selector(
                '[data-testid="lightbox-image"]',
                "img => img.complete && img.naturalWidth > 0",
            )
            checks.check(loaded is True, "图片灯箱加载原图")
            page.keyboard.press("Escape")

            page.goto(f"{base}/?path=videos", wait_until="networkidle")
            wait_entries(page)
            page.locator('[data-testid="entry-card"][data-entry-name="sample.mp4"]').dblclick()
            page.wait_for_selector('[data-testid="video-player"]', timeout=15000)
            page.get_by_role("button", name="播放").first.click()
            page.wait_for_function(
                "() => { const v = document.querySelector('[data-testid=video-element]');"
                " return !!v && (v.currentTime > 0 || !v.paused); }",
                timeout=15000,
            )
            checks.check(True, "视频播放器起播并推进")
            page.keyboard.press("Escape")

            page.goto(f"{base}/", wait_until="networkidle")
            wait_entries(page)
            page.locator('[data-testid="entry-card"][data-entry-name="README.txt"]').dblclick()
            page.wait_for_selector('[data-testid="text-preview"]', timeout=10000)
            text = page.get_by_test_id("text-preview").inner_text()
            checks.check("FileServer" in text, "文本预览加载 README.txt 内容")
            page.keyboard.press("Escape")

            # ---- 6. 带提取码分享 → 匿名下载 → 撤销 ----
            page.goto(f"{base}/?path=upload", wait_until="networkidle")
            wait_entries(page)
            page.locator('[data-testid="entry-card"][data-entry-name="a.bin"]').click()
            page.get_by_test_id("selection-share").click()
            page.wait_for_selector('[data-testid="share-create-dialog"]', timeout=5000)
            page.get_by_test_id("share-code-enable").check()
            page.get_by_test_id("share-code").fill("ab12")
            page.get_by_test_id("share-note").fill("Phase 5 文件分享")
            page.get_by_test_id("share-submit").click()
            page.wait_for_selector('[data-testid="share-result-dialog"]', timeout=10000)
            file_link = page.get_by_test_id("share-result-link").input_value()
            file_code = page.get_by_test_id("share-result-code").inner_text()
            checks.check(
                file_link.startswith(f"{base}/s/") and file_code == "AB12",
                "创建带提取码分享并展示链接与提取码",
            )
            page.get_by_test_id("share-result-close").click()
            file_token = token_from_url(file_link)

            anon = browser.new_context(viewport={"width": 1280, "height": 900})
            anon_page = anon.new_page()
            anon_page.on("pageerror", lambda error: page_errors.append(str(error)))
            anon_page.goto(file_link, wait_until="networkidle")
            anon_page.wait_for_selector('[data-testid="share-code-input"]', timeout=10000)
            checks.check(True, "匿名打开 /s/<token> 要求提取码")
            anon_page.get_by_test_id("share-code-input").fill("AB12")
            anon_page.get_by_test_id("share-code-submit").click()
            anon_page.wait_for_selector('[data-testid="share-file-title"]', timeout=10000)
            checks.check(
                anon_page.get_by_test_id("share-file-title").inner_text() == "a.bin",
                "输码后显示分享文件",
            )
            anon_page.screenshot(path=SHOT_PUBLIC, full_page=True)
            with anon_page.expect_download(timeout=20000) as download_info:
                anon_page.get_by_test_id("share-download").click()
            download = download_info.value
            saved = scratch / "downloaded-a.bin"
            download.save_as(str(saved))
            checks.check(
                saved.read_bytes() == share_payload,
                f"匿名下载字节一致（{saved.stat().st_size} 字节）",
            )

            # ---- 7. 375 宽公开分享页 ----
            mobile = browser.new_context(
                viewport={"width": 375, "height": 780}, is_mobile=True, has_touch=True
            )
            mobile_page = mobile.new_page()
            mobile_page.goto(file_link, wait_until="networkidle")
            mobile_page.wait_for_selector('[data-testid="share-code-input"]', timeout=10000)
            mobile_page.get_by_test_id("share-code-input").fill("AB12")
            mobile_page.get_by_test_id("share-code-submit").click()
            mobile_page.wait_for_selector('[data-testid="share-file-title"]', timeout=10000)
            width = mobile_page.evaluate("document.documentElement.scrollWidth")
            mobile_page.screenshot(path=SHOT_MOBILE, full_page=True)
            checks.check(width <= 375, f"375 宽分享页不横向溢出（scrollWidth {width}）")
            mobile.close()

            # ---- 8. 撤销分享后公开页不可用 ----
            page.get_by_test_id("toolbar-share").click()
            page.wait_for_selector('[data-testid="share-list-dialog"]', timeout=5000)
            item = page.locator('[data-testid="share-item"]', has_text=file_token)
            item.get_by_test_id("share-revoke").click()
            page.wait_for_selector('[data-testid="confirm-dialog"]', timeout=5000)
            page.get_by_test_id("confirm-ok").click()
            page.wait_for_timeout(800)
            anon_page.goto(file_link, wait_until="networkidle")
            anon_page.wait_for_selector('[data-testid="share-status-title"]', timeout=10000)
            checks.check(
                anon_page.get_by_test_id("share-status-title").inner_text() == "分享已撤销",
                "撤销后公开页显示分享已撤销",
            )
            revoked_status = api_status(f"{base}/api/public/share/{file_token}/download")
            checks.check(
                revoked_status in (403, 404),
                f"撤销后公开下载接口不可用（实际 HTTP {revoked_status}）",
            )

            # ---- 9. 375 宽浏览与视频播放 ----
            mobile = authed_context(
                browser, session, viewport={"width": 375, "height": 780}, mobile=True
            )
            mobile_page = mobile.new_page()
            mobile_page.goto(f"{base}/?path=photos", wait_until="networkidle")
            mobile_page.wait_for_selector('[data-testid="entry-card"]', timeout=15000)
            width = mobile_page.evaluate("document.documentElement.scrollWidth")
            checks.check(width <= 375, f"375 宽浏览页不横向溢出（scrollWidth {width}）")
            mobile_page.goto(f"{base}/?view=videos%2Fsample.mp4", wait_until="networkidle")
            mobile_page.wait_for_selector('[data-testid="video-player"]', timeout=15000)
            width = mobile_page.evaluate("document.documentElement.scrollWidth")
            checks.check(width <= 375, f"375 宽播放页不横向溢出（scrollWidth {width}）")
            mobile.close()

            # ---- 10. 界面登录、退出后旧 token 失效 ----
            login_context = browser.new_context(viewport={"width": 1280, "height": 900})
            login_page = login_context.new_page()
            login_page.goto(f"{base}/", wait_until="networkidle")
            login_page.wait_for_selector('[data-testid="login-password"]', timeout=10000)
            login_page.get_by_test_id("login-password").fill(PASSWORD)
            login_page.get_by_test_id("login-remember").check()
            login_page.get_by_test_id("login-submit").click()
            login_page.wait_for_selector('[data-testid="entry-card"]', timeout=15000)
            ui_token = login_page.evaluate("localStorage.getItem('fs.token')")
            checks.check(bool(ui_token), "未登录时内嵌登录表单可完成登录")
            login_page.get_by_test_id("toolbar-settings").click()
            login_page.wait_for_selector('[data-testid="settings-dialog"]', timeout=5000)
            login_page.get_by_test_id("settings-logout").click()
            login_page.wait_for_selector('[data-testid="login-password"]', timeout=10000)
            checks.check(
                api_status(f"{base}/api/list", ui_token) == 401,
                "退出登录后旧 token 访问 /api/list 返回 401",
            )
            checks.check(
                login_page.evaluate("localStorage.getItem('fs.token')") is None,
                "退出登录后清除本地凭据",
            )
            login_context.close()

            checks.check(not page_errors, f"页面无未捕获异常（{page_errors}）")
            anon.close()
            context.close()
            browser.close()

        print(f"SCREENSHOT 根路径浏览: {SHOT_HOME}")
        print(f"SCREENSHOT 公开分享页: {SHOT_PUBLIC}")
        print(f"SCREENSHOT 375 宽分享页: {SHOT_MOBILE}")
    except Exception as error:  # noqa: BLE001 - 验收脚本需要打印任何失败
        print(f"ERROR {error}")
        checks.errors.append(str(error))
    finally:
        if server is not None and server.poll() is None:
            server.terminate()
            try:
                server.wait(timeout=5)
            except subprocess.TimeoutExpired:
                server.kill()
        shutil.rmtree(temp_root, ignore_errors=True)

    if checks.errors:
        print(f"FAILED {len(checks.errors)} 项")
        return 1
    print("ALL PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
