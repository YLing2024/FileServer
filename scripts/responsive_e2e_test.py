"""移动端响应式巡检（Playwright）。

覆盖 `doc/REQUIREMENTS-responsive-mobile.md` 的可机检项：
  1. 390×844 触屏：工具栏/面包屑命中区 ≥44px、视图切换 ≥32×32、搜索/表单控件 ≥44px 高且
     字号 ≥16px、相邻操作间距 ≥8px；无横向溢出；无 <12px 可见文本。
  2. 新建弹窗窄屏单列、按钮 ≥44px。
  3. 登录页（可选 --auth-base）：口令输入 ≥44px、按钮 ≥44px、「记住我」命中区 ≥44px。
  4. 桌面回归护栏：1440×900 细指针下关键控件尺寸保持改造前数值（触屏规则不外泄）。

用法（先启动被测服务）：
    python scripts/responsive_e2e_test.py --base http://127.0.0.1:8099
    python scripts/responsive_e2e_test.py --base http://127.0.0.1:8099 --auth-base http://127.0.0.1:8100
"""
import argparse
import sys

from playwright.sync_api import sync_playwright

fail = []
notices = []

VISIBLE_SMALL_JS = r"""() => {
  const out = [];
  const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const seen = new Set();
  let n;
  while ((n = walk.nextNode())) {
    const t = (n.textContent || '').trim();
    if (!t) continue;
    const el = n.parentElement;
    if (!el || seen.has(el)) continue;
    const cs = getComputedStyle(el);
    if (cs.display === 'none' || cs.visibility === 'hidden') continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const fs = parseFloat(cs.fontSize);
    if (fs < 12) { seen.add(el); out.push({ text: t.slice(0, 24), cls: el.className || el.tagName, fs }); }
  }
  return out;
}"""


def box(page, sel):
    return page.evaluate(
        "(s) => { const e = document.querySelector(s); if (!e) return null; const b = e.getBoundingClientRect(); const cs = getComputedStyle(e);"
        " return {w: b.width, h: b.height, fs: parseFloat(cs.fontSize), display: cs.display, op: parseFloat(cs.opacity)}; }",
        sel,
    )


def check_touch_targets(page, label):
    for sel in ["#btnHome", "#btnRefresh", "#btnFFmpeg", "#btnNewText", "#btnOrder", "#btnTheme"]:
        b = box(page, sel)
        if not b:
            fail.append(f"{label}: 缺少 {sel}")
        elif b["w"] < 44 or b["h"] < 44:
            fail.append(f"{label}: {sel} 命中区不足 44px -> {b['w']:.0f}x{b['h']:.0f}")
    for sel in ["#btnGrid", "#btnList"]:
        b = box(page, sel)
        if not b:
            fail.append(f"{label}: 缺少 {sel}")
        elif b["w"] < 32 or b["h"] < 32:
            fail.append(f"{label}: 视图切换 {sel} 不足 32x32 -> {b['w']:.0f}x{b['h']:.0f}")
    crumb = page.evaluate("() => { const e = document.querySelector('.crumb'); return e ? e.getBoundingClientRect().height : 0; }")
    if crumb < 44:
        fail.append(f"{label}: 面包屑按钮高度 {crumb:.0f} < 44")
    sb, si = box(page, "#searchBox"), box(page, "#searchInput")
    if not sb or sb["h"] < 44:
        fail.append(f"{label}: 搜索框高度不足 44 -> {sb}")
    if not si or si["h"] < 44:
        fail.append(f"{label}: 搜索输入框高度不足 44 -> {si}")
    elif si["fs"] < 16:
        fail.append(f"{label}: 搜索输入框字号 {si['fs']} < 16")
    gaps = page.evaluate("() => { const cs = getComputedStyle(document.querySelector('.toolbar-actions')); return {col: cs.columnGap, row: cs.rowGap}; }")
    for k, v in gaps.items():
        val = float((v or "0").replace("px", "").strip() or 0)
        if val < 8:
            fail.append(f"{label}: 工具栏操作间距 {k}={v} < 8")


def check_no_small_text(page, label):
    small = page.evaluate(VISIBLE_SMALL_JS)
    if small:
        fail.append(f"{label}: 存在 <12px 可见文本 -> {small[:5]}")


def check_no_overflow(page, label):
    ov = page.evaluate("() => ({sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth})")
    if ov["sw"] > ov["cw"] + 1:
        fail.append(f"{label}: 横向溢出 scrollWidth={ov['sw']} clientWidth={ov['cw']}")


def run_touch_mobile(p, base):
    b = p.chromium.launch(headless=True)
    page = b.new_page(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True, is_mobile=True)
    page.on("pageerror", lambda e: fail.append(f"390:pageerror {e}"))
    page.goto(base, wait_until="networkidle")
    page.wait_for_selector("#grid .card", timeout=10000)
    page.wait_for_timeout(300)
    check_touch_targets(page, "390 文件页")
    check_no_small_text(page, "390 文件页")
    check_no_overflow(page, "390 文件页")

    # 新建弹窗：单列 + 控件 ≥44 + 无溢出
    page.click("#btnNewText")
    page.wait_for_timeout(250)
    page.fill("#nfName", "note.txt")  # 文本白名单 → 显示初始内容框
    page.wait_for_timeout(150)
    check_no_overflow(page, "390 新建弹窗")
    for sel in ["#nfName", "#nfContent"]:
        b2 = box(page, sel)
        if b2 and b2["display"] != "none" and b2["h"] > 0:
            if b2["h"] < 44:
                fail.append(f"390: {sel} 高度 {b2['h']:.0f} < 44")
            if b2["fs"] < 16:
                fail.append(f"390: {sel} 字号 {b2['fs']} < 16")
    for sel in ["#nfKindFile", "#nfKindDir", "#nfCancel", "#nfCreate"]:
        b2 = box(page, sel)
        if b2 and b2["h"] < 44:
            fail.append(f"390: {sel} 高度 {b2['h']:.0f} < 44")
    d = page.evaluate("() => getComputedStyle(document.querySelector('.modal-actions')).flexDirection")
    if d != "column":
        fail.append(f"390: 新建弹窗按钮非单列 flex-direction={d}")
    page.keyboard.press("Escape")

    # 列表视图：触屏下行操作常显且命中区 ≥32
    page.click("#btnList")
    page.wait_for_timeout(400)
    ra = box(page, ".row-act")
    if not ra or ra["op"] < 1:
        fail.append(f"390: 列表行操作在触屏下未常显 opacity={ra and ra['op']}")
    mb = box(page, ".row-act .mini-btn")
    if mb and mb["h"] < 32:
        fail.append(f"390: 列表行操作命中区 {mb['h']:.0f} < 32")
    check_no_overflow(page, "390 列表视图")

    # Toast 仍可见（沿用 #toast:not(.hidden) 契约）
    page.evaluate("() => window.toast('响应式自检', false)")
    page.wait_for_selector("#toast:not(.hidden)", timeout=3000)
    notices.append("390: #toast 可见性 OK")

    # 768 触屏平板：排序下拉等表单控件同样达标
    page2 = b.new_page(viewport={"width": 768, "height": 1024}, device_scale_factor=2, has_touch=True, is_mobile=True)
    page2.goto(base, wait_until="networkidle")
    page2.wait_for_selector("#grid .card", timeout=10000)
    ss = box(page2, "#sortSelect")
    if ss and ss["display"] != "none":
        if ss["h"] < 44:
            fail.append(f"768: 排序下拉高度 {ss['h']:.0f} < 44")
        if ss["fs"] < 16:
            fail.append(f"768: 排序下拉字号 {ss['fs']} < 16")
    check_touch_targets(page2, "768 文件页")
    check_no_overflow(page2, "768 文件页")
    b.close()


def run_desktop_guard(p, base):
    b = p.chromium.launch(headless=True)
    # 桌面鼠标：无 touch / is_mobile，细指针
    page = b.new_page(viewport={"width": 1440, "height": 900})
    page.goto(base, wait_until="networkidle")
    page.wait_for_selector("#grid .card", timeout=10000)
    home = box(page, "#btnHome")
    grid = box(page, "#btnGrid")
    sb = box(page, "#searchBox")
    si = box(page, "#searchInput")
    kbd_fs = page.evaluate("() => parseFloat(getComputedStyle(document.querySelector('.searchbox kbd')).fontSize)")
    if not home or abs(home["w"] - 36) > 0.5 or abs(home["h"] - 36) > 0.5:
        fail.append(f"桌面: #btnHome 应为 36x36 -> {home}")
    if not grid or abs(grid["w"] - 32) > 0.5 or abs(grid["h"] - 30) > 0.5:
        fail.append(f"桌面: #btnGrid 应为 32x30 -> {grid}")
    if not sb or abs(sb["h"] - 36) > 0.5:
        fail.append(f"桌面: 搜索框高度应保持 36 -> {sb}")
    if not si or abs(si["fs"] - 13.5) > 0.01:
        fail.append(f"桌面: 搜索输入字号应保持 13.5 -> {si}")
    if abs(kbd_fs - 11) > 0.01:
        fail.append(f"桌面: 搜索框 kbd 字号应保持 11 -> {kbd_fs}")
    b.close()


def run_auth(p, base):
    b = p.chromium.launch(headless=True)
    page = b.new_page(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True, is_mobile=True)
    page.goto(base, wait_until="networkidle")
    page.wait_for_selector("#auth:not(.hidden)", timeout=8000)
    check_no_overflow(page, "390 登录页")
    pw = box(page, "#auPw")
    if pw:
        if pw["h"] < 44:
            fail.append(f"登录页: 口令输入高度 {pw['h']:.0f} < 44")
        if pw["fs"] < 16:
            fail.append(f"登录页: 口令输入字号 {pw['fs']} < 16")
    else:
        notices.append("登录页: 无口令字段（仅 TOTP 形态）")
    sub = box(page, "#auSubmit")
    if not sub or sub["h"] < 44:
        fail.append(f"登录页: 登录按钮高度不足 44 -> {sub}")
    rem = box(page, ".auth-remember")
    if not rem or rem["h"] < 44:
        fail.append(f"登录页: 「记住我」命中区高度不足 44 -> {rem}")
    d = page.evaluate("() => getComputedStyle(document.querySelector('.modal-actions')).flexDirection")
    if d != "column":
        fail.append(f"登录页: 按钮未单列 flex-direction={d}")
    check_no_small_text(page, "390 登录页")
    b.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8099")
    ap.add_argument("--auth-base", default="", help="认证模式下（password 或 totp）的实例地址，用于登录页巡检")
    args = ap.parse_args()

    with sync_playwright() as p:
        run_touch_mobile(p, args.base)
        run_desktop_guard(p, args.base)
        if args.auth_base:
            run_auth(p, args.auth_base)
        else:
            notices.append("未提供 --auth-base，跳过登录页巡检")

    for n in notices:
        print("  ·", n)
    if fail:
        print("\n=== 响应式巡检失败项 ===")
        for f in fail:
            print("  ✗", f)
        sys.exit(1)
    print("\n响应式巡检全部通过 ✓")
    sys.exit(0)


if __name__ == "__main__":
    main()
