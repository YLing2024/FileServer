/** 浏览偏好持久化：视图/排序/方向沿用旧前端的 localStorage 键。 */
import { useCallback, useState } from 'react';

/**
 * 读写一个字符串偏好。
 *
 * @param key localStorage 键（fs.view / fs.sort / fs.order）
 * @param fallback 无记录或记录非法时的默认值
 * @param isValid 运行时校验，避免旧版本或手工改坏的值进入状态
 */
export function usePersistentChoice<T extends string>(
  key: string,
  fallback: T,
  isValid: (value: string | null) => value is T,
): [T, (value: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const saved = localStorage.getItem(key);
      return isValid(saved) ? saved : fallback;
    } catch {
      // 隐私模式下存储不可用：本次会话仍可切换，只是不持久化。
      return fallback;
    }
  });

  const update = useCallback(
    (next: T) => {
      setValue(next);
      try {
        localStorage.setItem(key, next);
      } catch {
        // 写入失败不影响当前会话的显示。
      }
    },
    [key],
  );

  return [value, update];
}
