/**
 * 反馈列表（FR-14 管理侧，/admin/feedbacks）：kb_id / reason / from / to 筛选 + 表格 + 分页。
 */

import { useState } from 'react';
import { ClipboardList } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { TablePagination } from '@/components/shared/table-pagination';
import { useFeedbacks } from '@/hooks/use-admin';
import { FEEDBACK_REASONS, FEEDBACK_REASON_LABELS, type FeedbackReason } from '@/lib/chat-events';
import { formatDateTime } from '@/lib/time';

/** datetime-local 本地值 → RFC3339 UTC；空值返回 null。 */
function localToRfc3339Utc(value: string): string | null {
  if (!value) return null;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

export function AdminFeedbacksPage() {
  const [kbIdInput, setKbIdInput] = useState('');
  const [reason, setReason] = useState<number | null>(null);
  const [fromLocal, setFromLocal] = useState('');
  const [toLocal, setToLocal] = useState('');
  const [page, setPage] = useState(1);

  const kbIdNum = Number(kbIdInput.trim());
  const filters = {
    kb_id: kbIdInput.trim() !== '' && Number.isFinite(kbIdNum) ? kbIdNum : null,
    reason,
    from: localToRfc3339Utc(fromLocal),
    to: localToRfc3339Utc(toLocal),
    page,
  };

  const feedbacks = useFeedbacks(filters);

  const hasFilters =
    filters.kb_id !== null || filters.reason !== null || filters.from !== null || filters.to !== null;

  return (
    <div className="mx-auto w-full max-w-5xl">
      <div className="mb-4">
        <h1 className="text-lg font-semibold">反馈列表</h1>
        <p className="text-sm text-[var(--muted)]">
          用户点踩反馈汇总（FR-14）：原因分布与被点踩回答摘要，帮助定位检索与模型问题。
        </p>
      </div>

      {/* 筛选表单 */}
      <div className="kb-card mb-4">
        <div className="grid grid-cols-2 items-end gap-3 md:grid-cols-5">
          <div className="space-y-1.5">
            <Label htmlFor="f-kb">知识库 ID</Label>
            <Input
              id="f-kb"
              value={kbIdInput}
              onChange={(e) => {
                setKbIdInput(e.target.value);
                setPage(1);
              }}
              placeholder="kb_id"
              inputMode="numeric"
            />
          </div>
          <div className="space-y-1.5">
            <Label>原因</Label>
            <Select
              value={reason === null ? 'all' : String(reason)}
              onValueChange={(v) => {
                setReason(v === 'all' ? null : Number(v));
                setPage(1);
              }}
            >
              <SelectTrigger className="w-full" aria-label="原因筛选">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部原因</SelectItem>
                {FEEDBACK_REASONS.map((r) => (
                  <SelectItem key={r} value={String(r)}>
                    {r} {FEEDBACK_REASON_LABELS[r as FeedbackReason]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="f-from">起始时间</Label>
            <Input
              id="f-from"
              type="datetime-local"
              value={fromLocal}
              onChange={(e) => {
                setFromLocal(e.target.value);
                setPage(1);
              }}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="f-to">截止时间（排他）</Label>
            <Input
              id="f-to"
              type="datetime-local"
              value={toLocal}
              onChange={(e) => {
                setToLocal(e.target.value);
                setPage(1);
              }}
            />
          </div>
          <div className="flex justify-end">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setKbIdInput('');
                setReason(null);
                setFromLocal('');
                setToLocal('');
                setPage(1);
              }}
            >
              清空筛选
            </Button>
          </div>
        </div>
      </div>

      <QueryBoundary
        query={feedbacks}
        skeleton={
          <div className="space-y-2">
            {Array.from({ length: 5 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        }
        empty={
          <EmptyState
            icon={ClipboardList}
            title={hasFilters ? '没有匹配的反馈记录' : '暂无点踩反馈'}
            description={hasFilters ? '调整筛选条件或时间窗后重试。' : '用户点踩回答后将在此汇总。'}
          />
        }
      >
        {(data) => (
          <>
            <div className="kb-card p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-36">反馈时间</TableHead>
                    <TableHead className="w-24">知识库</TableHead>
                    <TableHead className="w-28">反馈人</TableHead>
                    <TableHead className="w-28">原因</TableHead>
                    <TableHead>被点踩回答摘要</TableHead>
                    <TableHead className="w-48">补充说明</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.items.map((row) => (
                    <TableRow key={row.id} data-testid="feedback-row">
                      <TableCell className="text-xs tabular-nums text-[var(--muted)]">
                        {formatDateTime(row.created_at) || '—'}
                      </TableCell>
                      <TableCell className="tabular-nums">#{row.kb_id ?? '—'}</TableCell>
                      <TableCell className="text-sm">{row.username ?? '—'}</TableCell>
                      <TableCell>
                        <span className="inline-flex items-center rounded-full bg-[var(--destructive-soft)] px-2 py-0.5 text-xs text-[var(--destructive)]">
                          {FEEDBACK_REASON_LABELS[(row.reason ?? 0) as FeedbackReason] ?? row.reason ?? '—'}
                        </span>
                      </TableCell>
                      <TableCell className="max-w-80">
                        {row.message_text ? (
                          <span className="block truncate text-xs text-[var(--muted)]" title={row.message_text}>
                            {row.message_text}
                          </span>
                        ) : (
                          <span className="text-xs text-[var(--faint)]">—</span>
                        )}
                      </TableCell>
                      <TableCell className="max-w-48">
                        {row.comment ? (
                          <span className="block truncate text-xs text-[var(--muted)]" title={row.comment}>
                            {row.comment}
                          </span>
                        ) : (
                          <span className="text-xs text-[var(--faint)]">—</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
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
    </div>
  );
}
