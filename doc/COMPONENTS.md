# 前端组件登记

本仓库前端是**内嵌原生 JS（`internal/server/web/app.js`，无构建链、无组件框架）**，
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
| 上传队列（`#uploadPanel`、`enqueueUploadFiles`/`pumpUploads`/`startUpload`） | 逐文件上传、进度、取消/重试、完成高亮 | `#btnUpload`/`#uploadInput`/拖拽 `FileList`；`POST /api/upload?dir=&mode=` | 由工具栏按钮或整页投放层调用 `enqueueUploadFiles(files)` | 单文件单 XHR、顺序上传；目标目录在选择时快照；仅当服务端 `allow_upload=true` 才显示入口；离开目录后只在原目录完成才刷新高亮 |
| 上传冲突弹窗（`#uploadConflict`，`closeUploadConflict`） | 同名文件冲突时选择覆盖/自动改名/取消（整批生效） | `409 {code:"conflict",existing}` 驱动 | 由 `handleUploadResponse` 在 409 时打开 | 选择前队列暂停；取消只标记未完成项，已完成文件保留；自动改名以服务端返回落盘名为准 |
| 认证覆盖层（`renderLogin`/`renderSetup`，`#auth`） | 引导页与登录页（口令/TOTP/记住我） | `GET /api/auth/status` 状态驱动；提交 `/api/auth/setup`、`/api/auth/login` | 启动时 `bootstrap()` 按状态渲染 | 覆盖层不渲染任何目录内容；CSP 严格；失败文案统一；登录成功把响应体 token 写入 sessionStorage/localStorage |
| 凭据/短票助手（`authFetch`、`mediaURL`、`ensureTicket`、`clearToken`） | XHR 附 Bearer；原生媒体 URL 拼 `?t=`；401 清凭据回登录页 | `fs.token`/`fs.token.expires`；`POST /api/auth/ticket` | `authFetch(url, opts)`、`mediaURL(url)` | token 不进 URL/日志；短票绑定会话、只对 GET 生效，剩余 <60s 自动续签 |
| 会话管理弹窗（`#sessions`） | 列出会话并撤销 | `GET /api/auth/sessions`、`POST /api/auth/sessions/revoke` | 工具栏「会话」按钮打开 | 不显示 token 明文；撤销当前会话以外需确认 |
| 文件管理（`manage.js`、`window.FSManage`） | 多选、右键菜单、批量操作栏、快捷键 | `init()`/`applyCapabilities()`/`bindItem(el,e,path)`/`handleClick(e,ev)`/`onNavigate()`/`decorate()` | `startApp()` 调 `FSManage.init()`，`/api/info` 的 `manage` 决定入口显隐 | 仅登录会话可用；桌面单击选中、双击打开，移动端长按 500ms 进入选择 |
| 重命名弹窗（`#renameModal`） | 实时校验并调用 `POST /api/rename` | 选中项路径 + 新名称 | 操作栏/右键菜单/F2 打开 | 默认只选中主名；非法名称禁用确定；成功后选中保持在新名字 |
| 移动/复制弹窗（`#transferModal`） | 目录树选择目标 + 直接新建文件夹 | `POST /api/move|copy?mode=`；`mode=ask|overwrite|rename` | 操作栏/右键菜单打开 | 只显示目录；目标不能是自身子目录；冲突弹窗三选一 |
| 回收站弹窗（`#trashModal`） | 列出/恢复/彻底删除/清空回收站 | `GET /api/trash`、`POST /api/trash/restore|empty` | 工具栏回收站按钮打开 | 恢复冲突可自动改名；彻底删除与清空二次确认；显示总占用 |
| 通用确认/失败明细（`#confirmModal`/`#resultModal`） | 操作确认与批量失败逐项展示 | `confirmBox({title,message,okLabel,thirdLabel,danger})`；`showFailures(title,failed)` | 删除、冲突、回收站与批量操作调用 | `confirmBox` 返回 `ok|third|cancel`；失败项列出路径与原因 |

## 可访问性要点（通用）

- 所有交互一律用原生 `<button>` / `<input>` / `<textarea>`，原生可获得键盘焦点与语义。
- 弹窗（`.modal`）用 `role="dialog" aria-modal="true"`，打开时聚焦首个输入；Esc 关闭
  （新建弹窗与认证覆盖层已绑定）。
- 图标按钮均带 `aria-label`；表单控件用 `<label>` 包裹。
- 颜色全部走既有设计令牌（`--accent` 等），不新造颜色/圆角/字号。
- 深浅双主题下对比度由既有令牌保证，不单独引入色值。
