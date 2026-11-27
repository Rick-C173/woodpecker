// @vitest-environment jsdom
/** Button（shadcn 本地化）渲染契约：变体 class、尺寸、默认 type、禁用、点击。 */

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { Button } from './button';

describe('Button', () => {
  it('default 变体：强调色平涂 + 默认 type=button', () => {
    render(<Button>保存</Button>);
    const btn = screen.getByRole('button', { name: '保存' });
    expect(btn).toHaveClass('bg-[var(--accent)]');
    expect(btn).toHaveAttribute('type', 'button');
    expect(btn).toBeEnabled();
  });

  it('outline 变体：描边 + hover 背景类', () => {
    render(<Button variant="outline">取消</Button>);
    expect(screen.getByRole('button', { name: '取消' })).toHaveClass('border-[var(--border)]');
  });

  it('destructive 变体：弱底 + 危险色文字', () => {
    render(<Button variant="destructive">删除</Button>);
    expect(screen.getByRole('button', { name: '删除' })).toHaveClass('text-[var(--destructive)]');
  });

  it('size=sm：h-8 小尺寸', () => {
    render(<Button size="sm">小</Button>);
    expect(screen.getByRole('button', { name: '小' })).toHaveClass('h-8');
  });

  it('disabled：不可点击且不触发 onClick', async () => {
    const onClick = vi.fn();
    render(
      <Button disabled onClick={onClick}>
        禁用
      </Button>,
    );
    await userEvent.click(screen.getByRole('button', { name: '禁用' }));
    expect(onClick).not.toHaveBeenCalled();
  });

  it('点击触发 onClick；显式 type=submit 可覆盖默认', async () => {
    const onClick = vi.fn();
    render(
      <Button type="submit" onClick={onClick}>
        提交
      </Button>,
    );
    const btn = screen.getByRole('button', { name: '提交' });
    expect(btn).toHaveAttribute('type', 'submit');
    await userEvent.click(btn);
    expect(onClick).toHaveBeenCalledOnce();
  });
});
