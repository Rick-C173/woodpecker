/**
 * QueryBoundary（三态规范 §4.5）：loading / error / empty / 成功 统一包装。
 * - 加载：Skeleton 按目标布局占形（skeleton 传入），禁止裸 spinner 全屏。
 * - 错误：ErrorState（重试按钮 + request_id）。
 * - 空：emptyWhen(data) 判定或默认「数组 items 为空」兜底；渲染 empty 节点或 emptyProps。
 * - 成功：children 为 ReactNode 直接渲染；为函数时以 (data) => ReactNode 委托渲染。
 * 规范约束：任何列表/详情页面必须经 QueryBoundary 渲染。
 */

import type { ReactNode } from 'react';
import type { UseQueryResult } from '@tanstack/react-query';
import { Skeleton } from '@/components/ui/skeleton';
import { ErrorState } from './error-state';
import { EmptyState, type EmptyStateProps } from './empty-state';

interface QueryBoundaryProps<TData, TError> {
  query: UseQueryResult<TData, TError>;
  /** 成功态渲染：ReactNode 或 (data) => ReactNode */
  children: ReactNode | ((data: TData) => ReactNode);
  /** 加载骨架（按目标布局占形），默认整块灰底 */
  skeleton?: ReactNode;
  /** 空态节点（优先）；未传时用 emptyProps 渲染默认 EmptyState */
  empty?: ReactNode;
  /** 数据非空判定；未传时按「数组 / items 为空 / 数据为 null|undefined」兜底 */
  emptyWhen?: (data: TData) => boolean;
  /** 默认空态配置 */
  emptyProps?: Omit<EmptyStateProps, 'className'>;
}

function isEmptyByDefault<TData>(data: TData): boolean {
  if (data === null || data === undefined) return true;
  if (Array.isArray(data)) return data.length === 0;
  if (typeof data === 'object' && 'items' in data) {
    const items = (data as { items?: unknown[] }).items;
    return Array.isArray(items) ? items.length === 0 : false;
  }
  return false;
}

export function QueryBoundary<TData, TError = Error>({
  query,
  children,
  skeleton,
  empty,
  emptyWhen,
  emptyProps,
}: QueryBoundaryProps<TData, TError>) {
  if (query.isPending) {
    return <>{skeleton ?? <Skeleton className="h-40 w-full" />}</>;
  }

  if (query.isError) {
    return <ErrorState error={query.error} onRetry={() => query.refetch()} />;
  }

  const data = query.data;
  if (data === null || data === undefined) {
    return empty ?? (emptyProps ? <EmptyState {...emptyProps} /> : <EmptyState title="暂无数据" />);
  }

  if (emptyWhen ? emptyWhen(data) : isEmptyByDefault(data)) {
    return empty ?? (emptyProps ? <EmptyState {...emptyProps} /> : <EmptyState title="暂无数据" />);
  }

  return <>{typeof children === 'function' ? children(data) : children}</>;
}
