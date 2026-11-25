import * as React from 'react';
import { cn } from '@/lib/utils';

/** shadcn/ui Input（对齐定稿视觉：focus 渐变 ring）。 */
const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<'input'>>(
  ({ className, type = 'text', ...props }, ref) => (
    <input
      ref={ref}
      type={type}
      className={cn(
        'flex h-[var(--row)] w-full rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-3 py-1 text-sm text-[var(--foreground)] shadow-none transition-colors placeholder:text-[var(--faint)] focus-visible:border-[var(--border-strong)] focus-visible:shadow-[var(--ring)] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    />
  ),
);
Input.displayName = 'Input';

export { Input };
