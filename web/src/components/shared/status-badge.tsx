/**
 * StatusBadge：文档摄取状态四态徽标（待处理/处理中/已就绪/失败）+
 * 处理中 stage 文案（解析/切块/向量化）与旋转图标。
 * 另含文档类型徽标（仓库/文件/网页）与任务状态徽标。
 */

import { LoaderCircle } from 'lucide-react';
import { cn } from '@/lib/utils';

export type DocStatus = 'pending' | 'processing' | 'ready' | 'failed';

/** 状态 → 中文文案。 */
export const DOC_STATUS_LABELS: Record<string, string> = {
  pending: '待处理',
  processing: '处理中',
  ready: '已就绪',
  failed: '失败',
};

/** processing 阶段 → 中文文案。 */
export const DOC_STAGE_LABELS: Record<string, string> = {
  parsing: '解析中',
  chunking: '切块中',
  embedding: '向量化中',
};

/** 状态四态色（token 化：pending=muted / processing=primary / ready=accent / failed=destructive）。 */
const STATUS_CLASSES: Record<string, string> = {
  pending: 'bg-[var(--muted-bg)] text-[var(--muted-foreground)] border border-[var(--border)]',
  processing: 'bg-[var(--primary-soft)] text-[var(--primary)]',
  ready: 'bg-[var(--accent-soft)] text-[var(--accent)]',
  failed: 'bg-[var(--destructive-soft)] text-[var(--destructive)]',
};

/** 摄取状态徽标：处理中带旋转图标 + 阶段文案。 */
export function StatusBadge({
  status,
  stage,
  className,
}: {
  status?: string | null;
  stage?: string | null;
  className?: string;
}) {
  const s = status ?? '';
  const label = DOC_STATUS_LABELS[s] ?? s;
  const isProcessing = s === 'processing';
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium',
        STATUS_CLASSES[s] ?? 'bg-[var(--muted-bg)] text-[var(--muted-foreground)] border border-[var(--border)]',
        className,
      )}
    >
      {isProcessing ? <LoaderCircle className="size-3 animate-spin" aria-hidden="true" /> : null}
      {label}
      {isProcessing && stage ? ` · ${DOC_STAGE_LABELS[stage] ?? stage}` : ''}
    </span>
  );
}

/** 文档类型徽标：git=仓库 / upload=文件 / url=网页。 */
const TYPE_LABELS: Record<string, string> = {
  git: '仓库',
  upload: '文件',
  url: '网页',
};

export function TypeBadge({ type, className }: { type?: string | null; className?: string }) {
  const t = type ?? '';
  if (!TYPE_LABELS[t]) return null;
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-md border border-[var(--border)] px-1.5 py-0.5 text-xs text-[var(--muted-foreground)]',
        className,
      )}
    >
      {TYPE_LABELS[t]}
    </span>
  );
}

/** 文件清单状态（indexed/excluded/failed）。 */
export function FileStatusBadge({ status, className }: { status?: string | null; className?: string }) {
  const map: Record<string, { label: string; cls: string }> = {
    indexed: { label: '已入库', cls: 'bg-[var(--accent-soft)] text-[var(--accent)]' },
    excluded: { label: '已排除', cls: 'bg-[var(--muted-bg)] text-[var(--muted-foreground)]' },
    failed: { label: '失败', cls: 'bg-[var(--destructive-soft)] text-[var(--destructive)]' },
  };
  const item = map[status ?? ''];
  if (!item) return null;
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
        item.cls,
        className,
      )}
    >
      {item.label}
    </span>
  );
}

/** 任务历史状态徽标（queued/running/success/failed）。 */
export function TaskStatusBadge({ status, className }: { status?: string | null; className?: string }) {
  const map: Record<string, { label: string; cls: string }> = {
    queued: { label: '排队中', cls: 'bg-[var(--muted-bg)] text-[var(--muted-foreground)]' },
    running: { label: '运行中', cls: 'bg-[var(--primary-soft)] text-[var(--primary)]' },
    success: { label: '成功', cls: 'bg-[var(--accent-soft)] text-[var(--accent)]' },
    failed: { label: '失败', cls: 'bg-[var(--destructive-soft)] text-[var(--destructive)]' },
  };
  const item = map[status ?? ''];
  if (!item) return null;
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
        item.cls,
        className,
      )}
    >
      {item.label}
    </span>
  );
}
