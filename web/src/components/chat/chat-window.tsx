/**
 * ChatWindow：消息流容器。
 * - 历史消息从 Query cache（服务端权威）渲染；
 * - 流式期间（chatStore 非 idle）：普通提问在消息流尾部追加「本地提问气泡 + 流式回答
 *   气泡」；regenerate 则以流式内容替换目标 assistant 消息（保留提问）；
 * - error 状态在流式气泡内嵌错误卡（错误码文案映射）+ 重试按钮；
 * - 自动滚动到底部（用户上滚时暂停跟随）。
 */

import { useEffect, useRef } from 'react';
import { RotateCcw } from 'lucide-react';
import { MessageBubble } from '@/components/chat/message-bubble';
import { formatErrorMessage, formatRequestId } from '@/lib/errors';
import { useChatStreamView } from '@/stores/chat-store';
import type { MessageItem } from '@/hooks/use-chat';

export interface ChatWindowProps {
  kbId: number;
  conversationId: number;
  messages: MessageItem[];
  /** 引用抽屉打开（行内 [n] / 引用 chip 点击） */
  onCitationClick: (seq: number) => void;
  onRegenerate: (messageId: number) => void;
  onFeedback: (message: MessageItem) => void;
  onRevokeFeedback: (messageId: number) => void;
  /** 错误卡重试：以原提问内容重发 */
  onRetry: (content: string) => void;
}

/** 会话内错误卡（LLM_001/LLM_002/429 限流/超时均走此处，§4.2）。 */
function InlineErrorCard({
  code,
  message,
  requestId,
  canRetry,
  onRetry,
}: {
  code: string;
  message: string;
  requestId: string | null;
  canRetry: boolean;
  onRetry: () => void;
}) {
  return (
    <div
      className="mt-2 rounded-[var(--radius)] border border-[var(--destructive)] bg-[var(--destructive-soft)] px-3 py-2"
      data-testid="chat-error-card"
    >
      <p className="text-xs font-medium text-[var(--destructive)]">{message}</p>
      <p className="mt-0.5 font-mono text-[10px] text-[var(--faint)]">
        {code} {formatRequestId(requestId)}
      </p>
      {canRetry ? (
        <button
          type="button"
          className="mt-1.5 inline-flex cursor-pointer items-center gap-1 rounded-[var(--radius)] border border-[var(--destructive)] px-2 py-1 text-xs text-[var(--destructive)] transition-colors hover:bg-[var(--destructive)] hover:text-white"
          onClick={onRetry}
        >
          <RotateCcw className="size-3" aria-hidden="true" />
          重试
        </button>
      ) : null}
    </div>
  );
}

export function ChatWindow({
  kbId,
  conversationId,
  messages,
  onCitationClick,
  onRegenerate,
  onFeedback,
  onRevokeFeedback,
  onRetry,
}: ChatWindowProps) {
  const view = useChatStreamView();
  const bottomRef = useRef<HTMLDivElement | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const followRef = useRef(true);

  // 用户滚动位置：贴近底部时自动跟随新消息
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const onScroll = () => {
      const distance = container.scrollHeight - container.scrollTop - container.clientHeight;
      followRef.current = distance < 120;
    };
    container.addEventListener('scroll', onScroll, { passive: true });
    return () => container.removeEventListener('scroll', onScroll);
  }, []);

  // 新消息 / 流式增量到达时滚动到底部（仅跟随模式）
  useEffect(() => {
    if (followRef.current) {
      bottomRef.current?.scrollIntoView({ block: 'end' });
    }
  }, [messages.length, conversationId, view.buffer, view.status]);

  const busy = view.status === 'streaming';

  // 会话历史按服务端数据渲染；regenerate 重演时替换目标回答
  const rendered = messages.map((m) => {
    if (m.role === 'assistant' && view.regenerateMessageId !== null && m.id === view.regenerateMessageId) {
      return (
        <MessageBubble
          key={m.id}
          kbId={kbId}
          message={m}
          overriding={{
            content: view.buffer,
            streaming: view.status === 'streaming',
            interrupted: view.status === 'interrupted',
          }}
          onCitationClick={onCitationClick}
          onRegenerate={onRegenerate}
          onFeedback={onFeedback}
          onRevokeFeedback={onRevokeFeedback}
          busy={busy}
        />
      );
    }
    return (
      <MessageBubble
        key={m.id}
        kbId={kbId}
        message={m}
        onCitationClick={onCitationClick}
        onRegenerate={onRegenerate}
        onFeedback={onFeedback}
        onRevokeFeedback={onRevokeFeedback}
        busy={busy}
      />
    );
  });

  return (
    <div ref={containerRef} className="flex-1 overflow-y-auto px-4 py-4" data-testid="chat-window">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-4">
        {rendered}

        {/* 普通提问的本地 pending exchange（提问期间未落库） */}
        {view.regenerateMessageId === null && view.question !== null ? (
          <>
            <div className="msg flex justify-end" data-testid="message-user-pending">
              <div className="max-w-[75%] rounded-[var(--radius)] bg-[var(--primary-soft)] px-3.5 py-2.5 text-sm leading-relaxed whitespace-pre-wrap text-[var(--foreground)] shadow-[var(--shadow)]">
                {view.question}
              </div>
            </div>
            <div className="msg flex justify-start" data-testid="message-assistant-pending">
              <div className="max-w-[85%] min-w-0 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-3.5 py-2.5 shadow-[var(--shadow)]">
                {view.status === 'error' ? (
                  <>
                    {view.buffer.length > 0 ? (
                      <div className="text-sm leading-relaxed whitespace-pre-wrap break-words">
                        {view.buffer}
                      </div>
                    ) : null}
                    {view.error ? (
                      <InlineErrorCard
                        code={view.error.code}
                        message={formatErrorMessage(view.error)}
                        requestId={view.error.request_id}
                        canRetry={view.question.length > 0}
                        onRetry={() => onRetry(view.question ?? '')}
                      />
                    ) : null}
                  </>
                ) : view.buffer.length > 0 ? (
                  <div className="text-sm leading-relaxed whitespace-pre-wrap break-words">
                    {view.buffer}
                    {view.status === 'streaming' ? (
                      <span className="cursor" aria-hidden="true" />
                    ) : null}
                  </div>
                ) : view.status === 'streaming' ? (
                  <div className="flex items-center gap-1 py-1" data-testid="stream-hint">
                    <span className="size-2 animate-pulse rounded-full bg-[var(--primary)]" />
                    <span className="size-2 animate-pulse rounded-full bg-[var(--primary)] [animation-delay:150ms]" />
                    <span className="size-2 animate-pulse rounded-full bg-[var(--primary)] [animation-delay:300ms]" />
                    <span className="ml-2 text-xs text-[var(--faint)]">正在检索并生成回答…</span>
                  </div>
                ) : null}

                {/* citations 帧（流式尾声到达）：chips 即时渲染 */}
                {view.status === 'streaming' && view.citations.length > 0 ? (
                  <div className="mt-2.5 flex flex-wrap items-center gap-1.5 border-t border-[var(--border)] pt-2">
                    {view.citations.map((c) => (
                      <button
                        key={c.seq}
                        type="button"
                        className="inline-flex min-w-0 max-w-full cursor-pointer items-center gap-1 rounded-full border border-[var(--border)] bg-[var(--primary-soft)] px-2 py-0.5 text-xs text-[var(--primary)]"
                        onClick={() => onCitationClick(c.seq)}
                      >
                        <span className="font-mono font-semibold">[{c.seq}]</span>
                        <span className="truncate font-mono text-[11px]">
                          {c.file_path ?? c.heading_path ?? c.source_url ?? '引用'}
                        </span>
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>
            </div>
          </>
        ) : null}

        <div ref={bottomRef} />
      </div>
    </div>
  );
}
