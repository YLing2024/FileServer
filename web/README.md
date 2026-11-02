# FileServer 前端

React + TypeScript + Vite 前端，产物由 `go:embed` 嵌入单文件 exe，默认挂在根路径
`/`；`/v2/` 作为兼容前缀返回同一份 `index.html`（`/v2` 精确路径 301 到 `/`）。
旧的 `internal/server/web` 原生 JS 前端已在 Phase 5 删除，不再有两套页面。

Phase 0 交付工具链与骨架，Phase 1 交付浏览视图：工具栏、网格/列表、搜索、
排序、分页、图片/视频缩略图、空态/骨架/错误态与窄屏适配。Phase 2 交付预览与
播放：`?view=` 路由、图片灯箱、PDF/文本预览、音频、视频播放器（直链/HLS）与
移动端手势。Phase 3 交付上传与文件管理：文件/文件夹上传、分片断点续传、秒传、
同名冲突三选一、多选、重命名/移动/复制/删除/回收站。Phase 4 交付分享与设置：
分享创建/管理、独立公开分享页 `/s/<token>`、会话管理、内嵌登录、关于/版本信息。
Phase 5 把新前端切换为默认根路径并删除旧前端。

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

`make check` 会检测 pnpm 与 `web/node_modules`：缺失时打印提示并跳过前端检查，
不阻断 Go 构建；CI 的 frontend job 始终跑完整四段。

## 构建产物与根路径

- `vite.config.ts` 的 `base` 固定为 `/`，`assetsDir` 设为 `v2/assets`，
  因此构建出的资源 URL 仍是 `/v2/assets/<hash>.js|css`（Phase 5 切换后保持不变）。
- 产物提交入库（`internal/server/web-dist`），CI 用
  `git diff --exit-code internal/server/web-dist` 验证可复现。
- `internal/server/webdist.go` 把同一个 handler 挂到 `/` 与 `/v2/`：
    - `index.html` 与 SPA fallback：`Cache-Control: no-store`；
    - `/v2/assets/*`（兼容 `/assets/*`）：一年 immutable；
    - 缺失的 asset 与带扩展名的未知文件返回 404，未知页面路径回退 index.html；
    - `/v2` 精确路径 301 到 `/`，保留查询串。
- `pnpm build` 最后会写 `web-dist/.keep`，保证 `//go:embed all:web-dist`
  在产物被清空时仍能编译；运行时若缺少 `index.html`，根路径返回 503。

## 鉴权与主题约定

- token 沿用旧前端键：`fs.token` / `fs.token.expires`，sessionStorage 优先，
  其次 localStorage；请求同时带 `X-Auth` 与 `Authorization: Bearer`。
  401 清空凭据并跳回 `/`（认证开启时显示内嵌登录）。
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
- **文本/代码**：等宽预览；超过 2 MiB 用 Range 只取前 2 MiB 并提示截断。
  白名单扩展名可点「编辑」进入编辑态，保存带读取时版本；409 提示他人已修改。非白名单
  或超限只在服务端拒绝后提示，不破坏只读预览。
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

## 上传与管理（Phase 3）

- **上传入口**：工具栏「上传文件」「上传文件夹」、整页拖拽（含目录递归与空目录）
  与粘贴；入口按 `/api/info` 的 `allow_upload` 显隐，文件夹上传额外要求登录。
- **上传队列**：最多 3 个文件并发；逐文件进度 + 总进度；暂停/继续、取消、重试；
  上传完成后自动刷新当前目录并高亮完成数量，面板约 1.8 秒后自动关闭。
- **分片与断点续传**：超过 `upload_chunk_threshold` 的文件按 8 MiB 分片，先查
  `/api/upload/status` 跳过已收分片，失败按 500/网络错误重试 2 次；`upload_id`
  公式与服务端 `deriveUploadID` 一致。
- **秒传**：Worker 流式计算整文件 SHA-256（4 MiB 分片，不支持 Worker 时主线程
  回退），先调 `/api/upload/instant`；`sha256.ts` 为纯 TS 实现，不依赖安全上下文。
- **同名冲突**：409 弹出「跳过 / 自动改名 / 覆盖」；覆盖与改名对整批生效，跳过
  只处理当前文件或当前目录分组。目录上传先按层级调用 `/api/upload/mkdir`。
- **多选**：桌面单击选中、双击打开；Ctrl/Cmd 切换、Shift 连选、`Ctrl/Cmd+A`
  全选、Esc 退出；触屏长按 500ms 进入选择。选择条提供下载、重命名（仅单选）、
  移动、复制、删除与退出。
- **管理对话框**：重命名（实时校验 + 409 文案）、移动/复制（目录树、就地新建
  文件夹、冲突三选一）、删除确认、回收站（恢复/彻底删除/清空）、批量失败明细。
  下载单文件走 `/api/file`，目录走 `/api/zip`。
- **错误文案**：401/403/404/409/413 与网络错误分别给出中文提示，不使用 `any`。

## 分享与设置（Phase 4）

- **分享创建**：选择条「分享」为单个文件/文件夹创建链接；有效期 1/7/30 天或永久，
  提取码默认关闭、开启时自动生成 4 位可读码并允许手改，下载次数不限/10/100，
  可关闭在线预览并填写分享者备注。创建成功后展示绝对链接与提取码，支持复制。
- **分享管理**：工具栏「分享」打开列表，展示链接、状态、有效期、已下载次数、创建
  时间与备注；可复制链接、取消分享（立即失效）与删除记录（不影响源文件）。
- **公开分享页**：独立 Vite 入口 `share.html`，由 Go 在 `/s/<token>` 返回并沿用
  分享页严格 CSP；匿名访问，支持提取码、单文件信息/预览/下载、目录逐级浏览、
  目录内单文件预览（图片/视频/音频/PDF/文本/代码）、单文件下载与当前目录 zip。
  页面不加载管理端脚本，不显示上传/管理入口。
- **会话管理**：设置 →「已登录设备/会话」列出会话（UA、登录时间、记住我、IP），
  标出当前设备并可下线其他设备。
- **登录与失效**：`AuthGate` 探测 `/api/auth/status`；未登录时内嵌口令/TOTP/记住我
  表单；首次设置由 `SetupForm` 在新前端内完成（口令、TOTP 二维码/密钥、可跳过 TOTP）。
  API 401 清凭据与短票后原地重新登录。
- **设置/关于**：展示 `/api/health` 的版本/commit/Go/平台与 `/api/info` 的服务标识；
  冷门格式开关的说明文案与旧版一致，并提供开启/关闭按钮。
- **多入口构建**：`vite.config.ts` 的 `rollupOptions.input` 同时包含 `index.html` 与
  `share.html`，资源统一输出到 `/v2/assets`；Go 在 `/s/<token>` 返回
  `web-dist/share.html`，产物缺失时返回 404。

## 验收脚本

Phase 1/2 先启动服务（HLS 用例需要 `--ffmpeg`），再运行：

```bash
go run ./cmd/fileserver --dir testdata --host 127.0.0.1 --port 18080 --no-qr --ffmpeg
/root/.venv/cdp/bin/python scripts/v2_phase1_test.py
/root/.venv/cdp/bin/python scripts/v2_phase2_test.py
```

Phase 1 覆盖根目录 7 项、进入 photos、图片/视频缩略图、网格/列表切换、排序、
搜索、375×780 不溢出与 `/v2/` 兼容入口。Phase 2 覆盖灯箱缩放/切图/刷新恢复、文本
预览、视频播放/空格暂停/画中画、375×780 双击/长按/横滑/竖滑、返回滚动位置与
HLS `abandon=1`；没有 ffmpeg 时跳过 HLS 并打印原因。两个脚本都会输出截图路径。

Phase 3 自包含：脚本复制 `testdata` 到临时目录、随机高位端口启动带认证的服务，
结束后清理，不会改动源 testdata。

```bash
/root/.venv/cdp/bin/python scripts/v2_phase3_test.py
```

覆盖 <5 MiB 上传与进度 UI、同名冲突三选项、重命名/移动/复制/删除/回收站恢复、
Ctrl 多选批量删除、Shift 连选、目录 zip 下载、375 宽上传面板，以及上传中/选择条/
冲突对话框三张截图。

Phase 4 同样自包含（临时目录副本 + 随机端口 + 认证服务）：

```bash
/root/.venv/cdp/bin/python scripts/v2_phase4_test.py
```

覆盖带提取码的文件分享创建、匿名输码下载且字节一致、目录分享与图片预览、取消分享
后公开页不可用、会话列表标出当前设备、375 宽公开页不溢出，并输出分享创建对话框、
公开分享页、会话列表与 375 宽公开页截图。

Phase 5 是切换后的全量回归（同样自包含）：

```bash
/root/.venv/cdp/bin/python scripts/v2_phase5_test.py
```

覆盖根路径浏览/进入目录/网格列表/搜索/排序/主题/上传/多选删除、图片灯箱/视频播放/
文本预览、带码分享到匿名下载字节一致与撤销、未登录 401 与登录/退出、375 宽浏览、
播放与分享页，并验证旧资源路径 404 与 `/v2/` 兼容入口。

## 目录

```
src/
├── api/        fetch 封装、token/短票、错误归一、目录/搜索/能力探测/上传/管理/分享/会话/认证
├── domain/     文件类型、大小/时间格式化、路径工具、排序
├── components/ Toast、ErrorBoundary、ThemeToggle、Modal
├── features/auth/
│   └── AuthGate.tsx          认证状态探测与内嵌登录表单
├── features/browse/
│   ├── BrowsePage.tsx        页面组合（路径/预览/搜索/排序/分页/多选/上传/管理）
│   ├── Toolbar.tsx           品牌/面包屑/搜索/刷新/上传/回收站/视图/排序/主题
│   ├── GridView.tsx          网格卡片与骨架
│   ├── ListView.tsx          列表三列与骨架
│   ├── SelectionBar.tsx      批量操作条
│   ├── useSelection.ts       单击/Ctrl/Shift/全选/长按多选
│   ├── FileThumb.tsx         图片/视频/图标缩略图
│   ├── videoThumb.ts         视频抽帧队列（3 路并发 + 缓存 + 取消）
│   ├── useDirectory.ts       目录分页数据
│   ├── useSearch.ts          搜索数据
│   ├── useCapabilities.ts    /api/info + /api/health 与上传/管理能力
│   └── useBrowseRoute.ts     ?path= / ?view= 同步
├── features/preview/         预览与播放器（见 Phase 2）
├── features/upload/
│   ├── uploadQueue.ts        上传状态机（并发/分片/秒传/冲突/重试）
│   ├── useUploadQueue.ts     React 绑定
│   ├── UploadPanel.tsx       队列、总进度与逐文件操作
│   ├── UploadConflictDialog.tsx  跳过/自动改名/覆盖
│   ├── sha256.ts             纯 TS 增量 SHA-256
│   ├── sha256.worker.ts      大文件哈希 Worker
│   ├── hashFile.ts           Worker 与主线程回退
│   ├── uploadId.ts           upload_id 派生与分片长度
│   ├── collect.ts            文件/文件夹/拖拽/粘贴收集
│   ├── pageDrop.ts           整页拖拽与粘贴监听
│   └── DropOverlay.tsx       投放遮罩
├── features/manage/
│   ├── RenameDialog.tsx      重命名
│   ├── TransferDialog.tsx    移动/复制目录树
│   ├── TrashDialog.tsx       回收站
│   ├── ConfirmDialog.tsx     通用确认
│   ├── FailuresDialog.tsx    批量失败明细
│   └── messages.ts           状态码 → 中文文案
├── features/share/
│   ├── ShareCreateDialog.tsx 创建分享（有效期/提取码/次数/预览/备注）
│   ├── ShareListDialog.tsx   分享列表、复制、取消、删除记录
│   ├── shareCode.ts          提取码生成与规范化
│   └── clipboard.ts          剪贴板（含 execCommand 回退）
├── features/settings/
│   ├── SettingsDialog.tsx    关于/版本、冷门格式说明、账户入口
│   └── SessionsDialog.tsx    已登录设备列表与下线
├── share/                    公开分享页独立入口（share.html → ShareApp）
│   ├── main.tsx              React 挂载
│   ├── ShareApp.tsx          提取码/文件预览/目录浏览/下载
│   ├── api.ts                /api/public/share/* 数据访问
│   └── status.ts             公开页错误文案
├── styles/     tokens.css（复用旧变量）+ global.css + buttons.module.css
└── test/       Vitest 全局 setup
```
