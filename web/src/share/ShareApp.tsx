/**
 * 公开分享页：/s/<token> 的独立 React 入口。
 *
 * 只调用 `/api/public/share/*`，不加载管理端脚本、不发送登录凭据；支持
 * 提取码、单文件信息/预览/下载、目录逐级浏览/单文件下载/zip 打包。
 * 所有文案与布局在 375 宽下可用，渲染不写内联样式（分享页 CSP 不含
 * 'unsafe-inline'）。
 */
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { formatBytes, formatTime } from '../domain/format';
import { kindOf, type FileKind } from '../domain/fileType';
import {
  fetchShareInfo,
  fetchShareList,
  ShareError,
  shareDownloadUrl,
  sharePreviewUrl,
  shareZipUrl,
  verifyShareCode,
  type PublicEntry,
  type PublicShareInfo,
} from './api';
import { shareStatusText } from './status';
import styles from './ShareApp.module.css';

const TEXT_PREVIEW_LIMIT = 1024 * 1024;
const STATUS_CODES = new Set([
  'not_found',
  'expired',
  'revoked',
  'exhausted',
  'missing',
  'bad_path',
  'rate_limited',
  'need_code',
  'not_a_dir',
]);

type PageState = 'loading' | 'code' | 'error' | 'ready';

interface PreviewTarget {
  /** 目录内文件的相对分享根路径；单文件分享为 undefined。 */
  path?: string;
  name: string;
  kind: FileKind;
  size: number;
}

function tokenFromPath(): string {
  const parts = window.location.pathname.split('/').filter(Boolean);
  const last = parts[parts.length - 1] ?? '';
  try {
    return decodeURIComponent(last);
  } catch {
    return last;
  }
}

function joinSharePath(dir: string, name: string): string {
  return dir === '/' ? `/${name}` : `${dir.replace(/\/+$/, '')}/${name}`;
}

function errorText(error: unknown): string {
  if (error instanceof ShareError && error.message) return error.message;
  if (error instanceof Error && error.message) return error.message;
  return '请求失败，请稍后重试。';
}

function errorCode(error: unknown): string {
  return error instanceof ShareError ? error.code : '';
}

/** 预览组件：图片/视频/音频/PDF/文本；不允许预览时只给出提示。 */
function PreviewPanel({
  token,
  target,
  allowPreview,
}: {
  token: string;
  target: PreviewTarget;
  allowPreview: boolean;
}) {
  if (!allowPreview) {
    return <p className={styles.hint}>分享者未开启在线预览，请下载后查看。</p>;
  }
  const url = sharePreviewUrl(token, target.path);
  switch (target.kind) {
    case 'image':
      return <img className={styles.image} src={url} alt={target.name} loading="lazy" />;
    case 'video':
      return (
        <video className={styles.media} src={url} controls preload="metadata">
          浏览器不支持视频播放。
        </video>
      );
    case 'audio':
      return (
        <audio className={styles.audio} src={url} controls preload="metadata">
          浏览器不支持音频播放。
        </audio>
      );
    case 'pdf':
      return <iframe className={styles.frame} src={url} title={target.name || 'PDF 预览'} />;
    case 'text':
    case 'code':
      return <TextPreview url={url} />;
    default:
      return <p className={styles.hint}>该类型不支持在线预览，请下载后查看。</p>;
  }
}

/** 文本预览：流式读取并截断到 1 MiB，避免大文件占满内存。 */
function TextPreview({ url }: { url: string }) {
  const [text, setText] = useState('正在加载…');
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换到新预览必须先回到加载态
    setText('正在加载…');
    setFailed(false);

    const load = async (): Promise<void> => {
      try {
        const response = await fetch(url, {
          cache: 'no-store',
          credentials: 'same-origin',
          signal: controller.signal,
        });
        if (!response.ok) throw new Error('预览加载失败');
        if (!response.body) {
          const body = await response.text();
          if (!cancelled) setText(body.slice(0, TEXT_PREVIEW_LIMIT));
          return;
        }
        const reader = response.body.getReader();
        const chunks: Uint8Array[] = [];
        let total = 0;
        let truncated = false;
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          chunks.push(value);
          total += value.length;
          if (total > TEXT_PREVIEW_LIMIT) {
            truncated = true;
            await reader.cancel();
            break;
          }
        }
        const merged = new Uint8Array(Math.min(total, TEXT_PREVIEW_LIMIT));
        let offset = 0;
        for (const chunk of chunks) {
          if (offset >= merged.length) break;
          const take = Math.min(chunk.length, merged.length - offset);
          merged.set(chunk.subarray(0, take), offset);
          offset += take;
        }
        if (cancelled) return;
        const decoded = new TextDecoder().decode(merged);
        setText(
          truncated
            ? `${decoded}\n\n（内容较长，仅显示前 1 MiB，请下载查看完整文件）`
            : decoded,
        );
      } catch (error) {
        if (cancelled || (error instanceof DOMException && error.name === 'AbortError')) return;
        setFailed(true);
        setText('预览加载失败，请下载后查看。');
      }
    };

    void load();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [url]);

  return (
    <pre className={styles.pre} data-testid="share-text-preview" aria-busy={text === '正在加载…'}>
      {failed ? <span className={styles.hint}>{text}</span> : text}
    </pre>
  );
}

/** 公开分享页根组件。 */
export function ShareApp() {
  const [token] = useState(tokenFromPath);
  const listSeq = useRef(0);

  const [state, setState] = useState<PageState>('loading');
  const [info, setInfo] = useState<PublicShareInfo | null>(null);
  const [error, setError] = useState<{ title: string; text: string } | null>(null);
  const [code, setCode] = useState('');
  const [codeError, setCodeError] = useState('');
  const [verifying, setVerifying] = useState(false);

  const [dirPath, setDirPath] = useState('/');
  const [entries, setEntries] = useState<PublicEntry[]>([]);
  const [listLoading, setListLoading] = useState(false);
  const [listError, setListError] = useState('');
  const [truncated, setTruncated] = useState(false);
  const [preview, setPreview] = useState<PreviewTarget | null>(null);

  const showFailure = useCallback((failure: unknown) => {
    const shown = shareStatusText(errorCode(failure), errorText(failure));
    setError(shown);
    setState('error');
  }, []);

  const openDirectory = useCallback(
    async (path: string) => {
      const seq = listSeq.current + 1;
      listSeq.current = seq;
      setListLoading(true);
      setListError('');
      setPreview(null);
      try {
        const data = await fetchShareList(token, path);
        if (seq !== listSeq.current) return;
        setDirPath(data.path || path);
        setEntries(Array.isArray(data.entries) ? data.entries : []);
        setTruncated(data.truncated === true);
      } catch (listFailure) {
        if (seq !== listSeq.current) return;
        const codeName = errorCode(listFailure);
        if (STATUS_CODES.has(codeName)) {
          showFailure(listFailure);
          return;
        }
        setListError(errorText(listFailure));
      } finally {
        if (seq === listSeq.current) setListLoading(false);
      }
    },
    [showFailure, token],
  );

  const loadInfo = useCallback(
    async (signal?: AbortSignal) => {
      setState('loading');
      setError(null);
      setPreview(null);
      try {
        const next = await fetchShareInfo(token, signal);
        if (signal?.aborted) return;
        setInfo(next);
        document.title = `${next.name ? `${next.name} · ` : ''}文件分享`;
        if (next.has_code && !next.verified) {
          setState('code');
          return;
        }
        setState('ready');
        if (next.kind === 'dir') await openDirectory('/');
      } catch (loadFailure) {
        if (signal?.aborted) return;
        showFailure(loadFailure);
      }
    },
    [openDirectory, showFailure, token],
  );

  useEffect(() => {
    const controller = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 页面加载即读取分享信息
    void loadInfo(controller.signal);
    return () => controller.abort();
  }, [loadInfo]);

  const submitCode = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const value = code.trim();
    if (value.length !== 4) {
      setCodeError('请输入 4 位提取码');
      return;
    }
    setVerifying(true);
    setCodeError('');
    try {
      await verifyShareCode(token, value);
      await loadInfo();
    } catch (verifyFailure) {
      const codeName = errorCode(verifyFailure);
      setCodeError(
        codeName === 'rate_limited'
          ? '尝试过于频繁，请稍后再试'
          : codeName === 'bad_code'
            ? '提取码不正确'
            : errorText(verifyFailure),
      );
    } finally {
      setVerifying(false);
    }
  };

  const expiryText = info?.expires ? `有效期至 ${formatTime(info.expires)}` : '永久有效';
  const rootName = info?.name || '分享目录';

  return (
    <div className={styles.page}>
      <header className={styles.top}>
        <span className={styles.brand}>文件分享</span>
        <span className={styles.topMeta}>
          <span className={styles.note}>{info?.note || '共享文件'}</span>
          {info ? <span className={styles.expiry}>{expiryText}</span> : null}
        </span>
      </header>

      <main className={styles.main}>
        {state === 'loading' ? (
          <div className={styles.card} role="status">
            正在加载…
          </div>
        ) : null}

        {state === 'code' ? (
          <section className={styles.card} aria-labelledby="share-code-title">
            <h1 id="share-code-title" className={styles.title}>
              需要提取码
            </h1>
            <p className={styles.hint}>请输入分享者提供的 4 位提取码。</p>
            <form className={styles.codeForm} onSubmit={(event) => void submitCode(event)}>
              <label className={styles.codeField}>
                提取码
                <input
                  className={styles.codeInput}
                  type="text"
                  maxLength={4}
                  value={code}
                  autoComplete="off"
                  autoCapitalize="characters"
                  spellCheck={false}
                  onChange={(event) => {
                    setCode(event.target.value.replace(/[^A-Za-z0-9]/g, '').toUpperCase().slice(0, 4));
                    setCodeError('');
                  }}
                  data-testid="share-code-input"
                  autoFocus
                />
              </label>
              {codeError ? (
                <p className={styles.error} role="alert" data-testid="share-code-error">
                  {codeError}
                </p>
              ) : null}
              <button
                type="submit"
                className={styles.primary}
                disabled={verifying}
                data-testid="share-code-submit"
              >
                {verifying ? '验证中…' : '确定'}
              </button>
            </form>
          </section>
        ) : null}

        {state === 'error' && error ? (
          <section className={styles.card} aria-labelledby="share-status-title" role="alert">
            <h1 id="share-status-title" className={styles.title} data-testid="share-status-title">
              {error.title}
            </h1>
            <p className={styles.hint}>{error.text}</p>
          </section>
        ) : null}

        {state === 'ready' && info && info.kind !== 'dir' ? (
          <section className={styles.card} aria-labelledby="share-file-title">
            <div className={styles.fileHead}>
              <div className={styles.fileTitleGroup}>
                <h1 id="share-file-title" className={styles.title} data-testid="share-file-title">
                  {info.name || '文件'}
                </h1>
                <p className={styles.meta}>
                  {[formatBytes(info.size ?? -1), formatTime(info.mtime ?? 0)]
                    .filter(Boolean)
                    .join(' · ')}
                </p>
              </div>
              <a
                className={styles.primary}
                href={shareDownloadUrl(token)}
                download={info.name || ''}
                data-testid="share-download"
              >
                下载
              </a>
            </div>
            <div className={styles.preview}>
              <PreviewPanel
                token={token}
                target={{
                  name: info.name || '文件',
                  kind: (info.kind as FileKind | undefined) ?? kindOf(info.name || '', false),
                  size: info.size ?? 0,
                }}
                allowPreview={info.allow_preview}
              />
            </div>
          </section>
        ) : null}

        {state === 'ready' && info && info.kind === 'dir' ? (
          <section className={styles.card} aria-labelledby="share-dir-title">
            <div className={styles.fileHead}>
              <div className={styles.fileTitleGroup}>
                <h1 id="share-dir-title" className={styles.title}>
                  {rootName}
                </h1>
                <p className={styles.meta}>
                  {listLoading ? '正在加载…' : `${entries.length} 项`}
                </p>
              </div>
              <a className={styles.primary} href={shareZipUrl(token, dirPath)} data-testid="share-zip">
                打包下载 zip
              </a>
            </div>

            <nav className={styles.crumbs} aria-label="目录路径">
              <button
                type="button"
                className={styles.crumb}
                aria-current={dirPath === '/' ? 'page' : undefined}
                onClick={() => {
                  void openDirectory('/');
                }}
              >
                {rootName}
              </button>
              {dirPath
                .split('/')
                .filter(Boolean)
                .map((segment, index, all) => {
                  const target = `/${all.slice(0, index + 1).join('/')}`;
                  const current = index === all.length - 1;
                  return (
                    <span className={styles.crumbGroup} key={target}>
                      <span className={styles.sep} aria-hidden="true">
                        /
                      </span>
                      <button
                        type="button"
                        className={styles.crumb}
                        aria-current={current ? 'page' : undefined}
                        onClick={() => {
                          if (!current) void openDirectory(target);
                        }}
                      >
                        {segment}
                      </button>
                    </span>
                  );
                })}
            </nav>

            {preview ? (
              <div className={styles.previewBox} data-testid="share-preview">
                <div className={styles.previewHead}>
                  <span className={styles.previewName}>{preview.name}</span>
                  <div className={styles.previewActions}>
                    <a
                      className={styles.linkButton}
                      href={shareDownloadUrl(token, preview.path)}
                      download={preview.name}
                    >
                      下载
                    </a>
                    <button
                      type="button"
                      className={styles.linkButton}
                      onClick={() => setPreview(null)}
                    >
                      收起预览
                    </button>
                  </div>
                </div>
                <PreviewPanel token={token} target={preview} allowPreview={info.allow_preview} />
              </div>
            ) : null}

            {listError ? (
              <p className={styles.error} role="alert">
                {listError}
              </p>
            ) : null}

            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th scope="col">名称</th>
                    <th scope="col" className={styles.sizeCol}>
                      大小
                    </th>
                    <th scope="col" className={styles.timeCol}>
                      修改时间
                    </th>
                    <th scope="col" className={styles.actCol}>
                      <span className={styles.srOnly}>操作</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((entry) => {
                    const entryPath = joinSharePath(dirPath, entry.name);
                    const kind = (entry.is_dir ? 'dir' : entry.kind || kindOf(entry.name, false)) as FileKind;
                    const previewable =
                      !entry.is_dir &&
                      (kind === 'image' ||
                        kind === 'video' ||
                        kind === 'audio' ||
                        kind === 'pdf' ||
                        kind === 'text' ||
                        kind === 'code');
                    return (
                      <tr key={entry.name} data-testid="share-entry">
                        <td className={styles.nameCell}>
                          {entry.is_dir ? (
                            <button
                              type="button"
                              className={styles.entryButton}
                              title={entry.name}
                              onClick={() => {
                                void openDirectory(entryPath);
                              }}
                            >
                              <span className={styles.entryKind}>目录</span>
                              <span className={styles.entryName}>{entry.name}</span>
                            </button>
                          ) : (
                            <span className={styles.entryStatic}>
                              <span className={styles.entryKind}>文件</span>
                              {previewable ? (
                                <button
                                  type="button"
                                  className={styles.entryNameButton}
                                  title={`预览 ${entry.name}`}
                                  onClick={() =>
                                    setPreview({
                                      path: entryPath,
                                      name: entry.name,
                                      kind,
                                      size: entry.size,
                                    })
                                  }
                                >
                                  {entry.name}
                                </button>
                              ) : (
                                <span className={styles.entryName}>{entry.name}</span>
                              )}
                            </span>
                          )}
                        </td>
                        <td className={styles.sizeCell}>{entry.is_dir ? '—' : formatBytes(entry.size)}</td>
                        <td className={styles.timeCol}>{entry.mtime ? formatTime(entry.mtime) : ''}</td>
                        <td className={styles.actCol}>
                          {entry.is_dir ? null : (
                            <a
                              className={styles.rowLink}
                              href={shareDownloadUrl(token, entryPath)}
                              download={entry.name}
                            >
                              下载
                            </a>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {!listLoading && entries.length === 0 && !listError ? (
              <p className={styles.hint}>此文件夹为空</p>
            ) : null}
            {truncated ? <p className={styles.hint}>条目较多，仅显示前 2000 项。</p> : null}
          </section>
        ) : null}
      </main>

      <footer className={styles.foot}>由 FileServer 提供</footer>
    </div>
  );
}
