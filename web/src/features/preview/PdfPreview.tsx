/**
 * PDF 预览：优先内嵌浏览器 PDF 查看器；未启用插件/内嵌被系统拦截时提供下载兜底。
 *
 * 内嵌用 `<iframe>` 而不是 `<embed>`，以便设置标题与无障碍名称；下载链接始终可见，
 * 兼容移动端浏览器「不在页面内打开 PDF」的行为。
 */
import { useEffect, useState } from 'react';
import { downloadFile, ensureTicket, fileUrl, withTicket } from '../../api/media';
import styles from './PdfPreview.module.css';

interface PdfPreviewProps {
  path: string;
  name: string;
  onToast: (message: string, tone?: 'info' | 'error') => void;
}

function viewerEnabled(): boolean {
  const navigatorWithPdf = navigator as Navigator & { pdfViewerEnabled?: boolean };
  return navigatorWithPdf.pdfViewerEnabled !== false;
}

/** PDF 内嵌预览与下载兜底。 */
export function PdfPreview({ path, name, onToast }: PdfPreviewProps) {
  const [src, setSrc] = useState('');
  const [loaded, setLoaded] = useState(false);
  const supported = viewerEnabled();

  useEffect(() => {
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换 PDF 时重置加载态
    setLoaded(false);
    void ensureTicket().then((ticket) => {
      if (!cancelled) setSrc(withTicket(fileUrl(path), ticket));
    });
    return () => {
      cancelled = true;
    };
  }, [path]);

  return (
    <div className={styles.wrap}>
      <div className={styles.bar}>
        <span className={styles.hint}>若无法在页面内查看，请下载原文件</span>
        <button
          type="button"
          className={styles.download}
          onClick={() => {
            void downloadFile(path, name).catch(() => onToast('下载失败', 'error'));
          }}
        >
          下载 PDF
        </button>
      </div>

      {supported ? (
        <div className={styles.frameWrap}>
          {!loaded ? <div className={styles.skeleton} aria-label="PDF 加载中" /> : null}
          {src ? (
            <iframe
              className={styles.frame}
              src={src}
              title={`PDF 预览：${name}`}
              data-testid="pdf-frame"
              onLoad={() => setLoaded(true)}
            />
          ) : null}
        </div>
      ) : (
        <div className={styles.fallback} role="alert">
          <p>当前浏览器未启用内置 PDF 查看器。</p>
          <p>请下载后用本地阅读器打开。</p>
        </div>
      )}
    </div>
  );
}
