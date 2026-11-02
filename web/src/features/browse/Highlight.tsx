/** 搜索命中高亮：只拆分文本，不注入 HTML。 */
import type { ReactNode } from 'react';
import styles from './Highlight.module.css';

interface HighlightProps {
  text: string;
  query: string;
}

/**
 * 把 text 中与 query 匹配的片段包进 `<mark>`，大小写不敏感。
 * query 为空时原样返回文本；不会解释任何 HTML。
 */
export function Highlight({ text, query }: HighlightProps) {
  const needle = query.trim();
  if (!needle) return <>{text}</>;

  const haystack = text.toLocaleLowerCase();
  const lowerNeedle = needle.toLocaleLowerCase();
  const parts: ReactNode[] = [];
  let start = 0;
  let index = haystack.indexOf(lowerNeedle);

  while (index >= 0) {
    if (index > start) parts.push(text.slice(start, index));
    const end = index + needle.length;
    parts.push(
      <mark className={styles.mark} key={`${index}-${end}`}>
        {text.slice(index, end)}
      </mark>,
    );
    start = end;
    index = haystack.indexOf(lowerNeedle, start);
  }

  if (start < text.length) parts.push(text.slice(start));
  return <>{parts}</>;
}
