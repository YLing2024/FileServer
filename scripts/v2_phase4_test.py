#!/usr/bin/env python3
"""Phase 4 验收：根路径分享、会话、设置与公开分享页 /s/<token>。

脚本把 testdata 复制到临时目录并启动独立服务（随机高位端口、password 认证），
不会污染 testdata；结束后停止服务并删除临时目录。

运行：
    /root/.venv/cdp/bin/python scripts/v2_phase4_test.py

截图（可用环境变量覆盖）：
    V2_SHARE_CREATE_SHOT  分享创建对话框
    V2_PUBLIC_SHARE_SHOT  公开分享页
    V2_SESSIONS_SHOT      会话列表
    V2_MOBILE_SHARE_SHOT  375 宽公开分享页
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
CREATE_SHOT = os.environ.get("V2_SHARE_CREATE_SHOT", "/tmp/fileserver-v2-phase4-create.png")
PUBLIC_SHOT = os.environ.get("V2_PUBLIC_SHARE_SHOT", "/tmp/fileserver-v2-phase4-share.png")
SESSIONS_SHOT = os.environ.get("V2_SESSIONS_SHOT", "/tmp/fileserver-v2-phase4-sessions.png")
MOBILE_SHOT = os.environ.get("V2_MOBILE_SHARE_SHOT", "/tmp/fileserver-v2-phase4-mobile.png")
PASSWORD = "phase4pass"


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_for_server(base: str, proc: subprocess.Popen[bytes]) -> None:
    for _ in range(100):
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
    page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)


def main() -> int:
    checks = Checks()
    server: subprocess.Popen[bytes] | None = None
    temp_root = Path(tempfile.mkdtemp(prefix="fileserver-v2-phase4-"))
    service_dir = temp_root / "data"
    scratch = temp_root / "scratch"
    binary = temp_root / "fileserver-phase4"

    try:
        shutil.copytree(TESTDATA, service_dir)
        shutil.rmtree(service_dir / ".FileServer", ignore_errors=True)
        scratch.mkdir(parents=True)
        upload_dir = service_dir / "upload"
        upload_dir.mkdir()
        payload = os.urandom(1024 * 1024)
        (upload_dir / "a.bin").write_bytes(payload)

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
                f"phase4:{PASSWORD}",
            ],
            cwd=service_dir,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        wait_for_server(base, server)
        session = login(base)

        page_errors: list[str] = []
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            context = authed_context(browser, session)
            page = context.new_page()
            page.on("pageerror", lambda error: page_errors.append(str(error)))

            # ---- 1. 为 upload/a.bin 创建带提取码的分享 ----
            page.goto(f"{base}/?path=upload", wait_until="networkidle")
            wait_entries(page)
            page.locator('[data-testid="entry-card"][data-entry-name="a.bin"]').click()
            page.get_by_test_id("selection-share").click()
            page.wait_for_selector('[data-testid="share-create-dialog"]', timeout=5000)
            page.get_by_test_id("share-code-enable").check()
            page.get_by_test_id("share-code").fill("ab12")
            page.get_by_test_id("share-note").fill("Phase 4 文件分享")
            checks.check(
                page.get_by_test_id("share-code").input_value() == "AB12",
                "提取码可自定义并规范为大写 4 位",
            )
            page.screenshot(path=CREATE_SHOT)
            page.get_by_test_id("share-submit").click()
            page.wait_for_selector('[data-testid="share-result-dialog"]', timeout=10000)
            file_link = page.get_by_test_id("share-result-link").input_value()
            file_code = page.get_by_test_id("share-result-code").inner_text()
            checks.check(
                file_link.startswith(f"{base}/s/") and file_code == "AB12",
                "创建成功并展示公开链接与提取码",
            )
            page.get_by_test_id("share-result-close").click()
            file_token = token_from_url(file_link)

            # ---- 2. 匿名上下文打开公开页，输码并下载 ----
            anon = browser.new_context(viewport={"width": 1280, "height": 900})
            anon_page = anon.new_page()
            anon_page.on("pageerror", lambda error: page_errors.append(str(error)))
            anon_page.goto(file_link, wait_until="networkidle")
            anon_page.wait_for_selector('[data-testid="share-code-input"]', timeout=10000)
            checks.check(True, "公开页要求输入提取码")
            anon_page.get_by_test_id("share-code-input").fill("AB12")
            anon_page.get_by_test_id("share-code-submit").click()
            anon_page.wait_for_selector('[data-testid="share-file-title"]', timeout=10000)
            checks.check(
                anon_page.get_by_test_id("share-file-title").inner_text() == "a.bin",
                "输码后显示单文件分享内容",
            )
            anon_page.screenshot(path=PUBLIC_SHOT, full_page=True)

            with anon_page.expect_download(timeout=15000) as download_info:
                anon_page.get_by_test_id("share-download").click()
            download = download_info.value
            saved = scratch / "downloaded-a.bin"
            download.save_as(str(saved))
            checks.check(
                saved.read_bytes() == payload,
                f"匿名下载字节数一致（{saved.stat().st_size} 字节）",
            )

            # ---- 3. 目录分享：进入目录并预览图片 ----
            page.goto(f"{base}/?path=", wait_until="networkidle")
            wait_entries(page)
            page.locator('[data-testid="entry-card"][data-entry-name="photos"]').click()
            page.get_by_test_id("selection-share").click()
            page.wait_for_selector('[data-testid="share-create-dialog"]', timeout=5000)
            page.get_by_test_id("share-note").fill("Phase 4 目录分享")
            page.get_by_test_id("share-submit").click()
            page.wait_for_selector('[data-testid="share-result-dialog"]', timeout=10000)
            dir_link = page.get_by_test_id("share-result-link").input_value()
            page.get_by_test_id("share-result-close").click()
            dir_token = token_from_url(dir_link)

            anon_page.goto(dir_link, wait_until="networkidle")
            anon_page.wait_for_selector('[data-testid="share-entry"]', timeout=10000)
            checks.check(
                anon_page.locator('[data-testid="share-zip"]').count() == 1,
                "目录分享显示打包下载入口",
            )
            row = anon_page.locator('[data-testid="share-entry"]', has_text="sample.jpg")
            row.get_by_role("button").click()
            anon_page.wait_for_selector('[data-testid="share-preview"] img', timeout=10000)
            loaded = anon_page.eval_on_selector(
                '[data-testid="share-preview"] img',
                "img => img.complete && img.naturalWidth > 0",
            )
            checks.check(loaded is True, "目录内图片可在公开页预览")

            # ---- 4. 375 宽公开页可用 ----
            mobile = authed_context(
                browser, session, viewport={"width": 375, "height": 780}, mobile=True
            )
            mobile_page = mobile.new_page()
            mobile_page.goto(file_link, wait_until="networkidle")
            mobile_page.wait_for_selector('[data-testid="share-code-input"]', timeout=10000)
            mobile_page.get_by_test_id("share-code-input").fill("AB12")
            mobile_page.get_by_test_id("share-code-submit").click()
            mobile_page.wait_for_selector('[data-testid="share-file-title"]', timeout=10000)
            scroll_width = mobile_page.evaluate("document.documentElement.scrollWidth")
            mobile_page.screenshot(path=MOBILE_SHOT, full_page=True)
            checks.check(
                scroll_width <= 375,
                f"375 宽公开分享页不横向溢出（scrollWidth {scroll_width}）",
            )
            mobile.close()

            # ---- 5. 会话列表能看到当前设备，并可下线其他设备 ----
            login(base)  # 再登录一次，制造另一台“设备”的会话
            page.goto(f"{base}/", wait_until="networkidle")
            wait_entries(page)
            page.get_by_test_id("toolbar-settings").click()
            page.wait_for_selector('[data-testid="settings-dialog"]', timeout=5000)
            page.get_by_test_id("settings-sessions").click()
            page.wait_for_selector('[data-testid="sessions-dialog"]', timeout=5000)
            page.wait_for_selector('[data-testid="session-row"]', timeout=10000)
            checks.check(
                page.locator('[data-testid="session-row"]').count() == 2
                and page.get_by_test_id("session-current").count() == 1,
                "会话列表列出两台设备并标出当前设备",
            )
            page.screenshot(path=SESSIONS_SHOT)
            page.get_by_test_id("session-revoke").first.click()
            page.wait_for_timeout(800)
            checks.check(
                page.locator('[data-testid="session-row"]').count() == 1,
                "下线其他设备后列表只剩当前设备",
            )
            page.keyboard.press("Escape")

            # ---- 5b. 未登录访问根路径显示内嵌登录，成功后进入浏览页 ----
            login_context = browser.new_context(viewport={"width": 1280, "height": 900})
            login_page = login_context.new_page()
            login_page.goto(f"{base}/", wait_until="networkidle")
            login_page.wait_for_selector('[data-testid="login-password"]', timeout=10000)
            login_page.get_by_test_id("login-password").fill(PASSWORD)
            login_page.get_by_test_id("login-submit").click()
            login_page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
            checks.check(True, "未登录时内嵌登录表单可完成登录并进入浏览页")
            login_context.close()

            # ---- 6. 取消分享后 /s/<token> 不可用 ----
            page.get_by_test_id("toolbar-share").click()
            page.wait_for_selector('[data-testid="share-list-dialog"]', timeout=5000)
            item = page.locator('[data-testid="share-item"]', has_text=dir_token)
            item.get_by_test_id("share-revoke").click()
            page.wait_for_selector('[data-testid="confirm-dialog"]', timeout=5000)
            page.get_by_test_id("confirm-ok").click()
            page.wait_for_timeout(800)
            anon_page.goto(dir_link, wait_until="networkidle")
            anon_page.wait_for_selector('[data-testid="share-status-title"]', timeout=10000)
            checks.check(
                anon_page.get_by_test_id("share-status-title").inner_text() == "分享已撤销",
                "取消分享后公开页显示已撤销",
            )

            checks.check(not page_errors, f"页面无未捕获异常（{page_errors}）")
            anon.close()
            context.close()
            browser.close()

        print(f"SCREENSHOT 分享创建对话框: {CREATE_SHOT}")
        print(f"SCREENSHOT 公开分享页: {PUBLIC_SHOT}")
        print(f"SCREENSHOT 会话列表: {SESSIONS_SHOT}")
        print(f"SCREENSHOT 375 宽公开页: {MOBILE_SHOT}")
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
