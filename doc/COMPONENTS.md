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
| 新建文本弹窗（`btnNewText`/`#newFile`） | 目录内新建文本文件 | `POST /api/text/new` | 由工具栏按钮打开 | 文件名不得含分隔符；服务端二次校验 |
| 认证覆盖层（`renderLogin`/`renderSetup`，`#auth`） | 引导页与登录页（口令/TOTP/记住我） | `GET /api/auth/status` 状态驱动；提交 `/api/auth/setup`、`/api/auth/login` | 启动时 `bootstrap()` 按状态渲染 | 覆盖层不渲染任何目录内容；CSP 严格；失败文案统一 |
| 会话管理弹窗（`#sessions`） | 列出会话并撤销 | `GET /api/auth/sessions`、`POST /api/auth/sessions/revoke` | 工具栏「会话」按钮打开 | 不显示 token 明文；撤销当前会话以外需确认 |

## 可访问性要点（通用）

- 所有交互一律用原生 `<button>` / `<input>` / `<textarea>`，原生可获得键盘焦点与语义。
- 弹窗（`.modal`）用 `role="dialog" aria-modal="true"`，打开时聚焦首个输入；Esc 关闭
  （新建弹窗与认证覆盖层已绑定）。
- 图标按钮均带 `aria-label`；表单控件用 `<label>` 包裹。
- 颜色全部走既有设计令牌（`--accent` 等），不新造颜色/圆角/字号。
- 深浅双主题下对比度由既有令牌保证，不单独引入色值。
