"""导航历史回归测试：目录跳转必须写入 URL，物理返回键逐级回退而非退出应用"""
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

        def url_path():
            return page.evaluate("new URL(location.href).searchParams.get('path') || '/'")

        def list_names():
            return page.locator(".card-name").all_text_contents()

        # 1. 首页
        page.goto(BASE, wait_until="networkidle")
        assert url_path() == "/", f"首页 URL 异常: {url_path()}"
        print(f"[1] 首页 URL: {url_path()}")

        # 2. 逐级进入 deep/a/b，URL 应同步变化
        page.click('.card:has(.card-name:text-is("deep"))')
        page.wait_for_timeout(600)
        assert url_path() == "/deep", f"进入 deep 后 URL 异常: {url_path()}"
        print(f"[2] 进入 deep: URL={url_path()}")
        page.click('.card:has(.card-name:text-is("a"))')
        page.wait_for_timeout(600)
        assert url_path() == "/deep/a", f"进入 a 后 URL 异常: {url_path()}"
        page.click('.card:has(.card-name:text-is("b"))')
        page.wait_for_selector('.card:has(.card-name:text-is("file.txt"))', timeout=8000)
        assert url_path() == "/deep/a/b", f"进入 b 后 URL 异常: {url_path()}"
        print(f"[3] 进入 /deep/a/b: URL={url_path()}, 内容={list_names()}")

        # 3. 物理返回（浏览器后退）应逐级回退，而不是退出应用
        page.go_back()
        page.wait_for_timeout(700)
        assert url_path() == "/deep/a", f"返回1 URL 异常: {url_path()}"
        assert "b" in list_names(), f"返回1 内容异常: {list_names()}"
        print(f"[4] 后退1: URL={url_path()}, 内容={list_names()}")

        page.go_back()
        page.wait_for_timeout(700)
        assert url_path() == "/deep", f"返回2 URL 异常: {url_path()}"
        assert "a" in list_names(), f"返回2 内容异常: {list_names()}"
        print(f"[5] 后退2: URL={url_path()}, 内容={list_names()}")

        page.go_back()
        page.wait_for_timeout(700)
        assert url_path() == "/", f"返回3 URL 异常: {url_path()}"
        assert "deep" in list_names(), f"返回3 内容异常: {list_names()}"
        print(f"[6] 后退3: URL={url_path()}, 内容={list_names()}")

        # 4. 前进应逐级恢复
        page.go_forward()
        page.wait_for_timeout(700)
        assert url_path() == "/deep", f"前进1 URL 异常: {url_path()}"
        page.go_forward()
        page.wait_for_timeout(700)
        page.go_forward()
        page.wait_for_selector('.card:has(.card-name:text-is("file.txt"))', timeout=8000)
        assert url_path() == "/deep/a/b", f"前进3 URL 异常: {url_path()}"
        print(f"[7] 前进恢复: URL={url_path()}")

        # 5. 深层链接直接打开（刷新场景）
        page.goto(BASE + "/?path=/deep/a/b", wait_until="networkidle")
        page.wait_for_selector('.card:has(.card-name:text-is("file.txt"))', timeout=8000)
        assert url_path() == "/deep/a/b", f"深层链接 URL 异常: {url_path()}"
        print(f"[8] 深层链接直达: URL={url_path()}, 内容={list_names()}")

        # 6. 预览页与列表页历史互操作
        page.goto(BASE, wait_until="networkidle")
        page.click('.card:has(.card-name:text-is("videos"))')
        page.wait_for_timeout(600)
        assert url_path() == "/videos", f"进入 videos URL 异常: {url_path()}"
        page.click('.card:has(.card-name:text-is("sample.mp4"))')
        page.wait_for_selector("#player", timeout=8000)
        view = page.evaluate("new URL(location.href).searchParams.get('view')")
        assert view == "/videos/sample.mp4", f"预览 URL 异常: {view}"
        print(f"[9] 预览: view={view}")

        # 物理返回：预览 → 列表（videos），播放器释放
        page.go_back()
        page.wait_for_timeout(800)
        assert url_path() == "/videos", f"预览返回后 URL 异常: {url_path()}"
        assert page.locator('.card:has(.card-name:text-is("sample.mp4"))').count() == 1, "返回后列表未恢复"
        assert page.evaluate("document.querySelectorAll('#pvMain video').length === 0"), "返回后播放器未释放"
        print(f"[10] 预览返回: URL={url_path()}, 列表与播放器状态正确")

        # 再后退：videos → 根目录
        page.go_back()
        page.wait_for_timeout(700)
        assert url_path() == "/", f"videos 返回后 URL 异常: {url_path()}"
        print(f"[11] 逐级返回根目录: URL={url_path()}")

        browser.close()

    print("\n=== 导航回归结果 ===")
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
    print("URL 驱动导航全部通过 ✓")
    sys.exit(0)


if __name__ == "__main__":
    main()
