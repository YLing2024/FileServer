# 前端组件登记

前端只有一条链路：**React + TypeScript + Vite**（`web/src`），构建产物经
`go:embed` 嵌入，默认服务在 `/` 与 `/v2/`。旧的原生 JS 前端（`internal/server/web`）
已在 Phase 5 删除。新增可复用组件必须在本文件登记，文件头写 TSDoc。

## React 组件（`web/src`）

| 组件 | 用途 | 关键 props / 事件 | 最小示例 | 注意事项 |
|---|---|---|---|---|
| `ToastProvider` / `useToast` | 全局轻提示队列，3 秒自动消失 | `children`；`showToast(text, tone?)`，`tone` 默认 `info` | `<ToastProvider><App /></ToastProvider>`；组件内 `const { showToast } = useToast()` | 必须在 Provider 内调用 hook；不要在渲染期间调用 `showToast`；容器 `aria-live=polite`，错误项 `role=alert` |
| `ErrorBoundary` | 捕获子树渲染异常，避免白屏 | `children`、可选 `fallback`；内置“重试”复位 | `<ErrorBoundary><App /></ErrorBoundary>` | 只捕获渲染/生命周期错误，不捕获事件回调与异步异常 |
| `ThemeToggle` | 深浅主题切换，沿用 `fs.theme` | 无 props；读取/写入 `<html data-theme>` 与 `fs.theme` | `<ThemeToggle />` | 必须在 `main.tsx` 先调用 `initTheme()`；颜色只用既有 CSS 变量；原生 button + `aria-label="切换主题"` |
| `Modal` | 通用模态框：遮罩、Esc 关闭、初始焦点与焦点恢复 | `title`、`onClose`、`children`、`footer?`、`dismissible?`、`testId?`、`wide?` | `<Modal title="重命名" onClose={close}>…</Modal>` | `role=dialog aria-modal`；`dismissible=false` 用于冲突确认；关闭后焦点回到打开前元素 |
| `SelectionBar` / `useSelection` | 批量操作条与多选状态：单击选中、双击打开、Ctrl/Shift、全选、Esc、触屏长按 | `count` 与各操作回调；`entries`、`enabled`、`path` | `<SelectionBar count={2} … />`；`const sel = useSelection(entries, caps.manage, path)` | 选择按 fullPath 存储；路径变化自动失效；键盘激活（detail=0）按打开处理，避免 Enter 只选中 |
| `UploadPanel` | 上传队列：总进度、逐文件进度、暂停/继续/重试/取消/全部取消 | `snapshot`、`onClose/onPause/onResume/onCancel/onRetry/onCancelAll` | `<UploadPanel snapshot={upload.snapshot} … />` | 上传中禁止关闭；完成后约 1.8 秒自动关闭并清空进度；375 宽固定底部全宽 |
| `UploadConflictDialog` | 同名冲突三选一：跳过 / 自动改名 / 覆盖 | `conflict`、`onResolve(choice)` | `<UploadConflictDialog conflict={c} onResolve={resolve} />` | 覆盖/改名对整批生效；跳过只处理当前项或当前目录分组；不可用遮罩/Esc 关闭 |
| `UploadQueue` / `useUploadQueue` | 上传状态机与 React 绑定：3 文件并发、分片断点续传、秒传、目录预创建、冲突暂停 | 队列方法 `enqueue/enqueuePlain/enqueueFolder/pause/resume/cancel/retry/resolveConflict`；hook 返回 `snapshot` 与同名操作 | `const upload = useUploadQueue({ settings, onFinished, onToast, onUnauthorized })` | 目标目录在入队时快照；分片/秒传/目录接口仅登录会话；`upload_id` 必须与服务端公式一致 |
| `sha256.ts` / `hashFile` / `uploadId` | 纯 TS 流式 SHA-256、Worker 包装、`upload_id` 派生 | `hashBlob(blob,onProgress,signal,chunkSize)`、`hashFile(file,onProgress,signal)`、`computeUploadId(file,name)` | `const hex = await hashFile(file, setPct, signal)` | 不使用 Web Crypto（局域网 HTTP 非安全上下文）；Worker 失败回退主线程；测试含 RFC 向量 |
| `collect.ts` / `pageDrop` / `DropOverlay` | 文件/文件夹/拖拽/粘贴收集与整页投放层 | `collectPlainFiles/collectFolderFiles/collectDropEntries/collectClipboardFiles`；`usePageDrop`、`usePasteUpload` | `const dragging = usePageDrop({enabled,onCollected,onError})` | 文件夹保留相对路径并用 `webkitGetAsEntry` 递归（含空目录）；输入框聚焦时不接管粘贴 |
| `RenameDialog` | 单条重命名，实时校验并调用 `POST /api/rename` | `path`、`name`、`isDir`、`siblings`、`onRenamed`、`onClose` | `<RenameDialog path="a.txt" name="a.txt" isDir={false} siblings={[]} … />` | 默认只选中主名；非法名称禁用确定；409 显示“同名文件或文件夹已存在” |
| `TransferDialog` | 移动/复制：目录树、就地新建文件夹、冲突三选一 | `kind`、`paths`、`currentPath`、`onDone`、`onClose` | `<TransferDialog kind="move" paths={["a.txt"]} currentPath="" … />` | 只显示目录；选中项自身及子目录禁用；目录按需加载并在打开时展开到当前路径 |
| `TrashDialog` | 回收站：列表、全选、恢复、彻底删除、清空 | `onClose`、`onRestored`、`onFailures` | `<TrashDialog onClose={close} onRestored={refresh} onFailures={show} />` | 恢复冲突可自动改名；彻底删除/清空二次确认；id 一律按字符串处理 |
| `ConfirmDialog` / `FailuresDialog` | 通用确认与批量失败明细 | `title/message/confirmLabel/danger/thirdLabel/onConfirm/onThird/onCancel`；`failed[]` | `<ConfirmDialog … />`、`<FailuresDialog title="部分失败" failed={f} … />` | 危险操作使用 danger 样式；失败项逐条展示路径与原因 |
| `BrowsePage` | 浏览页：路径/预览/搜索/排序/分页/滚动恢复的组合层 | 无 props；依赖 `?path=` / `?view=`、`/api/list`、`/api/search`、`/api/info` | `<BrowsePage />` | 图片/视频/音频/PDF/文本/代码点击进入 `?view=` 预览，其余下载；进入预览前记录滚动位置；必须在 `ToastProvider` 内使用 |
| `Toolbar` | 品牌/回根、面包屑、搜索框、刷新、视图切换、排序、主题、冷门格式开关 | `path`、`view`、`sort`、`order`、`query`、`searching`、`hls`、`ffmpeg`、`hlsBusy`；`onHome/onNavigate/onViewChange/onSortChange/onOrderToggle/onQueryChange/onClearSearch/onRefresh/onToggleHls` | `<Toolbar path="" view="grid" ... />` | 搜索防抖在 `BrowsePage` 层；Esc 清空；`/` 聚焦搜索框；窄屏搜索独占一行、≤480 隐藏排序下拉 |
| `GridView` / `GridSkeleton` | 网格卡片（缩略图 + 名称/大小/时间）与加载骨架 | `entries`、`query`、`serverTranscode`、`onOpen`；骨架 `count` | `<GridView entries={entries} query="" serverTranscode={false} onOpen={open} />` | 卡片是原生 button，`aria-label` 为条目名；目录/文件都由 `onOpen` 分发 |
| `ListView` / `ListSkeleton` | 列表三列（名称/大小/修改时间），名称可点 | `entries`、`query`、`onOpen`；骨架 `count` | `<ListView entries={entries} query="" onOpen={open} />` | 目录优先由排序函数保证；≤820 隐藏时间列，≤480 收窄大小列 |
| `FileThumb` | 按类型渲染缩略图：图片 `/api/thumb`、视频抽帧/服务端缩略图、其余图标 | `entry`、`path`、`serverTranscode` | `<FileThumb entry={entry} path="photos/a.jpg" serverTranscode={false} />` | 视频抽帧并发 3、懒加载、卸载取消；冷门格式无 ffmpeg 时保持图标，不发 `/api/thumb-src` |
| `Highlight` | 搜索命中高亮，纯文本拆分不注入 HTML | `text`、`query` | `<Highlight text="sample.mp4" query="mp4" />` | 大小写不敏感；空 query 原样输出 |
| `PreviewPage` | 预览分发：图片灯箱/视频/音频/PDF/文本，统一返回与下载 | `file: PreviewFile`、`images`、`onClose`、`onChangeImage` | `<PreviewPage file={file} images={[]} onClose={close} onChangeImage={replace} />` | 图片走全屏灯箱；Esc 由 PreviewPage/灯箱统一处理；文档标题切到文件名 |
| `Lightbox` | 图片灯箱：原图、骨架、缩放/旋转、前后切换 | `path`、`paths`、`onClose`、`onChange`、`onToast` | `<Lightbox path="a.jpg" paths={["a.jpg","b.jpg"]} ... />` | 固定深色背景 `role=dialog aria-modal`；滚轮/双指 25%–800%；切图只 replaceState 不新增历史 |
| `VideoPlayer` | 视频播放器：直链/HLS、自绘控制条、快捷键、移动端手势 | `path`、`name`、`size` | `<VideoPlayer path="v.mp4" name="v.mp4" size={123} />` | 不自动播放；离开时 `abandon=1` + `hls.destroy()` + 释放 src；进度按 path+size 记忆 |
| `AudioPlayer` | 原生 `<audio controls>` + 进度记忆 | `path`、`name`、`size` | `<AudioPlayer path="a.mp3" name="a.mp3" size={123} />` | 直链 `/api/file`；与视频共用 `fs.progress.<path>:<size>` |
| `PdfPreview` | PDF 内嵌查看 + 下载兜底 | `path`、`name`、`onToast` | `<PdfPreview path="a.pdf" name="a.pdf" onToast={toast} />` | `navigator.pdfViewerEnabled === false` 时显示下载提示；下载按钮常驻 |
| `TextPreview` | 文本/代码预览与编辑 | `path`、`size`、`mtime`、`onToast` | `<TextPreview path="a.txt" size={10} mtime={0} onToast={toast} />` | 超过 2 MiB 用 Range 截断并提示；点击「编辑」才请求 `/api/text`，保存带版本校验（409 提示冲突）；`size<0` 时等待目录元数据 |
| `attachHls` | hls.js 挂载：8 秒 manifest 轮询、seek 收敛、短票重写 | `video`、`path`、`onReady/onSeekClamped/onError` | `const h = attachHls(video, path, callbacks); h.destroy()` | 仅 HLS 时动态加载 hls.js；离开预览必须 destroy；Safari 走原生 HLS |
| `usePlayerGestures` | 移动端播放器手势 | `playerRef`、`videoRef`、`GestureOptions` | `usePlayerGestures(playerRef, videoRef, opts)` | 仅 `pointer: coarse`/`ontouchstart` 注册；长按恢复原倍速；横滑松手才 seek |
| `progress.ts` | path+size 播放进度记忆 | `readProgress`、`saveProgress`、`clearProgress` | `saveProgress(path, size, time, duration)` | 片头 1s/结尾 3s 不保存；localStorage 不可用时静默降级 |
| `useBrowseRoute` | `?path=` / `?view=` 双向同步与历史恢复 | 无入参；返回 `path/view/navigate/openView/replaceView/closeView` | `const route = useBrowseRoute()` | 进入预览只写 `?view=`；深链 Esc 用 replace 回目录；灯箱切图 replace 不新增历史 |
| `useDirectory` | 目录分页数据：首页/加载更多/错误/刷新 | `path`、`sort`、`order`、`limit` | `const dir = useDirectory(path, sort, order, 300)` | 切换路径/排序取消过期请求；`loadMore` 用 `offset=entries.length` |
| `useSearch` | `/api/search` 数据，映射为可复用条目 | `query`、`path`、`limit` | `const search = useSearch(query, path, 2000)` | query 为空返回空闲态；结果携带 `searchPath/searchDir` |
| `useCapabilities` | 读取 `/api/info` 与 `/api/health`，切换冷门格式 | 无入参；返回 `ffmpeg/hls/searchLimit/listLimit/manage/shareAllowed/version/commit/goVersion/platform/toggling/toggleHls` | `const caps = useCapabilities()` | 优先读 `health.hls`，当前后端无该字段时回退 `/api/info`；开关走 `POST /api/settings/ffmpeg` |
| `AuthGate` | 认证状态探测与内嵌登录（口令/TOTP/记住我）；失效后原地重新登录 | `children`、`notice?` | `<AuthGate notice={notice}><BrowsePage /></AuthGate>` | 首次设置由 `SetupForm` 在新前端内完成；登录用裸 fetch 避免 401 触发全局清理；不渲染目录内容 |
| `SetupForm` | 首次设置：口令、TOTP 二维码/密钥与验证码、可跳过 TOTP | `onDone` | `<SetupForm onDone={reloadStatus} />` | 只在 `needs_setup` 时渲染；二维码来自 `/api/auth/totp-qr`；提交走 `/api/auth/setup`；失败文案服务端返回 |
| `ShareCreateDialog` | 创建分享：有效期/提取码/下载次数/预览开关/备注，成功后展示链接与提取码 | `target: ShareTarget`、`onClose`、`onCreated?` | `<ShareCreateDialog target={{path,name,isDir}} onClose={close} />` | 提取码 4 位字母数字、开启时自动生成可手改；提取码只在创建响应可见；必须位于 ToastProvider 内 |
| `ShareListDialog` | 分享列表：链接/状态/有效期/访问次数；复制、取消分享、删除记录 | `onClose` | `<ShareListDialog onClose={close} />` | 取消分享立即失效；删除记录不动源文件；过期/撤销项标灰保留 |
| `SessionsDialog` | 已登录设备列表，标出当前设备并下线其他会话 | `onClose` | `<SessionsDialog onClose={close} />` | 不显示 token 明文；服务端拒绝下线当前会话 |
| `SettingsDialog` | 关于/版本（health + info）、冷门格式说明与开关、会话/退出入口 | `name/version/commit/goVersion/platform/ffmpeg/hls/hlsBusy/manage`、`onToggleHls/onOpenSessions/onLogout/onClose` | `<SettingsDialog {...caps} onClose={close} … />` | 冷门格式文案与旧版一致；无 ffmpeg 时禁用开关 |
| `ShareApp`（公开页） | 独立入口 `/s/<token>`：提取码、单文件预览/下载、目录浏览/单文件预览/zip | 无 props；token 取自 `location.pathname` | 由 `share.html` 挂载 `<ShareApp />` | 只调 `/api/public/share/*`；不发送登录凭据、不渲染管理入口；分享页 CSP 不含 `unsafe-inline`，组件不写内联样式 |

样式：`web/src/styles/tokens.css` 的深浅令牌与历史版本保持一致；
组件样式使用 CSS Modules，不新增全局类名。

## 可访问性要点（通用）

- 所有交互一律用原生 `<button>` / `<input>` / `<textarea>`，原生可获得键盘焦点与语义。
- 弹窗（`.modal`）用 `role="dialog" aria-modal="true"`，打开时聚焦首个输入；Esc 关闭
  （新建弹窗与认证覆盖层已绑定）。
- 图标按钮均带 `aria-label`；表单控件用 `<label>` 包裹。
- 颜色全部走既有设计令牌（`--accent` 等），不新造颜色/圆角/字号。
- 深浅双主题下对比度由既有令牌保证，不单独引入色值。
