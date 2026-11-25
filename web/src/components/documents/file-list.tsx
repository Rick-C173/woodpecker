/**
 * FileList：文档详情的文件清单表（含「已排除」标记与失败/排除原因）。
 * git=仓库内文件 / upload=上传文件名 / url=抓取页面。
 */

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { FileStatusBadge } from '@/components/shared/status-badge';
import { EmptyState } from '@/components/states/empty-state';
import type { components } from '@/api/schema';

type DocumentFileItem = components['schemas']['service.DocumentFileItem'];

/** 字节数 → 人类可读（B/KB/MB）。 */
function formatBytes(bytes: number | null | undefined): string {
  const n = bytes ?? 0;
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function FileList({ files }: { files?: DocumentFileItem[] | null }) {
  const list = files ?? [];
  if (list.length === 0) {
    return (
      <EmptyState title="暂无文件清单" description="接入文档并完成摄取后，这里会展示文件清单与状态。" />
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>路径</TableHead>
          <TableHead className="w-20">语言</TableHead>
          <TableHead className="w-24 text-right">大小</TableHead>
          <TableHead className="w-24">状态</TableHead>
          <TableHead>原因</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {list.map((f) => (
          <TableRow key={f.id}>
            <TableCell className="max-w-[360px] truncate font-mono text-xs" title={f.path ?? ''}>
              {f.path}
            </TableCell>
            <TableCell className="text-xs text-[var(--muted)]">{f.language ?? '—'}</TableCell>
            <TableCell className="text-right text-xs tabular-nums">
              {formatBytes(f.size)}
            </TableCell>
            <TableCell>
              <FileStatusBadge status={f.status} />
            </TableCell>
            <TableCell
              className="max-w-[200px] truncate text-xs text-[var(--muted)]"
              title={f.error ?? ''}
            >
              {f.error ?? '—'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
