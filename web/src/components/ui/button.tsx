import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from '@/lib/utils';

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-[var(--radius)] text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: 'text-white shadow-[var(--btn-shadow)] btn-primary',
        ghost: 'btn-ghost',
        destructive:
          'bg-[var(--destructive)] text-white hover:bg-[var(--destructive)]/90',
        outline: 'border border-[var(--border)] bg-transparent hover:bg-[var(--primary-soft)]',
        link: 'text-[var(--primary)] underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-[var(--row)] px-4',
        sm: 'h-8 px-3 text-xs',
        lg: 'h-11 px-8',
        icon: 'h-[var(--row)] w-[var(--row)]',
      },
    },
    compoundVariants: [
      {
        variant: 'ghost',
        size: 'icon',
        class: 'btn-icon border-transparent hover:border-transparent',
      },
    ],
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
);

interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

/** shadcn/ui Button（对齐定稿视觉：default = 渐变主按钮 + 扫光）。 */
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
