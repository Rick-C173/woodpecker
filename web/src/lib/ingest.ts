/**
 * 接入/上传前端预校验（PRD R1/R2）：
 * - 扩展名白名单与后端 internal/rag/filter.go supportedExts 一一对应
 * - 上传 ≤50 个文件、单文件 ≤5MB（DOC_002 拒绝在服务端逐个报，前端先拦）
 * - ignore_rules 输入解析（逗号/换行分隔 glob）
 * 纯函数实现，供接入向导与单测使用。
 */

export const MAX_UPLOAD_FILES = 50;
export const MAX_UPLOAD_FILE_BYTES = 5 * 1024 * 1024;

/** R1 支持格式清单（与 internal/rag/filter.go supportedExts 同步维护）。 */
export const SUPPORTED_UPLOAD_EXTS: readonly string[] = [
  '.md', '.markdown', '.txt',
  '.go', '.py', '.java', '.js', '.ts',
  '.tsx', '.jsx', '.c', '.h', '.cpp', '.hpp',
  '.rs', '.rb', '.php', '.cs', '.swift', '.kt',
  '.sql', '.sh', '.bash', '.yaml', '.yml',
  '.json', '.toml', '.ini', '.xml', '.html',
  '.css', '.proto', '.graphql', '.dockerfile', '.makefile',
];

/** 无扩展名特判文件名（与 filter.go IsSupportedExt 一致）。 */
const SUPPORTED_BASENAMES: readonly string[] = ['dockerfile', 'makefile', 'gnumakefile'];

/** 文件名是否受支持（小写比较，含 Dockerfile/Makefile 特判）。 */
export function isSupportedFileName(name: string): boolean {
  const lower = name.toLowerCase();
  const base = lower.slice(lower.lastIndexOf('/') + 1);
  if (SUPPORTED_BASENAMES.includes(base)) return true;
  const dot = base.lastIndexOf('.');
  if (dot <= 0) return false; // 无扩展名或 ".gitignore" 类隐藏文件
  return SUPPORTED_UPLOAD_EXTS.includes(base.slice(dot));
}

/** 上传预校验错误条目。 */
export interface UploadFileError {
  name: string;
  reason: string;
}

/**
 * 上传文件批量预校验：数量 / 单文件大小 / 扩展名白名单。
 * 返回可提交的文件与逐个拒绝原因（UI 逐个展示）。
 */
export function validateUploadFiles(files: File[]): {
  accepted: File[];
  errors: UploadFileError[];
} {
  const accepted: File[] = [];
  const errors: UploadFileError[] = [];

  for (const f of files) {
    if (f.size > MAX_UPLOAD_FILE_BYTES) {
      errors.push({ name: f.name, reason: '超过单文件 5MB 限制' });
    } else if (!isSupportedFileName(f.name)) {
      errors.push({ name: f.name, reason: '文件格式不受支持' });
    } else {
      accepted.push(f);
    }
  }

  if (accepted.length > MAX_UPLOAD_FILES) {
    const overflow = accepted.splice(MAX_UPLOAD_FILES);
    for (const f of overflow) {
      errors.push({ name: f.name, reason: '超出单次 50 个文件上限' });
    }
  }

  return { accepted, errors };
}

/**
 * 解析 ignore_rules 输入（逗号或换行分隔的 glob）：
 * 去空白、去空项、去重；空输入返回 undefined（字段缺省不修改语义）。
 */
export function parseIgnoreRules(raw: string): string[] | undefined {
  const rules = Array.from(
    new Set(
      raw
        .split(/[\n,]+/)
        .map((s) => s.trim())
        .filter((s) => s.length > 0),
    ),
  );
  return rules.length > 0 ? rules : undefined;
}
