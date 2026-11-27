/**
 * DocumentTable：文档列表（列表页与知识库详情复用同一组件）。
 * v2：客户端名称筛选 + 可排序列头（名称/类型/状态/文件数/最近更新）。
 * 状态徽标四态色 + 处理中 stage + 失败原因摘要 + 类型徽标；
 * 操作按钮按后端 can_* 字段渲染（R6 不可见原则），删除按钮按前端 mayManageContent 控制；
 * 409 DOC_TASK_RUNNING → toast 提示已有任务。
 */

import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { toast } from 'sonner';
import { ArrowDown, ArrowUp, ChevronsUpDown, Eye, RefreshCw, RotateCcw, Search, Trash2, Wrench } from 'lucide-react';
import { Button, buttonVariants } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import { StatusBadge, TypeBadge } from '@/components/shared/status-badge';
import { fromNow } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { components } from '@/api/schema';
import {
  useDeleteDocument,
  useReindexDocument,
  useRetryDocument,
  useSyncDocument,
} from '@/hooks/use-documents';

type DocumentItem = components['schemas']['service.DocumentItem'];

type SortKey = 'name' | 'type' | 'status' | 'file_count' | 'updated_at';

interface DocumentTableProps {
  kbId: number;
  documents: DocumentItem[];
  /** 接入权限（R6 编辑者+）：同步/重试按钮 = mayIngest && can_sync/can_retry */
  mayIngest: boolean;
  /** 管理权限（R6 管理者+）：删除/重建索引按钮 = mayManageContent && can_reindex */
  mayManageContent: boolean;
  /** 删除成功回调（详情页删除后导航回列表用） */
  onDeleted?: (docId: number) => void;
}

/** 统一 mutation 错误提示（DOC_TASK_RUNNING 等已知码走文案表）。 */
function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

/** 可排序表头按钮：升/降/无三态图标。 */
function SortHead({
  label,
  k,
  sortKey,
  sortDir,
  onSort,
  className,
}: {
  label: string;
  k: SortKey;
  sortKey: SortKey;
  sortDir: 1 | -1;
  onSort: (k: SortKey) => void;
  className?: string;
}) {
  const active = sortKey === k;
  return (
    <TableHead className={className}>
      <button
        type="button"
        onClick={() => onSort(k)}
        className={cn(
          'inline-flex items-center gap-1 uppercase tracking-wide transition-colors hover:text-[var(--foreground)]',
          active && 'text-[var(--foreground)]',
        )}
        aria-label={`按${label}排序`}
      >
        {label}
        {active ? (
          sortDir === 1 ? (
            <ArrowUp className="size-3" aria-hidden="true" />
          ) : (
            <ArrowDown className="size-3" aria-hidden="true" />
          )
        ) : (
          <ChevronsUpDown className="size-3 opacity-50" aria-hidden="true" />
        )}
      </button>
    </TableHead>
  );
}

export function DocumentTable({
  kbId,
  documents,
  mayIngest,
  mayManageContent,
  onDeleted,
}: DocumentTableProps) {
  const [pendingDelete, setPendingDelete] = useState<DocumentItem | null>(null);
  const [filter, setFilter] = useState('');
  const [sortKey, setSortKey] = useState<SortKey>('name');
  const [sortDir, setSortDir] = useState<1 | -1>(1);

  const retry = useRetryDocument(kbId);
  const sync = useSyncDocument(kbId);
  const reindex = useReindexDocument(kbId);
  const remove = useDeleteDocument(kbId);

  const runTask = (fn: () => Promise<void>, successMsg: string) => {
    fn()
      .then(() => toast.success(successMsg))
      .catch(toastError);
  };

  const onSort = (k: SortKey) => {
    if (k === sortKey) {
      setSortDir((d) => (d === 1 ? -1 : 1));
    } else {
      setSortKey(k);
      setSortDir(1);
    }
  };

  // 客户端筛选 + 排序（服务端分页内的当页排序）
  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const filtered = q
      ? documents.filter((d) => (d.name ?? '').toLowerCase().includes(q))
      : documents;
    const collator = new Intl.Collator('zh-Hans-CN');
    return [...filtered].sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case 'name':
          cmp = collator.compare(a.name ?? '', b.name ?? '');
          break;
        case 'type':
          cmp = collator.compare(a.type ?? '', b.type ?? '');
          break;
        case 'status':
          cmp = collator.compare(a.status ?? '', b.status ?? '');
          break;
        case 'file_count':
          cmp = (a.file_count ?? 0) - (b.file_count ?? 0);
          break;
        case 'updated_at':
          cmp = collator.compare(a.updated_at ?? '', b.updated_at ?? '');
          break;
      }
      return cmp * sortDir;
    });
  }, [documents, filter, sortKey, sortDir]);

  return (
    <>
      {/* 筛选工具行 */}
      <div className="mb-3 flex items-center gap-3">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[var(--faint)]"
            aria-hidden="true"
          />
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="筛选文档名称…"
            aria-label="筛选文档名称"
            className="h-8 w-60 pl-8 text-[13px]"
          />
        </div>
        {filter ? (
          <span className="text-xs text-[var(--faint)]">
            {rows.length} / {documents.length} 条
          </span>
        ) : null}
      </div>

      <div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)]">
        <Table>
          <TableHeader>
            <TableRow>
              <SortHead label="名称" k="name" sortKey={sortKey} sortDir={sortDir} onSort={onSort} />
              <SortHead label="类型" k="type" sortKey={sortKey} sortDir={sortDir} onSort={onSort} className="w-20" />
              <SortHead label="状态" k="status" sortKey={sortKey} sortDir={sortDir} onSort={onSort} className="w-44" />
              <SortHead label="文件数" k="file_count" sortKey={sortKey} sortDir={sortDir} onSort={onSort} className="w-20 text-right" />
              <SortHead label="最近更新" k="updated_at" sortKey={sortKey} sortDir={sortDir} onSort={onSort} className="w-28" />
              <TableHead className="w-56 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="py-8 text-center text-sm text-[var(--faint)]">
                  {filter ? '没有匹配筛选条件的文档' : '暂无文档'}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((doc) => (
                <TableRow key={doc.id}>
                  <TableCell className="max-w-[280px]">
                    <Link
                      to={`/kbs/${kbId}/documents/${doc.id}`}
                      className="block truncate font-medium hover:text-[var(--primary)]"
                      title={doc.name ?? ''}
                    >
                      {doc.name}
                    </Link>
                    {doc.status === 'failed' && doc.error ? (
                      <p className="truncate text-xs text-[var(--destructive)]" title={doc.error}>
                        {doc.error}
                      </p>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <TypeBadge type={doc.type} />
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={doc.status} stage={doc.stage} />
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{doc.file_count ?? 0}</TableCell>
                  <TableCell className="text-xs text-[var(--muted)]">
                    {fromNow(doc.updated_at) || '—'}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <Link
                        to={`/kbs/${kbId}/documents/${doc.id}`}
                        className={buttonVariants({ variant: 'ghost', size: 'icon' })}
                        title="查看详情"
                        aria-label={`查看文档 ${doc.name ?? ''}`}
                      >
                        <Eye aria-hidden="true" />
                      </Link>
                      {mayIngest && doc.can_sync ? (
                        <Button
                          variant="ghost"
                          size="icon"
                          title="手动同步"
                          disabled={sync.isPending}
                          onClick={() =>
                            runTask(() => sync.mutateAsync(doc.id as number), '同步任务已排队')
                          }
                        >
                          <RefreshCw aria-hidden="true" />
                        </Button>
                      ) : null}
                      {mayIngest && doc.can_retry ? (
                        <Button
                          variant="ghost"
                          size="icon"
                          title="重试"
                          disabled={retry.isPending}
                          onClick={() =>
                            runTask(() => retry.mutateAsync(doc.id as number), '重试任务已排队')
                          }
                        >
                          <RotateCcw aria-hidden="true" />
                        </Button>
                      ) : null}
                      {mayManageContent && doc.can_reindex ? (
                        <Button
                          variant="ghost"
                          size="icon"
                          title="重建索引"
                          disabled={reindex.isPending}
                          onClick={() =>
                            runTask(() => reindex.mutateAsync(doc.id as number), '重建索引已排队')
                          }
                        >
                          <Wrench aria-hidden="true" />
                        </Button>
                      ) : null}
                      {mayManageContent ? (
                        <Button
                          variant="ghost"
                          size="icon"
                          title="删除文档"
                          className="text-[var(--destructive)]"
                          onClick={() => setPendingDelete(doc)}
                        >
                          <Trash2 aria-hidden="true" />
                        </Button>
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
        title="删除文档"
        description={`将删除文档「${pendingDelete?.name ?? ''}」及其分块、文件清单与任务历史，不可恢复。`}
        confirmLabel="删除"
        busy={remove.isPending}
        onConfirm={() => {
          if (pendingDelete?.id === undefined) return;
          const docId = pendingDelete.id;
          remove.mutate(docId, {
            onSuccess: () => {
              toast.success('文档已删除');
              setPendingDelete(null);
              onDeleted?.(docId);
            },
            onError: toastError,
          });
        }}
      />
    </>
  );
}
