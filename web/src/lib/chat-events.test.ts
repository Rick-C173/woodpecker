/**
 * chat-events 纯逻辑单测（F5 验收：SSE 事件到 store 的映射、点踩 payload 组装、
 * 行内引用分段）。不依赖 DOM / 网络环境。
 */

import { describe, expect, it } from 'vitest';
import {
  buildFeedbackPayload,
  normalizeCitationItems,
  normalizeDeltaContent,
  normalizeDoneMessageId,
  normalizeErrorBody,
  splitInlineCitations,
} from './chat-events';

describe('buildFeedbackPayload（点踩 payload 组装）', () => {
  it('原因 1-3 不带说明：comment 字段省略', () => {
    const r = buildFeedbackPayload(1, '   ');
    expect(r).toEqual({ ok: true, payload: { reason: 1 } });
  });

  it('原因 1-3 带说明：去重首尾空白后携带', () => {
    const r = buildFeedbackPayload(2, ' 引用 [1] 与代码不符  ');
    expect(r).toEqual({ ok: true, payload: { reason: 2, comment: '引用 [1] 与代码不符' } });
  });

  it('原因 4（其他）说明必填：空串拒绝', () => {
    const r = buildFeedbackPayload(4, '   ');
    expect(r).toEqual({ ok: false, error: '选择「其他」时请填写补充说明' });
  });

  it('原因 4（其他）带说明：通过', () => {
    const r = buildFeedbackPayload(4, '回答偏向设计文档而非实现');
    expect(r).toEqual({ ok: true, payload: { reason: 4, comment: '回答偏向设计文档而非实现' } });
  });

  it('非法 reason 拒绝', () => {
    // @ts-expect-error 故意传非法值验证运行时防御
    const r = buildFeedbackPayload(9, 'x');
    expect(r).toEqual({ ok: false, error: '请选择点踩原因' });
  });
});

describe('SSE 事件帧归一化', () => {
  it('normalizeDeltaContent：合法 delta 提取 content', () => {
    expect(normalizeDeltaContent({ content: '该接口在 ' })).toBe('该接口在 ');
  });

  it('normalizeDeltaContent：结构非法返回 null（静默忽略）', () => {
    expect(normalizeDeltaContent({ wrong: 1 })).toBeNull();
    expect(normalizeDeltaContent('raw string')).toBeNull();
    expect(normalizeDeltaContent(null)).toBeNull();
  });

  it('normalizeCitationItems：完整字段透传', () => {
    const items = normalizeCitationItems({
      items: [
        {
          seq: 1,
          document_id: 12,
          file_path: 'internal/handler/auth.go',
          line_start: 45,
          line_end: 62,
          heading_path: null,
          source_url: null,
          snippet: 'func Login(c *gin.Context)...',
        },
      ],
    });
    expect(items).toEqual([
      {
        seq: 1,
        document_id: 12,
        file_path: 'internal/handler/auth.go',
        line_start: 45,
        line_end: 62,
        heading_path: null,
        source_url: null,
        snippet: 'func Login(c *gin.Context)...',
      },
    ]);
  });

  it('normalizeCitationItems：字段缺省归一化，seq 缺省按顺序补号', () => {
    const items = normalizeCitationItems({ items: [{ file_path: 'a.go' }, { snippet: 'x' }] });
    expect(items[0].seq).toBe(1);
    expect(items[0].line_start).toBeNull();
    expect(items[0].document_id).toBe(0);
    expect(items[1].seq).toBe(2);
    expect(items[1].file_path).toBeNull();
  });

  it('normalizeCitationItems：非 JSON / 缺 items / 非数组 → 空数组', () => {
    expect(normalizeCitationItems(undefined)).toEqual([]);
    expect(normalizeCitationItems({})).toEqual([]);
    expect(normalizeCitationItems({ items: 'nope' })).toEqual([]);
  });

  it('normalizeDoneMessageId：合法帧提取 message_id', () => {
    expect(normalizeDoneMessageId({ message_id: 8821 })).toBe(8821);
    expect(normalizeDoneMessageId({})).toBeNull();
    expect(normalizeDoneMessageId('x')).toBeNull();
  });

  it('normalizeErrorBody：error 帧字段补齐兜底', () => {
    expect(normalizeErrorBody({ code: 'LLM_001', message: '模型服务不可用' })).toEqual({
      code: 'LLM_001',
      message: '模型服务不可用',
      request_id: null,
    });
    expect(normalizeErrorBody({})).toEqual({
      code: 'INTERNAL',
      message: '模型服务返回异常',
      request_id: null,
    });
    expect(normalizeErrorBody('garbage')).toBeNull();
  });
});

describe('splitInlineCitations（行内引用分段）', () => {
  it('混合文本与引用编号', () => {
    const segs = splitInlineCitations('限流在 middleware 中实现[1]，锁在 auth.go[12] 里。');
    expect(segs).toEqual([
      { type: 'text', value: '限流在 middleware 中实现', seq: null },
      { type: 'cite', value: '[1]', seq: 1 },
      { type: 'text', value: '，锁在 auth.go', seq: null },
      { type: 'cite', value: '[12]', seq: 12 },
      { type: 'text', value: ' 里。', seq: null },
    ]);
  });

  it('开头即为引用编号', () => {
    const segs = splitInlineCitations('[2] 开头的引用');
    expect(segs).toEqual([
      { type: 'cite', value: '[2]', seq: 2 },
      { type: 'text', value: ' 开头的引用', seq: null },
    ]);
  });

  it('无引用编号：整段为 text', () => {
    const segs = splitInlineCitations('依据不足的普通回答。');
    expect(segs).toEqual([{ type: 'text', value: '依据不足的普通回答。', seq: null }]);
  });

  it('空串返回空数组', () => {
    expect(splitInlineCitations('')).toEqual([]);
  });

  it('三位数 [123] 不误匹配（白名单 1-2 位）', () => {
    const segs = splitInlineCitations('数组下标 arr[123] 保持原样');
    expect(segs).toEqual([{ type: 'text', value: '数组下标 arr[123] 保持原样', seq: null }]);
  });
});
