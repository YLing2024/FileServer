/**
 * 整页拖拽上传与粘贴上传。
 *
 * 只在 `enabled` 时监听；拖拽用 dragDepth 计数避免子元素 dragleave 抖动。
 * 拖入目录时优先用 `webkitGetAsEntry` 递归读取，读不到再回退普通文件。
 */
import { useEffect, useRef, useState } from 'react';
import { collectClipboardFiles, collectDropEntries } from './collect';
import type { UploadCollected } from './types';

interface PageDropOptions {
  enabled: boolean;
  onCollected: (collected: UploadCollected) => void;
  onError: (message: string) => void;
}

function hasFiles(event: DragEvent): boolean {
  const types = event.dataTransfer?.types;
  if (!types) return false;
  return Array.from(types).includes('Files');
}

/** 返回 dragging 是否显示投放遮罩；同时负责 drop 数据读取。 */
export function usePageDrop({ enabled, onCollected, onError }: PageDropOptions): boolean {
  const [dragging, setDragging] = useState(false);
  const depth = useRef(0);
  const callbacks = useRef({ onCollected, onError });

  useEffect(() => {
    callbacks.current = { onCollected, onError };
  }, [onCollected, onError]);

  useEffect(() => {
    if (!enabled) {
      depth.current = 0;
      return;
    }
    const reset = (): void => {
      depth.current = 0;
      setDragging(false);
    };
    const onDragEnter = (event: DragEvent): void => {
      if (!hasFiles(event)) return;
      event.preventDefault();
      depth.current += 1;
      setDragging(true);
    };
    const onDragOver = (event: DragEvent): void => {
      if (!hasFiles(event)) return;
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    };
    const onDragLeave = (event: DragEvent): void => {
      if (!enabled) return;
      if (!hasFiles(event)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setDragging(false);
    };
    const onDrop = (event: DragEvent): void => {
      if (!hasFiles(event)) {
        reset();
        return;
      }
      event.preventDefault();
      reset();
      const data = event.dataTransfer;
      if (!data) return;
      collectDropEntries(data)
        .then((collected) => {
          if (collected) callbacks.current.onCollected(collected);
        })
        .catch((error: unknown) => {
          callbacks.current.onError(error instanceof Error ? error.message : '读取拖入内容失败');
        });
    };

    window.addEventListener('dragenter', onDragEnter);
    window.addEventListener('dragover', onDragOver);
    window.addEventListener('dragleave', onDragLeave);
    window.addEventListener('drop', onDrop);
    window.addEventListener('blur', reset);
    window.addEventListener('dragend', reset);
    return () => {
      window.removeEventListener('dragenter', onDragEnter);
      window.removeEventListener('dragover', onDragOver);
      window.removeEventListener('dragleave', onDragLeave);
      window.removeEventListener('drop', onDrop);
      window.removeEventListener('blur', reset);
      window.removeEventListener('dragend', reset);
    };
  }, [enabled]);

  return enabled && dragging;
}

/** 粘贴上传：焦点在输入框/可编辑区域时不接管，避免影响正常粘贴文本。 */
export function usePasteUpload(enabled: boolean, onFiles: (files: File[]) => void): void {
  const callback = useRef(onFiles);

  useEffect(() => {
    callback.current = onFiles;
  }, [onFiles]);

  useEffect(() => {
    if (!enabled) return;
    const onPaste = (event: ClipboardEvent): void => {
      const target = event.target;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        (target instanceof HTMLElement && target.isContentEditable)
      ) {
        return;
      }
      if (!event.clipboardData) return;
      const files = collectClipboardFiles(event.clipboardData);
      if (!files.length) return;
      event.preventDefault();
      callback.current(files);
    };
    document.addEventListener('paste', onPaste);
    return () => document.removeEventListener('paste', onPaste);
  }, [enabled]);
}
