/**
 * 错误态组件（三态规范 §4.5）：图标 + ERROR_MESSAGES 文案 + 重试按钮 + request_id 小字。
 */

import { AlertTriangle, RotateCcw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ApiError, formatRequestId } from '@/lib/errors';

interface ErrorStateProps {
  error: unknown;
  /** 重试回调（QueryBoundary 传 query.refetch） */
  onRetry?: () => void;
  className?: string;
}

/** 归一化任意错误为可展示的 {message, request_id}。 */
export function describeError(error: unknown): { message: string; requestId: string | null } {
  if (error instanceof ApiError) {
    return {
      message: error.message,
      requestId: error.request_id,
    };
  }
  return { message: '加载失败，请检查网络后重试', requestId: null };
}

export function ErrorState({ error, onRetry, className }: ErrorStateProps) {
  const { message, requestId } = describeError(error);
  return (
    <div
      className={
        'flex flex-col items-center justify-center gap-3 rounded-[var(--radius)] border border-[var(--destructive-soft)] bg-[var(--destructive-soft)] px-6 py-12 text-center' +
        (className ? ` ${className}` : '')
      }
    >
      <AlertTriangle className="size-7 text-[var(--destructive)]" aria-hidden="true" />
      <p className="text-sm font-medium text-[var(--foreground)]">{message}</p>
      {formatRequestId(requestId) ? (
        <p className="font-mono text-xs text-[var(--faint)]">{formatRequestId(requestId)}</p>
      ) : null}
      {onRetry ? (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RotateCcw aria-hidden="true" />
          重试
        </Button>
      ) : null}
    </div>
  );
}
