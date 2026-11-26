/**
 * 知识库列表页（F3）：GET /kbs 三态 + 建库/编辑库（管理员，KB_001 提示但不阻止）+
 * 删库（管理员，输入库名确认）。卡片展示名称/描述/文档数/角色徽标/相对更新时间。
 */

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import { FolderKanban, Globe2, Pencil, Plus, Trash2 } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { RoleBadge } from '@/components/shared/role-badge';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { fromNow } from '@/lib/time';
import { useAuth } from '@/hooks/use-auth';
import { useCreateKb, useDeleteKb, useKbsList, useUpdateKb } from '@/hooks/use-kbs';
import type { components } from '@/api/schema';

type KBItem = components['schemas']['service.KBItem'];

const kbFormSchema = z.object({
  name: z.string().min(1, '请输入知识库名称').max(50, '名称不超过 50 字符'),
  description: z.string().max(500, '描述不超过 500 字符').optional(),
  visibility: z.enum(['1', '2']),
});

type KbFormValues = z.infer<typeof kbFormSchema>;

/** 建库/编辑库共用表单 dialog；KB_001 toast 提示但不关闭不阻止。 */
function KbFormDialog({
  open,
  onOpenChange,
  editing,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 编辑目标；null = 新建 */
  editing: KBItem | null;
}) {
  const createKb = useCreateKb();
  const updateKb = useUpdateKb(editing?.id ?? 0);

  const form = useForm<KbFormValues>({
    resolver: zodResolver(kbFormSchema),
    defaultValues: { name: '', description: '', visibility: '1' },
  });

  // dialog 每次打开时按目标回填
  const [lastOpen, setLastOpen] = useState(false);
  if (open !== lastOpen) {
    setLastOpen(open);
    if (open) {
      form.reset({
        name: editing?.name ?? '',
        description: editing?.description ?? '',
        visibility: String(editing?.visibility ?? 1) === '2' ? '2' : '1',
      });
    }
  }

  const onSubmit = form.handleSubmit(async (values) => {
    try {
      if (editing) {
        await updateKb.mutateAsync({
          name: values.name,
          description: values.description || undefined,
          visibility: Number(values.visibility),
        });
        toast.success('知识库已更新');
      } else {
        await createKb.mutateAsync({
          name: values.name,
          description: values.description || undefined,
          visibility: Number(values.visibility),
        });
        toast.success('知识库已创建');
      }
      onOpenChange(false);
    } catch (e) {
      // KB_001 名称重复：提示但不阻止（保留表单继续编辑）
      toast.error(e instanceof Error ? e.message : '保存失败，请稍后重试');
    }
  });

  const submitting = createKb.isPending || updateKb.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{editing ? '编辑知识库' : '新建知识库'}</DialogTitle>
          <DialogDescription>
            {editing ? '名称同账号内唯一，不超过 50 字符。' : '创建后可在库内接入 Git 仓库、上传文件或接入网页。'}
          </DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={onSubmit} noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="kb-name">名称 *</Label>
            <Input id="kb-name" placeholder="如：订单服务知识库" {...form.register('name')} />
            {form.formState.errors.name ? (
              <p className="text-xs text-[var(--destructive)]">
                {form.formState.errors.name.message}
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="kb-desc">描述</Label>
            <Textarea
              id="kb-desc"
              rows={3}
              placeholder="一句话说明这个库的用途（可选）"
              {...form.register('description')}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="kb-visibility">可见性</Label>
            <Select value={form.watch('visibility')} onValueChange={(v) => form.setValue('visibility', v as '1' | '2')}>
              <SelectTrigger id="kb-visibility">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="1">私有 —— 仅成员可见</SelectItem>
                <SelectItem value="2">全员可见 —— 登录用户可浏览与问答（只读）</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-[var(--faint)]">
              全员可见适合公司规范、新人指南等共享内容；写操作仍按成员角色控制。
            </p>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
              取消
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? '保存中…' : editing ? '保存' : '创建'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function KbsPage() {
  const { user } = useAuth();
  const isAdmin = user?.is_admin ?? false;
  const list = useKbsList();

  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<KBItem | null>(null);
  const [deleting, setDeleting] = useState<KBItem | null>(null);
  const deleteKb = useDeleteKb();

  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };

  const openEdit = (kb: KBItem) => {
    setEditing(kb);
    setFormOpen(true);
  };

  return (
    <div className="mx-auto w-full max-w-5xl px-6 py-8">
      <div className="mb-6 flex items-center justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold">知识库</h1>
          <p className="text-sm text-[var(--muted)]">按成员授权访问；管理员可见全部库。</p>
        </div>
        {isAdmin ? (
          <Button onClick={openCreate}>
            <Plus aria-hidden="true" />
            新建知识库
          </Button>
        ) : null}
      </div>

      <QueryBoundary
        query={list}
        skeleton={
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-32 w-full" />
            ))}
          </div>
        }
        empty={
          <EmptyState
            icon={FolderKanban}
            title={isAdmin ? '还没有知识库，创建第一个吧' : '还没有可访问的知识库'}
            description={
              isAdmin
                ? '创建知识库后即可接入代码仓库、文档与网页。'
                : '被授权或全员可见的知识库会出现在这里。'
            }
            action={isAdmin ? { label: '新建知识库', onClick: openCreate } : undefined}
          />
        }
      >
        {(items) => (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            {items.map((kb) => (
              <Link key={kb.id} to={`/kbs/${kb.id}`} className="kb-card block">
                <div className="flex items-start justify-between gap-2">
                  <h2 className="truncate text-[15px] font-semibold" title={kb.name ?? ''}>
                    {kb.name}
                  </h2>
                  <div className="flex shrink-0 items-center gap-1">
                    {kb.visibility === 2 ? (
                      <span
                        className="inline-flex items-center gap-1 rounded-full bg-[var(--accent-soft)] px-2 py-0.5 text-xs text-[var(--accent)]"
                        title="全员可见：登录用户可浏览与问答（只读）"
                      >
                        <Globe2 className="size-3" aria-hidden="true" />
                        全员可见
                      </span>
                    ) : null}
                    <RoleBadge role={kb.my_role} />
                  </div>
                </div>
                <p className="mt-1 line-clamp-2 min-h-10 text-[13px] text-[var(--muted)]">
                  {kb.description || '暂无描述'}
                </p>
                <div className="mt-3 flex items-center justify-between text-xs text-[var(--faint)]">
                  <span>{kb.document_count ?? 0} 个文档</span>
                  <span>{fromNow(kb.updated_at) || '—'}</span>
                </div>
                {isAdmin ? (
                  <div className="mt-3 flex justify-end gap-1 border-t border-[var(--border)] pt-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        openEdit(kb);
                      }}
                    >
                      <Pencil aria-hidden="true" />
                      编辑
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-[var(--destructive)]"
                      onClick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        setDeleting(kb);
                      }}
                    >
                      <Trash2 aria-hidden="true" />
                      删除
                    </Button>
                  </div>
                ) : null}
              </Link>
            ))}
          </div>
        )}
      </QueryBoundary>

      <KbFormDialog open={formOpen} onOpenChange={setFormOpen} editing={editing} />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title="删除知识库"
        description={`将级联删除「${deleting?.name ?? ''}」内的全部文档、分块、会话与反馈，不可恢复。`}
        confirmLabel="删除"
        confirmName={deleting?.name}
        confirmNameHint={`输入「${deleting?.name ?? ''}」以确认删除`}
        busy={deleteKb.isPending}
        onConfirm={() => {
          if (deleting?.id === undefined) return;
          deleteKb.mutate(deleting.id, {
            onSuccess: () => {
              toast.success('知识库已删除');
              setDeleting(null);
            },
            onError: (e) => toast.error(e instanceof Error ? e.message : '删除失败，请稍后重试'),
          });
        }}
      />
    </div>
  );
}
