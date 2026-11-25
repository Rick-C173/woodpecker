/**
 * ConfirmDialog：危险操作二次确认。
 * 支持两种强度：
 * - 普通确认（删除文档/移除成员）
 * - 输入名称确认（删库：必须输入库原名才可提交）
 */

import { useEffect, useState, type ReactNode } from 'react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
  /** 传入后进入「输入名称确认」模式：必须输入与 confirmName 一致的值才可提交 */
  confirmName?: string;
  /** 名称确认模式的输入提示 */
  confirmNameHint?: string;
  onConfirm: () => void;
  /** 提交中（禁用按钮） */
  busy?: boolean;
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = '确认',
  cancelLabel = '取消',
  destructive = true,
  confirmName,
  confirmNameHint = '请输入名称以确认操作',
  onConfirm,
  busy = false,
}: ConfirmDialogProps) {
  const [nameInput, setNameInput] = useState('');
  const nameConfirmed = confirmName === undefined || nameInput === confirmName;

  // 打开时重置输入
  useEffect(() => {
    if (open) setNameInput('');
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>

        {confirmName !== undefined ? (
          <div className="space-y-1.5">
            <p className="text-xs text-[var(--muted)]">{confirmNameHint}</p>
            <Input
              value={nameInput}
              onChange={(e) => setNameInput(e.target.value)}
              placeholder={confirmName}
              aria-label={confirmNameHint}
            />
          </div>
        ) : null}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            {cancelLabel}
          </Button>
          <Button
            variant={destructive ? 'destructive' : 'default'}
            onClick={() => {
              onConfirm();
            }}
            disabled={busy || !nameConfirmed}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
