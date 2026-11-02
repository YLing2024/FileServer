/**
 * 上传队列（与 React 无关的状态机）。
 *
 * 行为对齐旧前端 app.js：
 * - 普通文件走单请求；登录后所有文件先算 SHA-256 尝试秒传；
 * - 超过阈值走 8 MiB 分片，先查 `/api/upload/status` 跳过已收分片；
 * - 文件夹按顶层目录分组，先创建目录（含空目录）再上传文件；
 * - 同名冲突暂停队列，由界面选择覆盖/自动改名/跳过。
 *
 * 新增并发：最多 3 个文件同时上传；单文件分片仍顺序发送，保证进度与
 * 断点语义简单可靠。
 */
import { ApiError } from '../../api/errors';
import {
  completeUpload,
  fetchUploadStatus,
  instantUpload,
  mkdirUpload,
  uploadChunk,
  uploadSmall,
  type UploadedFile,
} from '../../api/upload';
import { formatBytes } from '../../domain/format';
import { joinPath, normalizePath } from '../../domain/path';
import { hashFile } from './hashFile';
import { chunkLength, computeUploadId } from './uploadId';
import { groupSubdirs, splitRelPath, validNamePart, validRelPath } from './uploadName';
import type {
  UploadCollected,
  UploadConflictView,
  UploadGroupView,
  UploadItemView,
  UploadMode,
  UploadPhase,
  UploadSettings,
  UploadSnapshot,
  UploadStatus,
} from './types';

const CONCURRENCY = 3;
const CHUNK_RETRIES = 2;
const AUTO_CLOSE_MS = 1800;
const PAINT_INTERVAL_MS = 80;

interface QueueOptions {
  getSettings: () => UploadSettings;
  onFinished: (target: string, names: string[]) => void;
  onToast: (message: string, tone?: 'error') => void;
  onUnauthorized: () => void;
}

interface Item {
  id: number;
  file: File;
  name: string;
  relPath: string;
  size: number;
  target: string;
  dir: string;
  groupId: number | null;
  status: UploadStatus;
  phase: UploadPhase;
  progress: number;
  error: string;
  sha256: string;
  controller: AbortController | null;
}

interface Group {
  id: number;
  root: string;
  actualRoot: string;
  target: string;
  dirs: string[];
  itemIds: number[];
  mode: UploadMode | '';
  prepared: boolean;
  preparing: Promise<void> | null;
  status: UploadGroupView['status'];
  controller: AbortController | null;
}

interface Batch {
  target: string;
  mode: UploadMode;
  seq: number;
  closed: boolean;
  finished: boolean;
  autoClose: ReturnType<typeof setTimeout> | null;
}

interface ConflictState {
  kind: 'file' | 'group';
  itemId?: number;
  groupId?: number;
  names: string[];
}

function abortError(): DOMException {
  return new DOMException('已取消', 'AbortError');
}

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError';
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(abortError());
      return;
    }
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = (): void => {
      clearTimeout(timer);
      reject(abortError());
    };
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

/** 上传失败文案：413/403/409 与网络错误分别说明，其余用服务端文案。 */
export function uploadErrorMessage(error: unknown, maxBytes: number): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return '登录状态已失效，请重新登录';
    if (error.status === 403) return error.message || '没有上传权限（开关关闭或目录只读）';
    if (error.status === 413) return `超过大小上限 ${formatBytes(maxBytes)}`;
    if (error.status === 409) return error.message || '同名文件已存在';
    if (error.status === 400) return error.message || '文件或路径不合法';
    if (error.status === 0) return '网络错误，请检查连接';
    return error.message || `上传失败（HTTP ${error.status}）`;
  }
  if (error instanceof Error && error.message) return error.message;
  return '上传失败';
}

export class UploadQueue {
  private options: QueueOptions;
  private readonly listeners = new Set<() => void>();
  private items: Item[] = [];
  private groups: Group[] = [];
  private batch: Batch | null = null;
  private conflictState: ConflictState | null = null;
  private readonly running = new Set<number>();
  private preparing = 0;
  private destroyed = false;
  private lastPaint = 0;
  private snapshot: UploadSnapshot;

  constructor(options?: QueueOptions) {
    this.options = options ?? {
      getSettings: () => ({
        manage: false,
        maxBytes: 1024 * 1024 * 1024,
        chunkThreshold: 8 * 1024 * 1024,
        chunkMax: 64 * 1024 * 1024,
        maxFiles: 2000,
      }),
      onFinished: () => undefined,
      onToast: () => undefined,
      onUnauthorized: () => undefined,
    };
    this.snapshot = this.buildSnapshot();
  }

  /** 注入最新回调与能力（组件每次渲染后更新，避免闭包读到旧值）。 */
  configure(options: QueueOptions): void {
    this.options = options;
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = (): UploadSnapshot => this.snapshot;

  /** 是否还有未完成（含暂停/冲突）的工作；用于离开页面确认与关闭按钮。 */
  isBusy(): boolean {
    return this.snapshot.busy;
  }

  enqueuePlain(files: File[], target: string): void {
    const collected: UploadCollected = {
      files: files.map((file) => ({ file, relPath: file.name })),
      dirs: [],
    };
    this.enqueue(collected, target);
  }

  enqueueFolder(files: File[], target: string): void {
    const collected: UploadCollected = {
      files: files.map((file) => ({
        file,
        relPath: file.webkitRelativePath || file.name,
      })),
      dirs: [],
    };
    this.enqueue(collected, target);
  }

  enqueue(collected: UploadCollected, target: string): void {
    const settings = this.options.getSettings();
    const files = collected.files.filter((entry) => entry.file && entry.file.name);
    const dirs = collected.dirs.map((dir) => normalizePath(dir)).filter(Boolean);
    if (!files.length && !dirs.length) return;
    if (files.length > settings.maxFiles) {
      this.options.onToast(`单次最多上传 ${settings.maxFiles} 个文件`, 'error');
      return;
    }
    const folderUpload = files.some((entry) => splitRelPath(entry.relPath).length > 1);
    if (folderUpload && !settings.manage) {
      this.options.onToast('文件夹上传需要登录后使用', 'error');
      return;
    }
    if (this.isBusy() && this.batch && this.batch.target !== target) {
      this.options.onToast('请等待当前上传完成', 'error');
      return;
    }
    if (!this.batch || this.batch.closed || this.batch.finished) this.reset(target);

    const groupByRoot = new Map<string, Group>();
    const groupItems = new Map<number, Item[]>();
    const plain: Item[] = [];

    const ensureGroup = (root: string): Group => {
      const existing = groupByRoot.get(root);
      if (existing) return existing;
      const group: Group = {
        id: this.nextId(),
        root,
        actualRoot: root,
        target,
        dirs: [root],
        itemIds: [],
        mode: '',
        prepared: false,
        preparing: null,
        status: 'waiting',
        controller: null,
      };
      groupByRoot.set(root, group);
      this.groups.push(group);
      groupItems.set(group.id, []);
      return group;
    };

    for (const entry of files) {
      const relPath = normalizePath(entry.relPath || entry.file.name);
      const parts = splitRelPath(relPath);
      const baseName = parts[parts.length - 1] ?? entry.file.name;
      const group = parts.length > 1 ? ensureGroup(parts[0] ?? baseName) : null;
      const item: Item = {
        id: this.nextId(),
        file: entry.file,
        name: baseName,
        relPath,
        size: entry.file.size,
        target,
        dir: target,
        groupId: group?.id ?? null,
        status: 'waiting',
        phase: 'hash',
        progress: 0,
        error: '',
        sha256: '',
        controller: null,
      };
      if (group) {
        group.itemIds.push(item.id);
        for (const sub of groupSubdirs(relPath)) {
          if (!group.dirs.includes(sub)) group.dirs.push(sub);
        }
        groupItems.get(group.id)?.push(item);
      } else {
        plain.push(item);
      }
      if (!validNamePart(baseName) || !validRelPath(relPath)) {
        item.status = 'error';
        item.error = '名称不合法（路径、隐藏名或保留名）';
      } else if (item.size > settings.maxBytes) {
        item.status = 'error';
        item.error = `超过上限 ${formatBytes(settings.maxBytes)}`;
      }
    }

    // 空目录：只用于创建目录结构，不产生上传条目。
    for (const dir of dirs) {
      const parts = splitRelPath(dir);
      if (!parts.length || !validRelPath(dir)) continue;
      const group = ensureGroup(parts[0] ?? dir);
      for (let i = 1; i <= parts.length; i += 1) {
        const sub = parts.slice(0, i).join('/');
        if (!group.dirs.includes(sub)) group.dirs.push(sub);
      }
    }

    // 先按顶层目录展开，再追加普通文件：面板里同一目录的文件保持连续。
    for (const group of this.groups) {
      this.items.push(...(groupItems.get(group.id) ?? []));
    }
    this.items.push(...plain);

    if (!this.items.length) {
      this.options.onToast('没有可上传的文件', 'error');
      return;
    }
    this.paint(true);
    this.pump();
  }

  /** 关闭队列面板；上传中不允许关闭。 */
  close(): void {
    if (this.isBusy()) return;
    if (this.batch?.autoClose) clearTimeout(this.batch.autoClose);
    this.items = [];
    this.groups = [];
    this.batch = null;
    this.conflictState = null;
    this.running.clear();
    this.preparing = 0;
    this.snapshot = this.buildSnapshot();
    this.emit();
  }

  pauseItem(id: number): void {
    const item = this.item(id);
    if (!item) return;
    if (!['waiting', 'hashing', 'instant', 'uploading'].includes(item.status)) return;
    item.status = 'paused';
    item.error = '已暂停';
    item.progress = 0;
    item.controller?.abort();
    item.controller = null;
    this.paint(true);
    this.pump();
  }

  resumeItem(id: number): void {
    const item = this.item(id);
    if (!item || item.status !== 'paused') return;
    item.status = 'waiting';
    item.error = '';
    item.progress = 0;
    this.paint(true);
    this.pump();
  }

  cancelItem(id: number): void {
    const item = this.item(id);
    if (!item) return;
    if (['done', 'error', 'canceled', 'skipped'].includes(item.status)) return;
    item.status = 'canceled';
    item.error = '已取消';
    item.progress = 0;
    item.controller?.abort();
    item.controller = null;
    this.paint(true);
    this.pump();
  }

  retryItem(id: number): void {
    const item = this.item(id);
    if (!item) return;
    if (!['error', 'canceled', 'skipped'].includes(item.status)) return;
    item.status = 'waiting';
    item.error = '';
    item.progress = 0;
    item.phase = 'hash';
    this.paint(true);
    this.pump();
  }

  /** 取消所有未完成项（含等待与暂停）。 */
  cancelAll(): void {
    for (const item of this.items) {
      if (['done', 'error', 'canceled', 'skipped'].includes(item.status)) continue;
      item.status = 'canceled';
      item.error = '已取消';
      item.progress = 0;
      item.controller?.abort();
      item.controller = null;
    }
    for (const group of this.groups) {
      group.controller?.abort();
      group.controller = null;
    }
    this.conflictState = null;
    this.paint(true);
    this.pump();
  }

  /** 冲突对话框选择：覆盖/自动改名对整个批次生效；跳过只跳当前项/目录。 */
  resolveConflict(choice: 'overwrite' | 'rename' | 'skip'): void {
    const conflict = this.conflictState;
    if (!conflict || !this.batch) return;
    if (choice === 'skip') {
      if (conflict.kind === 'file' && conflict.itemId !== undefined) {
        const item = this.item(conflict.itemId);
        if (item) {
          item.status = 'skipped';
          item.error = '已跳过（同名）';
          item.progress = 0;
        }
      } else if (conflict.groupId !== undefined) {
        const group = this.group(conflict.groupId);
        if (group) {
          for (const id of group.itemIds) {
            const item = this.item(id);
            if (item && ['waiting', 'paused'].includes(item.status)) {
              item.status = 'skipped';
              item.error = '已跳过（同名目录）';
            }
          }
          group.status = 'error';
        }
      }
    } else {
      this.batch.mode = choice;
      if (conflict.kind === 'group' && conflict.groupId !== undefined) {
        const group = this.group(conflict.groupId);
        if (group) {
          group.mode = choice;
          group.prepared = false;
          group.actualRoot = group.root;
          group.status = 'waiting';
        }
      }
      if (conflict.kind === 'file' && conflict.itemId !== undefined) {
        const item = this.item(conflict.itemId);
        if (item) {
          item.status = 'waiting';
          item.error = '';
          item.progress = 0;
          item.phase = 'hash';
        }
      }
    }
    this.conflictState = null;
    this.paint(true);
    this.pump();
  }

  destroy(): void {
    this.destroyed = true;
    if (this.batch?.autoClose) clearTimeout(this.batch.autoClose);
    for (const item of this.items) item.controller?.abort();
    for (const group of this.groups) group.controller?.abort();
    this.listeners.clear();
  }

  private nextId(): number {
    if (!this.batch) throw new Error('上传批次未初始化');
    this.batch.seq += 1;
    return this.batch.seq;
  }

  private reset(target: string): void {
    if (this.batch?.autoClose) clearTimeout(this.batch.autoClose);
    this.items = [];
    this.groups = [];
    this.running.clear();
    this.preparing = 0;
    this.conflictState = null;
    this.batch = {
      target,
      mode: 'ask',
      seq: 0,
      closed: false,
      finished: false,
      autoClose: null,
    };
  }

  private item(id: number): Item | undefined {
    return this.items.find((item) => item.id === id);
  }

  private group(id: number): Group | undefined {
    return this.groups.find((group) => group.id === id);
  }

  private itemGroup(item: Item): Group | undefined {
    return item.groupId === null ? undefined : this.group(item.groupId);
  }

  private groupReady(item: Item): boolean {
    const group = this.itemGroup(item);
    return !group || group.prepared;
  }

  private groupCanPrepare(group: Group): boolean {
    if (group.prepared || group.preparing) return false;
    if (group.status === 'error' || group.status === 'conflict') return false;
    const waiting = group.itemIds.some((id) => {
      const item = this.item(id);
      return item?.status === 'waiting';
    });
    return waiting || group.itemIds.length === 0;
  }

  private pump(): void {
    if (this.destroyed) return;
    const batch = this.batch;
    if (!batch || batch.closed || this.conflictState) {
      this.paint();
      return;
    }

    while (this.running.size < CONCURRENCY) {
      const next = this.items.find(
        (item) =>
          item.status === 'waiting' && !this.running.has(item.id) && this.groupReady(item),
      );
      if (!next) break;
      this.start(next);
    }

    if (this.running.size < CONCURRENCY && this.preparing === 0) {
      const group = this.groups.find((candidate) => this.groupCanPrepare(candidate));
      if (group) {
        void this.prepareGroup(group)
          .catch(() => undefined)
          .finally(() => this.pump());
      }
    }

    const pending = this.items.some((item) =>
      ['waiting', 'hashing', 'instant', 'uploading', 'paused'].includes(item.status),
    );
    if (!pending && this.running.size === 0 && this.preparing === 0) {
      this.finish();
      return;
    }
    this.paint();
  }

  private start(item: Item): void {
    this.running.add(item.id);
    void this.runItem(item)
      .catch(() => undefined)
      .finally(() => {
        this.running.delete(item.id);
        this.paint(true);
        this.pump();
      });
  }

  private async runItem(item: Item): Promise<void> {
    const settings = this.options.getSettings();
    const controller = new AbortController();
    item.controller = controller;
    const { signal } = controller;
    try {
      if (settings.manage) {
        item.status = 'hashing';
        item.phase = 'hash';
        item.progress = 0;
        this.paint(true);
        if (!item.sha256) {
          item.sha256 = await hashFile(
            item.file,
            (progress) => this.setProgress(item, progress * 100),
            signal,
          );
        }
        if (signal.aborted) throw abortError();

        item.status = 'instant';
        item.phase = 'instant';
        item.progress = 0;
        this.paint(true);
        const instant = await instantUpload(
          item.sha256,
          item.name,
          item.dir,
          item.size,
          this.mode(),
          signal,
        );
        if (instant.instant) {
          this.applyNames(item, instant.uploaded);
          this.markDone(item);
          return;
        }
        if (signal.aborted) throw abortError();
      }

      item.status = 'uploading';
      item.phase = 'upload';
      item.progress = 0;
      this.paint(true);
      if (settings.manage && item.size > settings.chunkThreshold) {
        await this.chunkedUpload(item, settings, signal);
      } else {
        await this.smallUpload(item, signal);
      }
    } catch (error) {
      if (item.status === 'paused' || item.status === 'canceled') return;
      if (isAbort(error)) {
        item.status = 'canceled';
        item.error = '已取消';
        return;
      }
      if (error instanceof ApiError && error.status === 409 && error.code === 'conflict') {
        this.raiseConflict(item, error.existing);
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        this.options.onUnauthorized();
      }
      item.status = 'error';
      item.error = uploadErrorMessage(error, settings.maxBytes);
    } finally {
      if (item.controller === controller) item.controller = null;
    }
  }

  private mode(): UploadMode {
    return this.batch?.mode ?? 'ask';
  }

  private applyNames(item: Item, uploaded?: UploadedFile[]): void {
    const name = uploaded?.[0]?.name;
    if (name) item.name = name;
  }

  private markDone(item: Item): void {
    item.status = 'done';
    item.phase = '';
    item.progress = 100;
    item.error = '';
    this.paint(true);
  }

  private raiseConflict(item: Item, existing: readonly string[]): void {
    item.status = 'waiting';
    item.phase = '';
    item.progress = 0;
    this.conflictState = {
      kind: 'file',
      itemId: item.id,
      names: existing.length ? [...existing] : [item.name],
    };
    this.paint(true);
  }

  private async smallUpload(item: Item, signal: AbortSignal): Promise<void> {
    const result = await uploadSmall(
      item.file,
      item.name,
      item.dir,
      this.mode(),
      item.sha256,
      (loaded, total) => {
        if (total > 0) this.setProgress(item, Math.min(99, (loaded / total) * 100));
      },
      signal,
    );
    if (result.kind === 'conflict') {
      throw new ApiError(409, result.message, 'conflict', result.existing);
    }
    if (signal.aborted) throw abortError();
    this.applyNames(item, result.uploaded);
    this.markDone(item);
  }

  private async chunkedUpload(item: Item, settings: UploadSettings, signal: AbortSignal): Promise<void> {
    const chunkSize = Math.max(
      1,
      Math.min(settings.chunkThreshold || 8 * 1024 * 1024, settings.chunkMax || 64 * 1024 * 1024),
    );
    const total = Math.max(1, Math.ceil(item.size / chunkSize));
    const uploadId = await computeUploadId(item.file, item.name);
    if (signal.aborted) throw abortError();

    const status = await fetchUploadStatus(uploadId, item.dir, item.name, item.size, signal);
    const received = new Set(status.received ?? []);
    let sentBefore = 0;
    for (let index = 0; index < total; index += 1) {
      if (received.has(index)) sentBefore += chunkLength(item.size, total, index, chunkSize);
    }
    if (sentBefore > 0) this.setProgress(item, Math.min(99, (sentBefore / item.size) * 100));

    for (let index = 0; index < total; index += 1) {
      if (received.has(index)) continue;
      if (signal.aborted) throw abortError();
      const start = index * chunkSize;
      const length = chunkLength(item.size, total, index, chunkSize);
      const blob = item.file.slice(start, start + length);
      let attempt = 0;
      for (;;) {
        try {
          await uploadChunk(
            uploadId,
            index,
            total,
            item.size,
            item.name,
            item.dir,
            blob,
            (loaded) => {
              const bytes = sentBefore + loaded;
              this.setProgress(item, Math.min(99, (bytes / item.size) * 100));
            },
            signal,
          );
          break;
        } catch (error) {
          const retryable =
            error instanceof ApiError && (error.status === 0 || error.status >= 500);
          if (!retryable || attempt >= CHUNK_RETRIES || signal.aborted) throw error;
          attempt += 1;
          await delay(400 * attempt, signal);
        }
      }
      sentBefore += length;
      this.setProgress(item, Math.min(99, (sentBefore / item.size) * 100), true);
    }

    const complete = await completeUpload(
      uploadId,
      item.dir,
      item.name,
      this.mode(),
      item.sha256,
      signal,
    );
    if (signal.aborted) throw abortError();
    this.applyNames(item, complete.uploaded);
    this.markDone(item);
  }

  private async prepareGroup(group: Group): Promise<void> {
    if (group.prepared || group.preparing) return group.preparing ?? Promise.resolve();
    this.preparing += 1;
    const controller = new AbortController();
    group.controller = controller;
    const task = (async () => {
      group.status = 'preparing';
      this.paint(true);
      const mode = group.mode || this.mode();
      const root = await mkdirUpload(group.target, group.root, mode, controller.signal);
      group.actualRoot = root.name || group.root;

      const sorted = [...group.dirs].sort((a, b) => {
        const depthA = splitRelPath(a).length;
        const depthB = splitRelPath(b).length;
        return depthA === depthB ? a.localeCompare(b) : depthA - depthB;
      });
      for (const dir of sorted) {
        if (dir === group.root) continue;
        const parts = splitRelPath(dir);
        const parent = joinPath(
          joinPath(group.target, group.actualRoot),
          parts.slice(1, -1).join('/'),
        );
        await mkdirUpload(parent, parts[parts.length - 1] ?? dir, 'overwrite', controller.signal);
      }

      for (const id of group.itemIds) {
        const item = this.item(id);
        if (!item) continue;
        const parts = splitRelPath(item.relPath);
        item.dir = joinPath(joinPath(group.target, group.actualRoot), parts.slice(1, -1).join('/'));
      }
      group.prepared = true;
      group.status = 'ready';
      this.paint(true);
    })();

    group.preparing = task;
    try {
      await task;
    } catch (error) {
      if (isAbort(error)) {
        group.status = 'error';
        return;
      }
      if (error instanceof ApiError && error.status === 409 && error.code === 'conflict' && mode(group) === 'ask') {
        group.status = 'conflict';
        group.prepared = false;
        this.conflictState = {
          kind: 'group',
          groupId: group.id,
          names: error.existing.length ? [...error.existing] : [group.root],
        };
        this.paint(true);
        return;
      }
      group.status = 'error';
      for (const id of group.itemIds) {
        const item = this.item(id);
        if (!item || !['waiting', 'paused'].includes(item.status)) continue;
        item.status = 'error';
        item.error = uploadErrorMessage(error, this.options.getSettings().maxBytes);
      }
      this.options.onToast(error instanceof Error ? error.message : '无法创建文件夹', 'error');
      this.paint(true);
    } finally {
      group.preparing = null;
      group.controller = null;
      this.preparing = Math.max(0, this.preparing - 1);
    }
  }

  private setProgress(item: Item, progress: number, force = false): void {
    item.progress = Math.max(0, Math.min(100, progress));
    this.paint(force);
  }

  private finish(): void {
    const batch = this.batch;
    if (!batch || batch.finished) {
      this.paint();
      return;
    }
    batch.finished = true;
    const names = new Set<string>();
    for (const item of this.items) {
      if (item.status !== 'done') continue;
      names.add(item.name);
      const group = this.itemGroup(item);
      if (group) names.add(group.actualRoot || group.root);
    }
    // 只有空目录的分组没有 done 条目，但目录确实创建了，同样需要刷新。
    for (const group of this.groups) {
      if (group.prepared) names.add(group.actualRoot || group.root);
    }
    if (names.size) this.options.onFinished(batch.target, [...names]);
    this.paint(true);
    batch.autoClose = setTimeout(() => {
      if (this.batch === batch) this.close();
    }, AUTO_CLOSE_MS);
  }

  private paint(force = false): void {
    const now = Date.now();
    if (!force && now - this.lastPaint < PAINT_INTERVAL_MS) return;
    this.lastPaint = now;
    this.snapshot = this.buildSnapshot();
    this.emit();
  }

  private emit(): void {
    for (const listener of this.listeners) listener();
  }

  private buildSnapshot(): UploadSnapshot {
    const items: UploadItemView[] = this.items.map((item) => {
      const group = this.itemGroup(item);
      return {
        id: item.id,
        name: item.name,
        size: item.size,
        done: item.status === 'done',
        status: item.status,
        phase: item.phase,
        progress: item.status === 'done' ? 100 : item.progress,
        error: item.error,
        groupRoot: group?.actualRoot ?? group?.root ?? '',
      };
    });
    const groups: UploadGroupView[] = this.groups.map((group) => {
      const itemsOfGroup = group.itemIds
        .map((id) => this.item(id))
        .filter((item): item is Item => item !== undefined);
      return {
        id: group.id,
        root: group.root,
        actualRoot: group.actualRoot,
        done: itemsOfGroup.filter((item) => item.status === 'done').length,
        total: itemsOfGroup.length,
        status: group.status,
      };
    });
    const bytes = this.items.reduce((sum, item) => sum + item.size, 0);
    const sent = this.items.reduce(
      (sum, item) =>
        sum + item.size * (item.status === 'done' ? 100 : Math.max(0, item.progress)) / 100,
      0,
    );
    const doneCount = this.items.filter((item) => item.status === 'done').length;
    const failedCount = this.items.filter((item) => item.status === 'error').length;
    const busy =
      this.preparing > 0 ||
      this.conflictState !== null ||
      this.items.some((item) =>
        ['waiting', 'hashing', 'instant', 'uploading', 'paused'].includes(item.status),
      );
    const finished = Boolean(this.batch?.finished);
    return {
      items,
      groups,
      panelOpen: Boolean(this.batch && !this.batch.closed),
      busy,
      finished,
      totalProgress: bytes > 0 ? Math.round((sent / bytes) * 100) : finished ? 100 : 0,
      doneCount,
      failedCount,
      title: this.title(doneCount, failedCount, busy),
      conflict: this.conflictState ? this.conflictView(this.conflictState) : null,
    };
  }

  private title(done: number, failed: number, busy: boolean): string {
    const total = this.items.length;
    if (!total) return '上传';
    const paused = this.items.some((item) => item.status === 'paused');
    if (busy && paused && !this.running.size && this.preparing === 0) return '上传已暂停';
    if (busy) return `上传中 ${done}/${total}`;
    if (failed) return `上传结束：${done} 成功，${failed} 失败`;
    return done === total ? `上传完成（${done}）` : '上传结束';
  }

  private conflictView(state: ConflictState): UploadConflictView {
    if (state.kind === 'group') {
      const group = state.groupId === undefined ? undefined : this.group(state.groupId);
      return {
        kind: 'group',
        names: state.names,
        subject: group?.root ?? '文件夹',
      };
    }
    const item = state.itemId === undefined ? undefined : this.item(state.itemId);
    return { kind: 'file', names: state.names, subject: item?.name ?? '文件' };
  }
}

/** 分组当前模式：用于判断 409 是否需要询问。 */
function mode(group: Group): UploadMode {
  return group.mode || 'ask';
}
