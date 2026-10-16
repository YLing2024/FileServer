# ffmpeg 集成

## 1. 查找与打包

`FindFfmpeg()` 按顺序查找 `ffmpeg.exe`（ffprobe 同目录）：

1. exe 同目录的 `ffmpeg\` 子文件夹（发布包形态）
2. exe 同目录
3. `%LOCALAPPDATA%\FileServer\ffmpeg\`
4. 系统 PATH

```
你的文件夹\
├── FileServer.exe
└── ffmpeg\
    ├── ffmpeg.exe
    └── ffprobe.exe
```

- 未找到：服务端缩略图与转码自动禁用，前端降级为浏览器抽帧/直接下载。
- 仅装 ffmpeg 未装 ffprobe：时长探测回退 `ffmpeg -i` 解析 stderr。
- **构建版本**：release-essentials 即可满足缩略图与 CPU 转码；
  需要 GPU 转码（nvenc/amf/qsv）时必须用 full build（essentials 不带硬件编码器）。
  注意 essentials 构建的 CPU 抽帧明显更慢（无汇编优化），尽量用 full build。

## 2. 子进程管理（`runFFmpeg`）

- `exec.CommandContext`：ctx 取消即 Kill。
- 低优先级（Windows）：`CreationFlags = BELOW_NORMAL_PRIORITY_CLASS (0x4000)`。
- **防孤儿**（`platform.KillOnParentExit` + 平台进程终止函数）：
  - Windows：子进程挂入 Job Object，`KILL_ON_JOB_CLOSE`——服务进程退出时连带终止；
    Job 句柄**不可继承**（`bInheritHandle=0`），避免句柄泄漏导致 Job 永不关闭。
  - POSIX：子进程以 `Setpgid` 独立成组（`platform.SetProcessGroup`），终止时
    `kill(-pgid, SIGKILL)` 带走整组（`platform.TerminateTree`）；服务被强杀
    （信号处理来不及跑）时，遗留进程由**下次启动清理**（进程组登记在
    `.FileServer/procs.pids`，启动时按 `ps` 可执行名核验后强杀）。
  - 兜底：ctx.Done() 后等 5s 仍未退出才强杀整棵进程树（盲目 5s 强杀会误杀
    正常慢任务——历史 bug）。
- 使用场景（**视频缩略图已 100% 浏览器抽帧，服务端不再用 ffmpeg 抽帧**）：
  - HLS copy/转码（GPU 优先，长任务）；
  - 媒体探测（ffprobe JSON，2s 超时，绝不阻塞播放链路）。

## 3. 视频缩略图：已废弃服务端方案

历史教训（为什么不再用 ffmpeg 抽帧）：抽一帧本身微不足道，贵的是 ffmpeg 为
怪封装 moov（5~6M 条 stco 表项）建完整样本索引前的**碎片化随机读**——机械盘上
冷读 5~15s/张，多路并发直接打爆磁盘。曾尝试「顺序预热 moov 再抽帧」把冷读
压到 0.5~2.6s，但 3 路以上并发在这块碎片盘上依然互相拖慢到 15s 超时。

最终方案：**成本摊给浏览器**——前端 `<video preload=metadata>` 加载
`/api/thumb-src` 截短源（≤16MB 合法 MP4），Range 小读 + 硬件解码，服务端零
抽帧负担（详见 frontend.md / api.md 的 thumb-src）。

## 4. GPU 编码器探测（`probeEncoder`）

启动时后台执行（45s 上限，期间先用 libx264）：

1. `ffmpeg -encoders` 列出可用编码器；
2. 依次实测 `h264_nvenc`（CUDA）→ `h264_amf`（D3D11VA）→ `h264_qsv`：
   跑一段 0.5s 的真实编码（testsrc2 源），成功才启用——仅列出不等于可用；
3. 同时探测 zscale/tonemap 滤镜（HDR→SDR 色调映射）。

## 5. 媒体探测（`ProbeMedia`）

- ffprobe JSON：时长、视频编码、分辨率、像素格式、音频编码。
- 2s 超时 + 内存缓存（4096 条）+ 单飞（同文件并发探测共享结果）。
- 只在**后台**触发（video-info 未命中时），绝不阻塞播放链路；
  前端 2s 后二次查询补时长。

## 6. MP4 规整化已移除

历史版本的 faststart 重封装（`-c copy -movflags +faststart`，moov 前置）已删除。
MP4 若需**永久规整**（mdat 合并 / moov 前置），请使用独立项目
[mp4norm](https://github.com/YLing2024/mp4norm)；FileServer 只保留 MP4 顶层布局
**判定**（`mp4.go`：碎片化 mdat / moov 位置），服务于「怪封装走 HLS copy」的
播放决策，不再改写用户文件。
