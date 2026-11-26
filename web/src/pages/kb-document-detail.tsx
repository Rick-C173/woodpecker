/**
 * 文档详情页（F4）：状态与错误信息、文件清单表（含「已排除」标记）、任务历史、
 * 操作按钮（同列表页 R6 规则）、内容预览（url 类型展示原文链接）。
 * 处理中 → 3s 轮询；终态自动停。
 */

import { Link, useNavigate, useParams } from 'react-router-dom';
import { useState } from 'react';
import { toast } from 'sonner';
import { ExternalLink, RefreshCw, RotateCcw, Trash2, Wrench } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import { Breadcrumbs } from '@/components/shared/breadcrumbs';
import { StatusBadge, TaskStatusBadge, TypeBadge } from '@/components/shared/status-badge';
import { FileList } from '@/components/documents/file-list';
import { Button, buttonVariants } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { fromNow, formatDateTime } from '@/lib/time';
import {
  useDocumentDetail,
  useDeleteDocument,
  useReindexDocument,
  useRetryDocument,
  useSetAutoSync,
  useRotateWebhookSecret,
  useSyncDocument,
} from '@/hooks/use-documents';
import { useKbDetail } from '@/hooks/use-kbs';
import { useRole } from '@/hooks/use-role';
import { ApiError } from '@/lib/errors';
import type { components } from '@/api/schema';

type DocumentDetail = components['schemas']['service.DocumentDetail'];

/** 任务类型（full=摄取/重建 sync=增量同步）。 */
function taskTypeLabel(type?: string | null): string {
  if (type === 'sync') return '增量同步';
  if (type === 'full') return '全量摄取';
  return type ?? '—';
}

export function KbDocumentDetailPage() {
  const { kbId, docId } = useParams();
  const kbIdNum = Number(kbId);
  const docIdNum = Number(docId);

  const query = useDocumentDetail(kbIdNum, docIdNum);
  // 文档详情无 my_role 字段：借库详情判定操作按钮（R6：同步/重试=编辑者+，重建/删除=管理者+）
  const kbQuery = useKbDetail(kbIdNum);
  const { mayIngest, mayManageContent } = useRole(kbQuery.data?.my_role);

  const retry = useRetryDocument(kbIdNum);
  const sync = useSyncDocument(kbIdNum);
  const reindex = useReindexDocument(kbIdNum);
  const remove = useDeleteDocument(kbIdNum);

  const isNotFound =
    query.isError && query.error instanceof ApiError && query.error.status === 404;

  if (isNotFound) {
    return (
      <div className="mx-auto w-full max-w-5xl px-6 py-8">
        <EmptyState
          title="文档不存在或已被删除"
          action={
            <Link to={`/kbs/${kbIdNum}`}>
              <Button variant="outline">返回知识库</Button>
            </Link>
          }
        />
      </div>
    );
  }

  const runTask = (fn: () => Promise<void>, successMsg: string) => {
    fn()
      .then(() => toast.success(successMsg))
      .catch((e) => toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试'));
  };

  return (
    <div className="mx-auto w-full max-w-5xl px-6 py-8">
      <QueryBoundary query={query} skeleton={<Skeleton className="h-64 w-full" />}>
        {(doc: DocumentDetail) => (
          <>
            <Breadcrumbs
              items={[
                { label: '知识库', to: '/kbs' },
                { label: '库详情', to: `/kbs/${kbIdNum}` },
                { label: doc.name ?? '' },
              ]}
              className="mb-4"
            />

            {/* 状态与错误信息 */}
            <div className="kb-card mb-6 p-5">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h1 className="truncate text-lg font-semibold" title={doc.name ?? ''}>
                      {doc.name}
                    </h1>
                    <TypeBadge type={doc.type} />
                    <StatusBadge status={doc.status} stage={doc.stage} />
                  </div>
                  {doc.status === 'failed' && doc.error ? (
                    <p className="mt-2 rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2 text-xs text-[var(--destructive)]">
                      失败原因：{doc.error}
                    </p>
                  ) : null}
                  <p className="mt-2 text-xs text-[var(--faint)]">
                    {doc.file_count ?? 0} 个文件 · {doc.total_size ?? 0} 字节
                    {doc.git_ref ? ` · ${doc.git_ref}` : ''}
                    {doc.last_synced_at ? ` · 最近同步 ${formatDateTime(doc.last_synced_at)}` : ''}
                    {' · '}
                    更新于 {fromNow(doc.updated_at) || '—'}
                  </p>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  {/* 内容预览：url 类型展示原文链接 */}
                  {doc.type === 'url' && doc.source_url ? (
                    <a
                      href={doc.source_url}
                      target="_blank"
                      rel="noreferrer"
                      className={buttonVariants({ variant: 'outline' })}
                    >
                      <ExternalLink aria-hidden="true" />
                      查看原文
                    </a>
                  ) : null}
                  {doc.git_ref ? <span className="text-xs text-[var(--faint)]">{doc.git_ref}</span> : null}
                  {mayIngest && doc.can_sync ? (
                    <Button
                      variant="outline"
                      disabled={sync.isPending}
                      onClick={() => runTask(() => sync.mutateAsync(doc.id as number), '同步任务已排队')}
                    >
                      <RefreshCw aria-hidden="true" />
                      同步
                    </Button>
                  ) : null}
                  {mayIngest && doc.can_retry ? (
                    <Button
                      variant="outline"
                      disabled={retry.isPending}
                      onClick={() => runTask(() => retry.mutateAsync(doc.id as number), '重试任务已排队')}
                    >
                      <RotateCcw aria-hidden="true" />
                      重试
                    </Button>
                  ) : null}
                  {mayManageContent && doc.can_reindex ? (
                    <Button
                      variant="outline"
                      disabled={reindex.isPending}
                      onClick={() => runTask(() => reindex.mutateAsync(doc.id as number), '重建索引已排队')}
                    >
                      <Wrench aria-hidden="true" />
                      重建索引
                    </Button>
                  ) : null}
                  {mayManageContent ? (
                    <DeleteDocButton kbId={kbIdNum} docId={doc.id as number} docName={doc.name ?? ''} busy={remove.isPending} />
                  ) : null}
                </div>
              </div>

              {/* git 类型展示仓库地址 */}
              {doc.repo_url ? (
                <p className="mt-3 truncate font-mono text-xs text-[var(--faint)]" title={doc.repo_url}>
                  {doc.repo_url}
                </p>
              ) : null}
            </div>

            {/* M14 Git 自动同步（git 类型 + 编辑者+）：定时拉取开关 + webhook */}
            {doc.type === 'git' && mayIngest ? (
              <AutoSyncSection kbId={kbIdNum} doc={doc} />
            ) : null}

            {/* 内容预览（url 类型）：原文摘要区 */}
            {doc.type === 'url' && doc.source_url ? (
              <section className="kb-card mb-6 p-5">
                <h2 className="mb-2 text-sm font-semibold">网页来源</h2>
                <a
                  className="break-all font-mono text-xs text-[var(--primary)] hover:underline"
                  href={doc.source_url}
                  target="_blank"
                  rel="noreferrer"
                >
                  {doc.source_url}
                </a>
              </section>
            ) : null}

            {/* 文件清单 */}
            <section className="kb-card mb-6 p-5">
              <h2 className="mb-3 text-sm font-semibold">文件清单</h2>
              <FileList files={doc.files} />
            </section>

            {/* 任务历史 */}
            <section className="kb-card p-5">
              <h2 className="mb-3 text-sm font-semibold">任务历史</h2>
              {(doc.tasks ?? []).length === 0 ? (
                <EmptyState title="暂无任务记录" description="文档接入或同步后，这里会展示每次摄取任务的执行历史。" />
              ) : (
                <ul className="space-y-2">
                  {doc.tasks?.map((t) => (
                    <li
                      key={t.id}
                      className="flex flex-wrap items-center gap-2 rounded-[var(--radius)] border border-[var(--border)] px-3 py-2 text-xs"
                    >
                      <TaskStatusBadge status={t.status} />
                      <span className="font-medium">{taskTypeLabel(t.type)}</span>
                      <span className="text-[var(--faint)]">
                        创建 {formatDateTime(t.created_at) || '—'}
                        {t.finished_at ? ` · 完成 ${formatDateTime(t.finished_at)}` : ''}
                        {(t.retries ?? 0) > 0 ? ` · 已重试 ${t.retries} 次` : ''}
                      </span>
                      {t.error ? (
                        <span className="w-full truncate text-[var(--destructive)]" title={t.error}>
                          {t.error}
                        </span>
                      ) : null}
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </>
        )}
      </QueryBoundary>
    </div>
  );
}

/** 删除文档按钮 + 二次确认（删除成功后返回知识库）。 */
function DeleteDocButton({
  kbId,
  docId,
  docName,
  busy,
}: {
  kbId: number;
  docId: number;
  docName: string;
  busy: boolean;
}) {
  const navigate = useNavigate();
  const remove = useDeleteDocument(kbId);
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        variant="outline"
        className="text-[var(--destructive)]"
        disabled={busy}
        onClick={() => setOpen(true)}
      >
        <Trash2 aria-hidden="true" />
        删除
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="删除文档"
        description={`将删除文档「${docName}」及其分块、文件清单与任务历史，不可恢复。`}
        confirmLabel="删除"
        busy={remove.isPending}
        onConfirm={() => {
          remove.mutate(docId, {
            onSuccess: () => {
              toast.success('文档已删除');
              setOpen(false);
              navigate(`/kbs/${kbId}`);
            },
            onError: (e) => toast.error(e instanceof Error ? e.message : '删除失败'),
          });
        }}
      />
    </>
  );
}

/** M14 Git 自动同步区块：定时拉取开关 + webhook（token 明文仅生成时弹窗展示一次）。 */
function AutoSyncSection({ kbId, doc }: { kbId: number; doc: DocumentDetail }) {
  const docId = doc.id as number;
  const setAutoSync = useSetAutoSync(kbId);
  const rotate = useRotateWebhookSecret(kbId);
  const [issued, setIssued] = useState<{ token: string; url: string } | null>(null);

  const handleToggle = (checked: boolean) => {
    setAutoSync.mutate(
      { docId, autoSync: checked },
      {
        onSuccess: () => toast.success(checked ? '定时同步已开启' : '定时同步已关闭'),
        onError: (e) => toast.error(e instanceof Error ? e.message : '设置失败'),
      },
    );
  };

  const handleRotate = () => {
    rotate.mutate(docId, {
      onSuccess: (r) => setIssued({ token: r.webhook_token, url: r.webhook_url }),
      onError: (e) => toast.error(e instanceof Error ? e.message : '生成失败'),
    });
  };

  return (
    <section className="kb-card mb-6 p-5">
      <h2 className="mb-1 text-sm font-semibold">自动同步</h2>
      <p className="mb-3 text-xs text-[var(--muted)]">
        开启后由 worker 按固定周期自动拉取远端变更（增量同步）；也可用 webhook 实时触发。
      </p>

      <label className="flex cursor-pointer items-center gap-2 text-sm" data-testid="auto-sync-toggle">
        <input
          type="checkbox"
          checked={doc.auto_sync === true}
          disabled={setAutoSync.isPending}
          onChange={(e) => handleToggle(e.target.checked)}
          className="accent-[var(--primary)]"
        />
        {doc.auto_sync ? '定时拉取：已开启' : '定时拉取：已关闭'}
      </label>

      <div className="mt-4 border-t border-[var(--border)] pt-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-xs text-[var(--muted)]">
            Webhook（GitLab / GitHub 均可配置自定义 header：
            <code className="mx-1 rounded bg-[var(--border)] px-1 py-0.5">X-Woodpecker-Token</code>
            ，push 事件即时触发增量同步）
          </div>
          <Button variant="outline" size="sm" disabled={rotate.isPending} onClick={handleRotate}>
            {doc.has_webhook ? '轮换 token' : '生成 webhook token'}
          </Button>
        </div>
      </div>

      {/* token 明文仅本次展示（与初始密码同一语义）；URL 供 Git 平台 webhook 地址栏复制 */}
      <Dialog open={issued !== null} onOpenChange={(open) => !open && setIssued(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Webhook 配置</DialogTitle>
            <DialogDescription>
              在 Git 平台为该仓库添加 Push 事件 webhook（地址见下），并设置自定义 header。token 明文仅本次展示，请立即保存。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="wh-url">Webhook 地址</Label>
              <Input id="wh-url" readOnly value={issued?.url ?? ''} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
            </div>
            <div className="space-y-1">
              <Label htmlFor="wh-token">X-Woodpecker-Token 值</Label>
              <Input id="wh-token" readOnly value={issued?.token ?? ''} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                void navigator.clipboard.writeText(`url: ${issued?.url ?? ''}\nX-Woodpecker-Token: ${issued?.token ?? ''}`);
                toast.success('已复制到剪贴板');
              }}
            >
              复制配置
            </Button>
            <Button onClick={() => setIssued(null)}>我已保存，关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
