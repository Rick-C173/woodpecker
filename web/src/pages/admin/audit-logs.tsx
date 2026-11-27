/**
 * 审计查询（FR-15，/admin/audit）：user_id / action / object_type / from / to 筛选 + 分页。
 * from/to 用 datetime-local 输入，提交前转 RFC3339 UTC（to 为排他边界，由后端语义保证）。
 */

import { useState } from 'react';
import { ClipboardList } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { PageHeader } from '@/components/shared/page-header';
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
import { TablePagination } from '@/components/shared/table-pagination';
import { useAuditLogs } from '@/hooks/use-admin';
import { formatDateTime } from '@/lib/time';

/** datetime-local 本地值 → RFC3339 UTC；空值返回 null。 */
function localToRfc3339Utc(value: string): string | null {
  if (!value) return null;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

export function AdminAuditLogsPage() {
  const [userIdInput, setUserIdInput] = useState('');
  const [action, setAction] = useState('');
  const [objectType, setObjectType] = useState('');
  const [fromLocal, setFromLocal] = useState('');
  const [toLocal, setToLocal] = useState('');
  const [page, setPage] = useState(1);

  const userIdNum = Number(userIdInput.trim());
  const filters = {
    user_id: userIdInput.trim() !== '' && Number.isFinite(userIdNum) ? userIdNum : null,
    action: action.trim(),
    object_type: objectType.trim(),
    from: localToRfc3339Utc(fromLocal),
    to: localToRfc3339Utc(toLocal),
    page,
  };

  const audit = useAuditLogs(filters);

  const resetFilters = () => {
    setUserIdInput('');
    setAction('');
    setObjectType('');
    setFromLocal('');
    setToLocal('');
    setPage(1);
  };

  const hasFilters =
    filters.user_id !== null ||
    filters.action !== '' ||
    filters.object_type !== '' ||
    filters.from !== null ||
    filters.to !== null;

  return (
    <div className="w-full">
      <PageHeader
        title="审计日志"
        description="所有写操作与越权尝试均记录（FR-15）；支持按操作者、动作、对象与时间窗筛选。"
      />

      {/* 筛选表单 */}
      <div className="mb-4 rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] p-4 shadow-[var(--shadow)]">
        <div className="grid grid-cols-2 items-end gap-3 md:grid-cols-5">
          <div className="space-y-1.5">
            <Label htmlFor="f-user">操作者 ID</Label>
            <Input
              id="f-user"
              value={userIdInput}
              onChange={(e) => {
                setUserIdInput(e.target.value);
                setPage(1);
              }}
              placeholder="user_id"
              inputMode="numeric"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="f-action">动作</Label>
            <Input
              id="f-action"
              value={action}
              onChange={(e) => {
                setAction(e.target.value);
                setPage(1);
              }}
              placeholder="如 model.update"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="f-object">对象类型</Label>
            <Input
              id="f-object"
              value={objectType}
              onChange={(e) => {
                setObjectType(e.target.value);
                setPage(1);
              }}
              placeholder="如 model_service"
            />
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
        </div>
        <div className="mt-3 flex justify-end">
          <Button variant="ghost" size="sm" onClick={resetFilters}>
            清空筛选
          </Button>
        </div>
      </div>

      <QueryBoundary
        query={audit}
        skeleton={
          <div className="space-y-2">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        }
        empty={
          <EmptyState
            icon={ClipboardList}
            title={hasFilters ? '没有匹配的审计记录' : '暂无审计记录'}
            description={hasFilters ? '调整筛选条件或时间窗后重试。' : '有写操作后将自动记录。'}
          />
        }
      >
        {(data) => (
          <>
            <div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] shadow-[var(--shadow)]">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-36">时间</TableHead>
                    <TableHead className="w-32">操作者</TableHead>
                    <TableHead className="w-44">动作</TableHead>
                    <TableHead className="w-44">对象</TableHead>
                    <TableHead className="w-32">来源 IP</TableHead>
                    <TableHead>附加信息</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.items.map((row) => (
                    <TableRow key={row.id} data-testid="audit-row">
                      <TableCell className="text-xs tabular-nums text-[var(--muted)]">
                        {formatDateTime(row.created_at) || '—'}
                      </TableCell>
                      <TableCell className="text-sm">
                        {row.username ?? (row.user_id !== null && row.user_id !== undefined ? `#${row.user_id}` : '系统')}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{row.action}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {row.object_type
                          ? `${row.object_type}${row.object_id !== null && row.object_id !== undefined ? `#${row.object_id}` : ''}`
                          : '—'}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-[var(--muted)]">
                        {row.ip || '—'}
                      </TableCell>
                      <TableCell className="max-w-64">
                        {row.detail && Object.keys(row.detail).length > 0 ? (
                          <code
                            className="block truncate font-mono text-xs text-[var(--muted)]"
                            title={JSON.stringify(row.detail, null, 2)}
                          >
                            {JSON.stringify(row.detail)}
                          </code>
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
