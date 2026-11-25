/** errors.ts 单测：27 码存在性 + 未知码兜底（F2 验收）。 */

import { describe, expect, it } from 'vitest';
import {
  ApiError,
  ERROR_MESSAGES,
  KNOWN_ERROR_CODES,
  formatErrorMessage,
  formatRequestId,
} from './errors';

/** API设计.md §2 错误码全表（27 码，含 response.go 的 INTERNAL 兜底码）。 */
const EXPECTED_CODES = [
  'AUTH_001',
  'AUTH_002',
  'AUTH_003',
  'AUTH_004',
  'AUTH_005',
  'AUTH_006',
  'AUTH_007',
  'USER_001',
  'USER_002',
  'USER_003',
  'KB_001',
  'DOC_001',
  'DOC_002',
  'DOC_003',
  'DOC_004',
  'MODEL_001',
  'MODEL_002',
  'MODEL_003',
  'MODEL_004',
  'LLM_001',
  'LLM_002',
  'SYS_001',
  'DOC_TASK_RUNNING',
  'RATE_LIMITED',
  'NOT_FOUND',
  'VALIDATION_FAILED',
  'INTERNAL',
] as const;

describe('ERROR_MESSAGES 覆盖度', () => {
  it('27 个错误码全部有中文文案映射', () => {
    for (const code of EXPECTED_CODES) {
      const message = ERROR_MESSAGES[code];
      expect(message, `错误码 ${code} 缺少文案`).toBeDefined();
      expect(message.length, `错误码 ${code} 文案为空`).toBeGreaterThan(0);
    }
  });

  it('KNOWN_ERROR_CODES 与预期清单一致', () => {
    expect([...KNOWN_ERROR_CODES].sort()).toEqual([...EXPECTED_CODES].sort());
    expect(KNOWN_ERROR_CODES).toHaveLength(27);
  });

  it('文案为中文且不含原始错误码字面量', () => {
    for (const code of EXPECTED_CODES) {
      expect(ERROR_MESSAGES[code]).not.toContain(code);
    }
  });
});

describe('formatErrorMessage 兜底', () => {
  it('已知码走映射表', () => {
    expect(formatErrorMessage({ code: 'AUTH_001', message: 'bad credentials' })).toBe(
      '用户名或密码错误',
    );
    expect(formatErrorMessage({ code: 'RATE_LIMITED' })).toBe('操作过于频繁，请稍后再试');
  });

  it('未知码兜底展示后端 message', () => {
    expect(
      formatErrorMessage({ code: 'SOME_FUTURE_CODE', message: '后端新错误' }),
    ).toBe('后端新错误');
  });

  it('完全未知结构兜底：无 code 归一化为 INTERNAL 文案，未知码无 message 用通用文案', () => {
    expect(formatErrorMessage({})).toBe(ERROR_MESSAGES['INTERNAL']);
    expect(formatErrorMessage({ code: 'X_Y', message: '' })).toBe('操作失败，请稍后重试');
  });
});

describe('ApiError', () => {
  it('携带 code / request_id / status 并生成兜底 message', () => {
    const err = new ApiError({ code: 'KB_001', message: '知识库名称已存在', request_id: 'req_1' }, 409);
    expect(err.code).toBe('KB_001');
    expect(err.message).toBe('知识库名称已存在');
    expect(err.request_id).toBe('req_1');
    expect(err.status).toBe(409);
  });

  it('空错误体归一化为 INTERNAL 兜底', () => {
    const err = new ApiError({}, null);
    expect(err.code).toBe('INTERNAL');
    expect(err.message).toBe(ERROR_MESSAGES['INTERNAL']);
  });
});

describe('formatRequestId', () => {
  it('有 id 输出展示串，无 id 输出空串', () => {
    expect(formatRequestId('req_01H')).toBe('request_id: req_01H');
    expect(formatRequestId(null)).toBe('');
    expect(formatRequestId(undefined)).toBe('');
  });
});
