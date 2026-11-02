# FileServer 前端（V2）

Phase 0 的 React 骨架，产物由 `go:embed` 嵌入单文件 exe，挂在 `/v2/`；旧前端仍在 `/`。
两套前端共用同一组 `/api/*`，切换前互不影响。

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
  401 清空凭据并触发全局提示。
- 主题键 `fs.theme`（`dark` / `light`），未设置时跟随
  `prefers-color-scheme`。颜色只用 `src/styles/tokens.css` 中与旧前端一致的变量。

## 目录

```
src/
├── api/        fetch 封装、token、错误归一、目录列表
├── domain/     文件类型、大小/时间格式化、路径工具
├── components/ Toast、ErrorBoundary、ThemeToggle
├── features/   browse（最小目录列表）
├── styles/     tokens.css（复用旧变量）+ global.css
└── test/       Vitest 全局 setup
```
