/**
 * 聊天页（F5 核心，/kbs/:kbId/chat 与 /kbs/:kbId/chat/:cId 复用）：
 * - 左侧：会话列表侧栏（标题 + 相对时间、新建、重命名 inline 编辑、删除二次确认）；
 * - 右侧：消息历史 + SSE 流式问答（chatStore 状态机驱动，use-chat 消费 lib/sse.ts）；
 * - 引用抽屉（uiStore 开关 + 聚焦 seq）；点踩 dialog（4 原因单选可撤销）；
 * - 页面切换 / 会话切换：abort 进行中的流（interrupted 落库，服务端权威数据回填）。
 */

import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  ArrowLeft,
  ChevronRight,
  MessageSquarePlus,
  MessagesSquare,
  Pencil,
  Trash2,
  X,
} from 'lucide-react';
import { toast } from 'sonner';
import { ChatInput } from '@/components/chat/chat-input';
import { ChatWindow } from '@/components/chat/chat-window';
import { CitationsDrawer } from '@/components/chat/citations-drawer';
import { FeedbackDialog } from '@/components/chat/feedback-dialog';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import { useKbDetail } from '@/hooks/use-kbs';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import {
  useChatStream,
  useConversationDetail,
  useConversations,
  useCreateConversation,
  useDeleteConversation,
  useRenameConversation,
  useSetFeedback,
  useUnsetFeedback,
  type ConversationItem,
} from '@/hooks/use-chat';
import type { FeedbackReason } from '@/lib/chat-events';
import { fromNow } from '@/lib/time';
import { cn } from '@/lib/utils';
import { useChatStreamView, useChatStore } from '@/stores/chat-store';
import { useUiStore } from '@/stores/ui-store';

function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

/** 会话列表项：标题 + 相对时间；hover 出现重命名/删除；inline 重命名输入。 */
function ConversationListItem({
  conversation,
  active,
  editing,
  onStartEdit,
  onCancelEdit,
  onCommitRename,
  onDelete,
}: {
  conversation: ConversationItem;
  active: boolean;
  editing: boolean;
  onStartEdit: () => void;
  onCancelEdit: () => void;
  onCommitRename: (title: string) => void;
  onDelete: () => void;
}) {
  const [draft, setDraft] = useState(conversation.title ?? '');
  const inputRef = useRef<HTMLInputElement | null>(null);

  // 进入编辑态时聚焦
  useEffect(() => {
    if (editing) inputRef.current?.focus();
  }, [editing]);

  if (editing) {
    return (
      <div className="flex items-center gap-1 px-1.5 py-1">
        <input
          ref={inputRef}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
              const title = draft.trim();
              if (title.length > 0) onCommitRename(title);
              else onCancelEdit();
            } else if (e.key === 'Escape') {
              onCancelEdit();
            }
          }}
          className="h-8 w-full rounded-[var(--radius)] border border-[var(--border-strong)] bg-[var(--card)] px-2 text-[13px] outline-none"
          aria-label="会话名称"
        />
        <button
          type="button"
          className="flex size-7 flex-none cursor-pointer items-center justify-center rounded-[var(--radius)] text-[var(--muted)] transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]"
          onClick={() => onCancelEdit()}
          aria-label="取消重命名"
        >
          <X className="size-3.5" aria-hidden="true" />
        </button>
      </div>
    );
  }

  return (
    <div className={cn('group relative rounded-[var(--radius)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]', active && 'ring-1 ring-[var(--border-strong)]')}>
      <span className="absolute inset-0 rounded-[var(--radius)]" aria-hidden="true" />
      <div
        className={cn(
          'flex h-[var(--row)] items-center gap-2 rounded-[var(--radius)] px-3 transition-colors',
          active
            ? 'bg-[var(--primary-soft)] text-[var(--primary)]'
            : 'text-[var(--muted)] hover:bg-[var(--primary-soft)]',
        )}
      >
        <MessagesSquare className="size-4 flex-none" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-[13px]" title={conversation.title ?? ''}>
          {conversation.title ?? '新会话'}
        </span>
        <span className="flex-none text-[11px] tabular-nums text-[var(--faint)] group-hover:hidden">
          {fromNow(conversation.updated_at) || ''}
        </span>
        {/* hover 操作：重命名 / 删除 */}
        <span className="hidden flex-none items-center gap-0.5 group-hover:flex">
          <button
            type="button"
            className="flex size-6 cursor-pointer items-center justify-center rounded text-[var(--muted)] transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]"
            onClick={onStartEdit}
            title="重命名"
            aria-label={`重命名会话 ${conversation.title ?? ''}`}
          >
            <Pencil className="size-3.5" aria-hidden="true" />
          </button>
          <button
            type="button"
            className="flex size-6 cursor-pointer items-center justify-center rounded text-[var(--muted)] transition-colors hover:bg-[var(--destructive-soft)] hover:text-[var(--destructive)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]"
            onClick={onDelete}
            title="删除会话"
            aria-label={`删除会话 ${conversation.title ?? ''}`}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
          </button>
        </span>
      </div>
    </div>
  );
}

export function KbChatPage() {
  const { kbId, cId } = useParams();
  const navigate = useNavigate();
  const kbIdNum = Number(kbId);
  const cIdNum = Number(cId ?? 0);
  const kbValid = Number.isFinite(kbIdNum) && kbIdNum > 0;
  const convSelected = Number.isFinite(cIdNum) && cIdNum > 0;

  const view = useChatStreamView();
  const drawerOpen = useUiStore((s) => s.citationsDrawerOpen);
  const drawerSeq = useUiStore((s) => s.citationsSeq);
  const openCitation = useUiStore((s) => s.openCitation);
  const closeCitationsDrawer = useUiStore((s) => s.closeCitationsDrawer);

  const kbDetail = useKbDetail(kbIdNum);
  const conversations = useConversations(kbIdNum, kbValid);
  const detail = useConversationDetail(cIdNum, convSelected);

  const createConv = useCreateConversation(kbIdNum);
  const deleteConv = useDeleteConversation(kbIdNum);
  const renameConv = useRenameConversation(kbIdNum);
  const setFeedback = useSetFeedback(cIdNum);
  const unsetFeedback = useUnsetFeedback(cIdNum);
  const { send, stop } = useChatStream(kbIdNum, cIdNum);

  const [editingId, setEditingId] = useState<number | null>(null);
  const [pendingDelete, setPendingDelete] = useState<ConversationItem | null>(null);
  const [feedbackTarget, setFeedbackTarget] = useState<
    { id: number; feedback: { reason?: number; comment?: string } | null } | null
  >(null);

  // 会话切换：重置 chatStore 瞬态；离开页面 / 切换会话时 abort 进行中的流（interrupted）
  useEffect(() => {
    useChatStore.getState().reset();
    return () => {
      const s = useChatStore.getState();
      if (s.status === 'streaming') s.stop();
    };
  }, [cIdNum]);

  const handleCreateConversation = () => {
    createConv.mutate(undefined, {
      onSuccess: (item) => {
        toast.success('已新建会话');
        navigate(`/kbs/${kbIdNum}/chat/${item.id}`);
      },
      onError: toastError,
    });
  };

  const handleDeleteConversation = () => {
    const targetId = pendingDelete?.id;
    if (targetId === undefined || pendingDelete === null) return;
    const target = pendingDelete;
    deleteConv.mutate(targetId, {
      onSuccess: () => {
        toast.success('会话已删除');
        setPendingDelete(null);
        if (target.id === cIdNum) {
          useChatStore.getState().reset();
          navigate(`/kbs/${kbIdNum}/chat`, { replace: true });
        }
      },
      onError: toastError,
    });
  };

  const handleCommitRename = (cIdTarget: number, title: string) => {
    renameConv.mutate(
      { cId: cIdTarget, title },
      {
        onSuccess: () => toast.success('已重命名'),
        onError: toastError,
      },
    );
    setEditingId(null);
  };

  const handleSubmitFeedback = (
    messageId: number,
    reason: FeedbackReason,
    comment: string,
  ) => {
    setFeedback.mutate(
      { messageId, reason, comment: comment || undefined },
      {
        onSuccess: () => {
          toast.success('反馈已记录，感谢帮助改进');
          setFeedbackTarget(null);
        },
        onError: toastError,
      },
    );
  };

  const handleRevokeFeedback = (messageId: number) => {
    unsetFeedback.mutate(messageId, {
      onSuccess: () => toast.success('已撤销点踩'),
      onError: toastError,
    });
  };

  // 引用抽屉数据源：流式期间取 chatStore（citations 帧）；否则取最后一条回答的落库引用
  const messages = detail.data?.messages ?? [];
  const lastAssistant = [...messages].reverse().find((m) => m.role === 'assistant');
  const drawerCitations =
    view.status !== 'idle'
      ? view.citations
      : (lastAssistant?.citations?.map((c) => ({
          seq: c.seq ?? 0,
          document_id: c.document_id ?? 0,
          file_path: c.file_path ?? null,
          line_start: c.line_start ?? null,
          line_end: c.line_end ?? null,
          heading_path: c.heading_path ?? null,
          source_url: c.source_url ?? null,
          snippet: c.snippet ?? '',
        })) ?? []);

  return (
    <div className="flex h-full min-h-0" data-testid="kb-chat-page">
      {/* 左侧：会话列表侧栏（玻璃拟态白名单容器） */}
      <aside className="conv-list flex w-60 flex-none flex-col">
        <div className="flex items-center justify-between border-b border-[var(--border)] px-3 py-3">
          <h2 className="text-sm font-semibold">会话</h2>
          <Button
            variant="outline"
            size="sm"
            onClick={handleCreateConversation}
            disabled={createConv.isPending || !kbValid}
            title="新建会话"
            data-testid="new-conversation"
          >
            <MessageSquarePlus aria-hidden="true" />
            新会话
          </Button>
        </div>
        <div className="flex-1 space-y-0.5 overflow-y-auto px-1.5 py-2">
          <QueryBoundary
            query={conversations}
            skeleton={
              <div className="space-y-1.5 px-1.5">
                {Array.from({ length: 4 }, (_, i) => (
                  <Skeleton key={i} className="h-9 w-full" />
                ))}
              </div>
            }
            empty={
              <p className="px-3 py-6 text-center text-xs text-[var(--faint)]">
                还没有会话，点上方「新会话」开始提问
              </p>
            }
          >
            {(items) =>
              items.map((c) => (
                <div key={c.id} role="button" tabIndex={0} className="cursor-pointer"
                  onClick={() => {
                    if (editingId === null) navigate(`/kbs/${kbIdNum}/chat/${c.id}`);
                  }}
                  onKeyDown={(e) => {
                    if ((e.key === 'Enter' || e.key === ' ') && editingId === null) {
                      navigate(`/kbs/${kbIdNum}/chat/${c.id}`);
                    }
                  }}
                  data-testid="conversation-item"
                >
                  <ConversationListItem
                    conversation={c}
                    active={c.id === cIdNum}
                    editing={editingId === c.id}
                    onStartEdit={() => setEditingId(c.id ?? null)}
                    onCancelEdit={() => setEditingId(null)}
                    onCommitRename={(title) => handleCommitRename(c.id ?? 0, title)}
                    onDelete={() => setPendingDelete(c)}
                  />
                </div>
              ))
            }
          </QueryBoundary>
        </div>
      </aside>

      {/* 右侧：消息区 + 引用抽屉 */}
      <section className="relative flex min-w-0 flex-1 flex-col overflow-hidden">
        {!convSelected ? (
          <div className="flex flex-1 items-center justify-center px-6">
            <EmptyState
              icon={MessagesSquare}
              title="向知识库提问"
              description="新建或选择一个会话，即可基于库内文档与代码进行问答，回答附引用来源。"
              action={{ label: '新建会话', onClick: handleCreateConversation }}
            />
          </div>
        ) : (
          <>
            <div className="topbar flex-none">
              <Link
                to={`/kbs/${kbIdNum}`}
                className="flex-none rounded-[var(--radius)] p-1.5 text-[var(--muted)] transition-colors hover:bg-[var(--primary-soft)] hover:text-[var(--primary)]"
                aria-label="返回知识库详情"
                title="返回知识库详情"
              >
                <ArrowLeft className="size-4" aria-hidden="true" />
              </Link>
              <span
                className="max-w-40 flex-none truncate text-sm text-[var(--muted)]"
                title={kbDetail.data?.name ?? ''}
              >
                {kbDetail.data?.name ?? ''}
              </span>
              <ChevronRight className="size-3.5 flex-none text-[var(--faint)]" aria-hidden="true" />
              <div className="min-w-0 flex-1 truncate text-sm font-semibold">
                {detail.data?.title ?? '会话'}
              </div>
              {view.status === 'streaming' ? (
                <span className="flex-none text-xs text-[var(--primary)]">回答生成中…</span>
              ) : null}
            </div>

            <QueryBoundary
              query={detail}
              skeleton={
                <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col gap-4 px-4 py-4">
                  <Skeleton className="h-10 w-1/2 self-end" />
                  <Skeleton className="h-24 w-3/4 self-start" />
                  <Skeleton className="h-10 w-1/3 self-end" />
                  <Skeleton className="h-32 w-3/4 self-start" />
                </div>
              }
            >
              {() => (
                <ChatWindow
                  kbId={kbIdNum}
                  conversationId={cIdNum}
                  messages={messages}
                  onCitationClick={openCitation}
                  onRegenerate={(messageId) => void send({ regenerateMessageId: messageId })}
                  onFeedback={(m) =>
                    setFeedbackTarget({
                      id: m.id ?? 0,
                      feedback: m.feedback
                        ? { reason: m.feedback.reason, comment: m.feedback.comment }
                        : null,
                    })
                  }
                  onRevokeFeedback={handleRevokeFeedback}
                  onRetry={(content) => void send({ content })}
                />
              )}
            </QueryBoundary>

            <ChatInput
              streaming={view.status === 'streaming'}
              onSend={(content) => void send({ content })}
              onStop={stop}
            />

            <CitationsDrawer
              kbId={kbIdNum}
              citations={drawerCitations}
              open={drawerOpen && drawerCitations.length > 0}
              seq={drawerSeq}
              onSelect={openCitation}
              onClose={closeCitationsDrawer}
            />
          </>
        )}
      </section>

      {/* 删除会话二次确认 */}
      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
        title="删除会话"
        description={`将删除会话「${pendingDelete?.title ?? ''}」及其全部消息与反馈记录，不可恢复。`}
        confirmLabel="删除"
        busy={deleteConv.isPending}
        onConfirm={handleDeleteConversation}
      />

      {/* 点踩 dialog（四原因单选，其他必填说明；重复提交覆盖） */}
      <FeedbackDialog
        open={feedbackTarget !== null}
        onOpenChange={(open) => {
          if (!open) setFeedbackTarget(null);
        }}
        message={
          feedbackTarget
            ? {
                id: feedbackTarget.id,
                feedback: feedbackTarget.feedback,
              }
            : null
        }
        onSubmit={handleSubmitFeedback}
        busy={setFeedback.isPending}
      />
    </div>
  );
}
