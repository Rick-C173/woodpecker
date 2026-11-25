/**
 * chatStore：当前流式回答的瞬态（架构 §1.7 / §4.2）。
 * SSE 状态机 idle → streaming → done | interrupted | error。
 * 明确不放入：历史消息（Query cache）、会话列表（Query cache）、流式内容入缓存。
 * 本批只建骨架（F5 填充交互）。
 */

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

  /** 开始一次流式请求（idle/done/interrupted/error → streaming） */
  startStream: (controller: AbortController) => void;
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

  startStream: (controller) =>
    set({
      status: 'streaming',
      buffer: '',
      citations: [],
      error: null,
      messageId: null,
      controller,
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
    }),

  stop: () => {
    const controller = get().controller;
    controller?.abort();
  },
}));
