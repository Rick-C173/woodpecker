/**
 * ChatInput：提问输入（受控 textarea，非 RHF 表单——架构 §1.4）。
 * Enter 发送、Shift+Enter 换行；流式期间禁用输入、发送按钮变「停止」（正方形图标）。
 */

import { useState, type KeyboardEvent } from 'react';
import { ArrowUp, Square } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';

export interface ChatInputProps {
  /** 流式进行中：输入禁用、显示停止按钮 */
  streaming: boolean;
  disabled?: boolean;
  onSend: (content: string) => void;
  onStop: () => void;
}

const MAX_CONTENT_LENGTH = 4000; // 后端契约：提问内容 ≤4000 字符

export function ChatInput({ streaming, disabled = false, onSend, onStop }: ChatInputProps) {
  const [value, setValue] = useState('');

  const canSend = !streaming && !disabled && value.trim().length > 0;

  const send = () => {
    const content = value.trim();
    if (!canSend || content.length === 0) return;
    onSend(content);
    setValue('');
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      send();
    }
  };

  return (
    <div className="border-t border-[var(--border)] bg-[var(--card)] px-4 py-3">
      <div className="mx-auto flex w-full max-w-3xl items-end gap-2">
        <Textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={handleKeyDown}
          disabled={streaming || disabled}
          rows={2}
          maxLength={MAX_CONTENT_LENGTH}
          placeholder={
            streaming ? '回答生成中…' : '向知识库提问（Enter 发送，Shift+Enter 换行）'
          }
          className="max-h-40 min-h-[44px] flex-1 resize-none"
          aria-label="提问输入"
          data-testid="chat-input"
        />
        {streaming ? (
          <Button
            variant="outline"
            size="icon"
            onClick={onStop}
            title="停止生成（已生成部分将保留）"
            aria-label="停止生成"
            data-testid="chat-stop"
            className="size-11 flex-none"
          >
            <Square aria-hidden="true" />
          </Button>
        ) : (
          <Button
            size="icon"
            onClick={send}
            disabled={!canSend}
            title="发送"
            aria-label="发送"
            data-testid="chat-send"
            className="size-11 flex-none"
          >
            <ArrowUp aria-hidden="true" />
          </Button>
        )}
      </div>
      <p className="mx-auto mt-1 max-w-3xl text-right text-[11px] text-[var(--faint)]">
        {value.length > 0 ? `${value.length}/${MAX_CONTENT_LENGTH}` : ''}
      </p>
    </div>
  );
}
