# 前端第三方库登记

内嵌前端（`//go:embed web`，源码目录物理位置为 `internal/server/web/`）中引入的
第三方库集中登记于此，确保来源、版本、许可可追溯。

| 文件 | 库 | 版本 | 许可 | 来源 | SHA256 |
|---|---|---|---|---|---|
| `internal/server/web/hls.min.js` | [hls.js](https://github.com/video-dev/hls.js) | 1.5.20 | Apache-2.0 | `https://cdn.jsdelivr.net/npm/hls.js@1.5.20/dist/hls.min.js` | `d016c1230496ee59f3f5b01c16cce4cc01b5a1d3d357adec200c908b131ebe49` |

## 校验

```bash
sha256sum internal/server/web/hls.min.js
# 期望 d016c1230496ee59f3f5b01c16cce4cc01b5a1d3d357adec200c908b131ebe49
```

升级 hls.js 时：替换文件 → 重新编译 exe → 更新本表的版本 / 来源 / SHA256。

## V2 前端（npm 依赖）

V2（`web/`）的运行时依赖登记在 `package.json` / `pnpm-lock.yaml`：

| 包 | 版本 | 许可 | 用途 |
|---|---|---|---|
| `react` / `react-dom` | 19.x | MIT | UI 运行时 |
| `hls.js` | 1.5.20（与旧 vendor 同大版本，锁定精确版本） | Apache-2.0 | 冷门格式在线转码播放；按需动态加载，不进入首屏包 |

除上述依赖外不引入 UI 库或其他运行时依赖；开发依赖见 `package.json`。
