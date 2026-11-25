/**
 * 会话域 queries/mutations + SSE 提问流（架构 §1.2 / §4.2 / API设计 §3.7、§4）。
 * - 会话列表 / 消息历史入 Query cache（staleTime 30s / Infinity）；
 *   一切写操作后按资源前缀 invalidate，不做乐观更新。
 * - SSE 流式内容不入 cache：走 chatStore（权威瞬态），done/interrupted 后
 *   refetch 服务端落库数据并 reset（换权威数据，无闪烁）。
 * - 停止生成 = AbortController.abort()；网络闪断同样转 interrupted，绝不自动重连。
 */

import { useCallback } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { components } from '@/api/schema';
import { api } from '@/api/client';
import { patchConversation, postMessageFeedback } from '@/api/manual-body';
import { getToken } from '@/lib/auth';
import { ERROR_MESSAGES } from '@/lib/errors';
import {
  normalizeCitationItems,
  normalizeDeltaContent,
  normalizeDoneMessageId,
  normalizeErrorBody,
} from '@/lib/chat-events';
import { qk } from '@/lib/queryKeys';
import { consumeSSE, SSEAbortError, SSETimeoutError, type SSEEvent } from '@/lib/sse';
import { useChatStore } from '@/stores/chat-store';

export type ConversationItem = components['schemas']['service.ConversationItem'];
export type ConversationDetail = components['schemas']['service.ConversationDetail'];
export type MessageItem = components['schemas']['service.MessageItem'];
export type CitationItem = components['schemas']['service.CitationItem'];

/* ---------------- Queries ---------------- */

/** 我的会话列表（GET /kbs/{kb}/conversations，不分页）。 */
export function useConversations(kbId: number, enabled = true) {
  return useQuery({
    queryKey: qk.kbs.conversations(kbId),
    queryFn: async (): Promise<ConversationItem[]> => {
      const { data } = await api.GET('/kbs/{kb}/conversations', {
        params: { path: { kb: kbId } },
      });
      return data?.items ?? [];
    },
    staleTime: 30_000,
    enabled: enabled && Number.isFinite(kbId) && kbId > 0,
  });
}

/** 消息历史（GET /conversations/{c}，含引用）。staleTime Infinity：只被本地追加与 SSE done 后刷新。 */
export function useConversationDetail(cId: number, enabled = true) {
  return useQuery({
    queryKey: qk.conversations.detail(cId),
    queryFn: async (): Promise<ConversationDetail> => {
      const { data, response } = await api.GET('/conversations/{c}', {
        params: { path: { c: cId } },
      });
      if (!response.ok || !data) throw new Error('会话加载失败');
      return data;
    },
    staleTime: Number.POSITIVE_INFINITY,
    enabled: enabled && Number.isFinite(cId) && cId > 0,
  });
}

/* ---------------- Mutations ---------------- */

/** 新建会话（POST /kbs/{kb}/conversations，无请求体）。 */
export function useCreateConversation(kbId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (): Promise<ConversationItem> => {
      const { data, response } = await api.POST('/kbs/{kb}/conversations', {
        params: { path: { kb: kbId } },
      });
      if (!response.ok || !data) throw new Error('新建会话失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.conversations(kbId) });
    },
  });
}

/** 删除会话（DELETE /conversations/{c}，反馈级联删）。 */
export function useDeleteConversation(kbId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (cId: number): Promise<void> => {
      const { response } = await api.DELETE('/conversations/{c}', {
        params: { path: { c: cId } },
      });
      if (!response.ok) throw new Error('删除会话失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.conversations(kbId) });
    },
  });
}

/** 重命名会话（PATCH /conversations/{c}，swagger 缺 requestBody 走 manual-body）。 */
export function useRenameConversation(kbId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ cId, title }: { cId: number; title: string }): Promise<void> => {
      const { response } = await patchConversation(cId, { title });
      if (!response.ok) throw new Error('重命名失败');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: qk.kbs.conversations(kbId) });
      void qc.invalidateQueries({ queryKey: qk.conversations.detail(vars.cId) });
    },
  });
}

/** 点踩（POST /messages/{m}/feedback，重复提交覆盖）。 */
export function useSetFeedback(cId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      messageId,
      reason,
      comment,
    }: {
      messageId: number;
      reason: number;
      comment?: string;
    }): Promise<void> => {
      const { response } = await postMessageFeedback(messageId, { reason, comment });
      if (!response.ok) throw new Error('提交反馈失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.conversations.detail(cId) });
    },
  });
}

/** 撤销点踩（DELETE /messages/{m}/feedback，未点踩时幂等成功）。 */
export function useUnsetFeedback(cId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (messageId: number): Promise<void> => {
      const { response } = await api.DELETE('/messages/{m}/feedback', {
        params: { path: { m: messageId } },
      });
      if (!response.ok) throw new Error('撤销反馈失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.conversations.detail(cId) });
    },
  });
}

/* ---------------- SSE 提问流（核心） ---------------- */

/** refetch 会话历史 + 会话列表并 reset chatStore（含 inactive，保证换权威数据无闪烁）。 */
async function finishAndSync(
  qc: ReturnType<typeof useQueryClient>,
  kbId: number,
  cId: number,
): Promise<void> {
  await Promise.all([
    qc.refetchQueries({ queryKey: qk.conversations.detail(cId), type: 'all' }),
    qc.refetchQueries({ queryKey: qk.kbs.conversations(kbId), type: 'all' }),
  ]);
  useChatStore.getState().reset();
}

/**
 * SSE 提问/重新生成驱动：send({content}) 或 send({regenerateMessageId})。
 * 事件帧 → chatStore（chat-events 归一化）；done/interrupted 后换服务端权威数据；
 * error 保留 question 与部分 buffer 供 UI 重试卡使用（不落库，本地展示）。
 */
export function useChatStream(kbId: number, cId: number) {
  const qc = useQueryClient();

  const send = useCallback(
    async (options: { content?: string; regenerateMessageId?: number }): Promise<void> => {
      const controller = new AbortController();
      useChatStore.getState().startStream(controller, {
        question: options.content ?? null,
        regenerateMessageId: options.regenerateMessageId ?? null,
      });

      const url =
        options.regenerateMessageId !== undefined
          ? `/api/v1/messages/${options.regenerateMessageId}/regenerate`
          : `/api/v1/conversations/${cId}/messages`;

      try {
        await consumeSSE(
          url,
          {
            body: options.content !== undefined ? { content: options.content } : undefined,
            headers: { Authorization: `Bearer ${getToken() ?? ''}` },
            signal: controller.signal,
          },
          {
            onEvent: (event: SSEEvent) => {
              const s = useChatStore.getState();
              switch (event.type) {
                case 'delta': {
                  const content = normalizeDeltaContent(event.data);
                  if (content !== null) s.appendDelta(content);
                  break;
                }
                case 'citations':
                  s.setCitations(normalizeCitationItems(event.data));
                  break;
                case 'done':
                  s.markDone(normalizeDoneMessageId(event.data));
                  break;
                case 'error': {
                  const body = normalizeErrorBody(event.data);
                  if (body) s.markError(body);
                  break;
                }
                default:
                  break; // ping 心跳静默忽略（仅保活）
              }
            },
            onHttpError: (info) => {
              // 非 200（429 RATE_LIMITED / 401 / 403）：响应体是 JSON 而非事件流
              useChatStore
                .getState()
                .markError({ code: info.code, message: info.message, request_id: info.request_id });
            },
          },
        );
      } catch (err) {
        const s = useChatStore.getState();
        if (err instanceof SSEAbortError) {
          s.markInterrupted();
        } else if (err instanceof SSETimeoutError) {
          s.markError({ code: 'SYS_001', message: ERROR_MESSAGES['SYS_001'], request_id: null });
        } else if (s.status === 'streaming') {
          // 网络闪断：绝不自动重连重发（会重复提问/重复计费），转 interrupted 保留部分回答
          s.markInterrupted();
        }
        // 若已收到 done/error 帧后流的收尾异常，状态已被事件驱动，不覆盖
      }

      const status = useChatStore.getState().status;
      if (status === 'done' || status === 'interrupted') {
        // 后端已落库（interrupted 亦落库 detail 标记）：换服务端权威数据
        await finishAndSync(qc, kbId, cId);
      }
      // error：内容未落库，保留 question/buffer/error 供错误卡与重试
    },
    [cId, kbId, qc],
  );

  /** 用户点「停止生成」（AbortController.abort → interrupted）。 */
  const stop = useCallback((): void => {
    useChatStore.getState().stop();
  }, []);

  return { send, stop };
}
