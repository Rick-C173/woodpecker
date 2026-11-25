/**
 * SSE 解析器单测（架构 F2 验收：协议帧合法/非法/非 200 JSON 各用例 + abort）。
 * fetch 以 mock Response(ReadableStream) 注入，Node 22 原生支持。
 */

import { describe, expect, it } from 'vitest';
import {
  SSEAbortError,
  SSELineParser,
  SSETimeoutError,
  consumeSSE,
  type SSEEvent,
  type SSEHttpErrorInfo,
} from './sse';

/** 构造 SSE 文本流的 mock fetch。 */
function sseFetch(chunks: string[], opts?: { delayMs?: number }): typeof fetch {
  return async () => {
    const encoder = new TextEncoder();
    const stream = new ReadableStream<Uint8Array>({
      async start(controller) {
        for (const chunk of chunks) {
          if (opts?.delayMs) await sleep(opts.delayMs);
          controller.enqueue(encoder.encode(chunk));
        }
        controller.close();
      },
    });
    return new Response(stream, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
  };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

describe('SSELineParser', () => {
  it('解析 event:/data: 基本帧', () => {
    const parser = new SSELineParser();
    const events = parser.push('event: delta\ndata: {"content":"你好"}\n\n');
    expect(events).toEqual([{ type: 'delta', data: { content: '你好' } }]);
  });

  it('多行 data 以 \\n 拼接（拼接结果可 JSON 解析）', () => {
    const parser = new SSELineParser();
    const events = parser.push('event: delta\ndata: {"a":\ndata: 1}\n\n');
    expect(events).toEqual([{ type: 'delta', data: { a: 1 } }]);
  });

  it('多行 data 拼接结果非 JSON 时透传原始字符串', () => {
    const parser = new SSELineParser();
    const events = parser.push('event: delta\ndata: line one\ndata: line two\n\n');
    expect(events).toEqual([{ type: 'delta', data: 'line one\nline two' }]);
  });

  it('忽略注释行与空帧', () => {
    const parser = new SSELineParser();
    const events = parser.push(': keep-alive comment\n\n\n');
    expect(events).toEqual([]);
  });

  it('CRLF 兼容（\\r\\n 帧边界）', () => {
    const parser = new SSELineParser();
    const events = parser.push('event: ping\r\ndata: {}\r\n\r\n');
    expect(events).toEqual([{ type: 'ping', data: {} }]);
  });

  it('跨 chunk 拆行缓冲与 flush 兜底', () => {
    const parser = new SSELineParser();
    const first = parser.push('event: don');
    expect(first).toEqual([]);
    const second = parser.push('e\ndata: {"message_id":8821}\n\n');
    expect(second).toEqual([{ type: 'done', data: { message_id: 8821 } }]);
    // 无终止空行的最后一帧也能 flush 出来
    const third = parser.push('event: delta\ndata: {"content":"tail"}');
    expect(third).toEqual([]);
    const flushed = parser.flush();
    expect(flushed).toEqual([{ type: 'delta', data: { content: 'tail' } }]);
  });

  it('未知 event 类型透传（非法帧不崩溃）', () => {
    const parser = new SSELineParser();
    const events = parser.push('event: unknown\ndata: {"x":1}\n\n');
    expect(events).toEqual([{ type: 'unknown', data: { x: 1 } }]);
  });
});

describe('consumeSSE', () => {
  it('解析 delta 多帧 + citations + done 完整序列', async () => {
    const chunks = [
      'event: delta\ndata: {"content":"该接口在 "}\n\n',
      'event: delta\ndata: {"content":"internal/handler 中实现"}\n\n',
      'event: citations\ndata: {"items":[{"seq":1,"file_path":"a.go"}]}\n\n',
      'event: done\ndata: {"message_id":8821,"prompt_tokens":1820,"completion_tokens":431}\n\n',
    ];
    const events: SSEEvent[] = [];
    await consumeSSE('/api/v1/x', { fetchImpl: sseFetch(chunks) }, { onEvent: (e) => events.push(e), onHttpError: () => {} });

    expect(events.map((e) => e.type)).toEqual(['delta', 'delta', 'citations', 'done']);
    const done = events[3].data as { message_id: number; prompt_tokens: number };
    expect(done.message_id).toBe(8821);
  });

  it('ping 心跳静默透传不干扰事件序列', async () => {
    const chunks = [
      'event: ping\ndata: {}\n\n',
      'event: delta\ndata: {"content":"ok"}\n\n',
      'event: ping\ndata: {}\n\n',
      'event: done\ndata: {"message_id":1}\n\n',
    ];
    const types: string[] = [];
    await consumeSSE(
      '/api/v1/x',
      { fetchImpl: sseFetch(chunks), pingTimeoutMs: 5000 },
      { onEvent: (e) => types.push(e.type), onHttpError: () => {} },
    );
    expect(types).toEqual(['ping', 'delta', 'ping', 'done']);
  });

  it('error 帧透传错误体', async () => {
    const chunks = ['event: error\ndata: {"code":"LLM_001","message":"模型服务不可用"}\n\n'];
    const events: SSEEvent[] = [];
    await consumeSSE('/api/v1/x', { fetchImpl: sseFetch(chunks) }, { onEvent: (e) => events.push(e), onHttpError: () => {} });
    expect(events[0].type).toBe('error');
    expect(events[0].data).toEqual({ code: 'LLM_001', message: '模型服务不可用' });
  });

  it('非 200 JSON 响应走 onHttpError（不误当 SSE 帧）', async () => {
    const infos: SSEHttpErrorInfo[] = [];
    const events: SSEEvent[] = [];
    const jsonFetch: typeof fetch = async () =>
      new Response(
        JSON.stringify({ code: 'RATE_LIMITED', message: '请求过于频繁', request_id: 'req_1' }),
        { status: 429, headers: { 'Content-Type': 'application/json' } },
      );
    await consumeSSE(
      '/api/v1/x',
      { fetchImpl: jsonFetch },
      { onEvent: (e) => events.push(e), onHttpError: (info) => infos.push(info) },
    );
    expect(events).toHaveLength(0);
    expect(infos).toHaveLength(1);
    expect(infos[0]).toEqual({
      status: 429,
      code: 'RATE_LIMITED',
      message: '请求过于频繁',
      request_id: 'req_1',
    });
  });

  it('401 JSON 错误响应同样走 onHttpError', async () => {
    const infos: SSEHttpErrorInfo[] = [];
    const jsonFetch: typeof fetch = async () =>
      new Response(JSON.stringify({ code: 'AUTH_004', message: 'token 失效', request_id: 'r' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      });
    await consumeSSE(
      '/api/v1/x',
      { fetchImpl: jsonFetch },
      { onEvent: () => {}, onHttpError: (info) => infos.push(info) },
    );
    expect(infos[0].status).toBe(401);
    expect(infos[0].code).toBe('AUTH_004');
  });

  it('外部 abort 抛 SSEAbortError', async () => {
    const controller = new AbortController();
    // 流永不结束，等 abort
    const neverFetch: typeof fetch = async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start() {
            /* 不 enqueue 不 close */
          },
        }),
        { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
      );
    const promise = consumeSSE(
      '/api/v1/x',
      { fetchImpl: neverFetch, signal: controller.signal, pingTimeoutMs: 10_000 },
      { onEvent: () => {}, onHttpError: () => {} },
    );
    await sleep(20);
    controller.abort();
    await expect(promise).rejects.toBeInstanceOf(SSEAbortError);
  });

  it('超时无事件抛 SSETimeoutError', async () => {
    const neverFetch: typeof fetch = async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start() {
            /* 挂起 */
          },
        }),
        { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
      );
    const promise = consumeSSE(
      '/api/v1/x',
      { fetchImpl: neverFetch, pingTimeoutMs: 30 },
      { onEvent: () => {}, onHttpError: () => {} },
    );
    await expect(promise).rejects.toBeInstanceOf(SSETimeoutError);
  });

  it('POST 请求携带 JSON body 与 Accept 头', async () => {
    const captured: { init?: RequestInit } = {};
    const captureFetch: typeof fetch = async (_url, init) => {
      captured.init = init;
      return new Response('event: done\ndata: {"message_id":1}\n\n', {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
      });
    };
    await consumeSSE(
      '/api/v1/conversations/1/messages',
      { fetchImpl: captureFetch, body: { content: '你好' }, headers: { Authorization: 'Bearer t' } },
      { onEvent: () => {}, onHttpError: () => {} },
    );
    const init = captured.init;
    expect(init?.method).toBe('POST');
    const headers = init?.headers as Record<string, string>;
    expect(headers['Accept']).toBe('text/event-stream');
    expect(headers['Content-Type']).toBe('application/json');
    expect(init?.body).toBe(JSON.stringify({ content: '你好' }));
  });
});
