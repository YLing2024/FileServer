"""特殊字符路径端到端测试：URL 编码安全 + 全链路功能（真实浏览器）"""
import os
import sys
from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:8099"
errors = []
fail = []

SPECIAL_DIR = "目录+%#& 空格"
SPECIAL_FILES = [
    "a#b%c+d&e f.txt",
    "空格 和 中文.txt",
    "引号'单引号.txt",
    "括号(1)[2]{3}.txt",
    "emoji🔥符号.txt",
    "a=b&c=d%.txt",
]


def ensure_testdata():
    """在 testdata 下创建特殊字符测试文件（若不存在）"""
    root = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "testdata")
    d = os.path.join(root, "special", SPECIAL_DIR)
    os.makedirs(d, exist_ok=True)
    for name in SPECIAL_FILES:
        p = os.path.join(d, name)
        if not os.path.exists(p):
            with open(p, "w", encoding="utf-8") as f:
                f.write("content:" + name)


def main():
    ensure_testdata()
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: errors.append(str(e)))

        # 1. 逐级进入特殊字符目录，URL 必须正确编码
        page.goto(BASE, wait_until="networkidle")
        page.click('.card:has(.card-name:text-is("special"))')
        page.wait_for_timeout(700)
        page.click(f'.card:has(.card-name:text-is("{SPECIAL_DIR}"))')
        page.wait_for_selector(".card", timeout=8000)
        url = page.url
        print(f"[1] 进入特殊目录后 URL: {url}")
        assert "%" in url and "&" not in url.replace("%26", ""), f"URL 编码异常: {url}"
        # 面包屑正确显示原始名称
        crumbs = page.locator(".crumb").all_text_contents()
        assert SPECIAL_DIR in crumbs, f"面包屑异常: {crumbs}"
        print(f"[2] 面包屑: {crumbs}")

        # 2. 列表显示全部特殊字符文件
        names = page.locator(".card-name").all_text_contents()
        for n in SPECIAL_FILES:
            assert n in names, f"特殊字符文件未显示: {n}"
        print(f"[3] 列表完整显示 {len(SPECIAL_FILES)} 个特殊字符文件")

        # 3. 点击下载（触发真实下载，验证 URL 编码正确性）
        for n in SPECIAL_FILES[:2]:
            with page.expect_download(timeout=10000) as dl_info:
                page.hover(f'.card:has(.card-name:text-is("{n}"))')
                page.click(f'.card:has(.card-name:text-is("{n}")) .card-dl')
            dl = dl_info.value
            print(f"[4] 下载 {n!r} -> {dl.suggested_filename}")
            assert dl.suggested_filename == n, f"下载文件名异常: {dl.suggested_filename}"

        # 4. 文本预览（含单引号文件名的下载按钮注入测试）
        page.click('.card:has(.card-name:text-is("引号\'单引号.txt"))')
        page.wait_for_selector(".pv-text", timeout=8000)
        txt = page.locator(".pv-text").text_content()
        assert "content:引号'单引号.txt" in txt, f"预览内容异常: {txt}"
        print(f"[5] 预览特殊字符文件 OK: {txt[:30]}...")
        page.go_back()
        page.wait_for_timeout(700)

        # 5. 刷新页面（URL 含编码路径，刷新后应恢复目录）
        page.reload(wait_until="networkidle")
        page.wait_for_selector(".card", timeout=8000)
        names2 = page.locator(".card-name").all_text_contents()
        assert SPECIAL_FILES[0] in names2, "刷新后未恢复特殊目录"
        print("[6] 刷新后目录恢复 OK")

        # 6. 搜索特殊字符
        page.goto(BASE, wait_until="networkidle")
        page.fill("#searchInput", "emoji🔥")
        page.wait_for_timeout(1200)
        scards = page.locator(".card").count()
        print(f"[7] 搜索 emoji🔥: {scards} 个结果")
        assert scards >= 1

        browser.close()

    print("\n=== 特殊字符端到端结果 ===")
    real = [e for e in errors if "favicon" not in e]
    if real:
        print("控制台错误:")
        for e in real[:10]:
            print("  ", e)
    if fail:
        for f in fail:
            print("  ✗", f)
        sys.exit(1)
    print("全部通过 ✓")
    sys.exit(0)


if __name__ == "__main__":
    main()
