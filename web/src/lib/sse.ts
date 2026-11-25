/**
 * SSE 解析器（架构 §1.3 / §4.2）：fetch + ReadableStream + 逐行解析。
 * 协议（API设计.md §4）：event: delta/citations/done/error/ping + data: JSON 帧；
 * 心跳每 15s；多行 data 按 SSE 规范以 \n 拼接；注释行（: 开头）忽略；CRLF 兼容。
 *
 * 硬性约定：
 * - 非 200 响应先 response.ok 判定，把 JSON 错误体交给统一错误处理（不要误当 SSE 帧）。
 * - 45s（3 个心跳周期）无任何事件且流未结束 → 主动 abort 抛 SSETimeoutError（可配）。
 * - 断连不自动重连（服务端已落库 interrupted，重连会导致重复提问/重复计费）。
 */

import { ApiError } from './errors';

export type SSEEventType = 'delta' | 'citations' | 'done' | 'error' | 'ping';

export interface SSEEvent {
  type: SSEEventType;
  data: unknown;
}

/** 非 200 响应的 JSON 错误体（429 RATE_LIMITED、401 等）。 */
export interface SSEHttpErrorInfo {
  status: number;
  code: string;
  message: string;
  request_id: string | null;
}

export interface SSECallbacks {
  /** 协议事件回调（ping 也会回调，消费方静默忽略即可）。 */
  onEvent: (event: SSEEvent) => void;
  /** 非 200 响应（响应体是 JSON 而非事件流）。 */
  onHttpError: (info: SSEHttpErrorInfo) => void;
}

export interface SSEOptions {
  method?: string;
  headers?: Record<string, string>;
  body?: unknown;
  /** 外部中止信号（用户点停止 → AbortController.abort()）。 */
  signal?: AbortSignal;
  /** 无事件超时（ms），默认 45000（3 个 15s 心跳周期）。 */
  pingTimeoutMs?: number;
  /** 依赖注入（单测用 mock fetch）。 */
  fetchImpl?: typeof fetch;
}

/** 用户主动中止（点停止生成）——不是错误，转 interrupted。 */
export class SSEAbortError extends Error {
  constructor() {
    super('SSE 已被用户中止');
    this.name = 'SSEAbortError';
  }
}

/** 心跳超时（45s 无任何事件）——防悬挂，转 error。 */
export class SSETimeoutError extends Error {
  constructor() {
    super('SSE 心跳超时');
    this.name = 'SSETimeoutError';
  }
}

/**
 * 逐行 SSE 解析器（可独立单测）。
 * 帧规则：event: 名称；data: 载荷（多行以 \n 拼接）；空行分发帧；`:xxx` 注释忽略。
 */
export class SSELineParser {
  private buffer = '';
  private eventName: string | null = null;
  private dataLines: string[] = [];

  /** 追加一块收到的文本，返回解析出的完整帧。 */
  push(chunk: string): SSEEvent[] {
    this.buffer += chunk;
    const events: SSEEvent[] = [];
    // CRLF 兼容：按 \r?\n 切分，最后一段留作不完整行缓冲
    const lines = this.buffer.split(/\r?\n/);
    this.buffer = lines.pop() ?? '';
    for (const line of lines) {
      const event = this.handleLine(line);
      if (event) events.push(event);
    }
    return events;
  }

  /** 流结束后冲刷残留缓冲（无终止空行的最后一帧）。 */
  flush(): SSEEvent[] {
    const remaining = this.buffer;
    this.buffer = '';
    const event = remaining.length > 0 ? this.handleLine(remaining) : null;
    const final = this.handleLine(''); // 兜底分发未结束的帧
    return event ? [event, ...(final ? [final] : [])] : final ? [final] : [];
  }

  private handleLine(line: string): SSEEvent | null {
    // 空行 = 帧边界
    if (line === '') {
      return this.dispatch();
    }
    // 注释行忽略
    if (line.startsWith(':')) {
      return null;
    }
    const colon = line.indexOf(':');
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? '' : line.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1); // 规范：去掉紧跟的一个空格

    switch (field) {
      case 'event':
        this.eventName = value;
        return null;
      case 'data':
        this.dataLines.push(value);
        return null;
      default:
        // retry: / id: 等其他字段忽略
        return null;
    }
  }

  private dispatch(): SSEEvent | null {
    const type = this.eventName ?? 'message';
    this.eventName = null;
    if (this.dataLines.length === 0) return null; // 纯空行无载荷
    const rawData = this.dataLines.join('\n'); // 多行 data 以 \n 拼接
    this.dataLines = [];
    let data: unknown = rawData;
    try {
      data = JSON.parse(rawData);
    } catch {
      /* 载荷非 JSON 时透传原始字符串 */
    }
    return { type, data } as SSEEvent;
  }
}

/**
 * 消费一次 SSE 流：POST 提问并解析事件帧，直到流结束 / 中止 / 超时。
 * - 正常结束（含收到 done/error 帧）→ resolve。
 * - 外部 signal 中止 → reject SSEAbortError。
 * - pingTimeoutMs 无事件 → reject SSETimeoutError。
 * - 网络异常 → reject 原始 Error。
 */
export async function consumeSSE(
  url: string,
  options: SSEOptions,
  callbacks: SSECallbacks,
): Promise<void> {
  const {
    method = 'POST',
    headers = {},
    body,
    signal,
    pingTimeoutMs = 45_000,
    fetchImpl = fetch,
  } = options;

  // 内部控制器：合并外部 signal 与心跳超时。注意：abort 不一定经 fetch 传播到
  // 读循环（mock fetch / 部分环境不终止流），故用 abortPromise 与读循环竞速兜底。
  const internalController = new AbortController();
  const onExternalAbort = () => internalController.abort();
  if (signal) {
    if (signal.aborted) throw new SSEAbortError();
    signal.addEventListener('abort', onExternalAbort, { once: true });
  }

  // 心跳超时：任何事件（含 ping）都会重置计时
  let timedOut = false;
  let timeoutTimer: ReturnType<typeof setTimeout> | null = null;
  const armTimeout = () => {
    if (timeoutTimer !== null) clearTimeout(timeoutTimer);
    timeoutTimer = setTimeout(() => {
      timedOut = true;
      internalController.abort();
    }, pingTimeoutMs);
  };
  const disarmTimeout = () => {
    if (timeoutTimer !== null) {
      clearTimeout(timeoutTimer);
      timeoutTimer = null;
    }
  };

  const cleanup = () => {
    disarmTimeout();
    if (signal) signal.removeEventListener('abort', onExternalAbort);
  };

  /** 内部 controller 一旦 abort（外部中止或超时），立即以对应错误竞速打断读循环。 */
  const abortPromise = new Promise<never>((_, reject) => {
    internalController.signal.addEventListener(
      'abort',
      () => {
        if (signal?.aborted) {
          reject(new SSEAbortError());
        } else {
          timedOut = true;
          reject(new SSETimeoutError());
        }
      },
      { once: true },
    );
  });

  const init: RequestInit = {
    method,
    headers: {
      Accept: 'text/event-stream',
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...headers,
    },
    signal: internalController.signal,
  };
  if (body !== undefined) init.body = JSON.stringify(body);

  let response: Response;
  try {
    armTimeout();
    response = await fetchImpl(url, init);
  } catch (err) {
    cleanup();
    if (timedOut) throw new SSETimeoutError();
    if (signal?.aborted) throw new SSEAbortError();
    throw err;
  }

  // 硬性约定：非 200 先判定，响应体是 JSON 错误体而非事件流
  if (!response.ok) {
    cleanup();
    const info = await parseJsonErrorBody(response);
    callbacks.onHttpError(info);
    return;
  }
  if (!response.body) {
    cleanup();
    throw new Error('SSE 响应缺少可读流');
  }

  const parser = new SSELineParser();
  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8');

  const readLoop = async (): Promise<void> => {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      armTimeout(); // 收到任何字节即视为活跃，重置心跳计时
      for (const event of parser.push(decoder.decode(value, { stream: true }))) {
        callbacks.onEvent(event);
      }
    }
    for (const event of parser.push(decoder.decode())) {
      callbacks.onEvent(event);
    }
    for (const event of parser.flush()) {
      callbacks.onEvent(event);
    }
  };

  try {
    await Promise.race([readLoop(), abortPromise]);
  } catch (err) {
    if (err instanceof SSETimeoutError || err instanceof SSEAbortError) throw err;
    if (timedOut) throw new SSETimeoutError();
    if (signal?.aborted) throw new SSEAbortError();
    throw err;
  } finally {
    cleanup();
    reader.releaseLock();
  }
}

/** 解析非 200 响应的 JSON 错误体为 {code,message,request_id}。 */
async function parseJsonErrorBody(response: Response): Promise<SSEHttpErrorInfo> {
  let body: { code?: string; message?: string; request_id?: string } = {};
  try {
    const text = await response.text();
    if (text) body = JSON.parse(text) as typeof body;
  } catch {
    /* 非 JSON 错误体：保持空对象走兜底文案 */
  }
  return {
    status: response.status,
    code: body.code ?? 'INTERNAL',
    message: body.message ?? '服务暂时不可用，请稍后重试',
    request_id: body.request_id ?? null,
  };
}

/** 便捷封装：把非 200 JSON 错误体直接抛为 ApiError（供 prefer 抛错的调用方）。 */
export async function consumeSSEOrThrow(
  url: string,
  options: SSEOptions,
  callbacks: Pick<SSECallbacks, 'onEvent'>,
): Promise<void> {
  return consumeSSE(url, options, {
    onEvent: callbacks.onEvent,
    onHttpError: (info) => {
      throw new ApiError({ code: info.code, message: info.message, request_id: info.request_id }, info.status);
    },
  });
}
