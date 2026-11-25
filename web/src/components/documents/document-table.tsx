/**
 * DocumentTable：文档列表（列表页与知识库详情复用同一组件）。
 * 状态徽标四态色 + 处理中 stage + 失败原因摘要 + 类型徽标；
 * 操作按钮按后端 can_* 字段渲染（R6 不可见原则），删除按钮按前端 mayManageContent 控制；
 * 409 DOC_TASK_RUNNING → toast 提示已有任务。
 */

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { toast } from 'sonner';
import { Eye, RefreshCw, RotateCcw, Trash2, Wrench } from 'lucide-react';
import { Button, buttonVariants } from '@/components/ui/button';
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
import type { components } from '@/api/schema';
import {
  useDeleteDocument,
  useReindexDocument,
  useRetryDocument,
  useSyncDocument,
} from '@/hooks/use-documents';

type DocumentItem = components['schemas']['service.DocumentItem'];

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

export function DocumentTable({
  kbId,
  documents,
  mayIngest,
  mayManageContent,
  onDeleted,
}: DocumentTableProps) {
  const [pendingDelete, setPendingDelete] = useState<DocumentItem | null>(null);

  const retry = useRetryDocument(kbId);
  const sync = useSyncDocument(kbId);
  const reindex = useReindexDocument(kbId);
  const remove = useDeleteDocument(kbId);

  const runTask = (fn: () => Promise<void>, successMsg: string) => {
    fn()
      .then(() => toast.success(successMsg))
      .catch(toastError);
  };

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>名称</TableHead>
            <TableHead className="w-20">类型</TableHead>
            <TableHead className="w-44">状态</TableHead>
            <TableHead className="w-20 text-right">文件数</TableHead>
            <TableHead className="w-28">最近更新</TableHead>
            <TableHead className="w-56 text-right">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {documents.map((doc) => (
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
          ))}
        </TableBody>
      </Table>

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
