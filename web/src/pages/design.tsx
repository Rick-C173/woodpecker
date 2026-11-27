/**
 * /design —— 设计系统 v2 组件演示页（阶段 A 验收基准，规划 §5）。
 * 覆盖：明暗 × 密度 × 强调色（5 预设 + 自定义取色，localStorage 持久化）× 全部 ui/ 原子组件。
 * 非产品页面：路由公开，仅供开发走查；阶段 C 完成后保留为视觉回归基准。
 */

import { useEffect, useState } from 'react';
import { ArrowUpDown, Plus, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { PALETTES, PALETTE_NAMES, usePalette, type Palette } from '@/components/theme/palette-provider';
import { THEME_PREFERENCES, useTheme, type ThemePreference } from '@/components/theme/theme-provider';
import { applyCustomAccent, clearCustomAccent, readCustomAccent, restoreCustomAccent } from '@/components/theme/custom-accent';
import { applyDensity, densityLabel, DENSITIES, readStoredDensity, restoreDensity, type Density } from '@/components/theme/density';
import { cn } from '@/lib/utils';

/** 小节标题。 */
function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-4">
      <h2 className="text-xs font-semibold uppercase tracking-wider text-[var(--faint)]">{title}</h2>
      {children}
    </section>
  );
}

/** 色卡：填充 + 名称。 */
function Swatch({ name, css }: { name: string; css: React.CSSProperties }) {
  return (
    <div className="space-y-1.5">
      <div className="h-14 rounded-[var(--radius)] border border-[var(--border)]" style={css} />
      <p className="text-xs text-[var(--muted)]">{name}</p>
    </div>
  );
}

/** 状态徽标（新体系：token 驱动 pill，无旧 .badge 依赖）。 */
function Pill({ tone, children }: { tone: 'muted' | 'success' | 'warning' | 'danger'; children: React.ReactNode }) {
  const cls = {
    muted: 'bg-[var(--muted-bg)] text-[var(--muted)]',
    success: 'bg-[var(--success-weak)] text-[var(--success)]',
    warning: 'bg-[var(--accent-weak)] text-[var(--warning)]',
    danger: 'bg-[var(--destructive-soft)] text-[var(--destructive)]',
  }[tone];
  return <span className={cn('inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium', cls)}>{children}</span>;
}

const MODE_LABELS: Record<ThemePreference, string> = {
  system: '跟随系统',
  light: '浅色',
  dark: '深色',
};

export function DesignPage() {
  const { theme, setTheme } = useTheme();
  const { palette, setPalette } = usePalette();
  const [density, setDensity] = useState<Density>(readStoredDensity);
  const [custom, setCustom] = useState<string | null>(readCustomAccent);

  // 启动恢复：密度 + 自定义强调色（localStorage 持久化验收点）
  useEffect(() => {
    restoreDensity();
    restoreCustomAccent();
    setCustom(readCustomAccent());
  }, []);

  const pickPalette = (p: Palette) => {
    clearCustomAccent(); // 内联 --acc-h/s/l 优先级高于预设，必须先移除
    setCustom(null);
    setPalette(p);
  };

  const pickCustom = (value: string) => {
    if (applyCustomAccent(value)) {
      setCustom(value.toLowerCase());
    }
  };

  const pickDensity = (d: Density) => {
    applyDensity(d);
    setDensity(d);
  };

  return (
    <div className="min-h-screen bg-[var(--background)] text-[var(--foreground)]">
      {/* 工具条：明暗 × 配色 × 自定义取色 × 密度 */}
      <header className="sticky top-0 z-40 border-b border-[var(--border)] bg-[var(--card)]">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center gap-x-6 gap-y-3 px-6 py-3">
          <span className="text-sm font-bold">Woodpecker · 设计系统</span>
          <div className="flex items-center gap-1.5" role="group" aria-label="明暗模式">
            {THEME_PREFERENCES.map((p) => (
              <button
                key={p}
                type="button"
                onClick={() => setTheme(p)}
                aria-pressed={theme === p}
                className={cn(
                  'rounded-[var(--radius-sm)] px-2.5 py-1 text-xs transition-colors',
                  theme === p
                    ? 'bg-[var(--accent)] text-[var(--on-accent)]'
                    : 'text-[var(--muted)] hover:bg-[var(--hover-bg)]',
                )}
              >
                {MODE_LABELS[p]}
              </button>
            ))}
          </div>
          <div className="flex items-center gap-2" role="group" aria-label="强调色">
            {PALETTES.map((p) => (
              <button
                key={p}
                type="button"
                title={PALETTE_NAMES[p]}
                aria-label={'强调色：' + PALETTE_NAMES[p]}
                onClick={() => pickPalette(p)}
                className={cn(
                  'size-5 rounded-full transition-transform hover:scale-110',
                  palette === p && !custom && 'outline-2 outline-offset-2 outline-[var(--foreground)]',
                )}
                style={{ background: `hsl(${({ p1: '28,82%,57%', p2: '239,84%,60%', p3: '175,77%,38%', p4: '199,89%,48%', p5: '347,78%,51%' })[p]})` }}
              />
            ))}
            <input
              type="color"
              aria-label="自定义强调色"
              value={custom ?? '#e88b40'}
              onChange={(e) => pickCustom(e.target.value)}
              className="size-6 cursor-pointer rounded-full border border-dashed border-[var(--border-strong)] bg-transparent p-0"
            />
            {custom && (
              <button
                type="button"
                onClick={() => {
                  clearCustomAccent();
                  setCustom(null);
                }}
                className="inline-flex items-center gap-1 text-xs text-[var(--muted)] hover:text-[var(--foreground)]"
              >
                <RotateCcw className="size-3.5" aria-hidden="true" />
                恢复预设
              </button>
            )}
          </div>
          <div className="flex items-center gap-1.5" role="group" aria-label="密度">
            {DENSITIES.map((d) => (
              <button
                key={d}
                type="button"
                onClick={() => pickDensity(d)}
                aria-pressed={density === d}
                className={cn(
                  'rounded-[var(--radius-sm)] px-2.5 py-1 text-xs transition-colors',
                  density === d
                    ? 'bg-[var(--accent)] text-[var(--on-accent)]'
                    : 'text-[var(--muted)] hover:bg-[var(--hover-bg)]',
                )}
              >
                {densityLabel(d)}
              </button>
            ))}
          </div>
          {custom && <span className="text-xs text-[var(--faint)]">自定义 {custom}</span>}
        </div>
      </header>

      <main className="mx-auto max-w-5xl space-y-10 px-6 py-8">
        <Section title="色板 · 强调色派生链与语义色">
          <div className="grid grid-cols-4 gap-4 sm:grid-cols-8">
            <Swatch name="--accent" css={{ background: 'var(--accent)' }} />
            <Swatch name="--accent-hover" css={{ background: 'var(--accent-hover)' }} />
            <Swatch name="--accent-subtle" css={{ background: 'var(--accent-subtle)' }} />
            <Swatch name="--accent-weak" css={{ background: 'var(--accent-weak)' }} />
            <Swatch name="--success" css={{ background: 'var(--success)' }} />
            <Swatch name="--warning" css={{ background: 'var(--warning)' }} />
            <Swatch name="--info" css={{ background: 'var(--info)' }} />
            <Swatch name="--destructive" css={{ background: 'var(--destructive)' }} />
          </div>
          <div className="grid grid-cols-4 gap-4 sm:grid-cols-8">
            <Swatch name="--background" css={{ background: 'var(--background)' }} />
            <Swatch name="--card" css={{ background: 'var(--card)' }} />
            <Swatch name="--muted-bg" css={{ background: 'var(--muted-bg)' }} />
            <Swatch name="--hover-bg" css={{ background: 'var(--hover-bg)' }} />
            <Swatch name="--border" css={{ background: 'var(--border)' }} />
            <Swatch name="--border-strong" css={{ background: 'var(--border-strong)' }} />
            <Swatch name="--muted" css={{ background: 'var(--muted)' }} />
            <Swatch name="--faint" css={{ background: 'var(--faint)' }} />
          </div>
        </Section>

        <Section title="按钮 · 5 变体 × 4 尺寸">
          <div className="flex flex-wrap items-center gap-3">
            <Button>主要操作</Button>
            <Button variant="ghost">次要</Button>
            <Button variant="outline">描边</Button>
            <Button variant="destructive">危险</Button>
            <Button variant="link">链接</Button>
            <Button variant="outline" size="icon" aria-label="新建">
              <Plus />
            </Button>
            <Button disabled>禁用</Button>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Button size="sm">Small</Button>
            <Button size="default">Default</Button>
            <Button size="lg">Large</Button>
            <Button size="icon" variant="ghost" aria-label="新建">
              <Plus />
            </Button>
          </div>
        </Section>

        <Section title="表单 · 输入 / 选择">
          <div className="grid max-w-xl gap-5">
            <div className="grid gap-2">
              <Label htmlFor="demo-name">知识库名称</Label>
              <Input id="demo-name" placeholder="例如：产品手册" />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="demo-desc">描述</Label>
              <Textarea id="demo-desc" placeholder="支持多行输入" rows={3} />
            </div>
            <div className="grid gap-2">
              <Label>可见范围</Label>
              <Select defaultValue="org">
                <SelectTrigger className="w-48" aria-label="可见范围">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="org">全员可见</SelectItem>
                  <SelectItem value="group">按组织</SelectItem>
                  <SelectItem value="private">私有</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="demo-disabled">禁用输入</Label>
              <Input id="demo-disabled" disabled placeholder="不可编辑" />
            </div>
          </div>
        </Section>

        <Section title="表格 · 排序 / 状态徽标">
          <div className="overflow-hidden rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)]">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <span className="inline-flex items-center gap-1">
                      用户 <ArrowUpDown className="size-3" aria-hidden="true" />
                    </span>
                  </TableHead>
                  <TableHead>角色</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell>rick@example.com</TableCell>
                  <TableCell><Pill tone="muted">管理员</Pill></TableCell>
                  <TableCell><Pill tone="success">启用</Pill></TableCell>
                  <TableCell className="text-right"><Button variant="ghost" size="sm">编辑</Button></TableCell>
                </TableRow>
                <TableRow>
                  <TableCell>anna@example.com</TableCell>
                  <TableCell><Pill tone="muted">成员</Pill></TableCell>
                  <TableCell><Pill tone="warning">待激活</Pill></TableCell>
                  <TableCell className="text-right"><Button variant="ghost" size="sm">编辑</Button></TableCell>
                </TableRow>
                <TableRow>
                  <TableCell>bob@example.com</TableCell>
                  <TableCell><Pill tone="muted">成员</Pill></TableCell>
                  <TableCell><Pill tone="danger">已禁用</Pill></TableCell>
                  <TableCell className="text-right"><Button variant="ghost" size="sm">编辑</Button></TableCell>
                </TableRow>
                <TableRow>
                  <TableCell>carol@example.com</TableCell>
                  <TableCell><Pill tone="muted">只读访客</Pill></TableCell>
                  <TableCell><Pill tone="success">启用</Pill></TableCell>
                  <TableCell className="text-right"><Button variant="ghost" size="sm">编辑</Button></TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </Section>

        <Section title="反馈 · 对话框 / 提示 / 骨架屏">
          <div className="flex flex-wrap items-center gap-3">
            <Dialog>
              <DialogTrigger asChild>
                <Button variant="outline">打开对话框</Button>
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>删除知识库</DialogTitle>
                  <DialogDescription>此操作不可恢复，确认删除「产品手册」？</DialogDescription>
                </DialogHeader>
                <DialogFooter>
                  <Button variant="outline">取消</Button>
                  <Button variant="destructive">确认删除</Button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
            <Button
              variant="ghost"
              onClick={() => toast.success('已保存', { description: '设置已生效' })}
            >
              成功提示
            </Button>
            <Button
              variant="ghost"
              onClick={() => toast.error('操作失败', { description: '网络异常，请重试' })}
            >
              失败提示
            </Button>
            <Button
              variant="ghost"
              onClick={() => toast.info('提示信息', { description: '这是一条普通通知' })}
            >
              通知提示
            </Button>
          </div>
          <div className="flex max-w-md items-center gap-4">
            <Skeleton className="size-12 rounded-full" />
            <div className="w-full space-y-2">
              <Skeleton className="h-4 w-3/4" />
              <Skeleton className="h-4 w-1/2" />
            </div>
          </div>
        </Section>
      </main>
    </div>
  );
}
