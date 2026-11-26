/**
 * 邀请管理（FR-16，/admin/invitations）：
 * 批量邀请（textarea 粘贴邮箱，逗号/换行分隔）→ 逐条结果（成功/失败/降级链接复制）；
 * 邀请列表（状态徽标 + 发送状态 + 过期时间）+ 重发/撤销。
 */

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { toast } from 'sonner';
import { Copy, Mail, RotateCw, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
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
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import { TablePagination } from '@/components/shared/table-pagination';
import {
  deleteInvitation,
  getInvitations,
  postResendInvitation,
  postInvitations,
  type InviteItem,
  type InviteResult,
} from '@/api/manual-body';
import { formatDateTime } from '@/lib/time';
import { cn } from '@/lib/utils';

function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

const STATUS_LABEL: Record<string, string> = {
  pending: '待接受',
  accepted: '已接受',
  revoked: '已撤销',
};
const SEND_LABEL: Record<string, string> = {
  sent: '已发送',
  failed: '发送失败',
  not_sent: '未发送',
};

function StatusBadge({ status }: { status: string }) {
  const cls =
    status === 'accepted'
      ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
      : status === 'revoked'
        ? 'bg-[var(--destructive-soft)] text-[var(--destructive)]'
        : 'bg-[var(--muted-bg)] text-[var(--muted-foreground)]';
  return (
    <span className={cn('inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium', cls)}>
      {STATUS_LABEL[status] ?? status}
    </span>
  );
}

export function AdminInvitationsPage() {
  // 批量邀请表单
  const [emailsInput, setEmailsInput] = useState('');
  const [results, setResults] = useState<InviteResult[] | null>(null);
  const [sending, setSending] = useState(false);
  // 列表
  const [status, setStatus] = useState('');
  const [page, setPage] = useState(1);
  const pageSize = 20;
  const [items, setItems] = useState<InviteItem[] | null>(null);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [pendingRevoke, setPendingRevoke] = useState<InviteItem | null>(null);

  const loadList = async (p: number, st: string) => {
    setLoading(true);
    try {
      const { data, response } = await getInvitations(st, p, pageSize);
      if (!response.ok) {
        toastError(new Error('加载邀请列表失败'));
        return;
      }
      setItems(data?.items ?? []);
      setTotal(data?.total ?? 0);
      setPage(p);
    } catch (e) {
      toastError(e);
    } finally {
      setLoading(false);
    }
  };

  const handleInvite = async () => {
    const emails = emailsInput
      .split(/[,\n;，；\s]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (emails.length === 0) {
      toast.error('请输入至少一个邮箱（逗号或换行分隔可批量）');
      return;
    }
    setSending(true);
    try {
      const { data, response } = await postInvitations(emails);
      if (!response.ok || !data?.results) {
        toast.error('邀请发起失败');
        return;
      }
      setResults(data.results);
      setEmailsInput('');
      const okCount = data.results.filter((r: InviteResult) => r.ok).length;
      const sentCount = data.results.filter((r: InviteResult) => r.sent).length;
      toast.success(`已创建 ${okCount} 条邀请，其中 ${sentCount} 封邮件已发送`);
      await loadList(1, status);
    } catch (e) {
      toastError(e);
    } finally {
      setSending(false);
    }
  };

  const copyLink = async (link: string) => {
    try {
      await navigator.clipboard.writeText(link);
      toast.success('邀请链接已复制');
    } catch {
      toast.error('复制失败，请手动复制');
    }
  };

  const handleResend = async (id: number) => {
    try {
      const { response, data } = await postResendInvitation(id);
      if (!response.ok) {
        toast.error('重发失败');
        return;
      }
      toast.success('已重发（新有效期 7 天）');
      if (data?.link) await copyLink(data.link);
      await loadList(page, status);
    } catch (e) {
      toastError(e);
    }
  };

  const handleRevoke = async () => {
    if (!pendingRevoke) return;
    try {
      const { response } = await deleteInvitation(pendingRevoke.id);
      if (!response.ok) {
        toast.error('撤销失败');
        return;
      }
      toast.success('邀请已撤销');
      await loadList(page, status);
    } catch (e) {
      toastError(e);
    } finally {
      setPendingRevoke(null);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold">邀请管理</h1>
        <p className="mt-1 text-sm text-[var(--faint)]">
          通过邮箱邀请成员加入；被邀请人点击邮件中的链接自设密码完成注册。
          <Link to="/admin/email" className="ml-1 underline">配置邮件服务器 →</Link>
        </p>
      </div>

      {/* 批量邀请 */}
      <section className="rounded-lg border border-[var(--border)] p-4">
        <div className="flex items-center gap-2">
          <Mail className="size-4 text-[var(--faint)]" aria-hidden="true" />
          <Label htmlFor="invite-emails">邀请邮箱（逗号或换行分隔，单次最多 50 个）</Label>
        </div>
        <textarea
          id="invite-emails"
          value={emailsInput}
          onChange={(e) => setEmailsInput(e.target.value)}
          placeholder={'alice@corp.com\nbob@corp.com'}
          rows={4}
          className="mt-2 w-full rounded-md border border-[var(--border)] bg-transparent px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-[var(--accent)]"
        />
        <div className="mt-2 flex items-center justify-between">
          <p className="text-xs text-[var(--faint)]">
            SMTP 未配置或发送失败时，邀请仍会创建并可复制链接手动转发。
          </p>
          <Button onClick={handleInvite} disabled={sending}>
            {sending ? '发送中…' : '发送邀请'}
          </Button>
        </div>

        {results && results.length > 0 && (
          <div className="mt-4 space-y-2">
            <p className="text-sm font-medium">本次结果</p>
            {results.map((r) => (
              <div
                key={r.email}
                className={cn(
                  'flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm',
                  r.ok ? 'border-[var(--border)]' : 'border-[var(--destructive)]',
                )}
              >
                <span className="font-mono">{r.email}</span>
                {r.ok ? (
                  <>
                    <span className="text-xs text-[var(--faint)]">
                      {r.sent ? '邮件已发送' : '未发送（降级）'}
                    </span>
                    {r.link ? (
                      <Button variant="outline" size="sm" onClick={() => copyLink(r.link!)}>
                        <Copy className="size-3.5" aria-hidden="true" />
                        复制链接
                      </Button>
                    ) : (
                      !r.sent && (
                        <span className="text-xs text-[var(--muted)]">
                          邀请已创建，但未配置 APP_BASE_URL，无法生成注册链接（请配置后重发）
                        </span>
                      )
                    )}
                  </>
                ) : (
                  <span className="text-xs text-[var(--destructive)]">{r.error}</span>
                )}
              </div>
            ))}
          </div>
        )}
      </section>

      {/* 邀请列表 */}
      <section className="space-y-3">
        <div className="flex items-center gap-2">
          <Input
            placeholder="状态过滤：空=全部 / pending / accepted / revoked"
            value={status}
            onChange={(e) => {
              setStatus(e.target.value.trim());
              void loadList(1, e.target.value.trim());
            }}
            className="max-w-xs"
          />
          <span className="text-xs text-[var(--faint)]">共 {total} 条</span>
        </div>
        <div className="rounded-lg border border-[var(--border)]">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>邮箱</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>发送</TableHead>
                <TableHead>过期时间</TableHead>
                <TableHead>创建时间</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-sm text-[var(--faint)]">
                    加载中…
                  </TableCell>
                </TableRow>
              )}
              {!loading && items?.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-sm text-[var(--faint)]">
                    暂无邀请
                  </TableCell>
                </TableRow>
              )}
              {items?.map((inv) => (
                <TableRow key={inv.id}>
                  <TableCell className="font-mono">{inv.email}</TableCell>
                  <TableCell><StatusBadge status={inv.status} /></TableCell>
                  <TableCell>
                    <span
                      className={cn(
                        'text-xs',
                        inv.send_status === 'failed' ? 'text-[var(--destructive)]' : 'text-[var(--faint)]',
                      )}
                      title={inv.send_error}
                    >
                      {SEND_LABEL[inv.send_status] ?? inv.send_status}
                    </span>
                  </TableCell>
                  <TableCell>{formatDateTime(inv.expires_at)}</TableCell>
                  <TableCell>{formatDateTime(inv.created_at)}</TableCell>
                  <TableCell className="text-right">
                    {inv.status === 'pending' && (
                      <div className="inline-flex gap-1">
                        <Button variant="outline" size="sm" onClick={() => handleResend(inv.id)}>
                          <RotateCw className="size-3.5" aria-hidden="true" />
                          重发
                        </Button>
                        <Button variant="outline" size="sm" onClick={() => setPendingRevoke(inv)}>
                          <Trash2 className="size-3.5" aria-hidden="true" />
                          撤销
                        </Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <TablePagination page={page} pageSize={pageSize} total={total} onPageChange={(p) => void loadList(p, status)} />
      </section>

      <ConfirmDialog
        open={pendingRevoke !== null}
        onOpenChange={(v) => !v && setPendingRevoke(null)}
        title="撤销邀请"
        description={`确定撤销对 ${pendingRevoke?.email ?? ''} 的邀请？撤销后邀请链接立即失效。`}
        confirmLabel="撤销"
        onConfirm={handleRevoke}
      />
    </div>
  );
}
