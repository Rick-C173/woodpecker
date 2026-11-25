import { cn } from '@/lib/utils';

/** shadcn/ui Skeleton（三态规范：加载态按目标布局占形，禁止裸 spinner 全屏）。 */
function Skeleton({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      className={cn('animate-pulse rounded-[var(--radius)] bg-[var(--primary-soft)]', className)}
      {...props}
    />
  );
}

export { Skeleton };
