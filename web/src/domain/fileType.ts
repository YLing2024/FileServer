/** 文件类型判定：与后端 kindExts 同源的扩展名表，缺省回退 other。 */

export type FileKind =
  'dir' | 'image' | 'video' | 'audio' | 'pdf' | 'archive' | 'text' | 'doc' | 'code' | 'other';

const EXT_KIND: Readonly<Record<string, FileKind>> = {
  '.jpg': 'image',
  '.jpeg': 'image',
  '.png': 'image',
  '.gif': 'image',
  '.webp': 'image',
  '.bmp': 'image',
  '.tiff': 'image',
  '.tif': 'image',
  '.svg': 'image',
  '.ico': 'image',
  '.avif': 'image',
  '.jfif': 'image',
  '.mp4': 'video',
  '.mkv': 'video',
  '.mov': 'video',
  '.webm': 'video',
  '.avi': 'video',
  '.wmv': 'video',
  '.flv': 'video',
  '.m4v': 'video',
  '.m2ts': 'video',
  '.3gp': 'video',
  '.rmvb': 'video',
  '.rm': 'video',
  '.mpg': 'video',
  '.mpeg': 'video',
  '.ogv': 'video',
  '.mp3': 'audio',
  '.wav': 'audio',
  '.flac': 'audio',
  '.aac': 'audio',
  '.ogg': 'audio',
  '.m4a': 'audio',
  '.wma': 'audio',
  '.opus': 'audio',
  '.mid': 'audio',
  '.midi': 'audio',
  '.ape': 'audio',
  '.amr': 'audio',
  '.pdf': 'pdf',
  '.zip': 'archive',
  '.rar': 'archive',
  '.7z': 'archive',
  '.tar': 'archive',
  '.gz': 'archive',
  '.bz2': 'archive',
  '.xz': 'archive',
  '.iso': 'archive',
  '.zst': 'archive',
  '.txt': 'text',
  '.md': 'text',
  '.log': 'text',
  '.json': 'text',
  '.xml': 'text',
  '.yaml': 'text',
  '.yml': 'text',
  '.ini': 'text',
  '.conf': 'text',
  '.cfg': 'text',
  '.csv': 'text',
  '.toml': 'text',
  '.srt': 'text',
  '.ass': 'text',
  '.vtt': 'text',
  '.nfo': 'text',
  '.rtf': 'text',
  '.url': 'text',
  '.doc': 'doc',
  '.docx': 'doc',
  '.xls': 'doc',
  '.xlsx': 'doc',
  '.ppt': 'doc',
  '.pptx': 'doc',
  '.odt': 'doc',
  '.ods': 'doc',
  '.odp': 'doc',
  '.wps': 'doc',
  '.c': 'code',
  '.cpp': 'code',
  '.h': 'code',
  '.hpp': 'code',
  '.go': 'code',
  '.rs': 'code',
  '.py': 'code',
  '.js': 'code',
  '.mjs': 'code',
  '.ts': 'code',
  '.tsx': 'code',
  '.jsx': 'code',
  '.html': 'code',
  '.htm': 'code',
  '.css': 'code',
  '.scss': 'code',
  '.java': 'code',
  '.kt': 'code',
  '.swift': 'code',
  '.sh': 'code',
  '.bat': 'code',
  '.cmd': 'code',
  '.ps1': 'code',
  '.sql': 'code',
  '.php': 'code',
  '.rb': 'code',
  '.lua': 'code',
  '.pl': 'code',
  '.vue': 'code',
  '.svelte': 'code',
  '.dockerfile': 'code',
  '.gradle': 'code',
  '.properties': 'code',
};

const KNOWN_KINDS: ReadonlySet<string> = new Set<FileKind>([
  'dir',
  'image',
  'video',
  'audio',
  'pdf',
  'archive',
  'text',
  'doc',
  'code',
  'other',
]);

/** 取扩展名（含点、小写）；无扩展名返回空串。 */
export function extensionOf(name: string): string {
  const dot = name.lastIndexOf('.');
  if (dot <= 0) return '';
  return name.slice(dot).toLowerCase();
}

/** 按名称与是否目录判定类型。 */
export function kindOf(name: string, isDir: boolean): FileKind {
  if (isDir) return 'dir';
  return EXT_KIND[extensionOf(name)] ?? 'other';
}

/**
 * 优先采用服务端 /api/list 返回的 kind（与旧前端同源），
 * 服务端字段异常时回退到本地扩展名表。
 */
export function kindOfEntry(entry: { name: string; is_dir: boolean; kind?: string }): FileKind {
  if (entry.is_dir) return 'dir';
  if (entry.kind && KNOWN_KINDS.has(entry.kind)) return entry.kind as FileKind;
  return kindOf(entry.name, false);
}
