/**
 * Worker 包装与降级：优先在 Worker 里流式计算 SHA-256，环境不支持时回退
 * 主线程分片（仍是流式，不一次性读完整文件）。
 */
import { hashBlob } from './sha256';

interface HashMessage {
  type?: string;
  progress?: number;
  hex?: string;
  error?: string;
}

/** 单次哈希的 Worker 数量上限没有额外意义：队列最多并发 3 个文件。 */
function workerSupported(): boolean {
  return typeof Worker !== 'undefined';
}

/**
 * 计算文件 SHA-256。onProgress 为 0..1；signal 触发时终止 Worker。
 * Worker 创建或运行失败会自动回退到主线程，保证低端浏览器仍可秒传。
 */
export function hashFile(
  file: File,
  onProgress: (progress: number) => void,
  signal: AbortSignal,
): Promise<string> {
  if (!workerSupported()) return hashBlob(file, onProgress, signal);

  return new Promise<string>((resolve, reject) => {
    let worker: Worker;
    try {
      worker = new Worker(new URL('./sha256.worker.ts', import.meta.url), { type: 'module' });
    } catch {
      hashBlob(file, onProgress, signal).then(resolve, reject);
      return;
    }

    let settled = false;
    const cleanup = (): void => {
      worker.terminate();
      signal.removeEventListener('abort', onAbort);
    };
    const finish = (): void => {
      settled = true;
      cleanup();
    };
    const onAbort = (): void => {
      if (settled) return;
      finish();
      reject(new DOMException('已取消', 'AbortError'));
    };

    signal.addEventListener('abort', onAbort, { once: true });
    worker.onmessage = (event: MessageEvent<HashMessage>) => {
      const message = event.data;
      if (settled) return;
      if (message.type === 'progress' && typeof message.progress === 'number') {
        onProgress(message.progress);
        return;
      }
      if (message.type === 'done' && typeof message.hex === 'string') {
        finish();
        resolve(message.hex);
        return;
      }
      if (message.type === 'error') {
        finish();
        reject(new Error(message.error ?? '校验失败'));
      }
    };
    worker.onerror = () => {
      if (settled) return;
      finish();
      // Worker 运行期失败（CSP/引擎差异）时退回主线程，避免整个上传失败。
      hashBlob(file, onProgress, signal).then(resolve, reject);
    };
    if (signal.aborted) {
      onAbort();
      return;
    }
    worker.postMessage({ file, chunkSize: 4 * 1024 * 1024 });
  });
}
