/** 403 页（AUTH_007 以外的权限不足兜底页，AdminGuard 拦截目标）。 */

export function ForbiddenPage() {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="kb-card w-96 text-center">
        <h2 className="mb-2 text-lg font-semibold">无权访问</h2>
        <p className="text-sm text-[var(--muted)]">此页面仅系统管理员可访问。</p>
      </div>
    </div>
  );
}
