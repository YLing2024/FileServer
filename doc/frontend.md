# 前端架构

前端是 **React 19 + TypeScript + Vite** 单页应用，源码在 `web/`，构建产物在
`internal/server/web-dist`，由 `//go:embed all:web-dist` 嵌入单文件 exe。Phase 5
起新前端同时占据 `/` 与 `/v2/`（同一个 handler、同一份 `index.html`，不再有两套
页面）；旧的 `internal/server/web` 原生 JS 前端已删除。公开分享页仍由 `/s/<token>`
返回 `web-dist/share.html`（独立 Vite 入口）。

> 组件登记见 `doc/COMPONENTS.md`，开发与构建命令见 `web/README.md`，
> 前端检查与发布流程见 `doc/build-and-test.md`。

## 1. 目录

```
web/
├── index.html                主应用入口（/ 与 /v2/）
├── share.html                公开分享页入口（/s/<token>）
├── vite.config.ts            base=/、assetsDir=v2/assets、双入口、/api 与 /hls 代理
├── src/
│   ├── main.tsx              挂载、主题初始化
│   ├── App.tsx               错误边界 + Toast + 认证门禁 + 浏览页
│   ├── api/                  fetch 封装、凭据/短票、错误归一、各业务接口
│   ├── domain/               文件类型、大小/时间格式化、路径、排序
│   ├── components/           Toast、ErrorBoundary、ThemeToggle、Modal
│   ├── features/auth/        认证状态探测与内嵌登录
│   ├── features/browse/      工具栏、网格/列表、选择、搜索、缩略图、上传入口
│   ├── features/preview/     图片灯箱、视频/音频、PDF、文本预览
│   ├── features/upload/      上传队列、冲突处理、拖拽/粘贴
│   ├── features/manage/      重命名/移动/复制/删除/回收站
│   ├── features/share/       分享创建/管理
│   ├── features/settings/    关于/版本、会话管理
│   ├── share/                公开分享页独立入口
│   └── styles/               tokens.css、global.css、通用按钮
└── scripts/                  构建产物占位文件脚本
```

## 2. 路由与状态

- 浏览页由查询参数驱动：`?path=<相对路径>` 浏览目录，`?view=<相对路径>` 打开预览。
  刷新、前进/后退、手机返回手势都由 `popstate` 恢复；不在 URL 中存凭据。
- 视图/排序偏好沿用旧键：`fs.view`（grid/list）、`fs.sort`（name/size/time）、
  `fs.order`（asc/desc）；主题键 `fs.theme`（dark/light），未设置时跟随系统。
- 前端路由只有查询参数，不依赖 History API 的路径段，因此 Go 端的 SPA fallback
  只需把未知的非资源路径交回 `index.html`。

## 3. 服务端静态资源约定

- `vite.config.ts` 的 `base` 为 `/`，`assetsDir` 为 `v2/assets`，哈希资源 URL 仍是
  `/v2/assets/<hash>.js|css`，与 Phase 4 保持一致。
- `internal/server/webdist.go` 把同一个 handler 挂到 `/` 与 `/v2/`：
  - `/`、`/v2/`：同一份 `index.html`，`Cache-Control: no-store`；
  - `/v2/assets/*`（兼容 `/assets/*`）：一年 `immutable`；
  - 其他静态文件：`no-store`；
  - 未知页面路径回退 `index.html`；带扩展名的未知文件（如旧 `/app.js`）返回 404；
  - `/v2` 精确路径 301 到 `/`，保留查询串；
  - 未匹配的 `/api/*` 不会被 SPA 吞掉，仍返回 404。
- CSP、Referrer-Policy、nosniff 与切换前一致，未放宽；公开分享页使用更严格的
  独立 CSP（不允许内联样式）。
- 产物缺失时根路径返回 503，API 不受影响；`web-dist/.keep` 保证 embed 可编译。

## 4. 浏览视图

- 工具栏：品牌回根、面包屑、搜索、刷新、上传（可选）、回收站、分享、冷门格式开关、
  网格/列表切换、排序、主题、设置。
- 网格：图片走 `/api/thumb`；MP4/M4V/MOV/WebM 走 `/api/thumb-src` + `<video>`
  seek + canvas 抽帧（IntersectionObserver 懒加载、最多 3 路并发、卸载取消）；
  冷门格式在开启服务端转码时走 `/api/thumb`，否则显示图标。
- 列表：名称/大小/修改时间三列，目录优先，名称按钮可进入目录或下载文件。
- 分页：`/api/list` 使用 `limit=300` + `offset`，滚动到底或点击「加载更多」追加。
- 搜索：输入 250ms 防抖后请求 `/api/search`，Esc 清空并回到目录；命中片段高亮。
- 文件点击：图片/视频/音频/PDF/文本/代码进入预览，其余类型直接下载。
- 选择：登录管理会话下桌面单击选中、Ctrl/⌘ 增减、Shift 连选、Ctrl/⌘+A 全选、
  Esc 清空；移动端长按进入选择。选择条提供下载、分享、重命名、移动、复制、删除。
- 空态、骨架屏、错误态与重试按钮统一，不静默失败。

## 5. 预览与播放

- **图片灯箱**：`/api/file` 原图，滚轮/双指缩放（25%–800%）、旋转、左右切换、
  下载；键盘 `←/→/Esc/+/-/R`，触屏横滑切图。
- **视频**：先查 `/api/video-info`，原生格式直链 `<video>`；需要转码时由 hls.js
  拉 `/api/hls`。不自动播放；空格播放/暂停、←→ ±5s、↑↓ 音量、M 静音、F 全屏。
  离开预览时 `abandon=1` 终止服务端转码并销毁 hls 实例。
- **音频**：一律直链，浏览器原生控件。
- **PDF**：同源 `<iframe>`，未启用时显示下载兜底。
- **文本/代码**：等宽预览走 `/api/file`（超 2 MiB 用 Range 截断）；点击「编辑」按钮时
  才请求 `/api/text` 获取内容与版本，保存带版本校验，409 提示他人已修改；白名单与
  2 MiB 上限由服务端判定，超限仍可只读预览。
- 移动端手势：双击左/右 1/3 快退/快进 10s、中间播放暂停；长按 350ms 临时 2x；
  横滑 seek（松手才提交）；右侧竖滑调音量。桌面鼠标环境不注册这些手势。

## 6. 上传与文件管理

- 上传默认关闭：`/api/info` 的 `allow_upload=false` 时不渲染上传入口，端点自身也
  拒绝。开启后支持文件多选、文件夹（`webkitdirectory` / `webkitGetAsEntry`）、
  整页拖拽与粘贴；目标目录在选择时快照。
- 队列逐文件显示校验/秒传检查/上传进度，可取消与重试；文件夹先 `POST /api/upload/mkdir`
  保留空目录，单次最多 2000 文件。
- >8 MiB 文件按 8 MiB 分片，`upload_id` 由前 1 MiB + 大小 + 名称派生，中断后重新
  选择同一文件只补缺片；`POST /api/upload/complete` 合并。
- 秒传：前端流式 SHA-256（>200 MiB 用 Worker，失败回退主线程）后调
  `POST /api/upload/instant`；命中直接落位，未命中继续上传。
- 同名冲突：`mode=ask`，409 `{code:"conflict", existing:[...]}` 弹窗选择覆盖 /
  自动改名 / 取消，对剩余整批生效。
- 文件管理（仅登录会话）：重命名、移动、复制、删除到回收站、恢复/彻底删除/清空；
  批量逐项返回成功与失败明细，部分失败不静默。删除仅移入 `.FileServer/trash/`，
  不改用户原文件之外的路径。

## 7. 分享与认证

- 分享创建：文件或文件夹、1/7/30 天或永久、可选 4 位提取码、下载次数上限、
  预览开关与备注；创建成功展示绝对链接与提取码，可复制。
- 分享管理：列表展示状态/有效期/下载次数，可复制链接、撤销（立即失效）、删除记录。
- 公开分享页：独立 `share.html`，匿名访问，支持提取码、单文件预览/下载、目录浏览、
  目录内单文件预览与当前目录 zip；不加载管理端组件，错误页不显示真实路径。
- 认证：`AuthGate` 探测 `/api/auth/status`；未登录时内嵌口令/TOTP/记住我表单。
  首次设置由 `SetupForm` 在新前端内完成（口令、TOTP 二维码/密钥、可跳过 TOTP），
  不再依赖旧前端。凭据键仍为 `fs.token` / `fs.token.expires`，sessionStorage 优先、
  其次 localStorage；请求带 `Authorization: Bearer`，媒体/下载等原生请求用
  `POST /api/auth/ticket` 换 5 分钟短票并以 `?t=` 附加。401 清凭据与短票后原地重新登录。
  默认认证关闭。

## 8. 构建与测试

```bash
make web-install       # pnpm install --frozen-lockfile
make web-build         # 构建到 internal/server/web-dist
make web-check         # typecheck + lint + test + build
make check             # Go 门禁；有 pnpm 与依赖时附带 web-check
```

没有 pnpm 或未安装 `web/node_modules` 时，`make check` 会打印提示并跳过前端检查，
不阻断 Go 构建；CI 的 `frontend` job 仍强制跑完整四段并校验构建产物可复现。

端到端脚本在 `scripts/v2_phase*.py`，Phase 5 起根路径即新前端；完整回归见
`scripts/v2_phase5_test.py`。

## 9. 回退

旧原生前端已从仓库删除。如需回退到旧前端，只能依赖 git 历史：
`git log -- internal/server/web` 找到删除前的提交，恢复该目录并把
`internal/server/server.go` 的 `/` 路由指回旧 `frontendHandler`（同时恢复当时的
`webdist.go`）。当前版本不再提供运行时开关或两套前端并存。
