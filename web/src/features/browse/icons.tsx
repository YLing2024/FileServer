/** 文件类型线性图标；路径与旧前端 KIND_ICONS 同源，避免两套前端视觉分叉。 */
import type { FileKind } from '../../domain/fileType';

interface KindIconProps {
  kind: FileKind;
  className?: string;
}

const PATHS: Readonly<Record<FileKind, string>> = {
  dir: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z',
  image: 'M3.5 5h17v14h-17zM6.5 9.5a1.6 1.6 0 1 0 0-.01M4 17l4.5-4.5 3.5 3.5 3-3L20 17.5',
  video: 'M3 5.5h13v13H3zM16 10.5l5-3v9l-5-3',
  audio:
    'M9 18V6l10-2v11.5M9 18a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0M19 15.5a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0',
  pdf: 'M6 3h8l4 4v14H6zM14 3v4h4M9.5 12.5h5M9.5 15.5h5M9.5 9.5h2',
  archive: 'M4 8.5V20h16V8.5M3 4.5h18v4H3zM10 12h4M9 15h6',
  text: 'M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 15h6',
  doc: 'M6 3h8l4 4v14H6zM14 3v4h4M9.5 12.5h5M9.5 15.5h5M9.5 9.5h2',
  code: 'M8.5 7.5L4 12l4.5 4.5M15.5 7.5L20 12l-4.5 4.5M13.5 5l-3 14',
  other: 'M7 3.5h7L19.5 9v11H7zM14 3.5V9h5.5',
};

/** 按文件类型渲染 24×24 线性图标；装饰性图标默认 aria-hidden。 */
export function KindIcon({ kind, className }: KindIconProps) {
  return (
    <svg className={className} viewBox="0 0 24 24" aria-hidden="true">
      <path d={PATHS[kind]} />
    </svg>
  );
}
