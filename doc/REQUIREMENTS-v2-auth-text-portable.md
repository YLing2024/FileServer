# 需求：跨平台化 + 双因子认证 + 文本编辑（本轮）

> 下达：Linden Zhang（2026-10-16）· 文档：Hermes
> 目标项目：`FileServer`（本仓库）· 本轮结束即成为**跨平台**项目，Windows 与 Linux 均为一级运行目标。
> 本文件是本轮**唯一权威需求**。与仓库既有文档冲突处，以本文件为准；实现完把变化同步回 `doc/`。

---

## 0. 一句话

把 FileServer 从「Windows 局域网只读文件服务器」升级为**跨平台**的文件服务器：新增**密码 + TOTP 双因子认证**（首次进入引导设置、TOTP 可跳过、登录支持「记住我」）、**文本文件新建/预览/编辑**；删除已迁出到 mp4norm 的 **MP4 faststart 规整化**代码。局域网「双击即用」体验必须原样保留。

---

## 1. 背景与既有约定（必读）

- 仓库现有约定见 `AGENTS.md` 与 `doc/`（架构 / API / 安全 / 视频管线 / 缓存 / 前端 / 构建测试）。
- 现状：认证只有 `--auth user:pass`（Basic Auth，`internal/server/middleware.go`），**默认不传即敞开**。
- 现状定位：「浏览即读，不写用户文件」——本轮**有限放开**：仅允许「新建文本文件」与「编辑文本文件」，其余（删除/重命名/移动/上传）**本轮不做**。
- 现状定位：「服务器所在的 Linux VPS 不是运行目标」——**本条作废**：Linux 变一级目标（见 §4）。
- 前端是 `//go:embed web` 内嵌原生 JS，**无构建链**；改 `web/` 必须重新编译才生效。
- 提交纪律：`make check` 全绿才可提交；提交信息英文 conventional commits；小步多次提交；**不得**改动 git 身份配置。
- 不引入需要额外安装的运行时依赖；能自研就自研（仓库既有先例：qrcode）。

---

## 2. 认证（本轮核心）

### 2.1 认证模式（三种形态 + 关闭态）

新增启动参数 `--auth-mode`，取值：

| 值 | 语义 | 引导页行为 |
|---|---|---|
| `none`（默认） | 不启用认证，行为与现在**完全一致**（局域网敞开）| 不出现 |
| `totp` | **仅 TOTP**：登录只填 6 位验证码，无密码字段 | **必须**绑定 TOTP，不可跳过 |
| `password` | **仅密码** | 只设置密码 |
| `password+totp` | **密码 + TOTP**（双因素）| 设置密码 + 绑定 TOTP（**可跳过**，跳过即退化为 `password`，并在设置页提示可随时补绑）|

- 三种形态都必须支持「记住我」（见 §2.3）。
- `totp` 形态的**自救路径**（必要，否则丢设备即锁死）：提供命令行重置（如 `--reset-totp` / `--reset-auth`，命名自定），在服务未启动状态下清除 TOTP 绑定或全部凭据并重新进入引导；README 与 `doc/security.md` 必须写明操作步骤与安全提示（重置后任何人都能重新引导绑定，故需同时说明「应在受信网络下操作」）。

兼容：`--auth user:pass` 等价于「`--auth-mode password` + 预置口令」。环境变量沿用 `FILESERVER_*` 风格（新增 `FILESERVER_AUTH_MODE`、`FILESERVER_TOTP_SECRET`、`FILESERVER_REMEMBER_DAYS` 等，命名自定但要在 README 表格登记）。

**不要**改变默认值语义：不传任何认证参数时仍然是敞开（局域网自用场景）。

### 2.2 首次进入引导（首次运行 / 尚未配置时）

- 服务启动后，若 `--auth-mode != none` 且配置尚未完成（无口令哈希 或 需绑定 TOTP），浏览器首次访问落地到**引导页**：
  1. 设定访问密码（若未预置；两次输入 + 强度下限，长度 ≥ 8）
  2. 绑定 TOTP：显示二维码（自研 qrcode 已有，勿引第三方）+ 手输密钥 + 一次性验证码确认
  3. 允许**跳过 TOTP**（跳过即只用密码；页面明确说明随时可在设置里补绑）
- 引导页在配置完成后**自动关闭**：再次访问直接进登录页。
- 引导页与登录页**不得**泄露服务目录内容。

### 2.3 登录会话与「记住我」

登录页提供「记住我」勾选框：

- **不勾**：仅下发会话 cookie（**无 Max-Age**，浏览器关闭即失效），服务端会话 TTL 默认 12 小时（可配）。
- **勾选**：下发持久 cookie，有效期默认 **30 天**（`--remember-days` 可配），期间进入免登录。
- cookie 要求：`HttpOnly`、`SameSite=Lax`、`Path=/`；当请求为 https（或显式 `--cookie-secure`）时带 `Secure`。
- 凭据**只存服务端哈希**：`.FileServer/auth.json`（0600）保存口令哈希（Argon2id 或 bcrypt 之一，仓库自研/标准库实现，勿引第三方库）、TOTP 密钥（**加密或最少 0600 保护**，说明取舍）、会话与记住我 token 的 `sha256` 哈希 + 过期时间。
- 支持**登出**（`POST /api/auth/logout`）：使当前 token 失效。
- 提供「已登录设备/会话」列表与撤销（可选但强烈建议，用于「忘记我」场景）：列出 token 创建时间、最后使用时间、UA、来源 IP；可单独撤销。**不得**显示 token 明文。
- 语义提示：**只设密码、不绑 TOTP** 时等同单因素；**绑定 TOTP** 后为双因素。

### 2.4 安全要求（硬性）

- 口令与 TOTP 校验一律**恒定时间比较**（仓库已有 `subtle.ConstantTimeCompare` 先例）。
- **TOTP 重放防护**：同一时间步（含 ±1 步窗口）的验证码只能成功一次，成功后记录已消费时间步。
- **失败限速**：按客户端 IP 阶梯锁定（沿用 60s → 300s → 900s 思路；实现自定，需有测试）。支持 `--trust-proxy` 时按 `X-Forwarded-For` 最左/最右**明确写死一种**并在文档说明（默认不信任代理头）。
- 认证相关响应**不得**出现「用户不存在 / 密码错 / 验证码错」的可区分信息差异（统一文案）。
- 认证 cookie 与凭据**不得**出现在访问日志、错误日志、`-v` 详细日志中；`/api/auth/*` 路径日志需脱敏 token。
- 登录页 / 引导页必须带严格 CSP、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Cache-Control: no-store`（仓库 `drop` 项目已有同规范，可参考实现思路）。

---

## 3. 文本文件：新建 / 预览 / 编辑

### 3.1 能力

- **新建**：在浏览的目录内新建文本文件（指定文件名 + 初始内容，允许空文件）。
- **预览**：沿用现有 `kind: text / code` 预览（`web/app.js` 已有），编辑态与预览态可切换。
- **编辑并保存**：保存回原文件。
- **不做**：删除、重命名、移动、上传、目录创建（本轮明确不做，别顺手加）。

### 3.2 硬性约束

- **扩展名白名单**：仅允许文本类（如 `.txt .md .json .yaml .yml .toml .ini .conf .log .csv .xml .sh .py .js .ts .css .go .sql` 等，实现时给出一份明确清单并写进 `doc/api.md`）。白名单外的扩展名新建/编辑一律拒绝（403 + 说明）。
- **大小上限**：可编辑文件默认上限 2 MiB（可配 `--text-max-bytes`），超限只允许预览不允许编辑。
- **原子写**：临时文件 + `rename`，失败不留半截文件；保留原文件权限位。
- **并发防覆盖**：写请求必须携带读取时的版本标识（mtime + size 或 ETag，实现自定），不匹配返回 **409**，前端提示「文件已被其他人修改」。
- **只读回退**：目录或文件不可写（权限/只读介质）时返回明确错误，**不得**留下临时文件残留。
- **路径安全**：新建/编辑路径必须走既有 `path.go` 的穿越与符号链接防护（`path_test.go` / `path_posix_test.go` 是主要防线，**必须补测试覆盖新入口**）。
- `scriptableExt`（html/js/svg 等）现有规则**不得放宽**：直链访问仍强制附件下载，编辑能力不改变直链行为。
- 文档：`doc/api.md` 新增接口、`doc/security.md` 更新「写入范围」章节（从"完全不写"改为"仅文本新建与编辑 + 约束清单"）、`AGENTS.md` 同步该约定。

---

## 4. 跨平台（一级目标：Windows / Linux / macOS 三平台都要）

### 4.0 三平台均为一等目标

- **Windows**：双击即用（保留现状）；控制台 UTF-8、二维码、Job Object 防孤儿保持不变。
- **Linux**：可命令行 / systemd 运行；可只在反代后暴露；POSIX 进程组终止。
- **macOS**：可命令行运行；打开浏览器用 `open`（**当前 `platform_other.go` 用的是 `xdg-open`，在 macOS 上不成立，必须按 `runtime.GOOS` 分支**）；首次运行会弹「允许传入连接」防火墙提示，README 需说明；Intel 与 Apple Silicon（`arm64`）都要出产物。

### 4.1 平台隔离

- 所有 OS 差异**只能**出现在 `internal/platform/`（按平台拆文件，改一个必须同步其余；打开浏览器 / 进程终止 / 控制台能力三类差异必须逐平台明确）。
- 仓库既有 Linux 侧空缺（实测确认，必须补齐）：
  - `taskkill /F /T /PID` 三处硬编码（`internal/server/hls_session.go`、`internal/server/ffmpeg_run.go`、`internal/server/hls.go`）→ Linux/macOS 上静默失效。
  - `platform_other.go` 的 `KillOnParentExit` 是 **no-op** → 服务被 SIGKILL 后 ffmpeg 变孤儿。
  - **要求**：POSIX 侧用进程组实现等价语义（`SysProcAttr{Setpgid:true}` + `kill(-pgid, SIGKILL)`；强杀兜底改为平台函数），并**在服务启动时清理**本服务遗留的 ffmpeg 子进程。Windows 侧行为不得退化（Job Object 保持）。
  - 必须补一条 POSIX 上的回归测试：服务被强杀后无 ffmpeg 残留（或至少覆盖进程组终止函数）。

### 4.2 监听地址

- 新增 `--host`（默认 `0.0.0.0`，保持局域网扫码即用）。
- 设为 `127.0.0.1` 时可只在反代后暴露（公网场景）。
- 控制台地址输出：局域网地址列表逻辑（`lanip.go`）在 Linux 上必须正确（不要打出 `127.0.0.1` 之外的错地址），二维码渲染在 Linux 终端可用（ANSI 已有，勿依赖 Windows 控制台 API）。

### 4.3 构建与 CI

- `Makefile` 增加三平台产物目标（命名自定），至少覆盖：`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64`；Windows 现有目标保持不变。`make check` 需覆盖**三平台** vet/build。
- CI 矩阵：`windows-latest`（全量 `go test -race`）、`ubuntu-latest`（全量 `go test -race`）、`macos-latest`（全量 `go test`）——**三端任一红灯即失败**；现有的「Linux 只做 vet/build」必须升级为真跑测试。
- 定位文档同步：`README.md` / `README.en.md` / `AGENTS.md` / `doc/*.md` 中「服务器不是运行目标」「Windows 局域网」等表述改为跨平台事实描述；Linux/macOS 部署方式（命令行参数、systemd 单元示例、macOS 防火墙提示、反代到 443 的说明）写入 `doc/build-and-test.md` 或新增 `doc/deploy-linux.md`。

---

## 5. 删除 MP4 faststart 规整化（已迁至 mp4norm）

需求：项目内的 **MP4 规整化 / faststart 重封装**能力全部删除，只保留**判定**能力中仍然需要的那部分。

必须删除（实测清点，逐一核对）：

- `internal/server/server.go`：`faststartState` 字段、`fsDir` 构造与装配
- `internal/server/cache.go`：`faststartState` 类型定义与相关缓存逻辑（`.FileServer/faststart/`）
- `internal/server/video.go`：`warmFaststart` 触发与 `faststart` 响应字段
- `internal/server/file.go`：`?fs=1` 分支与注释
- `internal/server/prewarm.go`：与 faststart 预热相关的注释/逻辑（若已并入）
- `internal/server/thumb.go`：抽帧源「优先 faststart 缓存」分支
- `internal/server/faststart_test.go`：整文件删除
- 如 `mp4IsFastStart` 在删除后**无任何引用**，一并删除；`ffmpeg_test.go` 中对应测试同步清理。
- 文档：`doc/ffmpeg.md` §5、`doc/cache.md` §3、`doc/video-pipeline.md`、`doc/api.md`（`fs=1` 与 `faststart` 字段）、`doc/architecture.md`、`README.md`、`AGENTS.md` 中相关段落删除，并写清「**永久规整请用 mp4norm**」。
- `.FileServer/` 缓存目录只应剩 `thumb/` 与 `hls/`；`backup/` 历史遗留说明保留或清理，二者择一并在文档写明。

**保留**：`internal/server/mp4.go` 的 mp4 布局判定（碎片化 mdat / moov 过大）——它服务于「怪封装 MP4 走 HLS copy 重封装」的播放决策，**不是**规整化。

---

## 6. 明确不做（本轮）

上传文件 / 删除 / 重命名 / 移动 / 建目录、多用户与权限分级、WebDAV、分享链接、把认证接入外部 SSO。

（注：**仅 TOTP 免密码登录已列入 §2.1 的三种认证形态之一，必须实现**，不再属于「不做」。）

---

## 7. 验收清单（交付时必须逐条自证，并把原始输出贴进报告）

**构建与门禁**
1. `make check` 全绿；`go test ./... -race` 在 **Linux** 上全绿（Windows 侧 `GOOS=windows go vet/build` 通过）。
2. 三平台产物：`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64` 均能构建；Linux 二进制实测能启动（本机可跑）；CI 三平台矩阵配置已就位（windows / ubuntu / macos）。

**认证（三种形态）**
3. `--auth-mode none` 时行为与改前一致（无登录页）。
4. `password` 模式：首访进引导页 → 设密码 → 进登录页；错误密码被拒（文案不区分原因）；连续失败触发限速。
5. `password+totp`：引导页设密码 + 绑定 TOTP（二维码 + 手输密钥 + 一次性码确认）；**跳过**路径可用；跳过后再访问只要求密码。
6. `totp`（仅 TOTP）：引导页**必须**绑定、不可跳过；绑定后登录页只要求 6 位验证码（无密码字段）；只填验证码可登录成功。
7. `totp` 自救：用命令行重置（`--reset-totp` / `--reset-auth`）后能重新进入引导并重新绑定；README 有步骤。
8. TOTP 校验：正确码通过；**同一码重放被拒**；±1 步窗口行为明确并有测试。
9. 「记住我」勾选：关浏览器再开仍免登录（生效期内）；**不勾**：关浏览器后需重新登录；**三种形态都要验证**（至少 `password` 与 `totp` 各测一次）。
10. 登出后旧 token 失效；会话列表可撤销指定会话（若实现了该功能）。
11. 凭据文件 `.FileServer/auth.json` 权限 0600；日志（含 `-v`）中**无**口令/token/验证码。

**文本编辑**
12. 新建 `.txt` / `.md` 成功；白名单外扩展名被拒；超限文件只读预览。
13. 编辑保存成功、内容正确；并发冲突返回 409（两方持有同一版本先后保存）。
14. 只读目录/只读文件保存失败且无临时文件残留。
15. 路径穿越（`../../`、URL 编码变形、符号链接）在新接口上仍被拦截（补测试）。
16. 直链行为未变：`.html/.js/.svg` 仍强制附件下载。

**faststart 删除**
17. 全局 grep `faststart` 仅剩文档中「已迁移到 mp4norm」的说明与必要的 MP4 判定命名（若保留）；代码中无 faststart 缓存/重封装路径。
18. 运行后 `.FileServer/` 下**不再**出现 `faststart/`；抽帧与播放功能不退化。
19. HLS 决策仍正确：怪封装 MP4 → `hls`；普通 H.264 MP4 → `direct`；MKV → `hls`。

**跨平台（三平台）**
20. Linux 上：HLS 转码、缩略图、zip、搜索、Range 播放全部可用（真实文件，贴原始响应）。
21. 强杀服务后**无 ffmpeg 残留**（POSIX：Linux；有条件时附 macOS）。
22. `--host 127.0.0.1` 时只监听回环；默认 `0.0.0.0` 时局域网地址列表输出正确（Linux 要打出真实网卡地址而非 127.0.0.1）。
23. macOS 构建产物存在且 `open` 分支代码就位（无 macOS 机器时至少给交叉构建输出 + 代码走查结论，并**明说未实机验证**）。

---

## 8. 交付纪律

- 小步多次提交，英文 conventional commits；**不得**修改 git 身份或仓库级 git config。
- 同步更新：`README.md`、`README.en.md`、`AGENTS.md`、`doc/*.md`（至少 architecture / api / security / cache / ffmpeg / video-pipeline / build-and-test）。
- 不引入第三方依赖（现有 `golang.org/x/image` 是唯一例外）。
- 完成后把「实现摘要 + §7 逐条自证原始输出 + 未做/存疑项」写入报告文件。

---

## 8. 复查发现的缺陷修复（2026-10-16 Hermes 独立验收，必须修）

### F1 `password+totp` 引导页非原子提交（缺陷）

- **现象（实测原始输出）**：`POST /api/auth/setup` 提交 `{"password":"<≥8位>","code":"000000"}`（错误验证码）→ 返回 `400 {"error":"验证码不正确"}`；
  随后 `GET /api/auth/status` 变为 `configured=true, password_set=true, totp_bound=false, needs_setup=false`，
  且此时 `POST /api/auth/login` **只带正确口令即返回 200**（TOTP 从未绑定，双因素实际未启用）。
- **危害**：用户看到报错，却以为双因素已启用；引导页不再出现。
- **要求**：引导提交必须**原子**——先校验全部必需因素（口令强度、验证码），全部通过后再一次性持久化（口令哈希 + TOTP 绑定 + 配置状态）。
  任一步失败返回 `400`，**不得留下任何部分状态**：`status` 必须仍为 `needs_setup=true`，`password_set` 不得变 true，且后续仅凭口令登录必须 `401`。
- **注意**：`--auth user:pass` / 环境变量预置口令的路径不受影响（那是预置，不是引导）。

### F2 `/api/health` 需免鉴权（运维必需）

- **现象**：`--auth-mode password` 下 `GET /api/health` 返回 `401`。
- **危害**：外部探活（看门狗、nginx/反代健康检查、容器 healthcheck）无法匿名使用。
- **要求**：认证开启时 `GET /api/health` 仍可**匿名**访问并返回 `200`（响应体维持现状字段，不得新增目录/文件/会话信息）。
  其余 `/api/*`（含 `/api/info`、`/api/list`）维持现有 401 行为不变。

### F3 引导绑定用过的验证码必须计入重放集合（缺陷）

- **现象**：绑定 TOTP 成功时用的那个码，在同一 30 秒时间步内再次用于 `POST /api/auth/login` 仍返回 `200`。
- **要求**：任何一次 TOTP 校验成功（引导绑定或登录）都必须把该时间步写入"已用"集合；同一步内同码再次使用一律 `401`。
- **回归护栏**：窗口语义不得改变——当前步、±1 步可接受（未用过的码），±2 步拒绝。

### 本轮纪律

- `make check` 全绿；小步多次提交，英文 conventional commits；不得修改 git 身份或仓库级 git config；不引入第三方依赖。
- 文档同步：`doc/api.md` 标注 `/api/health` 免鉴权；`doc/security.md` 说明引导的原子性与重放集合范围。
- 完成后把「修复摘要 + F1/F2/F3 逐条实测原始输出（含 curl 状态码）」写入报告文件 `/tmp/oc-fs-v2-fixes-report.md` 并 `cat` 打印。
