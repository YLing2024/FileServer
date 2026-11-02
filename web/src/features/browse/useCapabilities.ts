/** 服务端能力与冷门格式开关。 */
import { useCallback, useEffect, useState } from 'react';
import { requestJson } from '../../api/client';
import { fetchHealth, fetchServerInfo } from '../../api/info';
import { DEFAULT_LIST_LIMIT } from '../../api/list';
import { DEFAULT_SEARCH_LIMIT } from '../../api/search';

interface CapabilitiesState {
  ffmpeg: boolean;
  hls: boolean;
  searchLimit: number;
  listLimit: number;
  ready: boolean;
}

const INITIAL_STATE: CapabilitiesState = {
  ffmpeg: false,
  hls: false,
  searchLimit: DEFAULT_SEARCH_LIMIT,
  listLimit: DEFAULT_LIST_LIMIT,
  ready: false,
};

/**
 * 读取 /api/info 与 /api/health，并暴露切换冷门格式的调用。
 *
 * 任务要求优先读 /api/health 的 hls 字段；当前后端健康检查只返回进程/版本
 * 字段，因此保留 health.hls 优先、/api/info 兜底的顺序，后端补字段后无需改前端。
 */
export function useCapabilities() {
  const [state, setState] = useState<CapabilitiesState>(INITIAL_STATE);
  const [toggling, setToggling] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    Promise.all([
      fetchServerInfo(controller.signal).catch(() => null),
      fetchHealth(controller.signal).catch(() => null),
    ]).then(([info, health]) => {
      if (controller.signal.aborted) return;
      const hlsFromHealth = typeof health?.hls === 'boolean' ? health.hls : undefined;
      setState({
        ffmpeg: Boolean(info?.ffmpeg),
        hls: hlsFromHealth ?? Boolean(info?.hls),
        searchLimit: info?.search_limit ?? DEFAULT_SEARCH_LIMIT,
        listLimit: info?.list_limit ?? DEFAULT_LIST_LIMIT,
        ready: true,
      });
    });
    return () => controller.abort();
  }, []);

  const toggleHls = useCallback(async () => {
    if (toggling) return;
    setToggling(true);
    try {
      const next = !state.hls;
      const body = await requestJson<{ hls?: boolean }>('/api/settings/ffmpeg', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: next }),
      });
      const hls = typeof body.hls === 'boolean' ? body.hls : next;
      setState((current) => ({ ...current, hls }));
    } finally {
      setToggling(false);
    }
  }, [state.hls, toggling]);

  return { ...state, toggling, toggleHls };
}
