/** 页面占位组件：F3~F6 批次按架构 §2 的文件清单逐页填充实现。 */

interface PagePlaceholderProps {
  title: string;
  batch: string;
}

export function PagePlaceholder({ title, batch }: PagePlaceholderProps) {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="kb-card w-96 text-center">
        <h2 className="mb-2 text-lg font-semibold">{title}</h2>
        <p className="text-sm text-[var(--muted)]">此页面将在 {batch} 批次实现。</p>
      </div>
    </div>
  );
}
