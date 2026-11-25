/**
 * TablePagination：列表分页控件（上一页/下一页 + 总数与页码），管理端列表共用。
 */

import { ChevronLeft, ChevronRight } from 'lucide-react';
import { Button } from '@/components/ui/button';

export interface TablePaginationProps {
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
}

export function TablePagination({ page, pageSize, total, onPageChange }: TablePaginationProps) {
  const totalPages = Math.max(1, Math.ceil(total / (pageSize || 20)));
  if (total === 0) return null;

  return (
    <div className="flex items-center justify-end gap-2 text-sm text-[var(--muted)]">
      <span className="text-xs">共 {total} 条</span>
      <Button
        variant="outline"
        size="sm"
        disabled={page <= 1}
        onClick={() => onPageChange(Math.max(1, page - 1))}
      >
        <ChevronLeft aria-hidden="true" />
        上一页
      </Button>
      <span className="tabular-nums">
        第 {page} / {totalPages} 页
      </span>
      <Button
        variant="outline"
        size="sm"
        disabled={page >= totalPages}
        onClick={() => onPageChange(page + 1)}
      >
        下一页
        <ChevronRight aria-hidden="true" />
      </Button>
    </div>
  );
}
