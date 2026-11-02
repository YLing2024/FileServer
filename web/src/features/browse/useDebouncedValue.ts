/** 输入防抖：搜索框 250ms 内连续输入只触发一次请求。 */
import { useEffect, useState } from 'react';

/** 返回 value 的防抖副本；value 变化后 delay 毫秒才更新。 */
export function useDebouncedValue<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);

  return debounced;
}
