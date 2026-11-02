#!/usr/bin/env python3
"""Phase 3 验收：根路径上传与管理。

脚本自己把 testdata 复制到临时目录并启动一个独立服务（随机高位端口、
password 认证），因此不会污染 testdata；结束后停止服务并删除临时目录。
需要 Node/pnpm 已构建过前端产物，或者先执行 `pnpm build`。

运行：
    /root/.venv/cdp/bin/python scripts/v2_phase3_test.py

截图（可用环境变量覆盖）：
    V2_UPLOAD_SHOT       上传中
    V2_SELECTION_SHOT    选择条
    V2_CONFLICT_SHOT     同名冲突对话框
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

from playwright.sync_api import sync_playwright

REPO = Path(__file__).resolve().parent.parent
TESTDATA = REPO / "testdata"
UPLOAD_SHOT = os.environ.get("V2_UPLOAD_SHOT", "/tmp/fileserver-v2-phase3-uploading.png")
SELECTION_SHOT = os.environ.get("V2_SELECTION_SHOT", "/tmp/fileserver-v2-phase3-selection.png")
CONFLICT_SHOT = os.environ.get("V2_CONFLICT_SHOT", "/tmp/fileserver-v2-phase3-conflict.png")
MOBILE_SHOT = os.environ.get("V2_MOBILE_UPLOAD_SHOT", "/tmp/fileserver-v2-phase3-mobile.png")
PASSWORD = "phase3pass"


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
            # 认证开启时 /api/health 也可能返回 401，只要端口有响应即视为已启动。
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


def write_file(path: Path, data: bytes) -> None:
    path.write_bytes(data)


class Checks:
    def __init__(self) -> None:
        self.errors: list[str] = []

    def check(self, condition: bool, message: str) -> None:
        if condition:
            print(f"PASS {message}")
        else:
            print(f"FAIL {message}")
            self.errors.append(message)


def wait_upload_done(page, timeout: int = 90000) -> None:
    page.wait_for_selector('[data-testid="upload-panel"]', state="detached", timeout=timeout)


def refresh(page) -> None:
    page.get_by_role("button", name="刷新").click()
    page.wait_for_timeout(400)


def main() -> int:
    checks = Checks()
    base = ""
    server: subprocess.Popen[bytes] | None = None
    temp_root = Path(tempfile.mkdtemp(prefix="fileserver-v2-phase3-"))
    service_dir = temp_root / "data"
    scratch = temp_root / "scratch"
    binary = temp_root / "fileserver-phase3"

    try:
        shutil.copytree(TESTDATA, service_dir)
        shutil.rmtree(service_dir / ".FileServer", ignore_errors=True)
        scratch.mkdir(parents=True)

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
                f"phase3:{PASSWORD}",
            ],
            cwd=service_dir,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        wait_for_server(base, server)
        session = login(base)

        small = scratch / "phase3-upload.bin"
        write_file(small, os.urandom(4 * 1024 * 1024))
        manage_file = service_dir / "phase3-manage.txt"
        manage_file.write_text("phase3", encoding="utf-8")
        batch_one = service_dir / "phase3-batch-1.txt"
        batch_two = service_dir / "phase3-batch-2.txt"
        batch_one.write_text("one", encoding="utf-8")
        batch_two.write_text("two", encoding="utf-8")
        mobile_file = scratch / "phase3-mobile.bin"
        write_file(mobile_file, os.urandom(3 * 1024 * 1024))

        page_errors: list[str] = []
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            context = browser.new_context(viewport={"width": 1280, "height": 900})
            context.add_init_script(
                "localStorage.setItem('fs.token', %s);"
                "localStorage.setItem('fs.token.expires', %s);"
                % (json.dumps(session["token"]), json.dumps(str(session["expires"])))
            )
            page = context.new_page()
            page.on("pageerror", lambda error: page_errors.append(str(error)))

            page.goto(f"{base}/", wait_until="networkidle")
            page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
            checks.check(
                page.locator('[data-testid="toolbar-upload"]').count() == 1
                and page.locator('[data-testid="toolbar-trash"]').count() == 1,
                "登录后工具栏出现上传/回收站入口",
            )

            # 本地回环上传太快，用 CDP 限制上传带宽，保证能观察到进度 UI 前进。
            cdp = context.new_cdp_session(page)
            cdp.send("Network.enable")
            cdp.send(
                "Network.emulateNetworkConditions",
                {
                    "offline": False,
                    "latency": 20,
                    "downloadThroughput": 10 * 1024 * 1024,
                    "uploadThroughput": 1024 * 1024,
                },
            )

            # ---- 1. 文件上传（<5 MiB），进度 UI 出现并在完成后关闭 ----
            page.locator('input[type="file"]').first.set_input_files(str(small))
            page.wait_for_selector('[data-testid="upload-panel"]', timeout=10000)
            max_progress = 0
            screenshot_taken = False
            for _ in range(80):
                value = page.evaluate(
                    "document.querySelector('[data-testid=upload-overall-fill]')?.dataset.progress ?? 'gone'"
                )
                if value == "gone":
                    break
                max_progress = max(max_progress, int(value))
                if not screenshot_taken and max_progress > 0:
                    page.screenshot(path=UPLOAD_SHOT)
                    screenshot_taken = True
                page.wait_for_timeout(100)
            if not screenshot_taken:
                page.screenshot(path=UPLOAD_SHOT)
            checks.check(max_progress > 0, f"上传进度 UI 出现且进度前进（峰值 {max_progress}%）")
            wait_upload_done(page)
            cdp.send(
                "Network.emulateNetworkConditions",
                {
                    "offline": False,
                    "latency": 0,
                    "downloadThroughput": -1,
                    "uploadThroughput": -1,
                },
            )
            page.wait_for_timeout(300)
            uploaded = service_dir / "phase3-upload.bin"
            checks.check(
                uploaded.exists() and uploaded.stat().st_size == small.stat().st_size,
                f"上传 1 个 <5 MiB 文件成功且大小一致（{uploaded.stat().st_size} 字节）",
            )
            checks.check(
                page.locator(
                    '[data-testid="entry-card"][data-entry-name="phase3-upload.bin"]'
                ).count()
                == 1,
                "上传完成后当前目录自动刷新出现新文件",
            )

            # ---- 2. 同名冲突三选项 ----
            page.locator('input[type="file"]').first.set_input_files(str(small))
            page.wait_for_selector('[data-testid="upload-conflict"]', timeout=30000)
            page.screenshot(path=CONFLICT_SHOT)
            checks.check(
                "phase3-upload.bin"
                in page.locator('[data-testid="upload-conflict"]').inner_text(),
                "同名上传弹出冲突对话框并列出已存在名称",
            )
            paused_progress = page.evaluate(
                "document.querySelector('[data-testid=upload-overall-fill]')?.dataset.progress ?? 'gone'"
            )
            checks.check(
                paused_progress == "0",
                f"冲突暂停时上传进度归零（当前 {paused_progress}%）",
            )
            page.locator('[data-testid="conflict-rename"]').click()
            wait_upload_done(page)
            page.wait_for_timeout(300)
            renamed = service_dir / "phase3-upload (1).bin"
            checks.check(renamed.exists(), "冲突选择“自动改名”生成 phase3-upload (1).bin")

            page.locator('input[type="file"]').first.set_input_files(str(small))
            page.wait_for_selector('[data-testid="upload-conflict"]', timeout=30000)
            page.locator('[data-testid="conflict-overwrite"]').click()
            wait_upload_done(page)
            page.wait_for_timeout(300)
            checks.check(
                uploaded.exists() and uploaded.stat().st_size == small.stat().st_size,
                "冲突选择“覆盖”后原文件仍存在且大小正确",
            )

            page.locator('input[type="file"]').first.set_input_files(str(small))
            page.wait_for_selector('[data-testid="upload-conflict"]', timeout=30000)
            page.locator('[data-testid="conflict-skip"]').click()
            wait_upload_done(page)
            page.wait_for_timeout(300)
            conflict_files = sorted(
                path.name for path in service_dir.glob("phase3-upload*")
            )
            checks.check(
                conflict_files == ["phase3-upload (1).bin", "phase3-upload.bin"],
                "冲突选择“跳过”不创建新文件，队列继续结束",
            )

            # ---- 2b. 文件夹上传保留相对路径 ----
            folder = scratch / "phase3-folder"
            (folder / "sub" / "deep").mkdir(parents=True)
            (folder / "a.txt").write_text("a", encoding="utf-8")
            (folder / "sub" / "b.txt").write_text("b", encoding="utf-8")
            (folder / "sub" / "deep" / "c.txt").write_text("c", encoding="utf-8")
            page.locator('input[type="file"]').nth(1).set_input_files(str(folder))
            page.wait_for_selector('[data-testid="upload-panel"]', timeout=10000)
            wait_upload_done(page)
            page.wait_for_timeout(300)
            checks.check(
                all(
                    (service_dir / rel).exists()
                    for rel in [
                        "phase3-folder/a.txt",
                        "phase3-folder/sub/b.txt",
                        "phase3-folder/sub/deep/c.txt",
                    ]
                ),
                "文件夹上传保留相对路径（phase3-folder/sub/deep/c.txt）",
            )

            # ---- 3. 重命名 / 移动 / 复制 / 删除 / 回收站恢复 ----
            refresh(page)
            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-manage.txt"]'
            ).click()
            page.get_by_role("button", name="重命名").click()
            page.wait_for_selector('[data-testid="rename-dialog"]', timeout=5000)
            page.locator('[data-testid="rename-dialog"] input').fill("phase3-renamed.txt")
            page.locator('[data-testid="rename-submit"]').click()
            page.wait_for_timeout(700)
            checks.check(
                (service_dir / "phase3-renamed.txt").exists()
                and not (service_dir / "phase3-manage.txt").exists(),
                "重命名成功（phase3-manage.txt -> phase3-renamed.txt）",
            )

            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-renamed.txt"]'
            ).click()
            page.get_by_role("button", name="移动").click()
            page.wait_for_selector('[data-testid="transfer-dialog"]', timeout=5000)
            page.locator('[data-testid="transfer-dialog"]').get_by_role(
                "button", name="docs", exact=True
            ).click()
            page.locator('[data-testid="transfer-submit"]').click()
            page.wait_for_selector('[data-testid="transfer-dialog"]', state="detached")
            page.wait_for_timeout(700)
            checks.check(
                (service_dir / "docs" / "phase3-renamed.txt").exists(),
                "移动成功（根目录 -> /docs）",
            )

            page.locator('[data-testid="entry-card"][data-entry-name="docs"]').dblclick()
            page.wait_for_timeout(600)
            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-renamed.txt"]'
            ).click()
            page.get_by_role("button", name="复制").click()
            page.wait_for_selector('[data-testid="transfer-dialog"]', timeout=5000)
            page.locator('[data-testid="transfer-dialog"]').get_by_role(
                "button", name="根目录", exact=True
            ).click()
            page.locator('[data-testid="transfer-submit"]').click()
            page.wait_for_selector('[data-testid="transfer-dialog"]', state="detached")
            page.wait_for_timeout(700)
            checks.check(
                (service_dir / "phase3-renamed.txt").exists()
                and (service_dir / "docs" / "phase3-renamed.txt").exists(),
                "复制成功（/docs -> 根目录，源文件保留）",
            )

            page.get_by_role("button", name="根目录", exact=True).first.click()
            page.wait_for_timeout(600)
            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-renamed.txt"]'
            ).click()
            page.get_by_role("button", name="删除", exact=True).click()
            page.wait_for_selector('[data-testid="confirm-dialog"]', timeout=5000)
            page.locator('[data-testid="confirm-ok"]').click()
            page.wait_for_timeout(900)
            checks.check(
                not (service_dir / "phase3-renamed.txt").exists(),
                "删除成功（移入回收站）",
            )

            page.locator('[data-testid="toolbar-trash"]').click()
            page.wait_for_selector('[data-testid="trash-dialog"]', timeout=5000)
            page.wait_for_timeout(500)
            trash_items = page.locator('[data-testid="trash-item"]')
            checks.check(trash_items.count() == 1, "回收站列表出现 1 个条目")
            page.locator('[data-testid="trash-dialog"] input[type="checkbox"]').nth(1).check()
            page.locator('[data-testid="trash-restore"]').click()
            page.wait_for_timeout(1000)
            page.locator('[data-testid="trash-dialog"] button', has_text="关闭").click()
            page.wait_for_timeout(700)
            checks.check(
                (service_dir / "phase3-renamed.txt").exists(),
                "回收站恢复成功（文件回到原位置）",
            )

            # ---- 4. 多选：Ctrl 两项、Shift 连选、选择条与批量删除 ----
            refresh(page)
            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-batch-1.txt"]'
            ).click()
            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-batch-2.txt"]'
            ).click(modifiers=["Control"])
            checks.check(
                page.locator('[data-testid="selection-count"]').inner_text() == "已选 2 项",
                "Ctrl 点选 2 项后选择条显示“已选 2 项”",
            )
            page.screenshot(path=SELECTION_SHOT)

            page.keyboard.press("Escape")
            page.wait_for_timeout(200)
            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-batch-1.txt"]'
            ).click()
            page.locator(
                '[data-testid="entry-card"][data-entry-name="phase3-batch-2.txt"]'
            ).click(modifiers=["Shift"])
            checks.check(
                page.locator('[data-testid="selection-count"]').inner_text() == "已选 2 项",
                "Shift 连选覆盖相邻两项",
            )

            page.get_by_role("button", name="删除", exact=True).click()
            page.wait_for_selector('[data-testid="confirm-dialog"]', timeout=5000)
            page.locator('[data-testid="confirm-ok"]').click()
            page.wait_for_timeout(1000)
            checks.check(
                not (service_dir / "phase3-batch-1.txt").exists()
                and not (service_dir / "phase3-batch-2.txt").exists(),
                "Ctrl 多选 2 项后批量删除成功",
            )

            # ---- 5. 批量下载走 /api/zip ----
            page.locator('[data-testid="entry-card"][data-entry-name="photos"]').click()
            with page.expect_download() as download_info:
                page.get_by_role("button", name="下载", exact=True).click()
            download = download_info.value
            checks.check(
                download.suggested_filename == "photos.zip",
                "目录下载触发 /api/zip 并且文件名正确（photos.zip）",
            )
            download.cancel()

            checks.check(not page_errors, f"页面无未捕获异常（{page_errors}）")
            context.close()
            browser.close()

        # ---- 6. 375 宽上传面板 ----
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            context = browser.new_context(
                viewport={"width": 375, "height": 780}, is_mobile=True, has_touch=True
            )
            context.add_init_script(
                "localStorage.setItem('fs.token', %s);"
                "localStorage.setItem('fs.token.expires', %s);"
                % (json.dumps(session["token"]), json.dumps(str(session["expires"])))
            )
            page = context.new_page()
            page.goto(f"{base}/", wait_until="networkidle")
            page.wait_for_selector('[data-testid="entry-card"]', timeout=10000)
            page.locator('input[type="file"]').first.set_input_files(str(mobile_file))
            page.wait_for_selector('[data-testid="upload-panel"]', timeout=10000)
            box = page.locator('[data-testid="upload-panel"]').bounding_box()
            scroll_width = page.evaluate("document.documentElement.scrollWidth")
            page.screenshot(path=MOBILE_SHOT, full_page=True)
            checks.check(
                box is not None
                and box["x"] >= 0
                and box["x"] + box["width"] <= 375
                and scroll_width <= 375,
                f"375 宽上传面板可用且不横向溢出（宽 {round(box['width']) if box else 0}px，scrollWidth {scroll_width}）",
            )
            wait_upload_done(page, timeout=60000)
            checks.check(
                (service_dir / "phase3-mobile.bin").exists(),
                "移动端上传完成并落盘",
            )
            context.close()
            browser.close()

        print(f"SCREENSHOT 上传中: {UPLOAD_SHOT}")
        print(f"SCREENSHOT 选择条: {SELECTION_SHOT}")
        print(f"SCREENSHOT 冲突对话框: {CONFLICT_SHOT}")
        print(f"SCREENSHOT 375 上传面板: {MOBILE_SHOT}")
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
