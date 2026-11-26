/**
 * CommandPalette（Ctrl+K，Linear/Vercel 式全局跳转）：
 * - CommandPaletteHost：全局快捷键监听 + 面板渲染（挂在根 layout，覆盖全部主应用路由）；
 * - 命令集：全局导航 / 当前库上下文（检索/问答/文档）/ 知识库跳转 / 管理后台（管理员）/
 *   明暗主题切换；
 * - 键盘：Ctrl/Cmd+K 开关、↑↓ 选择、Enter 执行、Esc 关闭；输入即滤（标题包含）。
 */
import { useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  ClipboardList,
  Cog,
  FolderKanban,
  LayoutGrid,
  MailPlus,
  MessageCircle,
  MessagesSquare,
  Moon,
  Search,
  Sun,
  SunMoon,
  Users,
  Wrench,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useUiStore } from '@/stores/ui-store';
import { useAuth } from '@/hooks/use-auth';
import { useKbsList } from '@/hooks/use-kbs';
import { useTheme } from '@/components/theme/theme-provider';

type Group = '导航' | '当前库' | '知识库' | '管理' | '动作';

interface CommandItem {
  id: string;
  label: string;
  group: Group;
  icon: LucideIcon;
  hint?: string;
  run: () => void;
}

/** Ctrl/Cmd 修饰键平台判定。 */
function isMod(e: KeyboardEvent): boolean {
  return e.ctrlKey || e.metaKey;
}

export function CommandPaletteHost() {
  const { commandOpen, setCommandOpen, toggleCommand } = useUiStore();
  const { theme, resolvedTheme, setTheme } = useTheme();

  // 全局快捷键：Ctrl/Cmd+K 开关（不管面板是否聚焦）；Esc 由面板内处理
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isMod(e) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        toggleCommand();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [toggleCommand]);

  if (!commandOpen) return null;
  return (
    <CommandPalette
      onClose={() => setCommandOpen(false)}
      theme={{
        label: theme === 'system' ? '跟随系统' : resolvedTheme === 'dark' ? '切到浅色' : '切到深色',
        run: () => setTheme(theme === 'dark' ? 'light' : 'dark'),
        icon: theme === 'system' ? SunMoon : resolvedTheme === 'dark' ? Sun : Moon,
      }}
    />
  );
}

function CommandPalette({
  onClose,
  theme,
}: {
  onClose: () => void;
  theme: { label: string; run: () => void; icon: LucideIcon };
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const { user } = useAuth();
  const kbs = useKbsList(true);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  // 当前库上下文：/kbs/:kbId(/search|/chat|/documents/:docId)
  const kbMatch = location.pathname.match(/^\/kbs\/(\d+)/);
  const currentKbId = kbMatch ? Number(kbMatch[1]) : null;

  const items = useMemo<CommandItem[]>(() => {
    const list: CommandItem[] = [];
    const nav = (label: string, to: string, icon: LucideIcon, hint?: string) =>
      list.push({ id: 'nav:' + to + label, label, group: '导航', icon, hint, run: () => navigate(to) });

    nav('全部知识库', '/kbs', LayoutGrid);

    if (currentKbId !== null) {
      const kbBase = `/kbs/${currentKbId}`;
      nav('当前库详情', kbBase, FolderKanban);
      nav('本库内容检索', `${kbBase}/search`, Search);
      nav('本库问答', `${kbBase}/chat`, MessageCircle);
    }

    for (const kb of (kbs.data ?? []).slice(0, 8)) {
      list.push({
        id: 'kb:' + kb.id,
        label: kb.name ?? '未命名库',
        group: '知识库',
        icon: FolderKanban,
        hint: `${kb.document_count ?? 0} 个文档`,
        run: () => navigate(`/kbs/${kb.id}`),
      });
    }

    if (user?.is_admin) {
      list.push({ id: 'adm:users', label: '用户管理', group: '管理', icon: Users, run: () => navigate('/admin/users') });
      list.push({ id: 'adm:inv', label: '邀请管理', group: '管理', icon: MailPlus, run: () => navigate('/admin/invitations') });
      list.push({ id: 'adm:email', label: '邮件配置', group: '管理', icon: MailPlus, run: () => navigate('/admin/email') });
      list.push({ id: 'adm:models', label: '模型服务', group: '管理', icon: Wrench, run: () => navigate('/admin/models') });
      list.push({ id: 'adm:audit', label: '审计日志', group: '管理', icon: ClipboardList, run: () => navigate('/admin/audit') });
      list.push({ id: 'adm:settings', label: '系统设置', group: '管理', icon: Cog, run: () => navigate('/admin/settings') });
      list.push({ id: 'adm:fb', label: '反馈列表', group: '管理', icon: MessagesSquare, run: () => navigate('/admin/feedbacks') });
    }

    list.push({ id: 'act:theme', label: '切换明暗主题', group: '动作', icon: theme.icon, hint: theme.label, run: theme.run });
    return list;
  }, [navigate, location.pathname, currentKbId, kbs.data, user?.is_admin, theme]);

  const kw = query.trim().toLowerCase();
  const filtered = kw ? items.filter((it) => it.label.toLowerCase().includes(kw)) : items;

  // 过滤结果变化时回到第一项
  useEffect(() => {
    setActive(0);
  }, [query]);

  // active 项滚动进可视区
  useEffect(() => {
    const el = listRef.current?.querySelector('[data-active="true"]');
    el?.scrollIntoView({ block: 'nearest' });
  }, [active, filtered]);

  const execute = (item: CommandItem) => {
    onClose();
    item.run();
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (filtered.length === 0 ? 0 : (i + 1) % filtered.length));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (filtered.length === 0 ? 0 : (i - 1 + filtered.length) % filtered.length));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const item = filtered[active];
      if (item) execute(item);
    }
  };

  // 分组渲染顺序
  let lastGroup: Group | null = null;
  return (
    <div
      className="fixed inset-0 z-[100] bg-black/50"
      onClick={onClose}
      data-testid="command-palette-overlay"
    >
      {/* 面板：点击冒泡到 overlay 关闭，阻止面板内点击冒泡 */}
      <div
        className="mx-auto mt-[15vh] w-full max-w-xl overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] shadow-[var(--shadow-hover)]"
        onClick={(e) => e.stopPropagation()}
        data-testid="command-palette"
      >
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="搜索页面、知识库或操作…"
          aria-label="命令面板"
          className="w-full border-b border-[var(--border)] bg-transparent px-4 py-3 text-sm outline-none placeholder:text-[var(--faint)]"
        />
        <div className="max-h-80 overflow-y-auto py-1.5" ref={listRef}>
          {filtered.length === 0 ? (
            <p className="px-4 py-6 text-center text-sm text-[var(--faint)]">没有匹配的命令</p>
          ) : (
            filtered.map((item, i) => {
              const showGroup = item.group !== lastGroup;
              lastGroup = item.group;
              const Icon = item.icon;
              return (
                <div key={item.id}>
                  {showGroup ? (
                    <p className="px-4 pb-1 pt-2 text-[11px] font-semibold tracking-wide text-[var(--faint)]">
                      {item.group}
                    </p>
                  ) : null}
                  <button
                    type="button"
                    data-active={i === active}
                    onMouseEnter={() => setActive(i)}
                    onClick={() => execute(item)}
                    className={
                      'flex w-full cursor-pointer items-center gap-2.5 px-4 py-2 text-left text-sm transition-colors ' +
                      (i === active
                        ? 'bg-[var(--primary-soft)] text-[var(--primary)]'
                        : 'text-[var(--foreground)]')
                    }
                  >
                    <Icon className="size-4 flex-none" aria-hidden="true" />
                    <span className="min-w-0 flex-1 truncate">{item.label}</span>
                    {item.hint ? (
                      <span className="flex-none text-[11px] text-[var(--faint)]">{item.hint}</span>
                    ) : null}
                    {i === active ? (
                      <kbd className="flex-none rounded border border-[var(--border)] px-1 text-[10px] text-[var(--faint)]">
                        ↵
                      </kbd>
                    ) : null}
                  </button>
                </div>
              );
            })
          )}
        </div>
        <div className="border-t border-[var(--border)] px-4 py-1.5 text-[11px] text-[var(--faint)]">
          ↑↓ 选择 · ↵ 执行 · Esc 关闭
        </div>
      </div>
    </div>
  );
}
