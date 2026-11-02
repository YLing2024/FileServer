/**
 * 从文件选择器、整页拖拽与粘贴事件收集待上传内容。
 *
 * 文件夹必须保留相对路径；拖拽还通过 `webkitGetAsEntry` 递归读取目录，
 * 因此空目录也能被创建。读取失败时抛出可展示的中文错误。
 */
import { normalizePath } from '../../domain/path';
import type { UploadCollected } from './types';

const MAX_FILES = 2000;

/** 判断环境是否支持 `webkitdirectory`（不支持时隐藏“上传文件夹”入口）。 */
export function supportsDirectoryPicker(): boolean {
  const input = document.createElement('input');
  return 'webkitdirectory' in input;
}

/** 普通文件选择/粘贴：全部平铺到当前目录。 */
export function collectPlainFiles(files: FileList | File[]): UploadCollected {
  return {
    files: Array.from(files)
      .filter((file) => file.name)
      .map((file) => ({ file, relPath: file.name })),
    dirs: [],
  };
}

/** `webkitdirectory` 选择器：用 webkitRelativePath 还原目录结构。 */
export function collectFolderFiles(files: FileList | File[]): UploadCollected {
  const dirs = new Set<string>();
  const collected: UploadCollected['files'] = [];
  for (const file of Array.from(files)) {
    if (!file.name) continue;
    const relPath = normalizePath(file.webkitRelativePath || file.name);
    const parts = relPath.split('/').filter(Boolean);
    for (let i = 1; i < parts.length; i += 1) dirs.add(parts.slice(0, i).join('/'));
    collected.push({ file, relPath });
  }
  return { files: collected, dirs: [...dirs] };
}

function readAllEntries(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => {
    const all: FileSystemEntry[] = [];
    const next = (): void => {
      reader.readEntries((batch) => {
        if (!batch.length) {
          resolve(all);
          return;
        }
        all.push(...batch);
        next();
      }, reject);
    };
    next();
  });
}

async function walkEntry(
  entry: FileSystemEntry,
  prefix: string,
  out: UploadCollected,
): Promise<void> {
  const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
  if (entry.isFile) {
    if (out.files.length >= MAX_FILES) throw new Error(`单次最多上传 ${MAX_FILES} 个文件`);
    const fileEntry = entry as FileSystemFileEntry;
    const file = await new Promise<File>((resolve, reject) => fileEntry.file(resolve, reject));
    out.files.push({ file, relPath: rel });
    return;
  }
  if (entry.isDirectory) {
    out.dirs.push(rel);
    const directory = entry as FileSystemDirectoryEntry;
    const children = await readAllEntries(directory.createReader());
    for (const child of children) await walkEntry(child, rel, out);
  }
}

/**
 * 读取拖拽内容；浏览器不支持 entries 时回退到 `dataTransfer.files`。
 * 返回 null 表示拖拽中没有文件。
 */
export async function collectDropEntries(data: DataTransfer): Promise<UploadCollected | null> {
  const entries: FileSystemEntry[] = [];
  for (const item of Array.from(data.items ?? [])) {
    if (item.kind !== 'file' || typeof item.webkitGetAsEntry !== 'function') continue;
    const entry = item.webkitGetAsEntry();
    if (entry) entries.push(entry);
  }
  if (!entries.length) {
    if (data.files.length) return collectPlainFiles(data.files);
    return null;
  }
  const out: UploadCollected = { files: [], dirs: [] };
  for (const entry of entries) await walkEntry(entry, '', out);
  return out;
}

/** 粘贴事件里提取文件；没有文件时返回空数组。 */
export function collectClipboardFiles(data: DataTransfer): File[] {
  const files: File[] = [];
  for (const item of Array.from(data.items ?? [])) {
    if (item.kind !== 'file') continue;
    const file = item.getAsFile();
    if (file) files.push(file);
  }
  if (files.length) return files;
  return Array.from(data.files);
}
