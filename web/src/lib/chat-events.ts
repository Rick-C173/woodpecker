/**
 * 聊天域纯逻辑（F5）：SSE 事件帧 → chatStore 数据的归一化映射、
 * 点踩 payload 组装（PRD FR-14：四原因单选、其他必填说明）、
 * 回答正文 [n] 行内引用分段（引用徽标渲染用）。
 * 全部为纯函数，便于单测（chat-events.test.ts）。
 */

import type { Citation, ChatErrorBody } from '@/stores/chat-store';

/* ---------------- 点踩（FR-14） ---------------- */

/** 点踩原因 1-4（API设计.md §3.7：1 答案不相关 2 引用错误 3 内容过时 4 其他）。 */
export type FeedbackReason = 1 | 2 | 3 | 4;

export const FEEDBACK_REASON_LABELS: Record<FeedbackReason, string> = {
  1: '答案不相关',
  2: '引用错误',
  3: '内容过时',
  4: '其他',
};

export const FEEDBACK_REASONS: readonly FeedbackReason[] = [1, 2, 3, 4];

export interface FeedbackPayload {
  reason: FeedbackReason;
  comment?: string;
}

export type FeedbackPayloadResult =
  | { ok: true; payload: FeedbackPayload }
  | { ok: false; error: string };

/**
 * 组装点踩请求体：reason 1-3 说明可选；reason 4（其他）说明必填。
 * comment 首尾去重空白，空串时省略字段。
 */
export function buildFeedbackPayload(
  reason: FeedbackReason,
  comment: string,
): FeedbackPayloadResult {
  if (!FEEDBACK_REASONS.includes(reason)) {
    return { ok: false, error: '请选择点踩原因' };
  }
  const trimmed = comment.trim();
  if (reason === 4 && trimmed.length === 0) {
    return { ok: false, error: '选择「其他」时请填写补充说明' };
  }
  return {
    ok: true,
    payload: trimmed.length > 0 ? { reason, comment: trimmed } : { reason },
  };
}

/* ---------------- SSE 事件 → store 数据归一化 ---------------- */

/**
 * delta 帧 {"content": "..."} → 增量文本；结构非法返回 null（静默忽略）。
 */
export function normalizeDeltaContent(data: unknown): string | null {
  if (typeof data === 'object' && data !== null && 'content' in data) {
    const content = (data as { content?: unknown }).content;
    if (typeof content === 'string') return content;
  }
  return null;
}

/**
 * citations 帧 {"items":[...]} → Citation[]（字段缺省归一化为 null/0）。
 * 非 JSON / 缺 items 返回空数组（「依据不足」场景无 citations 帧，此处仅防御）。
 */
export function normalizeCitationItems(data: unknown): Citation[] {
  if (typeof data !== 'object' || data === null || !('items' in data)) return [];
  const items = (data as { items?: unknown }).items;
  if (!Array.isArray(items)) return [];
  const result: Citation[] = [];
  for (const raw of items) {
    if (typeof raw !== 'object' || raw === null) continue;
    const it = raw as Record<string, unknown>;
    result.push({
      seq: typeof it.seq === 'number' ? it.seq : result.length + 1,
      document_id: typeof it.document_id === 'number' ? it.document_id : 0,
      file_path: typeof it.file_path === 'string' ? it.file_path : null,
      line_start: typeof it.line_start === 'number' ? it.line_start : null,
      line_end: typeof it.line_end === 'number' ? it.line_end : null,
      heading_path: typeof it.heading_path === 'string' ? it.heading_path : null,
      source_url: typeof it.source_url === 'string' ? it.source_url : null,
      snippet: typeof it.snippet === 'string' ? it.snippet : '',
    });
  }
  return result;
}

/** done 帧 {"message_id": n} → 消息 ID；缺失返回 null。 */
export function normalizeDoneMessageId(data: unknown): number | null {
  if (typeof data === 'object' && data !== null && 'message_id' in data) {
    const id = (data as { message_id?: unknown }).message_id;
    if (typeof id === 'number') return id;
  }
  return null;
}

/** error 帧 {"code","message","request_id"} → ChatErrorBody；缺省字段走兜底文案码。 */
export function normalizeErrorBody(data: unknown): ChatErrorBody | null {
  if (typeof data !== 'object' || data === null) return null;
  const it = data as Record<string, unknown>;
  return {
    code: typeof it.code === 'string' ? it.code : 'INTERNAL',
    message: typeof it.message === 'string' ? it.message : '模型服务返回异常',
    request_id: typeof it.request_id === 'string' ? it.request_id : null,
  };
}

/* ---------------- 回答正文 [n] 行内引用分段 ---------------- */

export interface InlineSegment {
  type: 'text' | 'cite';
  value: string;
  /** cite 段的引用序号；text 段为 null */
  seq: number | null;
}

const CITE_PATTERN = /\[(\d{1,2})\]/g;

/**
 * 把回答正文按 [n] 引用编号切成 text / cite 段序列。
 * Markdown 链接语法 [text](url) 经 remark 解析后不会以 "[n]" 文本节点出现，
 * 此处只处理 remark 保留为字面文本的 [n]（与后端 seq 语义一致）。
 */
export function splitInlineCitations(text: string): InlineSegment[] {
  if (text.length === 0) return [];
  const segments: InlineSegment[] = [];
  let last = 0;
  for (const match of text.matchAll(CITE_PATTERN)) {
    const start = match.index ?? 0;
    if (start > last) {
      segments.push({ type: 'text', value: text.slice(last, start), seq: null });
    }
    segments.push({ type: 'cite', value: match[0], seq: Number(match[1]) });
    last = start + match[0].length;
  }
  if (last < text.length) {
    segments.push({ type: 'text', value: text.slice(last), seq: null });
  }
  return segments;
}
