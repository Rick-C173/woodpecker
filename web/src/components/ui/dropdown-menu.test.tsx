// @vitest-environment jsdom
/** DropdownMenu（radix 本地化）契约：开合、item 点击回调、destructive 变体类。 */

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu';

function Demo({ onSelect, variant }: { onSelect: () => void; variant?: 'default' | 'destructive' }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button">打开菜单</button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem variant={variant} onSelect={onSelect}>
          退出登录
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

describe('DropdownMenu', () => {
  it('默认关闭，点击 trigger 后内容出现在 body（portal）', async () => {
    render(<Demo onSelect={() => {}} />);
    expect(screen.queryByText('退出登录')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '打开菜单' }));
    const item = await screen.findByText('退出登录');
    expect(item).toBeInTheDocument();
  });

  it('点击 item 触发 onSelect', async () => {
    const onSelect = vi.fn();
    render(<Demo onSelect={onSelect} />);
    await userEvent.click(screen.getByRole('button', { name: '打开菜单' }));
    await userEvent.click(await screen.findByText('退出登录'));
    expect(onSelect).toHaveBeenCalled();
  });

  it('destructive 变体：危险色类名', async () => {
    render(<Demo onSelect={() => {}} variant="destructive" />);
    await userEvent.click(screen.getByRole('button', { name: '打开菜单' }));
    const item = await screen.findByText('退出登录');
    expect(item).toHaveAttribute('data-variant', 'destructive');
  });
});
