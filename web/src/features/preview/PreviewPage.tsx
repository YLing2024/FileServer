/**
 * 预览页：按文件类型分发到灯箱/播放器/PDF/文本，并提供统一返回与下载。
 *
 * 预览目标由 `?view=` 驱动；Esc 或返回按钮回到浏览视图，滚动位置由 BrowsePage 恢复。
 */
import { useEffect } from 'react';
import { downloadFile } from '../../api/media';
import { useToast } from '../../components/Toast';
import type { FileKind } from '../../domain/fileType';
import { formatBytes, formatTime } from '../../domain/format';
import { AudioPlayer } from './AudioPlayer';
import { Lightbox } from './Lightbox';
import { PdfPreview } from './PdfPreview';
import { TextPreview } from './TextPreview';
import { VideoPlayer } from './VideoPlayer';
import styles from './PreviewPage.module.css';

/** 预览目标：路径、展示名、大小/时间与类型。 */
export interface PreviewFile {
  path: string;
  name: string;
  size: number;
  mtime: number;
  kind: FileKind;
}

interface PreviewPageProps {
  file: PreviewFile;
  /** 当前目录内图片路径，供灯箱左右切换。 */
  images: string[];
  onClose: () => void;
  onChangeImage: (path: string) => void;
}

/** 预览页外壳；图片走全屏灯箱，其余类型使用带标题栏的页面。 */
export function PreviewPage({ file, images, onClose, onChangeImage }: PreviewPageProps) {
  const { showToast } = useToast();

  useEffect(() => {
    const previous = document.title;
    document.title = `${file.name} - FileServer`;
    return () => {
      document.title = previous;
    };
  }, [file.name]);

  useEffect(() => {
    if (file.kind === 'image') return;
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key !== 'Escape') return;
      if (
        event.target instanceof HTMLInputElement ||
        event.target instanceof HTMLTextAreaElement ||
        event.target instanceof HTMLSelectElement
      ) {
        return;
      }
      event.preventDefault();
      onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [file.kind, onClose]);

  if (file.kind === 'image') {
    return (
      <Lightbox
        path={file.path}
        paths={images}
        onClose={onClose}
        onChange={onChangeImage}
        onToast={showToast}
      />
    );
  }

  const download = (): void => {
    void downloadFile(file.path, file.name).catch(() => showToast('下载失败', 'error'));
  };

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <button
          type="button"
          className={styles.back}
          aria-label="返回浏览"
          title="返回浏览 (Esc)"
          onClick={onClose}
        >
          <span aria-hidden="true">←</span>
          <span>返回</span>
        </button>

        <div className={styles.titles}>
          <h1 className={styles.name} title={file.name}>
            {file.name}
          </h1>
          <p className={styles.meta}>
            {[formatBytes(file.size), formatTime(file.mtime)].filter(Boolean).join(' · ')}
          </p>
        </div>

        <button type="button" className={styles.download} onClick={download}>
          下载文件
        </button>
      </header>

      <main className={styles.main}>
        {file.kind === 'video' ? (
          <VideoPlayer path={file.path} name={file.name} size={file.size} />
        ) : null}
        {file.kind === 'audio' ? (
          <AudioPlayer path={file.path} name={file.name} size={file.size} />
        ) : null}
        {file.kind === 'pdf' ? (
          <PdfPreview path={file.path} name={file.name} onToast={showToast} />
        ) : null}
        {file.kind === 'text' || file.kind === 'code' ? (
          <TextPreview path={file.path} size={file.size} mtime={file.mtime} onToast={showToast} />
        ) : null}
        {file.kind !== 'video' &&
        file.kind !== 'audio' &&
        file.kind !== 'pdf' &&
        file.kind !== 'text' &&
        file.kind !== 'code' ? (
          <div className={styles.unsupported}>
            <p>该文件类型不支持在线预览</p>
            <button type="button" className={styles.download} onClick={download}>
              下载文件
            </button>
          </div>
        ) : null}
      </main>
    </div>
  );
}
