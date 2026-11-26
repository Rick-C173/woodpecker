/**
 * 用户管理（FR-13，/admin/users）：
 * 列表（用户名/状态徽标/管理员徽标/加入时间）+ q/status 筛选 + 分页；
 * 创建用户 dialog（一次性初始密码展示卡 + 复制，仅此一次）；
 * 禁用/启用（USER_002 不能禁用自己）、设/撤管理员（USER_003 最后管理员保护）、
 * 重置密码（同初始密码展示卡）。
 */

import { useState } from 'react';
import { toast } from 'sonner';
import { KeyRound, ShieldCheck, ShieldOff, UserPlus, Users } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { PageHeader } from '@/components/shared/page-header';
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
import { InitialPasswordCard } from '@/components/shared/initial-password-card';
import { TablePagination } from '@/components/shared/table-pagination';
import { useDebouncedValue } from '@/hooks/use-debounced-value';
import {
  useAdminUsers,
  useCreateUser,
  usePatchUser,
  useResetPassword,
} from '@/hooks/use-admin';
import { useAuth } from '@/hooks/use-auth';
import { formatDateTime } from '@/lib/time';
import { cn } from '@/lib/utils';

function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

function StatusBadge({ status }: { status?: number }) {
  const enabled = status === 1;
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
        enabled
          ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
          : 'bg-[var(--destructive-soft)] text-[var(--destructive)]',
      )}
    >
      {enabled ? '启用' : '禁用'}
    </span>
  );
}

function AdminBadge({ isAdmin }: { isAdmin?: boolean }) {
  if (!isAdmin) return <span className="text-xs text-[var(--faint)]">—</span>;
  return <span className="badge role-admin">管理员</span>;
}

export function AdminUsersPage() {
  const { user: me } = useAuth();
  const [qInput, setQInput] = useState('');
  const [status, setStatus] = useState<number | null>(null);
  const [page, setPage] = useState(1);
  const q = useDebouncedValue(qInput.trim(), 300);

  const filters = { q, status, page };
  const users = useAdminUsers(filters);
  const createUser = useCreateUser();
  const patchUser = usePatchUser();
  const resetPassword = useResetPassword();

  // 创建用户 dialog
  const [createOpen, setCreateOpen] = useState(false);
  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newIsAdmin, setNewIsAdmin] = useState(false);
  // 一次性密码展示（创建 / 重置共用）
  const [issuedPassword, setIssuedPassword] = useState<{ username: string; password: string } | null>(null);
  // 重置密码确认
  const [pendingReset, setPendingReset] = useState<{ id: number; username: string } | null>(null);

  const handleCreate = () => {
    const username = newUsername.trim();
    const password = newPassword.trim();
    if (username.length === 0) {
      toast.error('请输入用户名');
      return;
    }
    if (password.length > 0 && password.length < 8) {
      toast.error('初始密码长度至少 8 位（留空则自动生成）');
      return;
    }
    createUser.mutate(
      { username, password: password || undefined, is_admin: newIsAdmin },
      {
        onSuccess: (resp) => {
          toast.success('用户已创建');
          setCreateOpen(false);
          setNewUsername('');
          setNewPassword('');
          setNewIsAdmin(false);
          if (resp.initial_password) {
            setIssuedPassword({ username: resp.username ?? username, password: resp.initial_password });
          }
        },
        onError: toastError,
      },
    );
  };

  const handlePatch = (id: number, body: { status?: number; is_admin?: boolean }, successMsg: string) => {
    patchUser.mutate(
      { id, body },
      {
        onSuccess: () => toast.success(successMsg),
        onError: toastError,
      },
    );
  };

  const handleResetPassword = () => {
    if (!pendingReset) return;
    resetPassword.mutate(pendingReset.id, {
      onSuccess: (resp) => {
        toast.success('密码已重置');
        if (resp.initial_password) {
          setIssuedPassword({ username: pendingReset.username, password: resp.initial_password });
        }
        setPendingReset(null);
      },
      onError: toastError,
    });
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      <PageHeader
        title="用户管理"
        description="创建账号、禁用/启用、分配管理员；初始密码仅创建/重置时展示一次。"
        actions={
          <Button onClick={() => setCreateOpen(true)} data-testid="create-user">
            <UserPlus aria-hidden="true" />
            创建用户
          </Button>
        }
      />

      {/* 筛选：q + status */}
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Input
          value={qInput}
          onChange={(e) => {
            setQInput(e.target.value);
            setPage(1);
          }}
          placeholder="按用户名搜索"
          className="w-56"
          aria-label="用户名关键词"
        />
        <Select
          value={status === null ? 'all' : String(status)}
          onValueChange={(v) => {
            setStatus(v === 'all' ? null : Number(v));
            setPage(1);
          }}
        >
          <SelectTrigger className="w-32" aria-label="状态筛选">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部状态</SelectItem>
            <SelectItem value="1">启用</SelectItem>
            <SelectItem value="2">禁用</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <QueryBoundary
        query={users}
        skeleton={
          <div className="space-y-2">
            {Array.from({ length: 5 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        }
        empty={
          <EmptyState
            icon={Users}
            title="没有匹配的用户"
            description="调整筛选条件，或创建第一个用户。"
            action={{ label: '创建用户', onClick: () => setCreateOpen(true) }}
          />
        }
      >
        {(data) => (
          <>
            <div className="kb-card p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>用户名</TableHead>
                    <TableHead className="w-24">状态</TableHead>
                    <TableHead className="w-28">角色</TableHead>
                    <TableHead className="w-20 text-right">知识库</TableHead>
                    <TableHead className="w-40">加入时间</TableHead>
                    <TableHead className="w-52 text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.items.map((u) => {
                    const isSelf = me?.user_id === u.id;
                    const enabled = u.status === 1;
                    return (
                      <TableRow key={u.id} data-testid="user-row">
                        <TableCell className="font-medium">
                          {u.username}
                          {isSelf ? (
                            <span className="ml-1.5 text-[10px] text-[var(--faint)]">（我）</span>
                          ) : null}
                        </TableCell>
                        <TableCell>
                          <StatusBadge status={u.status} />
                        </TableCell>
                        <TableCell>
                          <AdminBadge isAdmin={u.is_admin} />
                        </TableCell>
                        <TableCell className="text-right tabular-nums">{u.kb_count ?? 0}</TableCell>
                        <TableCell className="text-xs text-[var(--muted)]">
                          {formatDateTime(u.created_at) || '—'}
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex justify-end gap-1">
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={patchUser.isPending}
                              onClick={() =>
                                handlePatch(
                                  u.id ?? 0,
                                  { status: enabled ? 2 : 1 },
                                  enabled ? '账号已禁用' : '账号已启用',
                                )
                              }
                              title={enabled ? '禁用（USER_002：不能禁用自己）' : '启用'}
                            >
                              {enabled ? '禁用' : '启用'}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={patchUser.isPending}
                              onClick={() =>
                                handlePatch(
                                  u.id ?? 0,
                                  { is_admin: !u.is_admin },
                                  u.is_admin ? '已撤销管理员' : '已设为管理员',
                                )
                              }
                              title={
                                u.is_admin
                                  ? '撤销管理员（USER_003：最后管理员保护）'
                                  : '设为管理员'
                              }
                            >
                              {u.is_admin ? (
                                <>
                                  <ShieldOff aria-hidden="true" />
                                  撤管理员
                                </>
                              ) : (
                                <>
                                  <ShieldCheck aria-hidden="true" />
                                  设管理员
                                </>
                              )}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() =>
                                setPendingReset({ id: u.id ?? 0, username: u.username ?? '' })
                              }
                              title="重置密码（生成一次性初始密码）"
                            >
                              <KeyRound aria-hidden="true" />
                              重置密码
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
            <div className="mt-3">
              <TablePagination
                page={data.page}
                pageSize={data.page_size}
                total={data.total}
                onPageChange={setPage}
              />
            </div>
          </>
        )}
      </QueryBoundary>

      {/* 创建用户 dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>创建用户</DialogTitle>
            <DialogDescription>
              初始密码留空则自动生成；创建成功后展示一次性初始密码，仅此一次。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="new-username">用户名</Label>
              <Input
                id="new-username"
                value={newUsername}
                onChange={(e) => setNewUsername(e.target.value)}
                placeholder="登录用户名（唯一）"
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="new-password">初始密码</Label>
              <Input
                id="new-password"
                type="text"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                placeholder="留空自动生成；手动指定须 ≥8 位"
              />
            </div>
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={newIsAdmin}
                onChange={(e) => setNewIsAdmin(e.target.checked)}
                className="accent-[var(--primary)]"
              />
              设为系统管理员
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)} disabled={createUser.isPending}>
              取消
            </Button>
            <Button onClick={handleCreate} disabled={createUser.isPending}>
              创建
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 一次性初始密码展示卡 */}
      <Dialog
        open={issuedPassword !== null}
        onOpenChange={(open) => {
          if (!open) setIssuedPassword(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>请立即保存初始密码</DialogTitle>
            <DialogDescription>关闭本弹窗后将无法再次查看该密码。</DialogDescription>
          </DialogHeader>
          {issuedPassword ? (
            <InitialPasswordCard
              username={issuedPassword.username}
              password={issuedPassword.password}
            />
          ) : null}
        </DialogContent>
      </Dialog>

      {/* 重置密码二次确认 */}
      <ConfirmDialog
        open={pendingReset !== null}
        onOpenChange={(open) => {
          if (!open) setPendingReset(null);
        }}
        title="重置密码"
        description={`将重置用户「${pendingReset?.username ?? ''}」的密码，其当前密码与登录态立即失效，并展示一次性初始密码。`}
        confirmLabel="重置"
        destructive={false}
        busy={resetPassword.isPending}
        onConfirm={handleResetPassword}
      />
    </div>
  );
}
