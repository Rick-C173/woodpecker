/** 404 页（AppShell 内兜底）。 */

export function NotFoundPage() {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="panel w-96 text-center">
        <h2 className="mb-2 text-lg font-semibold">页面不存在</h2>
        <p className="text-sm text-[var(--muted)]">你访问的路径不存在或已被移除。</p>
      </div>
    </div>
  );
}
