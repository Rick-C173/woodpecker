/**
 * 用户组管理（FR-18，/admin/groups）：
 * 组列表（名称/描述/成员数/授权库数）+ 建组/改组/删组（级联确认）；
 * 组员管理 dialog（用户下拉 + 加人 GROUP_002 提示 + 移除）。
 */

import { useState } from 'react';
import { toast } from 'sonner';
import { Pencil, Plus, Trash2, UsersRound } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import {
  useAddGroupMember,
  useCreateGroup,
  useDeleteGroup,
  useGroupMembers,
  useGroupsList,
  useRemoveGroupMember,
  useUpdateGroup,
} from '@/hooks/use-groups';
import { useAdminUsers } from '@/hooks/use-admin';
import { formatDateTime } from '@/lib/time';
import type { components } from '@/api/schema';

type GroupItem = components['schemas']['service.GroupItem'];

function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

export function AdminGroupsPage() {
  const groups = useGroupsList();
  const createGroup = useCreateGroup();
  const updateGroup = useUpdateGroup();
  const deleteGroup = useDeleteGroup();
  const addMember = useAddGroupMember();
  const removeMember = useRemoveGroupMember();
  const usersQ = useAdminUsers({ q: '', status: null, page: 1 });

  // 表单 dialog（建/改共用）
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<GroupItem | null>(null);
  const [name, setName] = useState('');
  const [desc, setDesc] = useState('');
  // 删组确认
  const [deleting, setDeleting] = useState<GroupItem | null>(null);
  // 组员管理
  const [memberOpen, setMemberOpen] = useState(false);
  const [memberGroup, setMemberGroup] = useState<GroupItem | null>(null);
  const [newUserId, setNewUserId] = useState<string>('');

  const openCreate = () => {
    setEditing(null);
    setName('');
    setDesc('');
    setFormOpen(true);
  };

  const openEdit = (g: GroupItem) => {
    setEditing(g);
    setName(g.name ?? '');
    setDesc(g.description ?? '');
    setFormOpen(true);
  };

  const handleSubmit = () => {
    if (name.trim().length === 0) {
      toast.error('请输入组名');
      return;
    }
    const body = { name: name.trim(), description: desc.trim() };
    const opts = {
      onSuccess: () => {
        toast.success(editing?.id ? '用户组已更新' : '用户组已创建');
        setFormOpen(false);
      },
      onError: toastError,
    };
    if (editing?.id) {
      updateGroup.mutate({ id: editing.id, body }, opts);
    } else {
      createGroup.mutate(body, opts);
    }
  };

  const handleAddMember = () => {
    if (!memberGroup || !newUserId) return;
    addMember.mutate(
      { groupId: memberGroup.id ?? 0, userId: Number(newUserId) },
      {
        onSuccess: () => {
          toast.success('成员已加入');
          setNewUserId('');
        },
        onError: toastError,
      },
    );
  };

  return (
    <div className="w-full">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">用户组</h1>
          <p className="text-sm text-[var(--muted)]">
            按部门/项目组整组授权；组员的变化在全部授权库即时生效。
          </p>
        </div>
        <Button onClick={openCreate} data-testid="create-group">
          <Plus aria-hidden="true" />
          新建组
        </Button>
      </div>

      <QueryBoundary
        query={groups}
        skeleton={
          <div className="space-y-2">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        }
        empty={
          <EmptyState
            icon={UsersRound}
            title="还没有用户组"
            description="创建组并加入成员，再把组授权到知识库，即可整组生效。"
            action={{ label: '新建组', onClick: openCreate }}
          />
        }
      >
        {(items) => (
          <div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] shadow-[var(--shadow)]">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>组名</TableHead>
                  <TableHead>描述</TableHead>
                  <TableHead className="w-20 text-right">成员</TableHead>
                  <TableHead className="w-20 text-right">授权库</TableHead>
                  <TableHead className="w-36">创建时间</TableHead>
                  <TableHead className="w-56 text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((g) => (
                  <TableRow key={g.id} data-testid="group-row">
                    <TableCell className="font-medium">{g.name}</TableCell>
                    <TableCell className="text-[var(--muted)]">{g.description || '—'}</TableCell>
                    <TableCell className="text-right tabular-nums">{g.member_count ?? 0}</TableCell>
                    <TableCell className="text-right tabular-nums">{g.kb_count ?? 0}</TableCell>
                    <TableCell className="text-xs text-[var(--muted)]">
                      {formatDateTime(g.created_at) || '—'}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            setMemberGroup(g);
                            setMemberOpen(true);
                          }}
                        >
                          组员
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => openEdit(g)}>
                          <Pencil aria-hidden="true" />
                          编辑
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-[var(--destructive)]"
                          onClick={() => setDeleting(g)}
                        >
                          <Trash2 aria-hidden="true" />
                          删除
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </QueryBoundary>

      {/* 建组/改组 */}
      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{editing ? '编辑用户组' : '新建用户组'}</DialogTitle>
            <DialogDescription>组名全局唯一，不超过 50 字符。</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="group-name">组名 *</Label>
              <Input id="group-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="如：后端平台组" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="group-desc">描述</Label>
              <Input id="group-desc" value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="一句话说明（可选）" />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setFormOpen(false)} disabled={createGroup.isPending || updateGroup.isPending}>
              取消
            </Button>
            <Button onClick={handleSubmit} disabled={createGroup.isPending || updateGroup.isPending}>
              {createGroup.isPending || updateGroup.isPending ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 删组（级联） */}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title="删除用户组"
        description={`将删除组「${deleting?.name ?? ''}」并级联解除其全部组员与授权，组员在授权库立即失访，不可恢复。`}
        confirmLabel="删除"
        busy={deleteGroup.isPending}
        onConfirm={() => {
          if (!deleting) return;
          deleteGroup.mutate(deleting.id ?? 0, {
            onSuccess: () => {
              toast.success('用户组已删除');
              setDeleting(null);
            },
            onError: toastError,
          });
        }}
      />

      {/* 组员管理 */}
      <Dialog open={memberOpen} onOpenChange={setMemberOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>组员管理 — {memberGroup?.name}</DialogTitle>
            <DialogDescription>加入组即获得该组授权库的角色；移出即时失访。</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <Select value={newUserId} onValueChange={setNewUserId}>
                <SelectTrigger className="flex-1" aria-label="选择用户">
                  <SelectValue placeholder="选择要加入的用户" />
                </SelectTrigger>
                <SelectContent>
                  {(usersQ.data?.items ?? []).map((u) => (
                    <SelectItem key={u.id} value={String(u.id)}>
                      {u.username}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button onClick={handleAddMember} disabled={!newUserId || addMember.isPending}>
                {addMember.isPending ? '加入中…' : '加入'}
              </Button>
            </div>
            <div className="max-h-64 space-y-1 overflow-auto">
              {memberGroup ? (
                <GroupMemberRows
                  groupId={memberGroup.id ?? 0}
                  onRemove={(uid) =>
                    removeMember.mutate(
                      { groupId: memberGroup.id ?? 0, userId: uid },
                      { onSuccess: () => toast.success('已移出'), onError: toastError },
                    )
                  }
                  removing={removeMember.isPending}
                />
              ) : null}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function GroupMemberRows({
  groupId,
  onRemove,
  removing,
}: {
  groupId: number;
  onRemove: (userId: number) => void;
  removing: boolean;
}) {
  const members = useGroupMembers(groupId);
  if (members.isLoading) return <Skeleton className="h-20 w-full" />;
  const items = members.data ?? [];
  if (items.length === 0) {
    return <p className="py-4 text-center text-sm text-[var(--faint)]">暂无组员</p>;
  }
  return (
    <div className="divide-y divide-[var(--border)] rounded-md border border-[var(--border)]">
      {items.map((m) => (
        <div key={m.user_id} className="flex items-center justify-between px-3 py-2 text-sm">
          <span className="font-medium">{m.username}</span>
          <Button
            variant="ghost"
            size="sm"
            className="text-[var(--destructive)]"
            disabled={removing}
            onClick={() => onRemove(m.user_id ?? 0)}
          >
            <Trash2 aria-hidden="true" />
            移出
          </Button>
        </div>
      ))}
    </div>
  );
}
