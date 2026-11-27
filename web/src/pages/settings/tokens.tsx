/**
 * API Token 管理页（阶段 D，/settings/tokens；FR-20 / M15 契约）：
 * - 列表：名称 / 前缀（wpk_ + prefix，明文永不回显）/ 状态 / 最近使用 / 创建时间；
 * - 创建：名称 + 有效期（缺省永久）→ 明文 wpk_<64hex> 仅弹窗展示一次 + 复制；
 * - 吊销：软吊销（revoked_at），二次确认；TOK_001 超 20 个 toast 提示不关闭；
 * - MCP 指引：Bearer wpk_ 调用业务 API / POST /mcp（Streamable HTTP）接入提示。
 */

import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import { BookOpen, Copy, KeyRound, Plus, Trash2 } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { PageHeader } from '@/components/shared/page-header';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import { Button } from '@/components/ui/button';
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Skeleton } from '@/components/ui/skeleton';
import { fromNow, formatDateTime } from '@/lib/time';
import { cn } from '@/lib/utils';
import { useCreateToken, useRevokeToken, useTokensList } from '@/hooks/use-tokens';
import type { components } from '@/api/schema';

type APITokenItem = components['schemas']['service.APITokenItem'];

const EXPIRY_OPTIONS = [
  { value: '0', label: '永久有效' },
  { value: '30', label: '30 天' },
  { value: '90', label: '90 天' },
  { value: '365', label: '365 天' },
];

/** 状态派生：已吊销 > 已过期 > 有效。 */
function tokenStatus(t: APITokenItem): { label: string; tone: 'success' | 'danger' | 'muted' } {
  if (t.revoked_at) return { label: '已吊销', tone: 'danger' };
  if (t.expires_at && new Date(t.expires_at).getTime() < Date.now()) {
    return { label: '已过期', tone: 'muted' };
  }
  return { label: '有效', tone: 'success' };
}

function StatusPill({ t }: { t: APITokenItem }) {
  const s = tokenStatus(t);
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
        s.tone === 'success' && 'bg-[var(--success-weak)] text-[var(--success)]',
        s.tone === 'danger' && 'bg-[var(--destructive-soft)] text-[var(--destructive)]',
        s.tone === 'muted' && 'bg-[var(--muted-bg)] text-[var(--muted)]',
      )}
    >
      {s.label}
    </span>
  );
}

/** 建表单 dialog；成功后切换到明文展示 dialog（仅一次）。 */
function CreateTokenDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (plaintext: string) => void;
}) {
  const createToken = useCreateToken();
  const [error, setError] = useState<string | null>(null);

  const schema = z.object({
    name: z.string().min(1, '请输入名称').max(50, '名称不超过 50 字符'),
    expires: z.enum(['0', '30', '90', '365']),
  });
  const form = useForm<{ name: string; expires: '0' | '30' | '90' | '365' }>({
    resolver: zodResolver(schema),
    defaultValues: { name: '', expires: '0' },
  });

  // dialog 每次打开重置
  const [lastOpen, setLastOpen] = useState(false);
  if (open !== lastOpen) {
    setLastOpen(open);
    if (open) {
      form.reset({ name: '', expires: '0' });
      setError(null);
    }
  }

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    try {
      const ct = await createToken.mutateAsync({
        name: values.name,
        expires_in_days: values.expires === '0' ? undefined : Number(values.expires),
      });
      onOpenChange(false);
      onCreated(ct.token);
    } catch (e) {
      // TOK_001 超 20 个：toast 提示但不关闭，保留表单
      setError(e instanceof Error ? e.message : '创建失败，请稍后重试');
    }
  });

  const submitting = createToken.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>新建 API Token</DialogTitle>
          <DialogDescription>
            名称仅用于识别用途；每人最多 20 个有效 Token。
          </DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={onSubmit} noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="token-name">名称 *</Label>
            <Input
              id="token-name"
              placeholder="如：本机 MCP 客户端"
              {...form.register('name')}
            />
            {form.formState.errors.name ? (
              <p className="text-xs text-[var(--destructive)]">
                {form.formState.errors.name.message}
              </p>
            ) : null}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="token-expiry">有效期</Label>
            <Select
              value={form.watch('expires')}
              onValueChange={(v) => form.setValue('expires', v as '0' | '30' | '90' | '365')}
            >
              <SelectTrigger id="token-expiry" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EXPIRY_OPTIONS.map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {error ? (
            <p className="rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2 text-xs text-[var(--destructive)]">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
              取消
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? '创建中…' : '创建'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** 明文展示 dialog：仅本次返回，关闭后无法再取。 */
function PlaintextDialog({ plaintext, onClose }: { plaintext: string; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Token 已创建</DialogTitle>
          <DialogDescription>
            明文仅此一次展示，关闭后无法再次查看，请立即复制保存。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label htmlFor="token-plain">Token 明文</Label>
          <div className="flex gap-2">
            <Input
              id="token-plain"
              readOnly
              value={plaintext}
              className="font-mono text-xs"
              onFocus={(e) => e.target.select()}
            />
            <Button
              type="button"
              variant="outline"
              size="icon"
              aria-label="复制 Token"
              onClick={() => {
                void navigator.clipboard.writeText(plaintext);
                toast.success('已复制到剪贴板');
              }}
            >
              <Copy aria-hidden="true" />
            </Button>
          </div>
          <p className="text-xs text-[var(--faint)]">
            调用方式：<code className="rounded bg-[var(--muted-bg)] px-1">Authorization: Bearer {'{token}'}</code>
          </p>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>我已保存，关闭</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function SettingsTokensPage() {
  const list = useTokensList();
  const revoke = useRevokeToken();
  const [createOpen, setCreateOpen] = useState(false);
  const [plaintext, setPlaintext] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<APITokenItem | null>(null);

  return (
    <div className="w-full px-8 py-7">
      <PageHeader
        title="API Token"
        description="机器凭证（wpk_ 前缀）用于脚本与 MCP 客户端调用；不受改密影响，仅吊销或禁用时失效。"
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus aria-hidden="true" />
            新建 Token
          </Button>
        }
      />

      {/* MCP 接入指引 */}
      <div className="mb-6 flex flex-wrap items-center gap-x-6 gap-y-2 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-4 py-3 text-xs text-[var(--muted)]">
        <span className="inline-flex items-center gap-1.5 font-medium text-[var(--foreground)]">
          <BookOpen className="size-3.5" aria-hidden="true" />
          接入指引
        </span>
        <span>
          REST：<code className="rounded bg-[var(--muted-bg)] px-1 font-mono">Authorization: Bearer wpk_…</code>
        </span>
        <span>
          MCP：<code className="rounded bg-[var(--muted-bg)] px-1 font-mono">POST /mcp</code>
          （Streamable HTTP，Bearer 同样可用）
        </span>
        <span className="text-[var(--faint)]">详见仓库文档 md/MCP接入指南.md</span>
      </div>

      <QueryBoundary
        query={list}
        skeleton={
          <div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)]">
            <div className="space-y-2 p-4">
              {Array.from({ length: 3 }, (_, i) => (
                <Skeleton key={i} className="h-10 w-full" />
              ))}
            </div>
          </div>
        }
        empty={
          <EmptyState
            icon={KeyRound}
            title="还没有 API Token"
            description="创建 Token 后即可在脚本或 MCP 客户端中调用知识库接口。"
            action={{ label: '新建 Token', onClick: () => setCreateOpen(true) }}
          />
        }
      >
        {(items) => (
          <div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)]">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead className="w-44">前缀</TableHead>
                  <TableHead className="w-24">状态</TableHead>
                  <TableHead className="w-36">最近使用</TableHead>
                  <TableHead className="w-36">创建时间</TableHead>
                  <TableHead className="w-20 text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((t) => (
                  <TableRow key={t.id}>
                    <TableCell className="font-medium">{t.name}</TableCell>
                    <TableCell>
                      <code className="rounded bg-[var(--muted-bg)] px-1.5 py-0.5 font-mono text-xs text-[var(--muted)]">
                        wpk_{t.prefix}…
                      </code>
                    </TableCell>
                    <TableCell>
                      <StatusPill t={t} />
                    </TableCell>
                    <TableCell className="text-xs text-[var(--muted)]">
                      {t.last_used_at ? fromNow(t.last_used_at) : '从未使用'}
                    </TableCell>
                    <TableCell className="text-xs text-[var(--muted)]">
                      {formatDateTime(t.created_at) || '—'}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-[var(--destructive)]"
                        disabled={revoke.isPending || t.revoked_at !== null && t.revoked_at !== undefined}
                        onClick={() => setRevoking(t)}
                      >
                        <Trash2 aria-hidden="true" />
                        吊销
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </QueryBoundary>

      <CreateTokenDialog open={createOpen} onOpenChange={setCreateOpen} onCreated={setPlaintext} />
      {plaintext ? <PlaintextDialog plaintext={plaintext} onClose={() => setPlaintext(null)} /> : null}

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => {
          if (!open) setRevoking(null);
        }}
        title="吊销 Token"
        description={`将吊销「${revoking?.name ?? ''}」（前缀 wpk_${revoking?.prefix ?? ''}…），所有使用该 Token 的客户端立即 401，不可恢复。`}
        confirmLabel="吊销"
        busy={revoke.isPending}
        onConfirm={() => {
          if (revoking?.id === undefined) return;
          revoke.mutate(revoking.id, {
            onSuccess: () => {
              toast.success('Token 已吊销');
              setRevoking(null);
            },
            onError: (e) => toast.error(e instanceof Error ? e.message : '吊销失败'),
          });
        }}
      />
    </div>
  );
}
