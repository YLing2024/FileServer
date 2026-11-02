import { writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Vite 的 emptyOutDir 会清空整个输出目录。保留一个占位文件，让
// //go:embed all:web-dist 在尚未构建前端时也能通过编译；运行时再检查 index.html
// 是否存在，缺失时 /v2/ 返回明确的 503 而不是 panic。
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const placeholder = resolve(root, '../internal/server/web-dist/.keep');

writeFileSync(
  placeholder,
  'go:embed 需要目录至少包含一个文件；本文件由 pnpm build 生成，请勿手动删除。\n',
  'utf8',
);
