# REQUIREMENTS — 认证凭据的客户端存储（sessionStorage / localStorage）与 7 天留存

> 立项：2026-10-17。用户原话（逐条）：
> 「我最开始要求的是，如果没有记住我，应该是存 sessionstorage，这样就可以实现每次会话重新输」
> 「勾了就存到 localstorage 里面，这样就可以持久保留」
> 「后端 token 留存 7 天」
> 本篇是本次改动的**唯一权威需求**。与 `doc/architecture.md` / `doc/security.md` / `doc/api.md` / `doc/frontend.md`
> 冲突处，以本篇为准，并要求同步更新那几篇。

## 1. 现状（问题）

- 登录成功由**服务端** `Set-Cookie` 下发 `fs_session`（`HttpOnly`）；勾选「记住我」= 持久 cookie（默认 30 天），
  不勾 = 会话 cookie（服务端 TTL 默认 12h）。见 `internal/server/auth_handlers.go:104-122`。
- 前端**不存任何凭据**，一切靠 cookie 自动携带（`internal/server/web/app.js` 无 sessionStorage/localStorage 凭据）。
- 后果：「不勾」的真实语义由**浏览器对会话 cookie 的处理**决定——Chrome「继续浏览上次打开的网页」/ Safari 会
  恢复会话 cookie，于是"关浏览器就要重新输"根本不可保证；用户实测到这一点。
- 因此用户要求的**真源**是浏览器存储：不勾 = `sessionStorage`（关标签/浏览器即失效），勾选 = `localStorage`（持久）。

## 2. 目标行为（唯一权威）

| 情形 | 凭据存放 | 服务端 token 有效期 | 关闭该标签后 | 关闭浏览器后（7 天内） |
|---|---|---|---|---|
| **不勾**「记住我」 | `sessionStorage` | **7 天** | 需重新登录 | 需重新登录 |
| **勾选**「记住我」 | `localStorage` | **7 天** | 免登录（同浏览器其它标签也免登录） | 免登录 |

语义细节（验收按此逐条走）：

1. 同一标签内：刷新、前进/后退、进入预览/编辑弹层、切换目录 —— 都不重新登录。
2. **不勾**：关闭该标签 → 该标签的凭据消失 → 再打开需要重新登录（sessionStorage 天然如此，**不得**用任何
   "跨标签找回"的手段绕开它）。
3. **勾选**：`localStorage` 存凭据，关闭浏览器重开仍免登录，直到 7 天到期或主动登出。
4. 服务端 token 记录（`.FileServer/auth.json` 里的 sha256 + 过期时间）**统一留存 7 天**（勾/不勾都一样）。
   客户端存储决定"是否还要重新输"，服务端只负责"凭据是否仍然有效"。
5. 主动登出：清前端存储 + 服务端失效该 token（现有 `/api/auth/logout` 语义保留）。
6. 会话被撤销 / 过期 / 凭据无效：任何接口返回 `401` → 前端**清掉本地凭据**并回到登录页（不得静默失败）。

## 3. 关键设计决定

### 3.1 凭据真源在前端存储，`token` 由登录响应体返回

- `POST /api/auth/login` 成功响应体由 `{ok:true}` 扩展为 `{ok:true, token:"<明文 token>", expires:<unix>, remember:bool}`。
- **不再由服务端 `Set-Cookie`**（彻底消除"服务端偷偷维持会话"的路径）。
- 前端：`remember=false` → 写 `sessionStorage`；`remember=true` → 写 `localStorage`。键名 `fs.token`，
  另存 `fs.token.expires`（unix 秒，便于本地预判过期）。

### 3.2 媒体/原生请求的鉴权：**短票**（不接受 `token` 进 URL）

问题：`<video src>`、`<img src>`、`<a download>`、HLS 分片是**浏览器原生请求**，无法携带
`Authorization` 头；同时项目硬规矩是「日志绝不打印 token」（`AGENTS.md`），所以**不得**把 token 本体拼进 URL
（nginx access log 会记录查询串）。

方案：
1. 新增 `POST /api/auth/ticket`（需已登录：`Authorization: Bearer <token>`）→ `{ticket:"<明文短票>", expires:<unix>}`。
   - 短票绑定到签发它的会话（session id）；会话被撤销/过期 → 其短票立即失效。
   - 短票有效期默认 **5 分钟**（服务端常量或参数，写文档）；同会话可重复签发，前端在剩余 <60s 时自动续签。
   - 短票**只对 `GET` 生效**（`POST`/编辑类接口只认 `Authorization` 头）。
2. 前端统一收口：所有**媒体/下载/缩略图/HLS** 的 URL 一律经一个函数（如 `mediaURL(url)`）拼 `?t=<ticket>`；
   已有的 `fileURL()` / `thumbURL()` / `hlsURL()` / 下载链接构造点必须全部走它（一个都不能漏，
   漏一个就是"能登录但视频 401"）。
3. 服务端 `requireAuth`（及下载/预览等 GET 门禁）校验顺序：**短票 `?t=` → `Authorization: Bearer` → cookie（兼容旧会话，只读不再下发）**。
   - 短票/凭据无效一律 `401`（统一文案，不区分失败原因）。

### 3.3 有效期统一 7 天

- 服务端默认 token 有效期 **7 天**；新增 `--token-days`（默认 7，环境变量 `FILESERVER_TOKEN_DAYS`）。
- 移除 `--session-hours` 与 `--remember-days` 两个旧旋钮（README / `doc/security.md` / `--help` 同步移除），
  避免"两个都还在但语义重叠"。若为兼容保留，必须注释说明被 `--token-days` 取代。

### 3.4 登录页文案

- 「记住我」标签由「记住我（30 天内免登录）」改为 **「记住我（7 天内免登录）」**；
- 不勾的语义要能看懂：勾选框下补一行浅色说明（**不是**技术说明文，克制为主），例如
  「不勾选：关闭浏览器后需要重新登录」。

## 4. 非目标（明确不做）

- 不改口令 / TOTP / 限速 / 引导流程的任何校验逻辑。
- 不引入第三方库（保持标准库 + 自研；`go.mod` 只允许现有的 `golang.org/x/image`）。
- 不做多用户；不改 `--auth-mode none` 的敞开语义。
- **不得**把 token 本体放进 URL / 日志 / localStorage 之外的地方；不得为了兼容而恢复服务端 `Set-Cookie`。

## 5. 涉及面（改全，别漏）

- 后端：`internal/server/auth_handlers.go`（登录响应体、票据签发、门禁顺序）、`internal/auth/manager.go`
  （TTL 7 天、票据类型/校验/撤销联动）、`cmd/fileserver/config.go`（`--token-days`）、
  下载/预览/缩略图/HLS/ZIP 等所有 GET 门禁路径。
- 前端：`internal/server/web/app.js`（凭据读写、`mediaURL()` 收口、全局 401 处理、登出、登录/文案）。
- 文档：`doc/api.md`（登录响应体、`/api/auth/ticket`、门禁顺序）、`doc/security.md`（存储与留存）、
  `doc/frontend.md`（凭据存储与媒体 URL）、`README.md` / `README.en.md`（flag 与"记住我"语义）。
- 测试：`internal/auth/manager_test.go`（票据 TTL/撤销联动）、`internal/server/auth_handlers_test.go`
  （登录响应体带 token、票据签发与无效票据 401）、`scripts/smoke_test.py` 等端到端脚本按新流程调整。

## 6. 验收标准（Hermes 会逐条独立复验，含真人路径）

1. `make check` 全绿（fmt-check + 三平台交叉 vet/build + `go test ./...`）。
2. **不勾**：登录后 `sessionStorage['fs.token']` 有值、`localStorage` 里没有凭据；刷新免登录；
   **关闭标签再开 → 停在登录页**；关闭浏览器再开 → 停在登录页。
3. **勾选**：`localStorage['fs.token']` 有值；关闭浏览器（或新开标签）→ 免登录直进目录页（7 天内）。
4. 登录响应头里**没有** `Set-Cookie: fs_session`（可用 curl 断言）。
5. 媒体链路在**只有存储凭据、无 cookie** 的情况下全部可用：视频直链播放、HLS 播放与拖动、缩略图、
   图片预览、单文件下载、ZIP 下载、`/api/text` 读取与保存。
6. nginx access log 中**不出现 token 本体**（可以出现短票；短票 5 分钟失效）。
7. 短票过期后前端自动续签，播放不中断；服务端撤销该会话后 ≤1 个请求内 `401` 并回登录页。
8. 三种认证形态（`totp` / `password` / `password+totp`）都走一遍上述 2/3/5；`--auth-mode none` 行为不变。
9. 会话列表/撤销页（`/api/auth/sessions`）仍正常，且**不显示 token 明文**。
10. 文档四处（`doc/api.md`、`doc/security.md`、`doc/frontend.md`、README ×2）与新行为一致，flag 说明同改。
