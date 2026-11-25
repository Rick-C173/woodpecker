/**
 * chatStore 状态机转换单测（F5 验收：§4.2 状态机全路径）。
 * 纯 zustand store，直接驱动并断言状态切片。
 */

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useChatStore } from './chat-store';

function state() {
  return useChatStore.getState();
}

/** 状态机转换断言辅助：非法/合法迁移一览。 */
describe('chatStore 状态机（§4.2）', () => {
  beforeEach(() => {
    useChatStore.getState().reset();
  });

  it('初始为 idle，全部瞬态为空', () => {
    const s = state();
    expect(s.status).toBe('idle');
    expect(s.buffer).toBe('');
    expect(s.citations).toEqual([]);
    expect(s.error).toBeNull();
    expect(s.controller).toBeNull();
    expect(s.messageId).toBeNull();
    expect(s.question).toBeNull();
    expect(s.regenerateMessageId).toBeNull();
  });

  it('idle → streaming：startStream 清空缓冲并记录 question', () => {
    const controller = new AbortController();
    state().startStream(controller, { question: '限流怎么实现的？' });
    const s = state();
    expect(s.status).toBe('streaming');
    expect(s.controller).toBe(controller);
    expect(s.question).toBe('限流怎么实现的？');
    expect(s.regenerateMessageId).toBeNull();
    expect(s.buffer).toBe('');
  });

  it('streaming --delta--> 累加缓冲；citations 一次性设置', () => {
    state().startStream(new AbortController());
    state().appendDelta('该接口在 ');
    state().appendDelta('internal/handler 中实现');
    expect(state().buffer).toBe('该接口在 internal/handler 中实现');
    state().setCitations([
      {
        seq: 1,
        document_id: 1,
        file_path: 'a.go',
        line_start: 1,
        line_end: 2,
        heading_path: null,
        source_url: null,
        snippet: 'func A()',
      },
    ]);
    expect(state().citations).toHaveLength(1);
    expect(state().citations[0]?.seq).toBe(1);
  });

  it('streaming → done：记录 messageId、释放 controller', () => {
    state().startStream(new AbortController());
    state().appendDelta('完整回答');
    state().markDone(8821);
    const s = state();
    expect(s.status).toBe('done');
    expect(s.messageId).toBe(8821);
    expect(s.controller).toBeNull();
    // buffer 保留：done 后 UI 以服务端数据替换，reset 由 use-chat 在 refetch 后调用
    expect(s.buffer).not.toBe('');
  });

  it('streaming → interrupted（用户点停止）：无 error、controller 释放', () => {
    state().startStream(new AbortController());
    state().appendDelta('部分回答');
    state().markInterrupted();
    const s = state();
    expect(s.status).toBe('interrupted');
    expect(s.error).toBeNull();
    expect(s.controller).toBeNull();
    expect(s.buffer).toBe('部分回答');
  });

  it('streaming → error：记录错误体、保留 buffer（部分内容 + 重试卡）', () => {
    state().startStream(new AbortController(), { question: '什么是 X' });
    state().appendDelta('部分回答');
    state().markError({ code: 'LLM_001', message: '模型服务暂时不可用', request_id: 'req_1' });
    const s = state();
    expect(s.status).toBe('error');
    expect(s.error?.code).toBe('LLM_001');
    expect(s.error?.request_id).toBe('req_1');
    expect(s.question).toBe('什么是 X');
    expect(s.buffer).toBe('部分回答');
  });

  it('stop()：abort 当前 controller（触发 SSEAbortError → interrupted）', () => {
    const controller = new AbortController();
    const abortSpy = vi.spyOn(controller, 'abort');
    state().startStream(controller);
    state().stop();
    expect(abortSpy).toHaveBeenCalledOnce();
  });

  it('done/interrupted/error → streaming：重新提问重置全部瞬态', () => {
    // error → streaming（重试）
    state().markError({ code: 'LLM_001', message: 'x', request_id: null });
    const c1 = new AbortController();
    state().startStream(c1, { question: '重试提问' });
    expect(state().status).toBe('streaming');
    expect(state().error).toBeNull();
    expect(state().buffer).toBe('');
    expect(state().citations).toEqual([]);
    expect(state().messageId).toBeNull();

    // done → streaming（regenerate：带 regenerateMessageId）
    state().markDone(42);
    const c2 = new AbortController();
    state().startStream(c2, { regenerateMessageId: 42 });
    expect(state().status).toBe('streaming');
    expect(state().regenerateMessageId).toBe(42);
    expect(state().question).toBeNull();

    // interrupted → streaming
    state().markInterrupted();
    state().startStream(new AbortController());
    expect(state().status).toBe('streaming');
  });

  it('reset()：任意状态回 idle 并清空瞬态', () => {
    state().startStream(new AbortController(), { question: 'q' });
    state().appendDelta('abc');
    state().setCitations([
      {
        seq: 1,
        document_id: 1,
        file_path: null,
        line_start: null,
        line_end: null,
        heading_path: null,
        source_url: null,
        snippet: 's',
      },
    ]);
    state().reset();
    const s = state();
    expect(s.status).toBe('idle');
    expect(s.buffer).toBe('');
    expect(s.citations).toEqual([]);
    expect(s.question).toBeNull();
    expect(s.regenerateMessageId).toBeNull();
    expect(s.messageId).toBeNull();
  });
});
