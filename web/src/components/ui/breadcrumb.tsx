/**
 * Breadcrumb（shadcn/ui 源码本地化，纯 React 无 radix 依赖）：
 * 规则（规划 §2.2）：深度 ≥2 且有跳转意义才展示；末级用 BreadcrumbPage（不可点）。
 */

import * as React from 'react';
import { ChevronRightIcon, MoreHorizontalIcon } from 'lucide-react';
import { cn } from '@/lib/utils';

function Breadcrumb({ className, ...props }: React.ComponentProps<'nav'>) {
  return (
    <nav
      data-slot="breadcrumb"
      aria-label="面包屑"
      className={cn('min-w-0 overflow-hidden', className)}
      {...props}
    />
  );
}

function BreadcrumbList({ className, ...props }: React.ComponentProps<'ol'>) {
  return (
    <ol
      data-slot="breadcrumb-list"
      className={cn(
        'flex flex-wrap items-center gap-1.5 text-sm break-words text-[var(--muted)]',
        className,
      )}
      {...props}
    />
  );
}

function BreadcrumbItem({ className, ...props }: React.ComponentProps<'li'>) {
  return (
    <li
      data-slot="breadcrumb-item"
      className={cn('inline-flex items-center gap-1.5', className)}
      {...props}
    />
  );
}

function BreadcrumbLink({ className, ...props }: React.ComponentProps<'a'>) {
  return (
    <a
      data-slot="breadcrumb-link"
      className={cn(
        'transition-colors hover:text-[var(--foreground)] hover:underline',
        className,
      )}
      {...props}
    />
  );
}

function BreadcrumbPage({ className, ...props }: React.ComponentProps<'span'>) {
  return (
    <span
      data-slot="breadcrumb-page"
      aria-current="page"
      className={cn('font-medium text-[var(--foreground)]', className)}
      {...props}
    />
  );
}

function BreadcrumbSeparator({
  className,
  children,
  ...props
}: React.ComponentProps<'li'>) {
  return (
    <li
      data-slot="breadcrumb-separator"
      aria-hidden="true"
      className={cn('[&>svg]:size-3.5 text-[var(--faint)]', className)}
      {...props}
    >
      {children ?? <ChevronRightIcon />}
    </li>
  );
}

function BreadcrumbEllipsis({ className, ...props }: React.ComponentProps<'span'>) {
  return (
    <span
      data-slot="breadcrumb-ellipsis"
      aria-hidden="true"
      className={cn('flex size-5 items-center justify-center [&>svg]:size-4', className)}
      {...props}
    >
      <MoreHorizontalIcon />
    </span>
  );
}

export {
  Breadcrumb,
  BreadcrumbList,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbPage,
  BreadcrumbSeparator,
  BreadcrumbEllipsis,
};
