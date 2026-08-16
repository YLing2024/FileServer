# FileServer 项目文档

本目录是 FileServer（局域网文件服务器）的全面技术文档。面向希望了解、修改或扩展本项目的人。

## 文档索引

| 文档 | 内容 |
|---|---|
| [architecture.md](architecture.md) | 总体架构：模块划分、进程模型、请求数据流、关键设计决策 |
| [api.md](api.md) | HTTP API 全量参考：每个接口的参数、响应、错误语义 |
| [video-pipeline.md](video-pipeline.md) | 视频播放管线：决策树、HLS 会话生命周期、转码规格、起播/seek 优化 |
| [ffmpeg.md](ffmpeg.md) | ffmpeg 集成：查找与打包、GPU 编码器探测、HLS 转码、faststart、媒体探测（缩略图已弃用服务端方案） |
| [cache.md](cache.md) | 缓存体系：`.FileServer` 目录结构、各缓存的生命周期与清理策略 |
| [frontend.md](frontend.md) | 前端架构：模块、播放器、浏览器抽帧缩略图（thumb-src）、移动端适配 |
| [security.md](security.md) | 安全模型：路径穿越、符号链接、认证、只读保证 |
| [build-and-test.md](build-and-test.md) | 构建、发布打包、测试体系 |

## 快速导航

- **用户**（只想使用）：见根目录 [README.md](../README.md)
- **开发者**（想改代码）：从 architecture.md 开始，按需深入
- **排障**：先看 cache.md 理解 `.FileServer` 缓存，再看 video-pipeline.md 理解播放链路

## 技术栈一览

- 语言：Go（标准库 + embed.FS 内嵌前端），前端为原生 HTML/CSS/JS（无构建步骤）
- 视频：外部 ffmpeg/ffprobe（可选组件，发布时捆绑）；hls.js（内嵌 415KB）播放 HLS
- 测试：Go 单元/集成测试 + Playwright（Python）浏览器端到端测试
