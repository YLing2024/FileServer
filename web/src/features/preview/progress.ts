/**
 * 播放进度记忆：视频与音频共用，按「路径 + 文件大小」区分。
 *
 * 同一路径下文件被替换（大小变化）时旧进度自动失效，避免恢复到错误位置。
 * localStorage 不可用（隐私模式）时静默退化为不记忆。
 */

const PREFIX = 'fs.progress.';

/** 进度键：路径 + 字节数，旧版本/同名覆盖文件不会串进度。 */
export function progressKey(path: string, size: number): string {
  return `${PREFIX}${path}:${size}`;
}

/** 读取上次播放位置（秒）；无记录或数据损坏返回 0。 */
export function readProgress(path: string, size: number): number {
  try {
    const raw = localStorage.getItem(progressKey(path, size));
    if (!raw) return 0;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 1) return 0;
    return value;
  } catch {
    // 存储被禁用或内容损坏：当作没有进度。
    return 0;
  }
}

/**
 * 保存播放位置。片头 1 秒内与结尾 1 秒内不保存（避免每次打开都从 0 开始或
 * 一进来就触发「播放结束」）；短片（<6 秒）也能记住中段位置。
 */
export function saveProgress(path: string, size: number, time: number, duration: number): void {
  if (!Number.isFinite(time) || time < 1) return;
  if (!Number.isFinite(duration) || duration <= 0) return;
  if (time >= duration - 1) {
    clearProgress(path, size);
    return;
  }
  try {
    localStorage.setItem(progressKey(path, size), JSON.stringify(Math.round(time * 10) / 10));
  } catch {
    // 写入失败不影响播放。
  }
}

/** 清除某文件的进度（播放结束或用户从头播放时）。 */
export function clearProgress(path: string, size: number): void {
  try {
    localStorage.removeItem(progressKey(path, size));
  } catch {
    // 存储不可用：无需处理。
  }
}
