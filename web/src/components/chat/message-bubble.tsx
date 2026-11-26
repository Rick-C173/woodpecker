/**
 * MessageBubble：提问/回答气泡。
 * - 用户消息右对齐；AI 回答接 <Markdown>（含行内 [n] 引用徽标）。
 * - 回答尾部：引用 chips（seq 顺序）+ hover 操作行（点踩 / 撤销点踩 / 重新生成）。
 * - interrupted 标记（历史落库 interrupted 或本地中断）：灰色「已停止，部分回答已保留」，
 *   citations 不出现（后端断连落库无 citations，契约如此）。
 * - regenerate 重演态（overriding 非空）：以流式 buffer 替换服务端内容，流式光标动画。
 */

import { Link } from 'react-router-dom';
import { FileText, Globe, ThumbsDown, Undo2 } from 'lucide-react';
import { Markdown } from '@/components/shared/markdown';
import { cn } from '@/lib/utils';
import type { MessageItem } from '@/hooks/use-chat';
import { FEEDBACK_REASON_LABELS, type FeedbackReason } from '@/lib/chat-events';

export interface MessageBubbleProps {
  kbId: number;
  message: MessageItem;
  /** regenerate 重演：该回答以流式内容替换 */
  overriding?: { content: string; streaming: boolean; interrupted: boolean } | null;
  onCitationClick: (seq: number) => void;
  onRegenerate: (messageId: number) => void;
  /** 打开点踩 dialog（重选原因覆盖） */
  onFeedback: (message: MessageItem) => void;
  /** 撤销点踩 */
  onRevokeFeedback: (messageId: number) => void;
  /** 流式进行中（禁用重新生成/点踩） */
  busy: boolean;
}

/** 引用 chip：[n] file_path:lines（url 类型取 heading/source 域名）。 */
function CitationChip({
  kbId,
  seq,
  filePath,
  lineStart,
  lineEnd,
  headingPath,
  sourceUrl,
  documentId,
  onClick,
}: {
  kbId: number;
  seq: number;
  filePath: string | null;
  lineStart: number | null;
  lineEnd: number | null;
  headingPath: string | null;
  sourceUrl: string | null;
  documentId: number;
  onClick: (seq: number) => void;
}) {
  const lines =
    lineStart !== null && lineStart > 0
      ? `:${lineStart}${lineEnd !== null && lineEnd !== lineStart ? `-${lineEnd}` : ''}`
      : '';
  const label = filePath ? `${filePath}${lines}` : (headingPath ?? sourceUrl ?? '引用片段');
  const isUrl = filePath === null && sourceUrl !== null;
  return (
    <span className="inline-flex max-w-full items-center gap-1 overflow-hidden">
      <button
        type="button"
        className="inline-flex min-w-0 max-w-full cursor-pointer items-center gap-1 rounded-full border border-[var(--border)] bg-[var(--primary-soft)] px-2 py-0.5 text-xs text-[var(--primary)] transition-colors hover:border-[var(--border-strong)] hover:bg-[var(--accent-soft)] hover:text-[var(--accent)]"
        onClick={() => onClick(seq)}
        title={label}
      >
        <span className="font-mono font-semibold">[{seq}]</span>
        {isUrl ? <Globe className="size-3 flex-none" aria-hidden="true" /> : <FileText className="size-3 flex-none" aria-hidden="true" />}
        <span className="truncate font-mono text-[11px]">{label}</span>
      </button>
      {/* 来源文档直达（document_id 存在时） */}
      {documentId > 0 ? (
        <Link
          to={`/kbs/${kbId}/documents/${documentId}`}
          className="text-[10px] text-[var(--faint)] hover:text-[var(--primary)]"
          title="查看来源文档"
        >
          来源
        </Link>
      ) : null}
    </span>
  );
}

export function MessageBubble({
  kbId,
  message,
  overriding,
  onCitationClick,
  onRegenerate,
  onFeedback,
  onRevokeFeedback,
  busy,
}: MessageBubbleProps) {
  const isUser = message.role === 'user';
  const streaming = overriding?.streaming ?? false;
  const interrupted = message.interrupted === true || (overriding?.interrupted ?? false);
  // interrupted 落库无 citations；本地中断同样不展示引用区
  const citations = interrupted ? [] : (message.citations ?? []);
  const feedback = message.feedback ?? null;

  if (isUser) {
    return (
      <div className="msg flex justify-end" data-testid="message-user">
        <div className="max-w-[75%] rounded-[var(--radius)] bg-[var(--primary-soft)] px-3.5 py-2.5 text-sm leading-relaxed whitespace-pre-wrap text-[var(--foreground)] shadow-[var(--shadow)]">
          {message.content ?? ''}
        </div>
      </div>
    );
  }

  return (
    <div className="msg group flex justify-start" data-testid="message-assistant">
      <div className="max-w-[85%] min-w-0">
        {/* 流式内容或落库内容 */}
        <div className="rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-3.5 py-2.5 shadow-[var(--shadow)]">
          {streaming ? (
            <div className="text-sm leading-relaxed whitespace-pre-wrap break-words text-[var(--foreground)]">
              {overriding?.content}
              <span className="cursor" aria-hidden="true" />
            </div>
          ) : (
            <>
              <Markdown
                content={message.content ?? ''}
                onCite={citations.length > 0 ? onCitationClick : undefined}
              />
              {interrupted ? (
                <p className="mt-1.5 inline-flex items-center rounded-[var(--radius)] bg-[var(--muted-bg)] border border-[var(--border)] px-2 py-0.5 text-xs text-[var(--muted-foreground)]" data-testid="interrupted-tag">
                  已停止，部分回答已保留
                </p>
              ) : null}
            </>
          )}

          {/* 引用 chips（citations 帧 / 落库引用；interrupted 时不出现） */}
          {citations.length > 0 ? (
            <div
              className="mt-2.5 flex flex-wrap items-center gap-1.5 border-t border-[var(--border)] pt-2"
              data-testid="citation-chips"
            >
              {citations.map((c) => (
                <CitationChip
                  key={c.seq}
                  kbId={kbId}
                  seq={c.seq ?? 0}
                  filePath={c.file_path ?? null}
                  lineStart={c.line_start ?? null}
                  lineEnd={c.line_end ?? null}
                  headingPath={c.heading_path ?? null}
                  sourceUrl={c.source_url ?? null}
                  documentId={c.document_id ?? 0}
                  onClick={onCitationClick}
                />
              ))}
            </div>
          ) : null}
        </div>

        {/* hover 操作行：点踩（含已点踩态）/ 撤销 / 重新生成 */}
        {!streaming ? (
          <div
            className={cn(
              'mt-1 flex items-center gap-1 px-1 text-xs text-[var(--faint)] opacity-0 transition-opacity',
              'group-hover:opacity-100 focus-within:opacity-100',
            )}
          >
            {feedback ? (
              <>
                <span
                  className="inline-flex items-center gap-1 text-[var(--muted-foreground)]"
                  data-testid="feedback-active"
                >
                  <ThumbsDown className="size-3" aria-hidden="true" />
                  已点踩（{FEEDBACK_REASON_LABELS[(feedback.reason ?? 0) as FeedbackReason] ?? feedback.reason}）
                </span>
                <button
                  type="button"
                  className="inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]"
                  onClick={() => onRevokeFeedback(message.id ?? 0)}
                  title="撤销点踩"
                >
                  <Undo2 className="size-3" aria-hidden="true" />
                  撤销
                </button>
                <button
                  type="button"
                  className="inline-flex cursor-pointer items-center rounded px-1.5 py-0.5 transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]"
                  onClick={() => onFeedback(message)}
                  title="重新选择原因（覆盖上次反馈）"
                >
                  重选
                </button>
              </>
            ) : (
              <button
                type="button"
                className="inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]"
                onClick={() => onFeedback(message)}
                title="点踩（4 原因单选，可撤销）"
                data-testid="feedback-button"
              >
                <ThumbsDown className="size-3" aria-hidden="true" />
                点踩
              </button>
            )}
            <button
              type="button"
              className="inline-flex cursor-pointer items-center rounded px-1.5 py-0.5 transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)] disabled:cursor-not-allowed disabled:opacity-50"
              disabled={busy || (message.id ?? 0) <= 0}
              onClick={() => onRegenerate(message.id ?? 0)}
              title="重新生成（替换该回答）"
            >
              重新生成
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
