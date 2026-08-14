"""诊断移动端搜索框溢出：聚焦/输入前后测量尺寸与位置"""
from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:8099"

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    for vp in [{"width": 390, "height": 844}, {"width": 375, "height": 667}, {"width": 768, "height": 1024}]:
        page = browser.new_page(viewport=vp, has_touch=True)
        page.goto(BASE, wait_until="networkidle")
        info = page.evaluate("""() => {
          const sb = document.getElementById('searchBox');
          const ta = document.querySelector('.toolbar-actions');
          const tb = document.querySelector('.toolbar');
          const r = (el) => { const b = el.getBoundingClientRect(); return {x: Math.round(b.x), w: Math.round(b.width), right: Math.round(b.right)}; };
          return { vw: window.innerWidth, search: r(sb), actions: r(ta), toolbar: r(tb) };
        }""")
        print(f"viewport {vp['width']}px 初始:", info)
        page.click("#searchInput")
        page.fill("#searchInput", "这是一段非常长的搜索文字测试测试测试测试测试")
        info2 = page.evaluate("""() => {
          const sb = document.getElementById('searchBox');
          const b = sb.getBoundingClientRect();
          return {x: Math.round(b.x), w: Math.round(b.width), right: Math.round(b.right), vw: window.innerWidth,
                  overflowRight: b.right > window.innerWidth, overflowLeft: b.left < 0};
        }""")
        print(f"viewport {vp['width']}px 输入后:", info2)
        page.screenshot(path=f".tools/shot-search-{vp['width']}.png")
        page.close()
    browser.close()
