// @vitest-environment jsdom
/** RoleBadge 渲染契约：合法角色 → 角色徽标类名与文案；非法/空角色 → 不渲染。 */

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { RoleBadge } from './role-badge';

describe('RoleBadge', () => {
  it('owner → badge role-owner + 管理者文案', () => {
    const { container } = render(<RoleBadge role="owner" />);
    const badge = container.querySelector('.badge');
    expect(badge).toHaveClass('role-owner');
    expect(badge).toHaveTextContent('管理者');
  });

  it('editor / member / org 各自渲染对应类名', () => {
    const cases: Array<[string, string]> = [
      ['editor', 'role-editor'],
      ['member', 'role-member'],
      ['org', 'role-org'],
    ];
    for (const [role, cls] of cases) {
      const { container, unmount } = render(<RoleBadge role={role} />);
      expect(container.querySelector('.badge')).toHaveClass(cls);
      unmount();
    }
  });

  it('非法角色与空值 → 不渲染任何元素（R6 不可见原则，管理员非成员不显示徽标）', () => {
    for (const role of ['superadmin', '', null, undefined]) {
      const { container } = render(<RoleBadge role={role} />);
      expect(container.querySelector('.badge')).toBeNull();
    }
  });
});
