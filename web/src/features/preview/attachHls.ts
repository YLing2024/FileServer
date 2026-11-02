/**
 * HLS 挂载：与旧前端同参数、同轮询、同 seek 收敛策略。
 *
 * - Safari 用原生 HLS；其余浏览器用 hls.js（npm 依赖，按需动态加载）。
 * - manifest 每 8 秒重载一次，推动 hls.js 看到新分片；出现 ENDLIST 即停止。
 * - 每次 XHR 前用最新短票重写 `?t=`，长转码会话不会因票过期中断。
 * - seek 超出已生成范围时收敛到最后可用位置，并只提示一次。
 */
import { request } from '../../api/client';
import { cachedTicket, ensureTicket, hlsUrl, withTicket } from '../../api/media';

/** attachHls 需要的回调；组件负责 UI 状态，本模块只处理协议细节。 */
export interface HlsOptions {
  /** 首个播放列表就绪（原生 HLS 为元数据就绪）。 */
  onReady: () => void;
  /** seek 被收敛时提示用户。 */
  onSeekClamped: (message: string) => void;
  /** hls.js 或原生 HLS 无法启动。 */
  onError: (message: string) => void;
}

/** 可清理的 HLS 会话。 */
export interface HlsAttachment {
  destroy: () => void;
}

interface ManifestState {
  total: number;
  ended: boolean;
}

async function readManifest(path: string): Promise<ManifestState | null> {
  const ticket = await ensureTicket();
  const response = await request(withTicket(hlsUrl(path), ticket), { cache: 'no-store' });
  if (!response.ok) return null;
  const text = await response.text();
  let total = 0;
  for (const line of text.split('\n')) {
    const match = /^#EXTINF:([\d.]+)/.exec(line);
    if (match?.[1]) total += Number.parseFloat(match[1]);
  }
  return { total, ended: text.includes('#EXT-X-ENDLIST') };
}

function isSafari(): boolean {
  return /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
}

/** 把 HLS 流挂到 video 元素；返回的 destroy 必须在离开预览时调用。 */
export function attachHls(
  video: HTMLVideoElement,
  path: string,
  options: HlsOptions,
): HlsAttachment {
  let destroyed = false;
  let hls: import('hls.js').default | null = null;
  let pollTimer: number | null = null;
  let ticketTimer: number | null = null;
  let available = 0;
  let clampNotifiedUntil = 0;

  const converge = (): void => {
    if (available <= 4 || video.currentTime <= available - 1.5) return;
    video.currentTime = Math.max(0, available - 3);
    if (Date.now() >= clampNotifiedUntil) {
      clampNotifiedUntil = Date.now() + 1500;
      options.onSeekClamped('已跳到当前可播位置');
    }
  };

  const onSeeking = (): void => converge();

  const refreshAvailable = (): void => {
    void readManifest(path)
      .then((state) => {
        if (destroyed || !state) return;
        if (state.total > 0) available = state.total;
        converge();
      })
      .catch(() => {
        // manifest 暂时不可读：下一轮轮询会重试。
      });
  };

  const finish = (): void => {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer);
      pollTimer = null;
    }
  };

  video.addEventListener('seeking', onSeeking);
  void ensureTicket();
  // 短票剩余寿命不足 60 秒时 ensureTicket 会续签；30 秒检查一次足够覆盖长转码。
  ticketTimer = window.setInterval(() => {
    void ensureTicket();
  }, 30_000);

  if (isSafari() && video.canPlayType('application/vnd.apple.mpegurl')) {
    void ensureTicket().then((ticket) => {
      if (destroyed) return;
      video.src = withTicket(hlsUrl(path), ticket);
      video.addEventListener('loadedmetadata', () => options.onReady(), { once: true });
    });
    return {
      destroy: () => {
        destroyed = true;
        video.removeEventListener('seeking', onSeeking);
        if (ticketTimer !== null) window.clearInterval(ticketTimer);
      },
    };
  }

  void import('hls.js')
    .then(({ default: Hls }) => {
      if (destroyed) return;
      if (!Hls.isSupported()) {
        void ensureTicket().then((ticket) => {
          if (destroyed) return;
          video.src = withTicket(hlsUrl(path), ticket);
          video.addEventListener('loadedmetadata', () => options.onReady(), { once: true });
        });
        return;
      }

      const instance = new Hls({
        enableWorker: true,
        backBufferLength: 30,
        liveSyncDurationCount: 999,
        fragLoadingTimeOut: 30_000,
        fragLoadingMaxRetry: 4,
        fragLoadingRetryDelay: 500,
        manifestLoadingTimeOut: 30_000,
        manifestLoadingMaxRetry: 4,
        xhrSetup: (xhr, requestUrl) => {
          const ticket = cachedTicket();
          if (!ticket) return;
          try {
            const url = new URL(requestUrl, window.location.origin);
            url.searchParams.set('t', ticket);
            xhr.open('GET', `${url.pathname}${url.search}`, true);
          } catch {
            // URL 解析失败时保持 hls.js 默认行为。
          }
        },
      });
      hls = instance;
      instance.loadSource(withTicket(hlsUrl(path), cachedTicket()));
      instance.attachMedia(video);

      instance.on(Hls.Events.MANIFEST_PARSED, () => options.onReady());
      instance.on(Hls.Events.ERROR, (_event, data) => {
        if (!data.fatal) return;
        if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
          instance.startLoad();
        } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          instance.recoverMediaError();
        } else {
          window.setTimeout(() => {
            if (!destroyed) instance.startLoad();
          }, 2000);
        }
      });

      refreshAvailable();
      pollTimer = window.setInterval(() => {
        void readManifest(path)
          .then((state) => {
            if (destroyed || !state) return;
            if (state.total > 0) available = state.total;
            converge();
            if (state.ended) {
              finish();
              return;
            }
            try {
              instance.startLoad();
            } catch {
              // 实例已销毁或尚未就绪：忽略，下一轮再试。
            }
          })
          .catch(() => {
            // 转码尚未产出播放列表：保持轮询。
          });
      }, 8000);
    })
    .catch(() => {
      options.onError('HLS 组件加载失败');
    });

  return {
    destroy: () => {
      destroyed = true;
      finish();
      if (ticketTimer !== null) {
        window.clearInterval(ticketTimer);
        ticketTimer = null;
      }
      video.removeEventListener('seeking', onSeeking);
      if (hls) {
        try {
          hls.destroy();
        } catch {
          // hls.js 已销毁：重复清理无需处理。
        }
        hls = null;
      }
    },
  };
}
