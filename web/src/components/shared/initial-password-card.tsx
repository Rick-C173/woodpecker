/**
 * InitialPasswordCard：一次性初始密码展示卡（创建用户 / 重置密码共用）。
 * 仅本次响应可见，关闭后无法再次查看；提供复制按钮。
 */

import { useEffect, useState } from 'react';
import { Check, Copy, KeyRound } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';

export interface InitialPasswordCardProps {
  username: string;
  password: string;
  /** 卡片说明（创建/重置场景文案差异） */
  hint?: string;
}

export function InitialPasswordCard({ username, password, hint }: InitialPasswordCardProps) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(t);
  }, [copied]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(password);
      setCopied(true);
      toast.success('初始密码已复制到剪贴板');
    } catch {
      toast.error('复制失败，请手动选择复制');
    }
  };

  return (
    <div
      className="rounded-[var(--radius)] border border-[var(--border-strong)] bg-[var(--primary-soft)] p-4"
      data-testid="initial-password-card"
    >
      <p className="mb-1 text-sm font-semibold">
        <KeyRound className="mr-1.5 inline size-4 align-[-2px]" aria-hidden="true" />
        用户「{username}」的初始密码
      </p>
      <p className="mb-3 text-xs text-[var(--muted)]">
        {hint ?? '密码仅本次展示，关闭后无法再次查看，请立即复制并交付用户。'}
      </p>
      <div className="flex items-center gap-2">
        <code
          className="min-w-0 flex-1 select-all break-all rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-3 py-2 font-mono text-sm tracking-wide"
          data-testid="initial-password"
        >
          {password}
        </code>
        <Button variant="outline" size="sm" className="flex-none" onClick={() => void copy()}>
          {copied ? (
            <Check className="text-[var(--accent)]" aria-hidden="true" />
          ) : (
            <Copy aria-hidden="true" />
          )}
          {copied ? '已复制' : '复制'}
        </Button>
      </div>
    </div>
  );
}
