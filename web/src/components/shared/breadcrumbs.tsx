/**
 * Breadcrumbs：层级导航（GitHub 式）。末段为当前位置（不可点），其余为链接。
 * 用于多级页面：知识库列表 / 库详情 / 检索 / 文档详情 / 会话。
 */
import { Fragment } from 'react';
import { Link } from 'react-router-dom';
import { ChevronRight } from 'lucide-react';

export interface Crumb {
  label: string;
  /** 缺省 = 当前位置（纯文本） */
  to?: string;
}

export function Breadcrumbs({ items, className }: { items: Crumb[]; className?: string }) {
  return (
    <nav
      aria-label="面包屑"
      className={
        (className ?? '') + ' mb-3 flex min-w-0 items-center gap-1 text-[13px] text-[var(--muted)]'
      }
    >
      {items.map((c, i) => (
        <Fragment key={i}>
          {i > 0 ? (
            <ChevronRight className="size-3.5 flex-none text-[var(--faint)]" aria-hidden="true" />
          ) : null}
          {c.to && i < items.length - 1 ? (
            <Link
              to={c.to}
              className="truncate rounded px-0.5 transition-colors hover:text-[var(--primary)]"
              title={c.label}
            >
              {c.label}
            </Link>
          ) : (
            <span className="truncate font-medium text-[var(--foreground)]" title={c.label}>
              {c.label}
            </span>
          )}
        </Fragment>
      ))}
    </nav>
  );
}
