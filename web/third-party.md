# 前端第三方库登记

前端（`web/`，React + TypeScript + Vite）的运行时依赖集中登记于此，确保来源、
版本、许可可追溯。旧的 `internal/server/web/hls.min.js` 已随旧前端删除，hls.js
改为 npm 依赖并由 Vite 打包。

| 包 | 版本 | 许可 | 用途 |
|---|---|---|---|
| `react` / `react-dom` | 19.x | MIT | UI 运行时 |
| `hls.js` | 1.5.20（锁定精确版本） | Apache-2.0 | 冷门格式在线转码播放；按需动态加载，不进入首屏包 |

依赖清单与完整性校验以 `package.json` / `pnpm-lock.yaml` 为准：

```bash
cd web
pnpm install --frozen-lockfile
pnpm why hls.js
```

升级 hls.js 时：更新 `package.json`（精确版本）→ `pnpm install` → 重新
`pnpm build` → 提交 `pnpm-lock.yaml` 与 `internal/server/web-dist`。

除上述依赖外不引入 UI 库或其他运行时依赖；开发依赖见 `web/package.json`。
