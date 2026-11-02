# FileServer 前端（V2）

React + TypeScript + Vite 前端，产物由 `go:embed` 嵌入单文件 exe，挂在 `/v2/`；
旧前端仍在 `/`。两套前端共用同一组 `/api/*`，切换前互不影响。

Phase 0 交付工具链与骨架，Phase 1 交付浏览视图：工具栏、网格/列表、搜索、
排序、分页、图片/视频缩略图、空态/骨架/错误态与窄屏适配。Phase 2 交付预览与
播放：`?view=` 路由、图片灯箱、PDF/文本预览、音频、视频播放器（直链/HLS）与
移动端手势。

## 命令

需要 Node.js >= 24 与 pnpm 12（版本见 `packageManager` 与 `.nvmrc`）。

```bash
pnpm install --frozen-lockfile   # 安装依赖
pnpm dev                          # 开发服务器（127.0.0.1:5173，/api 与 /hls 代理到 8080）
pnpm typecheck                    # tsc --noEmit
pnpm lint                         # ESLint + Stylelint
pnpm test                         # Vitest
pnpm build                        # 构建到 ../internal/server/web-dist
pnpm preview                      # 预览构建产物
```

根目录等价入口：

```bash
make web-install
make web-build
make web-check                    # typecheck + lint + test + build
```

`make check` 仍只跑 Go 部分；没有 pnpm 的环境不会被前端拖挂。

## 构建产物与 /v2/

- `vite.config.ts` 的 `base` 固定为 `/`，`assetsDir` 设为 `v2/assets`，
  因此构建出的资源 URL 正好是 `/v2/assets/<hash>.js|css`。
- 产物提交入库（`internal/server/web-dist`），CI 用
  `git diff --exit-code internal/server/web-dist` 验证可复现。
- `internal/server/webdist.go` 注册 `/v2` → `/v2/` 与 `/v2/` 子树：
    - `index.html` 与 SPA fallback：`Cache-Control: no-store`；
    - `/v2/assets/*`：一年 immutable；
    - 缺失的 asset 返回 404，未知页面路径回退 index.html。
- `pnpm build` 最后会写 `web-dist/.keep`，保证 `//go:embed all:web-dist`
  在产物被清空时仍能编译；运行时若缺少 `index.html`，`/v2/` 返回 503。

## 鉴权与主题约定

- token 沿用旧前端键：`fs.token` / `fs.token.expires`，sessionStorage 优先，
  其次 localStorage；请求同时带 `X-Auth` 与 `Authorization: Bearer`。
  401 清空凭据并跳回 `/`（认证开启时旧前端显示登录覆盖层）。
- 图片/视频等浏览器原生 GET 请求通过 `POST /api/auth/ticket` 换短票，
  以 `?t=` 附加；认证关闭时不产生该请求。
- 主题键 `fs.theme`（`dark` / `light`），未设置时跟随
  `prefers-color-scheme`。颜色只用 `src/styles/tokens.css` 中与旧前端一致的变量。
- 视图/排序偏好沿用旧键：`fs.view`（grid/list）、`fs.sort`（name/size/time）、
  `fs.order`（asc/desc）。

## 浏览视图行为

- 路径由 `?path=` 驱动，面包屑每级可点，品牌按钮回根目录。
- 搜索框 250ms 防抖后请求 `/api/search`，Esc 清空并回到目录；命中片段高亮。
- 网格视图：图片走 `/api/thumb`；MP4/M4V/MOV/WebM 走 `/api/thumb-src` +
  `<video>` seek + canvas 抽帧（IntersectionObserver 懒加载、最多 3 路并发、
  卸载时取消）；冷门格式在开启服务端转码时走 `/api/thumb`，否则显示图标。
- 列表视图：名称/大小/修改时间三列，目录优先，名称按钮可进入目录或下载文件。
- 分页：`/api/list` 的 `limit=300` + `offset`，滚动到底或点击「加载更多」追加。
- 文件点击：图片/视频/音频/PDF/文本/代码进入预览，其余类型直接下载。
- 预览目标由 `?view=` 驱动；刷新、前进/后退、Esc/返回按钮都能恢复；返回列表时
  还原进入前的滚动位置。

## 预览与播放器（Phase 2）

- **图片灯箱**：`/api/file` 原图优先，加载骨架，滚轮/双指缩放（25%–800%）、
  旋转、左右切换、下载；键盘 `←/→/Esc/+/-/R`，触屏横滑切图；固定深色背景。
- **PDF**：`<iframe>` 内嵌浏览器 PDF 查看器，未启用时显示下载兜底；下载入口常驻。
- **文本/代码**：等宽只读预览（编辑留 Phase 3）；超过 2 MiB 用 Range 只取前
  2 MiB 并提示截断。
- **音频**：原生 `<audio controls>`，与视频共用进度记忆。
- **视频播放器**：
    - `/api/video-info` 决策：`direct` 直链或 `hls`（hls.js 1.5.20，按需动态加载）；
    - HLS：8 秒 manifest 轮询、ENDLIST 停止、超前 seek 收敛到已生成范围并提示；
    - 每次 hls.js 请求用最新短票重写 `?t=`；离开预览立即 `abandon=1` 并销毁实例；
    - 自绘控制条：播放/暂停、缓冲进度、时间、音量、倍速、全屏、画中画；
    - 键盘：空格、`←/→` ±5s、`↑/↓` 音量、`M/F/P`；
    - 移动端：双击左右 ±10s、双击中间暂停、长按 350ms 2x（松手恢复原速率）、
      横滑 seek、右侧竖滑音量；
    - 进度记忆键为 `fs.progress.<path>:<size>`，文件大小变化时旧进度自动失效。
- **无障碍**：控制条全部使用原生 button/input/select，带 `aria-label`；375 宽不溢出。

## 验收脚本

先启动服务（HLS 用例需要 `--ffmpeg`），再运行：

```bash
go run ./cmd/fileserver --dir testdata --host 127.0.0.1 --port 18080 --no-qr --ffmpeg
/root/.venv/cdp/bin/python scripts/v2_phase1_test.py
/root/.venv/cdp/bin/python scripts/v2_phase2_test.py
```

Phase 1 覆盖根目录 7 项、进入 photos、图片/视频缩略图、网格/列表切换、排序、
搜索、375×780 不溢出与旧前端 `/` 回归。Phase 2 覆盖灯箱缩放/切图/刷新恢复、文本
预览、视频播放/空格暂停/画中画、375×780 双击/长按/横滑/竖滑、返回滚动位置与
HLS `abandon=1`；没有 ffmpeg 时跳过 HLS 并打印原因。两个脚本都会输出截图路径。

## 目录

```
src/
├── api/        fetch 封装、token/短票、错误归一、目录/搜索/能力探测
├── domain/     文件类型、大小/时间格式化、路径工具、排序
├── components/ Toast、ErrorBoundary、ThemeToggle
├── features/browse/
│   ├── BrowsePage.tsx        页面组合（路径/预览/搜索/排序/分页/滚动恢复）
│   ├── Toolbar.tsx           品牌/面包屑/搜索/刷新/视图/排序/主题/冷门格式
│   ├── GridView.tsx          网格卡片与骨架
│   ├── ListView.tsx          列表三列与骨架
│   ├── FileThumb.tsx         图片/视频/图标缩略图
│   ├── videoThumb.ts         视频抽帧队列（3 路并发 + 缓存 + 取消）
│   ├── useDirectory.ts       目录分页数据
│   ├── useSearch.ts          搜索数据
│   ├── useCapabilities.ts    /api/info + /api/health 与冷门格式开关
│   └── useBrowseRoute.ts     ?path= / ?view= 同步
├── features/preview/
│   ├── PreviewPage.tsx       预览分发与返回/下载
│   ├── Lightbox.tsx          图片灯箱（缩放/旋转/切图）
│   ├── VideoPlayer.tsx       视频播放器（直链/HLS、控制条、快捷键、手势）
│   ├── AudioPlayer.tsx       原生音频 + 进度记忆
│   ├── PdfPreview.tsx        PDF 内嵌 + 下载兜底
│   ├── TextPreview.tsx       文本/代码只读预览
│   ├── attachHls.ts          hls.js 挂载/轮询/seek 收敛
│   ├── usePlayerGestures.ts  移动端手势
│   └── progress.ts           path+size 进度记忆
├── styles/     tokens.css（复用旧变量）+ global.css
└── test/       Vitest 全局 setup
```
