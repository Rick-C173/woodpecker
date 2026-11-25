/**
 * 知识库详情页（F3/F4）：头部信息 + Tab（文档列表 / 成员管理[仅管理员]）。
 * - 文档 tab：接入按钮（编辑者+）+ DocumentTable（3s 轮询自动启停）
 * - 成员 tab：成员列表、添加用户（user_id + role）、改角色、移除（二次确认）
 * - 非成员访问（后端 404 NOT_FOUND，R6 不可见原则）→ EmptyState「知识库不存在或无权访问」
 * 删库入口统一在列表页（输入库名确认），详情页不重复提供。
 */

import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import { FolderOpen, MessageCircle, Plus, Search, Trash2, Users } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { RoleBadge } from '@/components/shared/role-badge';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import { DocumentTable } from '@/components/documents/document-table';
import { IngestWizard } from '@/components/documents/ingest-wizard';
import { Button, buttonVariants } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { ApiError } from '@/lib/errors';
import { KB_ROLES, ROLE_LABELS } from '@/lib/role';
import { fromNow } from '@/lib/time';
import { cn } from '@/lib/utils';
import { useKbDetail } from '@/hooks/use-kbs';
import { useRole } from '@/hooks/use-role';
import {
  useDocumentsList,
  type DocumentPage,
} from '@/hooks/use-documents';
import {
  useAddMember,
  useMembers,
  useRemoveMember,
  useUpdateMemberRole,
  type MemberRole,
} from '@/hooks/use-members';
import type { components } from '@/api/schema';

type KBMemberItem = components['schemas']['service.KBMemberItem'];

/* ------------------------------ 成员管理 Tab ------------------------------ */

/** 成员角色下拉（R6：owner=管理者 editor=编辑者 member=成员）。 */
function RoleSelect({
  value,
  disabled,
  onChange,
}: {
  value: string;
  disabled?: boolean;
  onChange: (role: MemberRole) => void;
}) {
  return (
    <select
      className="h-8 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-2 text-xs"
      value={value}
      disabled={disabled}
      aria-label="成员角色"
      onChange={(e) => onChange(e.target.value as MemberRole)}
    >
      {KB_ROLES.map((r) => (
        <option key={r} value={r}>
          {ROLE_LABELS[r]}
        </option>
      ))}
    </select>
  );
}

/** 添加成员 dialog：user_id + role。 */
function AddMemberDialog({
  open,
  onOpenChange,
  kbId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  kbId: number;
}) {
  const addMember = useAddMember(kbId);
  const [error, setError] = useState<string | null>(null);

  const schema = z.object({
    user_id: z.coerce.number().int().positive('请输入合法的用户 ID'),
    role: z.enum(['owner', 'editor', 'member']),
  });

  const form = useForm<{ user_id: number; role: MemberRole }>({
    resolver: zodResolver(schema),
    defaultValues: { user_id: 0, role: 'member' },
  });

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    try {
      await addMember.mutateAsync(values);
      toast.success('成员已添加');
      onOpenChange(false);
      form.reset({ user_id: 0, role: 'member' });
    } catch (e) {
      // 409 已是成员 / 404 用户不存在 → errors 文案或后端 message
      setError(e instanceof Error ? e.message : '添加失败，请稍后重试');
    }
  });

  const submitting = addMember.isPending;
  const errors = form.formState.errors;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>添加成员</DialogTitle>
          <DialogDescription>按用户 ID 添加并指派库内角色。</DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={onSubmit} noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="member-uid">用户 ID *</Label>
            <Input
              id="member-uid"
              type="number"
              min={1}
              placeholder="用户 ID（管理后台用户列表可查）"
              {...form.register('user_id')}
            />
            {errors.user_id ? (
              <p className="text-xs text-[var(--destructive)]">{errors.user_id.message}</p>
            ) : null}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="member-role">角色</Label>
            <select
              id="member-role"
              className="h-[var(--row)] w-full rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-3 text-sm"
              {...form.register('role')}
            >
              {KB_ROLES.map((r) => (
                <option key={r} value={r}>
                  {ROLE_LABELS[r]}（{r}）
                </option>
              ))}
            </select>
          </div>
          {error ? (
            <p className="rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2 text-xs text-[var(--destructive)]">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? '添加中…' : '添加'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** 成员管理 Tab（仅管理员可见）。 */
function MembersTab({ kbId }: { kbId: number }) {
  const members = useMembers(kbId);
  const updateRole = useUpdateMemberRole(kbId);
  const removeMember = useRemoveMember(kbId);
  const [removing, setRemoving] = useState<KBMemberItem | null>(null);
  const [addOpen, setAddOpen] = useState(false);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-[var(--muted)]">
          管理者可删改内容，编辑者可补充内容，成员只读（R6）。
        </p>
        <Button onClick={() => setAddOpen(true)}>
          <Plus aria-hidden="true" />
          添加成员
        </Button>
      </div>

      <QueryBoundary
        query={members}
        skeleton={<Skeleton className="h-48 w-full" />}
        empty={
          <EmptyState
            icon={Users}
            title="暂无成员"
            description="点击「添加成员」授权用户访问本库。"
          />
        }
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>用户名</TableHead>
                <TableHead className="w-24">用户 ID</TableHead>
                <TableHead className="w-32">角色</TableHead>
                <TableHead className="w-36">加入时间</TableHead>
                <TableHead className="w-24 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((m) => (
                <TableRow key={m.user_id}>
                  <TableCell className="font-medium">{m.username}</TableCell>
                  <TableCell className="tabular-nums">{m.user_id}</TableCell>
                  <TableCell>
                    <RoleSelect
                      value={m.role ?? 'member'}
                      disabled={updateRole.isPending}
                      onChange={(role) => {
                        updateRole.mutate(
                          { userId: m.user_id as number, role },
                          {
                            onSuccess: () => toast.success('角色已更新'),
                            onError: (e) =>
                              toast.error(e instanceof Error ? e.message : '角色变更失败'),
                          },
                        );
                      }}
                    />
                  </TableCell>
                  <TableCell className="text-xs text-[var(--muted)]">
                    {fromNow(m.created_at) || '—'}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-[var(--destructive)]"
                      disabled={removeMember.isPending}
                      onClick={() => setRemoving(m)}
                    >
                      <Trash2 aria-hidden="true" />
                      移除
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>

      <AddMemberDialog open={addOpen} onOpenChange={setAddOpen} kbId={kbId} />

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null);
        }}
        title="移除成员"
        description={`将移除「${removing?.username ?? ''}」对本库的访问，其历史会话保留但立即失访。`}
        confirmLabel="移除"
        busy={removeMember.isPending}
        onConfirm={() => {
          if (removing?.user_id === undefined) return;
          removeMember.mutate(removing.user_id, {
            onSuccess: () => {
              toast.success('成员已移除');
              setRemoving(null);
            },
            onError: (e) => toast.error(e instanceof Error ? e.message : '移除失败'),
          });
        }}
      />
    </div>
  );
}

/* ------------------------------ 文档列表 Tab ------------------------------ */

/** 文档列表 Tab（复用 DocumentTable；接入入口按钮按 R6 控制）。 */
function DocumentsTab({
  kbId,
  mayIngest,
  mayManageContent,
  onWizard,
}: {
  kbId: number;
  mayIngest: boolean;
  mayManageContent: boolean;
  onWizard: () => void;
}) {
  const [page, setPage] = useState(1);
  const list = useDocumentsList(kbId, { page });

  return (
    <div className="space-y-4">
      <QueryBoundary
        query={list}
        skeleton={<Skeleton className="h-56 w-full" />}
        empty={
          <EmptyState
            icon={FolderOpen}
            title="还没有接入任何知识源"
            description={
              mayIngest
                ? '接入 Git 仓库、上传文件或接入网页，状态就绪后即可检索与问答。'
                : '请联系具备编辑权限的成员接入知识源。'
            }
            action={mayIngest ? { label: '接入知识源', onClick: onWizard } : undefined}
          />
        }
      >
        {(data: DocumentPage) => (
          <>
            <DocumentTable
              kbId={kbId}
              documents={data.items}
              mayIngest={mayIngest}
              mayManageContent={mayManageContent}
            />
            {data.total > data.page_size ? (
              <div className="flex items-center justify-end gap-2 text-sm text-[var(--muted)]">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page <= 1}
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                >
                  上一页
                </Button>
                <span className="tabular-nums">
                  第 {data.page} / {Math.max(1, Math.ceil(data.total / data.page_size))} 页 · 共{' '}
                  {data.total} 条
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={data.page >= Math.ceil(data.total / data.page_size)}
                  onClick={() => setPage((p) => p + 1)}
                >
                  下一页
                </Button>
              </div>
            ) : null}
          </>
        )}
      </QueryBoundary>
    </div>
  );
}

/* ------------------------------ 页面 ------------------------------ */

export function KbDetailPage() {
  const { kbId } = useParams();
  const kbIdNum = Number(kbId);
  const detail = useKbDetail(kbIdNum);

  // R6 权限：my_role（库详情返回）+ is_admin（登录态）→ 按钮显隐唯一依据
  const { mayIngest, mayManageContent, mayManageMembers } = useRole(detail.data?.my_role);

  const [tab, setTab] = useState<'documents' | 'members'>('documents');
  const [wizardOpen, setWizardOpen] = useState(false);

  // R6 非成员 404（后端不泄露库名存在性）：统一 EmptyState
  const isNotFound =
    detail.isError && detail.error instanceof ApiError && detail.error.status === 404;

  if (isNotFound) {
    return (
      <div className="mx-auto w-full max-w-5xl px-6 py-8">
        <EmptyState
          icon={FolderOpen}
          title="知识库不存在或无权访问"
          description="该知识库可能已被删除，或你的账号未被授权访问。"
          action={
            <Link to="/kbs">
              <Button variant="outline">返回知识库列表</Button>
            </Link>
          }
        />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl px-6 py-8">
      <QueryBoundary query={detail} skeleton={<Skeleton className="h-28 w-full" />}>
        {(data) => (
          <>
            {/* 头部：库信息 + 动作 */}
            <div className="kb-card mb-6 p-5">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <h1 className="truncate text-lg font-semibold" title={data.name ?? ''}>
                      {data.name}
                    </h1>
                    <RoleBadge role={data.my_role} />
                  </div>
                  <p className="mt-1 text-sm text-[var(--muted)]">
                    {data.description || '暂无描述'}
                  </p>
                  <p className="mt-2 text-xs text-[var(--faint)]">
                    {data.document_count ?? 0} 个文档 · 创建于 {fromNow(data.created_at) || '—'} ·
                    最近更新 {fromNow(data.updated_at) || '—'}
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Link to={`/kbs/${kbIdNum}/search`} className={buttonVariants({ variant: 'outline' })}>
                    <Search aria-hidden="true" />
                    检索
                  </Link>
                  <Link to={`/kbs/${kbIdNum}/chat`} className={buttonVariants({ variant: 'outline' })}>
                    <MessageCircle aria-hidden="true" />
                    问答
                  </Link>
                  {mayIngest ? (
                    <Button onClick={() => setWizardOpen(true)}>
                      <Plus aria-hidden="true" />
                      接入知识源
                    </Button>
                  ) : null}
                </div>
              </div>
            </div>

            {/* Tab 切换：文档列表 / 成员管理（仅管理员可见成员 tab） */}
            <div className="mb-4 flex gap-1.5 border-b border-[var(--border)] pb-2">
              <button
                type="button"
                className={cn('nav-item', tab === 'documents' && 'active')}
                onClick={() => setTab('documents')}
                aria-pressed={tab === 'documents'}
              >
                文档列表
              </button>
              {mayManageMembers ? (
                <button
                  type="button"
                  className={cn('nav-item', tab === 'members' && 'active')}
                  onClick={() => setTab('members')}
                  aria-pressed={tab === 'members'}
                >
                  成员管理
                </button>
              ) : null}
            </div>

            {tab === 'documents' ? (
              <DocumentsTab
                kbId={kbIdNum}
                mayIngest={mayIngest}
                mayManageContent={mayManageContent}
                onWizard={() => setWizardOpen(true)}
              />
            ) : null}
            {tab === 'members' ? <MembersTab kbId={kbIdNum} /> : null}
          </>
        )}
      </QueryBoundary>

      <IngestWizard open={wizardOpen} onOpenChange={setWizardOpen} kbId={kbIdNum} />
    </div>
  );
}
