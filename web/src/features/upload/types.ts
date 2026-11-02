/**
 * 上传队列的展示态类型。
 *
 * 队列内部持有 File 与可取消控制器；快照只暴露渲染所需字段，避免组件
 * 直接改内部状态。
 */
export type UploadMode = 'ask' | 'overwrite' | 'rename';

export type UploadPhase = '' | 'hash' | 'instant' | 'upload';

export type UploadStatus =
  | 'waiting'
  | 'hashing'
  | 'instant'
  | 'uploading'
  | 'paused'
  | 'done'
  | 'error'
  | 'canceled'
  | 'skipped';

export interface UploadItemView {
  id: number;
  name: string;
  size: number;
  done: boolean;
  status: UploadStatus;
  phase: UploadPhase;
  progress: number;
  error: string;
  groupRoot: string;
}

export interface UploadGroupView {
  id: number;
  root: string;
  actualRoot: string;
  done: number;
  total: number;
  status: 'waiting' | 'preparing' | 'ready' | 'conflict' | 'error';
}

export interface UploadConflictView {
  kind: 'file' | 'group';
  names: string[];
  /** 展示用：单个文件名为文件名，目录冲突为顶层目录名。 */
  subject: string;
}

export interface UploadSnapshot {
  items: UploadItemView[];
  groups: UploadGroupView[];
  panelOpen: boolean;
  busy: boolean;
  finished: boolean;
  totalProgress: number;
  doneCount: number;
  failedCount: number;
  title: string;
  conflict: UploadConflictView | null;
}

/** 服务端上传能力；由 /api/info 注入，队列不自行请求。 */
export interface UploadSettings {
  /** 分片/秒传/目录预创建仅登录会话可用；关闭时退回单请求。 */
  manage: boolean;
  maxBytes: number;
  chunkThreshold: number;
  chunkMax: number;
  maxFiles: number;
}

/** 收集到的一组待上传文件（文件夹上传带相对路径与空目录）。 */
export interface UploadCollected {
  files: { file: File; relPath: string }[];
  dirs: string[];
}
