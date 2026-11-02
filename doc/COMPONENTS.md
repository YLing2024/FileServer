# 前端组件登记

本仓库前端有两条并行链路：

- **旧前端（默认 `/`）**：内嵌原生 JS（`internal/server/web/app.js`，无构建链、无组件框架）。
- **V2 前端（`/v2/`，Phase 0 起）**：React + TypeScript + Vite（`web/src`），
  产物经 `go:embed` 嵌入。新增可复用组件必须在本文件登记，文件头写 TSDoc。

## V2 React 组件（`web/src`）

| 组件 | 用途 | 关键 props / 事件 | 最小示例 | 注意事项 |
|---|---|---|---|---|
| `ToastProvider` / `useToast` | 全局轻提示队列，3 秒自动消失 | `children`；`showToast(text, tone?)`，`tone` 默认 `info` | `<ToastProvider><App /></ToastProvider>`；组件内 `const { showToast } = useToast()` | 必须在 Provider 内调用 hook；不要在渲染期间调用 `showToast`；容器 `aria-live=polite`，错误项 `role=alert` |
| `ErrorBoundary` | 捕获子树渲染异常，避免白屏 | `children`、可选 `fallback`；内置“重试”复位 | `<ErrorBoundary><App /></ErrorBoundary>` | 只捕获渲染/生命周期错误，不捕获事件回调与异步异常 |
| `ThemeToggle` | 深浅主题切换，沿用 `fs.theme` | 无 props；读取/写入 `<html data-theme>` 与 `fs.theme` | `<ThemeToggle />` | 必须在 `main.tsx` 先调用 `initTheme()`；颜色只用既有 CSS 变量；原生 button + `aria-label="切换主题"` |
| `BrowsePage` | Phase 1 浏览页：路径/搜索/排序/分页/滚动恢复的组合层 | 无 props；依赖 `?path=`、`/api/list`、`/api/search`、`/api/info` | `<BrowsePage />` | 文件点击暂只下载原文件；预览/播放留到 Phase 2；必须在 `ToastProvider` 内使用 |
| `Toolbar` | 品牌/回根、面包屑、搜索框、刷新、视图切换、排序、主题、冷门格式开关 | `path`、`view`、`sort`、`order`、`query`、`searching`、`hls`、`ffmpeg`、`hlsBusy`；`onHome/onNavigate/onViewChange/onSortChange/onOrderToggle/onQueryChange/onClearSearch/onRefresh/onToggleHls` | `<Toolbar path="" view="grid" ... />` | 搜索防抖在 `BrowsePage` 层；Esc 清空；`/` 聚焦搜索框；窄屏搜索独占一行、≤480 隐藏排序下拉 |
| `GridView` / `GridSkeleton` | 网格卡片（缩略图 + 名称/大小/时间）与加载骨架 | `entries`、`query`、`serverTranscode`、`onOpen`；骨架 `count` | `<GridView entries={entries} query="" serverTranscode={false} onOpen={open} />` | 卡片是原生 button，`aria-label` 为条目名；目录/文件都由 `onOpen` 分发 |
| `ListView` / `ListSkeleton` | 列表三列（名称/大小/修改时间），名称可点 | `entries`、`query`、`onOpen`；骨架 `count` | `<ListView entries={entries} query="" onOpen={open} />` | 目录优先由排序函数保证；≤820 隐藏时间列，≤480 收窄大小列 |
| `FileThumb` | 按类型渲染缩略图：图片 `/api/thumb`、视频抽帧/服务端缩略图、其余图标 | `entry`、`path`、`serverTranscode` | `<FileThumb entry={entry} path="photos/a.jpg" serverTranscode={false} />` | 视频抽帧并发 3、懒加载、卸载取消；冷门格式无 ffmpeg 时保持图标，不发 `/api/thumb-src` |
| `Highlight` | 搜索命中高亮，纯文本拆分不注入 HTML | `text`、`query` | `<Highlight text="sample.mp4" query="mp4" />` | 大小写不敏感；空 query 原样输出 |
| `useDirectory` | 目录分页数据：首页/加载更多/错误/刷新 | `path`、`sort`、`order`、`limit` | `const dir = useDirectory(path, sort, order, 300)` | 切换路径/排序取消过期请求；`loadMore` 用 `offset=entries.length` |
| `useSearch` | `/api/search` 数据，映射为可复用条目 | `query`、`path`、`limit` | `const search = useSearch(query, path, 2000)` | query 为空返回空闲态；结果携带 `searchPath/searchDir` |
| `useCapabilities` | 读取 `/api/info` 与 `/api/health`，切换冷门格式 | 无入参；返回 `ffmpeg/hls/searchLimit/listLimit/toggling/toggleHls` | `const caps = useCapabilities()` | 优先读 `health.hls`，当前后端无该字段时回退 `/api/info`；开关走 `POST /api/settings/ffmpeg` |

V2 样式：`web/src/styles/tokens.css` 与旧 `style.css` 的深浅令牌逐项一致；
组件样式使用 CSS Modules，不新增全局类名。

## 旧前端可复用 UI 单元

本仓库旧前端是**内嵌原生 JS（`internal/server/web/app.js`，无构建链、无组件框架）**，
不存在框架级组件。下表登记的是可复用的 UI 单元（函数/视图），供维护者快速定位与修改。
新增可复用 UI 单元必须同一笔登记。

| 单元 | 用途 | 关键参数/事件 | 最小用法 | 注意事项 |
|---|---|---|---|---|
| `renderPreview(path, entry)` | 预览分发：按文件类型渲染图片/视频/音频/PDF/文本 | `path` 相对路径、`entry` 条目对象（name/size/mtime） | 由列表点击与深链调用，勿直接 DOM 调用 | 每次进入先 `stopPreview()` 释放上一条；用 `pvSeq` 丢弃迟到 fetch |
| `buildPlayer(container, path, entry, kind)` | 自绘播放器（direct/hls、进度、快捷键、全屏） | `container`、`path`、`entry`、`kind`（video/audio） | 由 `renderPreview` 调用 | 不自动播放；离开预览必须走 `stopPreview` 释放 |
| `renderTextPreview(main, path, entry)` | 文本/代码预览：白名单内提供编辑，否则只读 | `main`、`path`、`entry` | 由 `renderPreview` 调用 | 只读预览上限 2MB；编辑态按 `/api/info` 白名单判定 |
| `renderTextEditor(main, path, entry, info)` | 文本查看/编辑切换 + 保存（版本校验、409 载入最新） | `info` 为 `GET /api/text` 响应 | 白名单内文件进入预览时调用 | 409 时提示「已被他人修改」并载入最新内容；按钮 `aria` 走原生 button |
| `openLightbox(path, entries)` / `lbClose()` | 图片灯箱：缩放、旋转、前后切换 | `path`、`entries` 队列 | 点击图片卡片调用 | 键盘 ←/→/±/R/Esc；`role=dialog aria-modal` |
| `toast(msg, isError)` | 轻提示 | `msg` 文案、`isError` 是否错误样式 | 任何异步失败处 | 单一实例，重复调用覆盖上一条 |
| 新建弹窗（`btnNewText`/`#newFile`，`setNewKind`/`syncNewFileFields`） | 目录内新建空文件（扩展名不限）或单层文件夹 | `POST /api/create`；`kind` 文件/文件夹二选一 | 由工具栏按钮打开，默认「文件」 | 仅文本白名单扩展名才显示初始内容框，否则提示「该类型只创建空文件」；名称空白时禁用创建；服务端二次校验命名 |
| 上传队列（`#uploadPanel`、`enqueueUploadFiles`/`enqueueFolderFiles`/`pumpUploads`/`startUpload`） | 文件/文件夹上传、分片断点续传、秒传、进度、取消/重试、分组进度与完成高亮 | `#btnUpload`/`#uploadInput`/`#btnUploadDir`/`#uploadDirInput`/拖拽 `DataTransfer`；`POST /api/upload`、`/api/upload/chunk`、`/api/upload/complete`、`/api/upload/instant`、`/api/upload/mkdir` | 由工具栏按钮或整页投放层调用 `enqueueUploadFiles(files)` / `enqueueFolderFiles(files)` / `enqueueUploadCollected(...)` | 目标目录在选择时快照；>8 MiB 走 8 MiB 分片并按 `upload_id` 续传；文件夹先建目录（含空目录），顶层冲突整目录三选一；分片/秒传/目录接口仅登录会话，认证关闭时回退单请求上传 |
| 上传冲突弹窗（`#uploadConflict`，`closeUploadConflict`） | 同名文件/顶层目录冲突时选择覆盖/自动改名/取消（整批生效） | `409 {code:"conflict",existing}` 驱动 | 由 `instantRequest`/`smallUpload`/`chunkedUpload`/`prepareGroup` 在 409 时打开 | 选择前队列暂停；取消只标记未完成项，已完成文件保留；自动改名以服务端返回落盘名为准；目录冲突对整棵目录生效 |
| 内容哈希（`sha256.js`、`hash-worker.js`、`computeFileSHA`/`uploadIDFor`） | 增量 SHA-256、秒传指纹、`upload_id` 派生 | `FSSHA256.hashBlob(blob,onProgress,chunkSize)`；文件 >200 MiB 走 Worker | `computeFileSHA(item,onProgress)`；`uploadIDFor(item)` | Worker 失败回退主线程；`upload_id` 公式必须与服务端 `deriveUploadID` 一致；不引入第三方依赖 |
| 认证覆盖层（`renderLogin`/`renderSetup`，`#auth`） | 引导页与登录页（口令/TOTP/记住我） | `GET /api/auth/status` 状态驱动；提交 `/api/auth/setup`、`/api/auth/login` | 启动时 `bootstrap()` 按状态渲染 | 覆盖层不渲染任何目录内容；CSP 严格；失败文案统一；登录成功把响应体 token 写入 sessionStorage/localStorage |
| 凭据/短票助手（`authFetch`、`mediaURL`、`ensureTicket`、`clearToken`） | XHR 附 Bearer；原生媒体 URL 拼 `?t=`；401 清凭据回登录页 | `fs.token`/`fs.token.expires`；`POST /api/auth/ticket` | `authFetch(url, opts)`、`mediaURL(url)` | token 不进 URL/日志；短票绑定会话、只对 GET 生效，剩余 <60s 自动续签 |
| 会话管理弹窗（`#sessions`） | 列出会话并撤销 | `GET /api/auth/sessions`、`POST /api/auth/sessions/revoke` | 工具栏「会话」按钮打开 | 不显示 token 明文；撤销当前会话以外需确认 |
| 文件管理（`manage.js`、`window.FSManage`） | 多选、右键菜单、批量操作栏、快捷键 | `init()`/`applyCapabilities()`/`bindItem(el,e,path)`/`handleClick(e,ev)`/`onNavigate()`/`decorate()` | `startApp()` 调 `FSManage.init()`，`/api/info` 的 `manage` 决定入口显隐 | 仅登录会话可用；桌面单击选中、双击打开，移动端长按 500ms 进入选择 |
| 重命名弹窗（`#renameModal`） | 实时校验并调用 `POST /api/rename` | 选中项路径 + 新名称 | 操作栏/右键菜单/F2 打开 | 默认只选中主名；非法名称禁用确定；成功后选中保持在新名字 |
| 移动/复制弹窗（`#transferModal`） | 目录树选择目标 + 直接新建文件夹 | `POST /api/move|copy?mode=`；`mode=ask|overwrite|rename` | 操作栏/右键菜单打开 | 只显示目录；目标不能是自身子目录；冲突弹窗三选一 |
| 回收站弹窗（`#trashModal`） | 列出/恢复/彻底删除/清空回收站 | `GET /api/trash`、`POST /api/trash/restore|empty` | 工具栏回收站按钮打开 | 恢复冲突可自动改名；彻底删除与清空二次确认；显示总占用 |
| 通用确认/失败明细（`#confirmModal`/`#resultModal`） | 操作确认与批量失败逐项展示 | `confirmBox({title,message,okLabel,thirdLabel,danger})`；`showFailures(title,failed)` | 删除、冲突、回收站与批量操作调用 | `confirmBox` 返回 `ok|third|cancel`；失败项列出路径与原因 |

| 分享创建弹窗（`#shareCreate`，`FSShare.openCreate`） | 为单个文件/文件夹生成公开链接：有效期、提取码、下载上限、预览开关、备注 | `openCreate(paths)`；`POST /api/share`；`#scDays`/`#scCodeOn`/`#scCode`/`#scMax`/`#scPreview`/`#scNote` | 工具栏/右键菜单/选择栏的「分享」调用 `FSShare.openCreate(FSManage.selectedPaths())` | 只支持单选；开启提取码时自动生成 4 位（可手改，仅字母数字）；目录分享提示包含后续新增内容；服务端二次校验 |
| 分享结果弹窗（`#shareResult`） | 展示链接与提取码并复制 | `#srLink`/`#srCode`；`navigator.clipboard` 失败回退 `execCommand` | 创建成功后由 `FSShare` 自动打开 | 链接用 `location.origin` 拼绝对地址；提取码只在创建时可见，关闭后无法找回 |
| 分享管理弹窗（`#shareList`，`FSShare.openList`） | 列出分享、复制、撤销、删除记录 | `GET /api/share`、`POST /api/share/revoke`、`DELETE /api/share/record` | 工具栏「分享链接」按钮打开 | 过期/撤销/超次项标灰但保留；撤销立即失效；删除记录不影响源文件；状态文案走设计令牌 |
| 公开分享页（`share.html` / `share.js` / `share.css`） | 独立免登录页面：文件信息/预览/下载、目录列表/单文件下载/zip、提取码 | URL `/s/<token>`；`GET info/list/download/zip/preview`、`POST verify` | `GET /s/<token>` 由服务端直接返回模板 | 不加载 `app.js`/`manage.js`；文本预览写 `textContent` 不当 HTML；错误页不显示路径；375px 可用 |

## 可访问性要点（通用）

- 所有交互一律用原生 `<button>` / `<input>` / `<textarea>`，原生可获得键盘焦点与语义。
- 弹窗（`.modal`）用 `role="dialog" aria-modal="true"`，打开时聚焦首个输入；Esc 关闭
  （新建弹窗与认证覆盖层已绑定）。
- 图标按钮均带 `aria-label`；表单控件用 `<label>` 包裹。
- 颜色全部走既有设计令牌（`--accent` 等），不新造颜色/圆角/字号。
- 深浅双主题下对比度由既有令牌保证，不单独引入色值。
