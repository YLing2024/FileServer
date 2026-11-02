/**
 * React 绑定：把 UploadQueue 状态机接到 useSyncExternalStore。
 *
 * 队列只在组件首次渲染时创建；设置与回调通过 ref 读取最新值，避免
 * 每次 render 重建队列丢失上传进度。
 */
import { useEffect, useState, useSyncExternalStore } from 'react';
import { UploadQueue } from './uploadQueue';
import type { UploadSettings, UploadSnapshot } from './types';

interface UseUploadQueueOptions {
  settings: UploadSettings;
  onFinished: (target: string, names: string[]) => void;
  onToast: (message: string, tone?: 'error') => void;
  onUnauthorized: () => void;
}

export interface UploadQueueApi {
  snapshot: UploadSnapshot;
  enqueuePlain: (files: File[], target: string) => void;
  enqueueFolder: (files: File[], target: string) => void;
  enqueue: (collected: import('./types').UploadCollected, target: string) => void;
  close: () => void;
  pauseItem: (id: number) => void;
  resumeItem: (id: number) => void;
  cancelItem: (id: number) => void;
  retryItem: (id: number) => void;
  cancelAll: () => void;
  resolveConflict: (choice: 'overwrite' | 'rename' | 'skip') => void;
}

export function useUploadQueue(options: UseUploadQueueOptions): UploadQueueApi {
  const [queue] = useState(() => new UploadQueue());

  useEffect(() => {
    queue.configure({
      getSettings: () => options.settings,
      onFinished: (target, names) => options.onFinished(target, names),
      onToast: (message, tone) => options.onToast(message, tone),
      onUnauthorized: () => options.onUnauthorized(),
    });
  }, [queue, options]);

  const snapshot = useSyncExternalStore(queue.subscribe, queue.getSnapshot, queue.getSnapshot);

  useEffect(() => {
    if (!snapshot.busy) return;
    const onBeforeUnload = (event: BeforeUnloadEvent): void => {
      event.preventDefault();
      // 浏览器只显示通用文案，不能自定义。
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [snapshot.busy]);

  return {
    snapshot,
    enqueuePlain: (files, target) => queue.enqueuePlain(files, target),
    enqueueFolder: (files, target) => queue.enqueueFolder(files, target),
    enqueue: (collected, target) => queue.enqueue(collected, target),
    close: () => queue.close(),
    pauseItem: (id) => queue.pauseItem(id),
    resumeItem: (id) => queue.resumeItem(id),
    cancelItem: (id) => queue.cancelItem(id),
    retryItem: (id) => queue.retryItem(id),
    cancelAll: () => queue.cancelAll(),
    resolveConflict: (choice) => queue.resolveConflict(choice),
  };
}
