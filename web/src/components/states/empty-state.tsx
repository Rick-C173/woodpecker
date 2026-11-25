/**
 * 空态组件（三态规范 §4.5）：图标 + 一句话说明 + 可选主 CTA。
 * 每页文案须明确引导下一步动作；部分数据的空子态（如搜索无命中）不用整页 EmptyState。
 */

import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export interface EmptyStateProps {
  icon?: LucideIcon;
  title: string;
  description?: string;
  /** 主 CTA（如「接入 Git 仓库」），按权限控制是否传入 */
  action?: { label: string; onClick: () => void } | ReactNode;
  className?: string;
}

export function EmptyState({ icon: Icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-2 rounded-[var(--radius)] border border-dashed border-[var(--border)] bg-[var(--card)] px-6 py-14 text-center',
        className,
      )}
    >
      {Icon ? (
        <Icon className="mb-1 size-8 text-[var(--faint)]" aria-hidden="true" />
      ) : null}
      <p className="text-sm font-medium text-[var(--foreground)]">{title}</p>
      {description ? <p className="text-sm text-[var(--muted)]">{description}</p> : null}
      {action ? (
        <div className="mt-3">
          {action instanceof Object && 'onClick' in action && 'label' in action ? (
            <Button onClick={action.onClick}>{action.label}</Button>
          ) : (
            (action as ReactNode)
          )}
        </div>
      ) : null}
    </div>
  );
}
