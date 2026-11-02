# FileServer 前端（V2）

React + TypeScript + Vite 前端，产物由 `go:embed` 嵌入单文件 exe，挂在 `/v2/`；
旧前端仍在 `/`。两套前端共用同一组 `/api/*`，切换前互不影响。

Phase 0 交付工具链与骨架，Phase 1 交付浏览视图：工具栏、网格/列表、搜索、
排序、分页、图片/视频缩略图、空态/骨架/错误态与窄屏适配。

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
- 文件点击在 Phase 1 只下载原文件；图片灯箱、视频播放等留给 Phase 2。

## 验收脚本

先启动服务（示例端口 18080），再运行：

```bash
go run ./cmd/fileserver --dir testdata --host 127.0.0.1 --port 18080
/root/.venv/cdp/bin/python scripts/v2_phase1_test.py
```

脚本覆盖根目录 7 项、进入 photos、图片/视频缩略图、网格/列表切换、排序、
搜索、375×780 不溢出与旧前端 `/` 回归，并输出三张截图路径。

## 目录

```
src/
├── api/        fetch 封装、token/短票、错误归一、目录/搜索/能力探测
├── domain/     文件类型、大小/时间格式化、路径工具、排序
├── components/ Toast、ErrorBoundary、ThemeToggle
├── features/browse/
│   ├── BrowsePage.tsx        页面组合（路径/搜索/排序/分页/滚动恢复）
│   ├── Toolbar.tsx           品牌/面包屑/搜索/刷新/视图/排序/主题/冷门格式
│   ├── GridView.tsx          网格卡片与骨架
│   ├── ListView.tsx          列表三列与骨架
│   ├── FileThumb.tsx         图片/视频/图标缩略图
│   ├── videoThumb.ts         视频抽帧队列（3 路并发 + 缓存 + 取消）
│   ├── useDirectory.ts       目录分页数据
│   ├── useSearch.ts          搜索数据
│   ├── useCapabilities.ts    /api/info + /api/health 与冷门格式开关
│   └── useUrlPath.ts         ?path= 同步
├── styles/     tokens.css（复用旧变量）+ global.css
└── test/       Vitest 全局 setup
```
