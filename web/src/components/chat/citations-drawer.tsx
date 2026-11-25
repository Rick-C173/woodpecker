/**
 * CitationsDrawer：引用抽屉（玻璃拟态白名单容器，transform 滑入滑出）。
 * 片段原文 pre 块 + file_path:line_start-line_end（或 heading_path）+ 来源文档链接；
 * url 类型提供「跳转原文」（source_url 外链）；git 类型后端未提供仓库 web 链接时
 * 仅展示路径与行号（后端契约不含 web URL）。
 */

import { useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { ChevronRight, ExternalLink, X } from 'lucide-react';
import type { Citation } from '@/stores/chat-store';
import { cn } from '@/lib/utils';

export interface CitationsDrawerProps {
  kbId: number;
  citations: Citation[];
  open: boolean;
  /** 当前聚焦的引用 seq */
  seq: number | null;
  onSelect: (seq: number) => void;
  onClose: () => void;
}

function citationLabel(c: Citation): string {
  const lines =
    c.line_start !== null && c.line_start > 0
      ? `:${c.line_start}${c.line_end !== null && c.line_end !== c.line_start ? `-${c.line_end}` : ''}`
      : '';
  if (c.file_path) return `${c.file_path}${lines}`;
  if (c.heading_path) return c.heading_path;
  if (c.source_url) return c.source_url;
  return `引用 ${c.seq}`;
}

export function CitationsDrawer({
  kbId,
  citations,
  open,
  seq,
  onSelect,
  onClose,
}: CitationsDrawerProps) {
  const selectedRef = useRef<HTMLDivElement | null>(null);

  // 选中项变化时滚动到对应条目
  useEffect(() => {
    if (open && seq !== null) {
      selectedRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    }
  }, [open, seq]);

  return (
    <aside
      className={cn('cite-drawer', open && 'open')}
      data-testid="citations-drawer"
      aria-hidden={!open}
      aria-label="引用详情"
    >
      <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-3">
        <h2 className="text-sm font-semibold">
          引用来源
          {citations.length > 0 ? (
            <span className="ml-1.5 text-xs font-normal text-[var(--faint)]">
              {citations.length} 条
            </span>
          ) : null}
        </h2>
        <button
          type="button"
          className="flex size-7 cursor-pointer items-center justify-center rounded-[var(--radius)] text-[var(--muted)] transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)]"
          onClick={onClose}
          aria-label="关闭引用抽屉"
        >
          <X className="size-4" aria-hidden="true" />
        </button>
      </div>

      <div className="flex-1 space-y-3 overflow-y-auto px-4 py-3">
        {citations.length === 0 ? (
          <p className="py-8 text-center text-xs text-[var(--faint)]">本条回答没有引用来源</p>
        ) : (
          citations.map((c) => (
            <div
              key={c.seq}
              ref={c.seq === seq ? selectedRef : undefined}
              className={cn(
                'rounded-[var(--radius)] border bg-[var(--card)] p-3 transition-colors',
                c.seq === seq
                  ? 'border-[var(--border-strong)] shadow-[var(--shadow-hover)]'
                  : 'border-[var(--border)]',
              )}
            >
              <div className="mb-1.5 flex items-center justify-between gap-2">
                <button
                  type="button"
                  className="inline-flex min-w-0 cursor-pointer items-center gap-1 font-mono text-xs text-[var(--primary)] hover:underline"
                  onClick={() => onSelect(c.seq)}
                  title={citationLabel(c)}
                >
                  <span className="flex-none rounded-full bg-[var(--primary-soft)] px-1.5 font-semibold">
                    {c.seq}
                  </span>
                  <ChevronRight
                    className="size-3 flex-none text-[var(--faint)]"
                    aria-hidden="true"
                  />
                  <span className="truncate">{citationLabel(c)}</span>
                </button>
                {c.source_url ? (
                  <a
                    href={c.source_url}
                    target="_blank"
                    rel="noreferrer noopener"
                    className="inline-flex flex-none items-center gap-1 text-[11px] text-[var(--accent)] hover:underline"
                    title="跳转原文"
                  >
                    <ExternalLink className="size-3" aria-hidden="true" />
                    原文
                  </a>
                ) : null}
              </div>

              {/* 片段原文快照（pre 纯文本，与来源分块一致） */}
              <pre className="max-h-52 overflow-auto whitespace-pre-wrap rounded-[var(--radius)] bg-[var(--code-bg)] p-2.5 font-mono text-[11px] leading-relaxed text-[var(--code-fg)]">
                {c.snippet}
              </pre>

              {c.document_id ? (
                <p className="mt-1.5">
                  <Link
                    to={`/kbs/${kbId}/documents/${c.document_id}`}
                    className="text-[11px] text-[var(--primary)] hover:underline"
                  >
                    查看来源文档 →
                  </Link>
                </p>
              ) : null}
            </div>
          ))
        )}
      </div>
    </aside>
  );
}
