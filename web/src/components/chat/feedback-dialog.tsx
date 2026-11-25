/**
 * FeedbackDialog：点踩（PRD FR-14）——四原因单选（1 答案不相关 / 2 引用错误 /
 * 3 内容过时 / 4 其他），选「其他」时补充说明必填；重复点踩直接重开重选（覆盖提交）。
 */

import { useEffect, useState } from 'react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import {
  buildFeedbackPayload,
  FEEDBACK_REASONS,
  FEEDBACK_REASON_LABELS,
  type FeedbackReason,
} from '@/lib/chat-events';
import { cn } from '@/lib/utils';

export interface FeedbackDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 目标回答消息（含已有 feedback 时预选原因）；仅需 id 与 feedback 两个维度 */
  message: { id: number; feedback: { reason?: number; comment?: string } | null } | null;
  /** 提交点踩（reason + comment） */
  onSubmit: (messageId: number, reason: FeedbackReason, comment: string) => void;
  busy?: boolean;
}

export function FeedbackDialog({
  open,
  onOpenChange,
  message,
  onSubmit,
  busy = false,
}: FeedbackDialogProps) {
  const [reason, setReason] = useState<FeedbackReason>(1);
  const [comment, setComment] = useState('');
  const [error, setError] = useState<string | null>(null);

  // 打开时重置：已有反馈预选上次原因（覆盖重选），否则默认第一项
  useEffect(() => {
    if (open) {
      const prev = message?.feedback?.reason;
      setReason(
        prev === 1 || prev === 2 || prev === 3 || prev === 4 ? prev : 1,
      );
      setComment(message?.feedback?.comment ?? '');
      setError(null);
    }
  }, [open, message]);

  const handleSubmit = () => {
    const result = buildFeedbackPayload(reason, comment);
    if (!result.ok) {
      setError(result.error);
      return;
    }
    setError(null);
    if (message?.id) {
      onSubmit(message.id, result.payload.reason, result.payload.comment ?? '');
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" data-testid="feedback-dialog">
        <DialogHeader>
          <DialogTitle>点踩这条回答</DialogTitle>
          <DialogDescription>
            {message?.feedback
              ? '该回答已点踩过，重新提交将覆盖上一次反馈。'
              : '请告诉我们哪里不满意，反馈会帮助改进检索与回答质量。'}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2" role="radiogroup" aria-label="点踩原因">
          {FEEDBACK_REASONS.map((r) => (
            <label
              key={r}
              className={cn(
                'flex cursor-pointer items-center gap-2.5 rounded-[var(--radius)] border px-3 py-2 text-sm transition-colors',
                reason === r
                  ? 'border-[var(--border-strong)] bg-[var(--primary-soft)] text-[var(--primary)]'
                  : 'border-[var(--border)] hover:bg-[var(--primary-soft)]',
              )}
            >
              <input
                type="radio"
                name="feedback-reason"
                value={r}
                checked={reason === r}
                onChange={() => setReason(r)}
                className="accent-[var(--primary)]"
              />
              {FEEDBACK_REASON_LABELS[r]}
            </label>
          ))}
        </div>

        <div className="space-y-1.5">
          <label className="text-xs text-[var(--muted)]" htmlFor="feedback-comment">
            补充说明{reason === 4 ? '（必填）' : '（选填）'}
          </label>
          <Textarea
            id="feedback-comment"
            value={comment}
            onChange={(e) => {
              setComment(e.target.value);
              if (error) setError(null);
            }}
            placeholder={reason === 4 ? '请描述具体问题（必填）' : '补充问题细节，帮助定位（选填）'}
            rows={3}
            className="resize-none"
          />
          {error ? <p className="text-xs text-[var(--destructive)]">{error}</p> : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            取消
          </Button>
          <Button onClick={handleSubmit} disabled={busy} data-testid="feedback-submit">
            {message?.feedback ? '覆盖提交' : '提交反馈'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
