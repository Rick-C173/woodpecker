import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from '@/lib/utils';

/**
 * shadcn/ui Button（v2 冷静工具风：纯 token 驱动，不再耦合 app.css 装饰类）。
 * 高度体系：default 36px（与 Select/Input 的 h-9 对齐）、sm 32、lg 40；
 * 表格行高另由 --row-h 控制（cozy 44 / compact 32），与按钮解耦。
 */
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-[var(--radius)] text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-[var(--accent-weak)] disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: 'bg-[var(--accent)] text-[var(--on-accent)] hover:bg-[var(--accent-hover)]',
        ghost: 'text-[var(--muted)] hover:bg-[var(--hover-bg)] hover:text-[var(--foreground)]',
        outline:
          'border border-[var(--border)] bg-[var(--card)] text-[var(--foreground)] hover:border-[var(--border-strong)] hover:bg-[var(--hover-bg)]',
        destructive:
          'bg-[var(--destructive-soft)] text-[var(--destructive)] hover:bg-[var(--destructive)] hover:text-white',
        link: 'text-[var(--primary)] underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-9 px-4',
        sm: 'h-8 px-3 text-xs',
        lg: 'h-10 px-6',
        icon: 'size-9',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
);

interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

/** shadcn/ui Button（对齐设计系统 v2：default = 强调色平涂，hover 加深 7%）。 */
const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, type = 'button', ...props }, ref) => (
    <button
      ref={ref}
      type={type}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  ),
);
Button.displayName = 'Button';

export { Button, buttonVariants, type ButtonProps };
