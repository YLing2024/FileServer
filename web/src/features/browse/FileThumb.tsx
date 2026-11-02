/** 网格/列表缩略图：图片走 /api/thumb，视频按容器类型选择抽帧或服务端缩略图。 */
import { useEffect, useState } from 'react';
import type { ListEntry } from '../../api/list';
import { ensureTicket, thumbUrl, withTicket } from '../../api/media';
import { kindOfEntry } from '../../domain/fileType';
import { KindIcon } from './icons';
import { useVideoThumbnail } from './useVideoThumbnail';
import styles from './FileThumb.module.css';

/** 浏览器原生可解码的 MP4/WebM 家族；其余视频容器交给 ffmpeg 或图标兜底。 */
const BROWSER_VIDEO = /\.(mp4|m4v|mov|webm)$/i;

interface FileThumbProps {
  entry: ListEntry;
  path: string;
  serverTranscode: boolean;
}

function useMediaSource(url: string): { src: string; failed: boolean; retry: () => void } {
  const [src, setSrc] = useState('');
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    ensureTicket()
      .then((ticket) => {
        if (!cancelled) setSrc(withTicket(url, ticket));
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [url]);

  const retry = (): void => {
    if (attempt < 1) setAttempt((value) => value + 1);
    else setFailed(true);
  };

  const finalSrc = src && attempt > 0 ? `${src}${src.includes('?') ? '&' : '?'}r=${attempt}` : src;
  return { src: finalSrc, failed, retry };
}

function ImageThumb({ path, testId }: { path: string; testId: string }) {
  const { src, failed, retry } = useMediaSource(thumbUrl(path));
  if (!src || failed) return <KindIcon kind="image" className={styles.typeIcon} />;
  return (
    <img
      className={styles.image}
      src={src}
      alt=""
      loading="lazy"
      decoding="async"
      data-testid={testId}
      onError={retry}
    />
  );
}

function BrowserVideoThumb({ path }: { path: string }) {
  const { ref, src, failed } = useVideoThumbnail(path, true);
  return (
    <div className={styles.thumb} ref={ref}>
      {src && !failed ? (
        <img className={styles.image} src={src} alt="" data-testid="thumb-video" />
      ) : (
        <KindIcon kind="video" className={styles.typeIcon} />
      )}
    </div>
  );
}

function ServerVideoThumb({ path }: { path: string }) {
  return (
    <div className={styles.thumb}>
      <ImageThumb path={path} testId="thumb-video" />
    </div>
  );
}

function IconThumb({
  kind,
}: {
  kind: 'video' | 'audio' | 'pdf' | 'text' | 'doc' | 'code' | 'archive' | 'other';
}) {
  return (
    <div className={styles.thumb}>
      <KindIcon kind={kind} className={styles.typeIcon} />
    </div>
  );
}

/** 渲染一个条目的缩略图区域；目录与不支持预览的类型使用线性图标。 */
export function FileThumb({ entry, path, serverTranscode }: FileThumbProps) {
  const kind = kindOfEntry(entry);

  if (kind === 'dir') {
    return (
      <div className={`${styles.thumb} ${styles.dir}`}>
        <KindIcon kind="dir" className={styles.dirIcon} />
      </div>
    );
  }

  if (kind === 'image') {
    return (
      <div className={styles.thumb}>
        <ImageThumb path={path} testId="thumb-image" />
      </div>
    );
  }

  if (kind === 'video') {
    if (BROWSER_VIDEO.test(path)) return <BrowserVideoThumb path={path} />;
    if (serverTranscode) return <ServerVideoThumb path={path} />;
    return <IconThumb kind="video" />;
  }

  if (
    kind === 'audio' ||
    kind === 'pdf' ||
    kind === 'text' ||
    kind === 'doc' ||
    kind === 'code' ||
    kind === 'archive' ||
    kind === 'other'
  ) {
    return <IconThumb kind={kind} />;
  }

  return <IconThumb kind="other" />;
}
