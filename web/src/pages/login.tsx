/**
 * 登录页（占位：F3 填充 RHF+zod 表单与登录逻辑）。
 * 本批验收点：卡片顶部提供主题/配色切换入口（架构 §5.4 第 7 条）。
 */

import { ThemeControls } from '@/components/theme/theme-controls';

export function LoginPage() {
  return (
    <div className="flex h-screen items-center justify-center">
      <div className="login-card">
        <div className="mb-4">
          <ThemeControls />
        </div>
        <div className="login-logo" aria-hidden="true">
          🐦
        </div>
        <h1 className="text-xl">啄木鸟 Woodpecker</h1>
        <p className="mb-6 mt-1 text-[13px] text-[var(--muted)]">企业内部知识库问答系统</p>
        <p className="text-sm text-[var(--muted)]">登录表单将在 F3 批次实现。</p>
        <p className="mt-4 text-center text-xs text-[var(--faint)]">
          账号由管理员统一创建 · 忘记密码请联系管理员
        </p>
      </div>
    </div>
  );
}
