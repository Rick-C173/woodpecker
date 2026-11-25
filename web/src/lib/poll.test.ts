/** poll.ts 单测：函数式 refetchInterval 的启停判定（架构 §4.4 验收）。 */

import { describe, expect, it } from 'vitest';
import {
  DOCUMENT_POLL_INTERVAL_MS,
  documentsRefetchInterval,
  isActiveDocument,
} from './poll';

describe('isActiveDocument', () => {
  it('pending/processing 为活跃态', () => {
    expect(isActiveDocument({ status: 'pending' })).toBe(true);
    expect(isActiveDocument({ status: 'processing' })).toBe(true);
  });

  it('ready/failed 为终态', () => {
    expect(isActiveDocument({ status: 'ready' })).toBe(false);
    expect(isActiveDocument({ status: 'failed' })).toBe(false);
  });

  it('缺 status 视为非活跃', () => {
    expect(isActiveDocument({})).toBe(false);
    expect(isActiveDocument(null)).toBe(false);
  });
});

describe('documentsRefetchInterval', () => {
  const wrap = (data: unknown) => ({ state: { data } });

  it('列表存在活跃文档 → 3000ms', () => {
    const q = wrap({ items: [{ status: 'ready' }, { status: 'processing' }] });
    expect(documentsRefetchInterval(q)).toBe(DOCUMENT_POLL_INTERVAL_MS);
  });

  it('列表全部终态 → false（停止轮询）', () => {
    const q = wrap({ items: [{ status: 'ready' }, { status: 'failed' }] });
    expect(documentsRefetchInterval(q)).toBe(false);
  });

  it('pending 也算活跃态', () => {
    const q = wrap({ items: [{ status: 'pending' }] });
    expect(documentsRefetchInterval(q)).toBe(DOCUMENT_POLL_INTERVAL_MS);
  });

  it('详情单对象形态同样支持', () => {
    expect(documentsRefetchInterval(wrap({ status: 'processing', stage: 'embedding' }))).toBe(
      DOCUMENT_POLL_INTERVAL_MS,
    );
    expect(documentsRefetchInterval(wrap({ status: 'ready' }))).toBe(false);
  });

  it('数据缺失/非法形态 → false', () => {
    expect(documentsRefetchInterval(wrap(null))).toBe(false);
    expect(documentsRefetchInterval(wrap(undefined))).toBe(false);
    expect(documentsRefetchInterval(wrap('ready'))).toBe(false);
  });

  it('items 非数组时按单对象处理', () => {
    expect(documentsRefetchInterval(wrap({ items: undefined, status: 'pending' }))).toBe(
      DOCUMENT_POLL_INTERVAL_MS,
    );
  });
});
