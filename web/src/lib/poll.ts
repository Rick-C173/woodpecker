/**
 * 文档状态轮询（架构 §4.4）：函数式 refetchInterval，3s 周期，数据驱动自动启停。
 * pending 与 processing 都算活跃态；全部终态（ready/failed）自动停止。
 * 纯函数实现（不耦合 React），供列表与详情查询共用 + 单测。
 */

export const DOCUMENT_POLL_INTERVAL_MS = 3000;

/** 活跃态：可能随时进入下一状态，需要继续轮询。 */
export const ACTIVE_DOCUMENT_STATUSES: readonly string[] = ['pending', 'processing'];

/** 单个文档是否处于活跃态（status 为 pending/processing）。 */
export function isActiveDocument(doc: unknown): boolean {
  const status = (doc as { status?: unknown } | null)?.status;
  return status === 'pending' || status === 'processing';
}

/**
 * TanStack Query 函数式 refetchInterval：
 * 兼容两种数据形态——文档列表 {items: [...]} 与文档详情单对象。
 * 任一活跃态文档存在 → 3000ms；否则 false（停止轮询）。
 */
export function documentsRefetchInterval(query: { state: { data: unknown } }): number | false {
  const data = query.state.data;
  if (data === null || data === undefined || typeof data !== 'object') return false;

  const raw = data as { items?: unknown };
  const items = Array.isArray(raw.items) ? raw.items : [data];
  return items.some(isActiveDocument) ? DOCUMENT_POLL_INTERVAL_MS : false;
}
