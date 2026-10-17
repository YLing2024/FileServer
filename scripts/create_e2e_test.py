"""新建文件夹 / 空文件 + 文本编辑端到端流程（Playwright）。

流程：建文件夹 → 进入 → 建非白名单空文件（确认 0 字节）→ 建 .txt 并编辑保存
      → 回上级确认条目存在。结束后清理本次创建的文件夹（需可写文件系统）。

用法（先启动被测服务，目录指向仓库 testdata）：
    python scripts/create_e2e_test.py
    python scripts/create_e2e_test.py --base http://127.0.0.1:8099 --root testdata
"""
import argparse
import json
import os
import shutil
import sys
import time

from playwright.sync_api import sync_playwright

errors = []


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8099")
    ap.add_argument("--root", default="testdata", help="被测服务根目录（用于清理）")
    args = ap.parse_args()
    base, root = args.base, os.path.abspath(args.root)

    folder = "oc-create-%d" % int(time.time())
    created_dir = os.path.join(root, folder)

    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            page = browser.new_page(viewport={"width": 1440, "height": 900})
            page.on("console", lambda m: errors.append(f"[console.{m.type}] {m.text}")
                    if m.type in ("error", "warning") else None)
            page.on("pageerror", lambda e: errors.append(f"[pageerror] {e}"))

            # 1. 建文件夹
            page.goto(base, wait_until="networkidle")
            page.click("#btnNewText")
            page.click("#nfKindDir")
            assert page.locator("#nfContentField").is_hidden(), "文件夹不应显示初始内容框"
            page.fill("#nfName", folder)
            page.click("#nfCreate")
            page.wait_for_selector(f'.card:has(.card-name:text-is("{folder}"))', timeout=8000)
            print(f"[1] 新建文件夹: {folder}")

            # 2. 进入文件夹
            page.click(f'.card:has(.card-name:text-is("{folder}"))')
            page.wait_for_timeout(500)
            crumbs = page.locator(".crumb").all_text_contents()
            assert folder in crumbs, f"未进入文件夹, 面包屑={crumbs}"
            print(f"[2] 进入文件夹, 面包屑: {crumbs}")

            # 3. 建非白名单空文件：内容框隐藏 + 灰字提示
            page.click("#btnNewText")
            page.fill("#nfName", "sample.bin")
            page.wait_for_timeout(150)
            assert page.locator("#nfContentField").is_hidden(), "非文本扩展名不应显示内容框"
            assert page.locator("#nfHint").is_visible(), "非文本扩展名应提示只创建空文件"
            page.click("#nfCreate")
            page.wait_for_selector('.card:has(.card-name:text-is("sample.bin"))', timeout=8000)
            resp = page.request.get(f"{base}/api/list?path=/{folder}")
            entries = {e["name"]: e for e in resp.json()["entries"]}
            assert entries["sample.bin"]["size"] == 0, f"空文件应为 0 字节: {entries['sample.bin']}"
            print(f"[3] 新建空文件 sample.bin: size={entries['sample.bin']['size']}")

            # 4. 建 .txt 白名单文件并编辑保存
            page.click("#btnNewText")
            page.fill("#nfName", "note.txt")
            page.wait_for_timeout(150)
            assert page.locator("#nfContentField").is_visible(), "文本扩展名应显示内容框"
            page.fill("#nfContent", "初始内容")
            page.click("#nfCreate")
            page.wait_for_selector('.card:has(.card-name:text-is("note.txt"))', timeout=8000)

            page.click('.card:has(.card-name:text-is("note.txt"))')
            page.wait_for_selector(".pv-text", timeout=8000)
            assert "初始内容" in page.locator(".pv-text").text_content(), "初始内容未写入"
            page.click('.pv-tools button:has-text("编辑")')
            page.wait_for_selector(".pv-editor", timeout=5000)
            page.fill(".pv-editor", "编辑后的内容")
            page.click('.pv-tools button:has-text("保存")')
            page.wait_for_selector("#toast:not(.hidden)", timeout=5000)
            print("[4] 建 note.txt 并编辑保存")

            # 5. 回上级确认条目存在
            page.click("#btnBack")
            page.wait_for_timeout(600)
            page.click('.crumb:has-text("根目录")')
            page.wait_for_selector(f'.card:has(.card-name:text-is("{folder}"))', timeout=8000)
            print(f"[5] 回上级确认文件夹 {folder} 存在")

            browser.close()
    finally:
        # 清理：删除本次创建的文件夹（服务端无删除接口，直接操作文件系统）
        if os.path.isdir(created_dir):
            shutil.rmtree(created_dir, ignore_errors=True)
            print(f"[cleanup] 已删除 {created_dir}")

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
