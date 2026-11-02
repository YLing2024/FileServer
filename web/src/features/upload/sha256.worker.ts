/**
 * 大文件 SHA-256 Worker：按 4 MiB 流式读取，避免占用主线程。
 *
 * 主线程通过 `hashFile.ts` 创建本 Worker，并在取消/完成后 terminate；
 * 这里只负责单次计算，不缓存跨文件状态。
 */
import { hashBlob } from './sha256';

interface HashRequest {
  file: File;
  chunkSize?: number;
}

interface WorkerScope {
  onmessage: ((event: MessageEvent<HashRequest>) => void) | null;
  postMessage: (message: unknown) => void;
}

const scope = self as unknown as WorkerScope;

scope.onmessage = async (event) => {
  const { file, chunkSize } = event.data;
  try {
    const hex = await hashBlob(
      file,
      (progress) => {
        scope.postMessage({ type: 'progress', progress });
      },
      undefined,
      chunkSize,
    );
    scope.postMessage({ type: 'done', hex });
  } catch (error) {
    const message = error instanceof Error ? error.message : '校验失败';
    scope.postMessage({ type: 'error', error: message });
  }
};
