/** 展示层格式化：大小与时间。所有 UI 统一走这里，避免各页各写一套。 */

const SIZE_UNITS = ['KB', 'MB', 'GB', 'TB'] as const;

/** 把字节数格式化为人类可读字符串；负数/非有限值视为未知。 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '';
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < SIZE_UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
  const unitLabel = SIZE_UNITS[unit] ?? 'TB';
  return `${value.toFixed(digits)} ${unitLabel}`;
}

const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

/** Unix 秒转本地时间文本；0/非法值返回空串。 */
export function formatTime(unixSeconds: number): string {
  if (!Number.isFinite(unixSeconds) || unixSeconds <= 0) return '';
  return dateFormatter.format(new Date(unixSeconds * 1000));
}
