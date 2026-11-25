/**
 * chatStore：当前流式回答的瞬态（架构 §1.7 / §4.2）。
 * SSE 状态机 idle → streaming → done | interrupted | error。
 * 明确不放入：历史消息（Query cache）、会话列表（Query cache）、流式内容入缓存。
 *
 * 状态语义（§4.2 状态机）：
 * - streaming：delta 追加 buffer；UI 禁输入、流式光标；question 为本次提问内容
 *   （提问期间消息未落库，须本地展示）；regenerateMessageId 非空 = 重演模式，
 *   该条 assistant 消息以 buffer 替换服务端内容。
 * - done：messageId 记录后由 use-chat refetch 服务端落库数据，随后 reset 回 idle。
 * - interrupted：用户点停止 / 页面关闭 / 网络闪断（绝不自动重连重发）。
 * - error：error 帧 / 非 200 / 心跳超时；保留 question 与部分 buffer 供「重试」。
 */

import { useEffect, useRef, useState } from 'react';
import { create } from 'zustand';

export type ChatStatus = 'idle' | 'streaming' | 'done' | 'interrupted' | 'error';

export interface ChatErrorBody {
  code: string;
  message: string;
  request_id: string | null;
}

export interface Citation {
  seq: number;
  document_id: number;
  file_path: string | null;
  line_start: number | null;
  line_end: number | null;
  heading_path: string | null;
  source_url: string | null;
  snippet: string;
}

export interface StartStreamOptions {
  /** 本次提问内容（regenerate 时为 null） */
  question?: string | null;
  /** 重演目标消息 ID（regenerate）；null = 普通提问 */
  regenerateMessageId?: number | null;
}

interface ChatState {
  status: ChatStatus;
  /** 流式回答增量缓冲（150ms 节流渲染，见架构 §1.5） */
  buffer: string;
  /** 引用列表（citations 帧 / done 落库后由 Query 层接管） */
  citations: Citation[];
  /** error 状态的错误体（错误码文案映射 §4.3） */
  error: ChatErrorBody | null;
  /** 当前流式请求持有的 AbortController（点停止 → abort → interrupted） */
  controller: AbortController | null;
  /** done 帧返回的消息 ID（invalidate 历史消息用） */
  messageId: number | null;
  /** 本轮提问内容（提问期间未落库，需本地气泡展示） */
  question: string | null;
  /** regenerate 重演目标消息 ID（非空 = 替换该回答） */
  regenerateMessageId: number | null;

  /** 开始一次流式请求（idle/done/interrupted/error → streaming） */
  startStream: (controller: AbortController, options?: StartStreamOptions) => void;
  /** delta 帧：追加增量 */
  appendDelta: (text: string) => void;
  /** citations 帧：一次性设置 */
  setCitations: (items: Citation[]) => void;
  /** done 帧：记录 message_id 并转 done */
  markDone: (messageId: number | null) => void;
  /** 用户点停止 / 断连 → interrupted（不展示任何失败错误） */
  markInterrupted: () => void;
  /** error 帧 / 非 200 / 超时 → error */
  markError: (error: ChatErrorBody) => void;
  /** 回 idle（清空缓冲与引用） */
  reset: () => void;
  /** 用户主动停止生成 */
  stop: () => void;
}

export const useChatStore = create<ChatState>((set, get) => ({
  status: 'idle',
  buffer: '',
  citations: [],
  error: null,
  controller: null,
  messageId: null,
  question: null,
  regenerateMessageId: null,

  startStream: (controller, options) =>
    set({
      status: 'streaming',
      buffer: '',
      citations: [],
      error: null,
      messageId: null,
      controller,
      question: options?.question ?? null,
      regenerateMessageId: options?.regenerateMessageId ?? null,
    }),

  appendDelta: (text) => set((s) => ({ buffer: s.buffer + text })),

  setCitations: (items) => set({ citations: items }),

  markDone: (messageId) => set({ status: 'done', messageId, controller: null }),

  markInterrupted: () => set({ status: 'interrupted', controller: null }),

  markError: (error) => set({ status: 'error', error, controller: null }),

  reset: () =>
    set({
      status: 'idle',
      buffer: '',
      citations: [],
      error: null,
      messageId: null,
      controller: null,
      question: null,
      regenerateMessageId: null,
    }),

  stop: () => {
    const controller = get().controller;
    controller?.abort();
  },
}));

/* ---------------- 150ms 节流视图 hook（架构 §1.5 增量渲染） ---------------- */

export interface ChatStreamView {
  status: ChatStatus;
  buffer: string;
  citations: Citation[];
  error: ChatErrorBody | null;
  question: string | null;
  regenerateMessageId: number | null;
}

/**
 * 以 150ms 定时节流读取 buffer（避免每 token 一次全量重渲染），
 * 其余低频字段直接按 store selector 订阅。
 */
export function useChatStreamView(throttleMs = 150): ChatStreamView {
  const [buffer, setBuffer] = useState(() => useChatStore.getState().buffer);
  const latest = useRef(useChatStore.getState().buffer);

  useEffect(() => {
    const unsubscribe = useChatStore.subscribe((s) => {
      latest.current = s.buffer;
    });
    const timer = setInterval(() => {
      setBuffer((prev) => (prev === latest.current ? prev : latest.current));
    }, throttleMs);
    return () => {
      unsubscribe();
      clearInterval(timer);
    };
  }, [throttleMs]);

  const status = useChatStore((s) => s.status);
  const citations = useChatStore((s) => s.citations);
  const error = useChatStore((s) => s.error);
  const question = useChatStore((s) => s.question);
  const regenerateMessageId = useChatStore((s) => s.regenerateMessageId);

  return { status, buffer, citations, error, question, regenerateMessageId };
}
