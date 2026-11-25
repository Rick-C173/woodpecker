/**
 * Toaster（sonner）—— 对齐本项目的主题系统（架构 §5.4）：
 * 不用 next-themes，改读自定义 ThemeProvider 的解析结果（light/dark）。
 * toast 用于操作失败提示（错误码文案见 lib/errors.ts）。
 */

import type * as React from 'react';
import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from 'lucide-react';
import { Toaster as Sonner, type ToasterProps } from 'sonner';
import { useTheme } from '@/components/theme/theme-provider';

const Toaster = ({ ...props }: ToasterProps) => {
  const { resolvedTheme } = useTheme();

  return (
    <Sonner
      theme={(resolvedTheme ?? 'light') as ToasterProps['theme']}
      className="toaster group"
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          '--normal-bg': 'var(--popover, var(--card))',
          '--normal-text': 'var(--popover-foreground, var(--foreground))',
          '--normal-border': 'var(--border)',
          '--border-radius': 'var(--radius)',
        } as React.CSSProperties
      }
      {...props}
    />
  );
};

export { Toaster };
