/** ingest.ts 单测：上传预校验（R1/R2）与 ignore_rules 解析。 */

import { describe, expect, it } from 'vitest';
import {
  MAX_UPLOAD_FILES,
  MAX_UPLOAD_FILE_BYTES,
  isSupportedFileName,
  parseIgnoreRules,
  validateUploadFiles,
} from './ingest';

function makeFile(name: string, size: number): File {
  return new File(['x'.repeat(Math.max(size, 0))], name);
}

describe('isSupportedFileName（R1）', () => {
  it('支持 Markdown/文本/代码', () => {
    expect(isSupportedFileName('README.md')).toBe(true);
    expect(isSupportedFileName('notes.markdown')).toBe(true);
    expect(isSupportedFileName('main.go')).toBe(true);
    expect(isSupportedFileName('app.tsx')).toBe(true);
    expect(isSupportedFileName('config.yaml')).toBe(true);
    expect(isSupportedFileName('dockerfile')).toBe(true);
    expect(isSupportedFileName('Makefile')).toBe(true);
  });

  it('拒绝不受支持的扩展名', () => {
    expect(isSupportedFileName('photo.png')).toBe(false);
    expect(isSupportedFileName('archive.zip')).toBe(false);
    expect(isSupportedFileName('binary.exe')).toBe(false);
  });

  it('拒绝无扩展名与隐藏文件', () => {
    expect(isSupportedFileName('LICENSE')).toBe(false);
    expect(isSupportedFileName('.gitignore')).toBe(false);
  });
});

describe('validateUploadFiles（R2）', () => {
  it('放行合法文件', () => {
    const { accepted, errors } = validateUploadFiles([makeFile('a.md', 100)]);
    expect(accepted).toHaveLength(1);
    expect(errors).toHaveLength(0);
  });

  it('超 5MB 拒绝', () => {
    const { accepted, errors } = validateUploadFiles([
      makeFile('big.md', MAX_UPLOAD_FILE_BYTES + 1),
    ]);
    expect(accepted).toHaveLength(0);
    expect(errors[0]).toMatchObject({ name: 'big.md' });
    expect(errors[0]?.reason).toContain('5MB');
  });

  it('格式不支持拒绝', () => {
    const { accepted, errors } = validateUploadFiles([makeFile('x.png', 10)]);
    expect(accepted).toHaveLength(0);
    expect(errors[0]?.reason).toContain('不受支持');
  });

  it('超 50 个文件：保留前 50，多余逐个报错', () => {
    const files = Array.from({ length: MAX_UPLOAD_FILES + 2 }, (_, i) =>
      makeFile(`f${i}.md`, 10),
    );
    const { accepted, errors } = validateUploadFiles(files);
    expect(accepted).toHaveLength(MAX_UPLOAD_FILES);
    expect(errors).toHaveLength(2);
    expect(errors[0]?.reason).toContain('50');
  });
});

describe('parseIgnoreRules', () => {
  it('逗号与换行混合解析 + 去空白', () => {
    expect(parseIgnoreRules('docs/drafts/**, *.pem\nsecrets/ , *.key')).toEqual([
      'docs/drafts/**',
      '*.pem',
      'secrets/',
      '*.key',
    ]);
  });

  it('去重', () => {
    expect(parseIgnoreRules('*.pem, *.pem')).toEqual(['*.pem']);
  });

  it('空输入返回 undefined（字段缺省）', () => {
    expect(parseIgnoreRules('')).toBeUndefined();
    expect(parseIgnoreRules('  \n , ')).toBeUndefined();
  });
});
