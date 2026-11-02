/**
 * 移动/复制对话框：目录树选择目标、就地新建文件夹、同名冲突三选一。
 *
 * 只展示目录（不展示文件）；选中项自身及其子目录被禁用，避免把目录移进
 * 自己。目录树按需加载 `/api/list`，展开状态与缓存都在本次对话框内。
 */
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { ApiError } from '../../api/errors';
import { fetchDirectory, type ListEntry } from '../../api/list';
import {
  copyEntries,
  createFolder,
  moveEntries,
  type ManageFailure,
  type TransferPair,
} from '../../api/manage';
import { Modal } from '../../components/Modal';
import { basename, joinPath, normalizePath } from '../../domain/path';
import buttonStyles from '../../styles/buttons.module.css';
import { manageErrorMessage } from './messages';
import styles from './TransferDialog.module.css';

type TransferKind = 'move' | 'copy';

interface TransferDialogProps {
  kind: TransferKind;
  paths: string[];
  currentPath: string;
  onClose: () => void;
  onDone: (result: { kind: TransferKind; done: TransferPair[]; failed: ManageFailure[] }) => void;
}

function sortDirs(entries: ListEntry[]): ListEntry[] {
  return entries
    .filter((entry) => entry.is_dir)
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));
}

export function TransferDialog({ kind, paths, currentPath, onClose, onDone }: TransferDialogProps) {
  const [target, setTarget] = useState(normalizePath(currentPath));
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set(['']));
  const [children, setChildren] = useState<Map<string, ListEntry[]>>(() => new Map());
  const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set());
  const loadedRef = useRef(new Set<string>());
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [conflict, setConflict] = useState<string[] | null>(null);
  const [newName, setNewName] = useState('');
  const [creating, setCreating] = useState(false);

  const loadChildren = useCallback(async (path: string): Promise<void> => {
    if (loadedRef.current.has(path)) return;
    loadedRef.current.add(path);
    setPending((current) => new Set(current).add(path));
    try {
      const data = await fetchDirectory(path, { limit: 500 });
      setChildren((current) => {
        const next = new Map(current);
        next.set(path, sortDirs(data.entries ?? []));
        return next;
      });
    } catch (loadError) {
      loadedRef.current.delete(path);
      setError(manageErrorMessage(loadError, '目录加载失败'));
    } finally {
      setPending((current) => {
        const next = new Set(current);
        next.delete(path);
        return next;
      });
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    const load = async (): Promise<void> => {
      await loadChildren('');
      if (cancelled) return;
      let current = '';
      const nextExpanded = new Set(['']);
      for (const part of normalizePath(currentPath).split('/').filter(Boolean)) {
        current = joinPath(current, part);
        nextExpanded.add(current);
        await loadChildren(current);
        if (cancelled) return;
      }
      setExpanded(nextExpanded);
    };
    void load();
    return () => {
      cancelled = true;
    };
    // 只在对话框打开时展开到当前目录；target/expanded 由用户后续操作维护。
  }, [currentPath, loadChildren]);

  const blocked = (path: string): boolean =>
    paths.some((source) => path === source || path.startsWith(`${source}/`));

  const toggle = (path: string): void => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else {
        next.add(path);
        void loadChildren(path);
      }
      return next;
    });
  };

  const run = async (mode: 'ask' | 'overwrite' | 'rename'): Promise<void> => {
    if (busy) return;
    if (blocked(target)) {
      setError('不能移动到选中项自身或其子目录');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const result =
        kind === 'move'
          ? await moveEntries(paths, target, mode)
          : await copyEntries(paths, target, mode);
      const done = (kind === 'move' ? result.moved : result.copied) ?? [];
      onDone({ kind, done, failed: result.failed ?? [] });
    } catch (submitError) {
      if (
        submitError instanceof ApiError &&
        submitError.status === 409 &&
        submitError.code === 'conflict'
      ) {
        setConflict(
          submitError.existing.length
            ? [...submitError.existing]
            : paths.map((path) => basename(path)),
        );
      } else {
        setError(manageErrorMessage(submitError, kind === 'move' ? '移动失败' : '复制失败'));
      }
    } finally {
      setBusy(false);
    }
  };

  const createDir = async (): Promise<void> => {
    const name = newName.trim();
    if (!name || creating) return;
    setCreating(true);
    setError('');
    try {
      const result = await createFolder(target, name);
      const created = normalizePath(result.path);
      loadedRef.current.delete(target);
      setChildren((current) => {
        const next = new Map(current);
        next.delete(target);
        return next;
      });
      setExpanded((current) => new Set(current).add(target).add(created));
      await loadChildren(target);
      setTarget(created);
      setNewName('');
    } catch (createError) {
      setError(manageErrorMessage(createError, '新建文件夹失败'));
    } finally {
      setCreating(false);
    }
  };

  const renderChildren = (parent: string, depth: number): ReactNode => {
    const kids = children.get(parent);
    if (!kids && pending.has(parent)) {
      return (
        <p className={styles.treeHint} style={{ paddingLeft: depth * 18 + 26 }}>
          加载中…
        </p>
      );
    }
    if (!kids || kids.length === 0) {
      return (
        <p className={styles.treeHint} style={{ paddingLeft: depth * 18 + 26 }}>
          （无子文件夹）
        </p>
      );
    }
    return kids.map((entry) => {
      const path = joinPath(parent, entry.name);
      const open = expanded.has(path);
      const disabled = blocked(path);
      return (
        <div key={path}>
          <div
            className={`${styles.treeRow} ${path === target ? styles.active : ''}`}
            style={{ paddingLeft: depth * 18 + 4 }}
            role="treeitem"
            aria-expanded={open}
          >
            <button
              type="button"
              className={styles.caret}
              aria-label={open ? '收起' : '展开'}
              onClick={() => toggle(path)}
            >
              {open ? '▾' : '▸'}
            </button>
            <button
              type="button"
              className={styles.treeName}
              title={disabled ? '不能选择选中项自身或其子目录' : path}
              disabled={disabled}
              onClick={() => setTarget(path)}
            >
              {entry.name}
            </button>
          </div>
          {open ? renderChildren(path, depth + 1) : null}
        </div>
      );
    });
  };

  const title = kind === 'move' ? '移动' : '复制';
  return (
    <Modal
      title={title}
      onClose={busy ? () => undefined : onClose}
      wide
      testId="transfer-dialog"
      footer={
        <>
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.ghost}`}
            onClick={onClose}
            disabled={busy}
          >
            取消
          </button>
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.primary}`}
            disabled={busy || blocked(target)}
            onClick={() => {
              void run('ask');
            }}
            data-testid="transfer-submit"
          >
            {busy ? '处理中…' : title}
          </button>
        </>
      }
    >
      <div className={styles.content}>
        <p className={styles.hint}>选择目标文件夹；点击箭头展开，点击名称选中。</p>
        <div className={styles.tree} role="tree" aria-label="目录树">
          <div
            className={`${styles.treeRow} ${target === '' ? styles.active : ''}`}
            role="treeitem"
            aria-expanded={expanded.has('')}
          >
            <button
              type="button"
              className={styles.caret}
              aria-label={expanded.has('') ? '收起' : '展开'}
              onClick={() => toggle('')}
            >
              {expanded.has('') ? '▾' : '▸'}
            </button>
            <button type="button" className={styles.treeName} onClick={() => setTarget('')}>
              根目录
            </button>
          </div>
          {expanded.has('') ? renderChildren('', 1) : null}
        </div>

        <div className={styles.newRow}>
          <input
            className={styles.input}
            type="text"
            value={newName}
            placeholder="新建文件夹"
            aria-label="新建文件夹名称"
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => setNewName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                void createDir();
              }
            }}
          />
          <button
            type="button"
            className={`${buttonStyles.button} ${buttonStyles.small}`}
            disabled={!newName.trim() || creating}
            onClick={() => {
              void createDir();
            }}
          >
            {creating ? '创建中…' : '新建文件夹'}
          </button>
        </div>

        <p className={styles.target} data-testid="transfer-target">
          目标：/{target}
          {blocked(target) ? '（不可用）' : ''}
        </p>
        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : null}

        {conflict ? (
          <div className={styles.conflict} role="alert" data-testid="transfer-conflict">
            <p>目标目录已有同名项：{conflict.join('、')}</p>
            <div className={styles.conflictActions}>
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.ghost}`}
                onClick={() => setConflict(null)}
              >
                取消
              </button>
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small}`}
                onClick={() => {
                  setConflict(null);
                  void run('rename');
                }}
              >
                自动改名
              </button>
              <button
                type="button"
                className={`${buttonStyles.button} ${buttonStyles.small} ${buttonStyles.primary}`}
                onClick={() => {
                  setConflict(null);
                  void run('overwrite');
                }}
              >
                覆盖
              </button>
            </div>
          </div>
        ) : null}
      </div>
    </Modal>
  );
}
